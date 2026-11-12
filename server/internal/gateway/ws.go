package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

// pluginConn 包装插件连接（单连接，串行写）。
type pluginConn struct {
	ws   *websocket.Conn
	wmu  chan struct{} // 用作二值信号量的写锁
	g    *Gateway
	done chan struct{}
}

func (p *pluginConn) send(event string, data any) bool {
	msg, err := json.Marshal(NewEnvelope(event, data))
	if err != nil {
		return false
	}
	select {
	case p.wmu <- struct{}{}:
		defer func() { <-p.wmu }()
	default:
		return false // 上一条还没写完，视为繁忙失败
	}
	p.ws.SetWriteDeadline(time.Now().Add(writeWait))
	if err := p.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
		return false
	}
	return true
}

// consoleConn 每连接一个发送通道。
type consoleConn struct {
	ws *websocket.Conn
	ch chan []byte
}

// ServePluginWS 处理 /ws/ext：握手鉴权后进入事件循环。
func (g *Gateway) ServePluginWS(w http.ResponseWriter, r *http.Request) {
	if !g.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	pc := &pluginConn{ws: ws, wmu: make(chan struct{}, 1), g: g, done: make(chan struct{})}

	// 第一步：等待 hello 完成鉴权
	if !g.handshake(pc) {
		ws.Close()
		return
	}

	g.mu.Lock()
	old := g.pluginWS
	g.pluginWS = pc
	g.mu.Unlock()
	if old != nil {
		// 旧连接被顶替：规范场景插件只有一个 service worker 连接
		old.close()
	}

	g.BroadcastConsole(EvPluginConnected, map[string]any{})
	g.logger.Info("plugin connected")

	go pinger(pc)
	g.readPlugin(pc)
}

// handshake 等待 hello 事件并校验 token。
func (g *Gateway) handshake(pc *pluginConn) bool {
	pc.ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := pc.ws.ReadMessage()
	if err != nil {
		return false
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Event != EvHello {
		pc.send(EvHelloAck, map[string]any{"paired": false, "error": "expected hello"})
		return false
	}
	var hello struct {
		Token       string `json:"token"`
		ExtVersion  string `json:"ext_version"`
		PairingCode string `json:"pairing_code"`
	}
	_ = json.Unmarshal(env.Payload, &hello)

	g.mu.Lock()
	paired, token, code := g.paired, g.localToken, g.pairingCode
	pairingHit := !paired && code != "" && hello.PairingCode != "" && hello.PairingCode == code
	if !pairingHit && !paired && hello.Token != "" && hello.Token == code {
		pairingHit = true // 兼容：配对码直接放 token 字段
	}
	if pairingHit {
		// 一次性配对码命中：绑定，持久化本地 token，消费配对码
		g.paired = true
		g.pairingCode = ""
		_ = g.store.SetSetting(settingPaired, "true")
		paired, token = true, g.localToken
		g.mu.Unlock()
		pc.ws.SetReadDeadline(time.Time{})
		pc.send(EvHelloAck, map[string]any{"paired": true, "bind_token": token})
		g.logger.Info("plugin paired via pairing code")
		return true
	}
	ok := hello.Token != "" && hello.Token == token
	g.mu.Unlock()

	pc.ws.SetReadDeadline(time.Time{})
	if ok {
		pc.send(EvHelloAck, map[string]any{"paired": true})
		return true
	}
	pc.send(EvHelloAck, map[string]any{"paired": false, "error": "invalid token"})
	g.logger.Warn("plugin hello rejected: invalid token")
	return false
}

func (p *pluginConn) close() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.ws.Close()
}

func pinger(pc *pluginConn) {
	t := time.NewTicker(pingPeriod)
	defer t.Stop()
	for {
		select {
		case <-pc.done:
			return
		case <-t.C:
			pc.wmu <- struct{}{}
			pc.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := pc.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				<-pc.wmu
				pc.close()
				return
			}
			<-pc.wmu
		}
	}
}

// readPlugin 插件事件读循环与分发。
func (g *Gateway) readPlugin(pc *pluginConn) {
	defer func() {
		pc.close()
		g.mu.Lock()
		if g.pluginWS == pc {
			g.pluginWS = nil
		}
		g.mu.Unlock()
		g.BroadcastConsole(EvPluginDisconnected, map[string]any{})
		g.logger.Info("plugin disconnected")
	}()
	pc.ws.SetReadDeadline(time.Now().Add(pongWait))
	pc.ws.SetPongHandler(func(string) error {
		pc.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, raw, err := pc.ws.ReadMessage()
		if err != nil {
			return
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			g.logger.Warn("bad plugin envelope", "err", err)
			continue
		}
		// 全量镜像给控制台（只读事件流）
		g.BroadcastConsole(env.Event, env.Payload)
		switch env.Event {
		case EvChatMessage:
			var m struct {
				ConvKey   string `json:"conv_key"`
				Direction string `json:"direction"`
				Content   string `json:"content"`
				MsgType   string `json:"msg_type"`
				TS        string `json:"ts"`
			}
			if json.Unmarshal(env.Payload, &m) == nil && m.ConvKey != "" {
				g.delegateOrNil().HandleChatMessage(m.ConvKey, m.Direction, m.Content, m.MsgType, m.TS)
			}
		case EvChatSnapshot:
			g.delegateOrNil().HandleChatSnapshot(env.Payload)
		case EvDeliverResult:
			var r struct {
				JobKey   string `json:"job_key"`
				OK       bool   `json:"ok"`
				FailCode string `json:"fail_code"`
			}
			if json.Unmarshal(env.Payload, &r) == nil {
				g.delegateOrNil().HandleDeliverResult(r.JobKey, r.OK, r.FailCode)
			}
		case EvJobsList:
			var l struct {
				Keyword string       `json:"keyword"`
				Jobs    []ScannedJob `json:"jobs"`
			}
			if json.Unmarshal(env.Payload, &l) == nil && len(l.Jobs) > 0 {
				g.delegateOrNil().HandleJobsList(l.Keyword, l.Jobs)
			}
		case EvPageAnomaly:
			var a struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(env.Payload, &a) == nil {
				g.delegateOrNil().HandleAnomaly(a.Type, a.Payload)
			}
		case EvHello:
			// 重复 hello 忽略（已在握手处理）
		default:
			g.logger.Debug("unknown plugin event", "event", env.Event)
		}
	}
}

// ServeConsoleWS 处理 /api/ws：控制台只读事件流。
func (g *Gateway) ServeConsoleWS(w http.ResponseWriter, r *http.Request) {
	if !g.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	cc := &consoleConn{ws: ws, ch: make(chan []byte, 64)}
	g.mu.Lock()
	g.consoles[cc] = struct{}{}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.consoles, cc)
		g.mu.Unlock()
		ws.Close()
	}()

	// 读循环：忽略一切客户端输入（只读总线），仅用于感知断开
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		ws.SetReadLimit(4 << 10)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case msg := <-cc.ch:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}

// AlertGuard 向控制台推送护栏告警（验证码/掉登录等，需人工确认消除）。
func (g *Gateway) AlertGuard(reason string) {
	g.BroadcastConsole(EvGuardAlert, map[string]any{"reason_code": reason})
}
