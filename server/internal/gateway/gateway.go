// Package gateway 实现插件 WS 网关与控制台 WS 事件总线：
// 首次配对码（一次性）、本地 token 鉴权、Origin 校验、§5.1 双向事件路由。
package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

// 事件名（§5.1）
const (
	EvDeliverNext   = "deliver.next"
	EvChatSend      = "chat.send"
	EvChatPoll      = "chat.poll"
	EvGuardPause    = "guard.pause"
	EvConfigSync    = "config.sync"
	EvReviewStats   = "review.stats"
	EvHello         = "hello"
	EvHelloAck      = "hello_ack"
	EvChatMessage   = "chat.message"
	EvChatSnapshot  = "chat.snapshot"
	EvDeliverResult = "deliver.result"
	EvPageAnomaly   = "page.anomaly"
	EvJobsScan      = "jobs.scan"
	EvJobsList      = "jobs.list"

	// 控制台扩展事件
	EvDraftReady         = "draft.ready"
	EvPluginConnected    = "plugin.connected"
	EvPluginDisconnected = "plugin.disconnected"
	EvGuardAlert         = "guard.alert"
)

// settings 键
const (
	settingPaired = "auth.paired"
	settingToken  = "auth.token"
)

// Envelope 为 WS 事件信封：{"event": "...", "payload": {...}, "ts": ...}。
type Envelope struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Ts      int64           `json:"ts,omitempty"`
}

// NewEnvelope 构造带时间戳的信封。
func NewEnvelope(event string, data any) Envelope {
	return Envelope{Event: event, Payload: rawData(data), Ts: time.Now().UnixMilli()}
}

// Delegate 由编排层实现，处理插件上报事件。
type Delegate interface {
	HandleChatMessage(convKey, direction, content, msgType, ts string)
	HandleChatSnapshot(data json.RawMessage)
	HandleDeliverResult(jobKey string, ok bool, failCode string)
	HandleAnomaly(typ string, payload json.RawMessage)
	HandleJobsList(keyword string, jobs []ScannedJob)
}

// ScannedJob 为 jobs.list 上报的职位卡片条目（§5.1）。
type ScannedJob struct {
	JobKey  string `json:"job_key"`
	Title   string `json:"title"`
	Company string `json:"company"`
	Salary  string `json:"salary"`
	JDText  string `json:"jd_text"`
}

// Gateway 管理插件连接、控制台连接与事件分发。
type Gateway struct {
	mu       sync.Mutex
	store    *store.Store
	logger   *slog.Logger
	delegate Delegate

	upgrader websocket.Upgrader

	pairingCode string
	localToken  string
	paired      bool

	pluginWS   *pluginConn
	consoles   map[*consoleConn]struct{}
	onPause    func(reason string) // guard 告警转发（由外层注入）
}

// New 创建网关。未配对时生成一次性配对码。
func New(st *store.Store, delegate Delegate, logger *slog.Logger) (*Gateway, error) {
	if logger == nil {
		logger = slog.Default()
	}
	g := &Gateway{
		store:    st,
		logger:   logger,
		delegate: delegate,
		consoles: make(map[*consoleConn]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  8 << 10,
			WriteBufferSize: 8 << 10,
			// Origin 已在握手前由 checkOrigin 校验，这里放行交给 Upgrade
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
	v, err := st.GetSetting(settingPaired)
	if errors.Is(err, store.ErrNotFound) {
		g.pairingCode = newCode()
		g.paired = false
	} else if err != nil {
		return nil, err
	} else {
		g.paired = v == "true"
	}
	if g.paired {
		tok, err := st.GetSetting(settingToken)
		if err != nil {
			return nil, err
		}
		g.localToken = tok
	} else {
		g.localToken = newCode()
		if err := st.SetSetting(settingToken, g.localToken); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// SetPauseAlert 注入告警回调（guard 暂停 → 控制台告警）。
func (g *Gateway) SetPauseAlert(fn func(reason string)) {
	g.mu.Lock()
	g.onPause = fn
	g.mu.Unlock()
}

// SetDelegate 注入事件处理者（必须在开始监听前调用）。
func (g *Gateway) SetDelegate(d Delegate) {
	g.mu.Lock()
	g.delegate = d
	g.mu.Unlock()
}

func (g *Gateway) delegateOrNil() Delegate {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.delegate
}

func newCode() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 不可用属于不可恢复环境错误
	}
	return hex.EncodeToString(b)
}

// PairingStatus 供 GET /api/pairing。
func (g *Gateway) PairingStatus() (paired bool, code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paired, g.pairingCode
}

// checkOrigin 校验握手 Origin：允许空（非浏览器客户端）、chrome-extension:// 任意 id、
// 本服务自身来源（控制台页面）。
func (g *Gateway) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1:7422"
	}
	if origin == "http://"+host || origin == "https://"+host {
		return true
	}
	// chrome-extension://<id>（id 在插件加载后动态分配，无法预知，放行该 scheme）
	if strings.HasPrefix(origin, "chrome-extension://") && len(origin) > len("chrome-extension://") {
		return true
	}
	return false
}

// PluginConnected 返回插件是否在线（/api/stats）。
func (g *Gateway) PluginConnected() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pluginWS != nil
}

// SendToPlugin 向插件下发事件；插件离线返回 false。
func (g *Gateway) SendToPlugin(event string, data any) bool {
	g.mu.Lock()
	pc := g.pluginWS
	g.mu.Unlock()
	if pc == nil {
		return false
	}
	return pc.send(event, data)
}

// BroadcastConsole 向全部控制台连接广播事件。
func (g *Gateway) BroadcastConsole(event string, data any) {
	msg, err := json.Marshal(NewEnvelope(event, data))
	if err != nil {
		return
	}
	g.mu.Lock()
	for c := range g.consoles {
		select {
		case c.ch <- msg:
		default:
			// 慢消费者丢弃本条，避免阻塞广播
		}
	}
	g.mu.Unlock()
}

func rawData(data any) json.RawMessage {
	if data == nil {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	return b
}

// SendChat 实现 review.Sender：下发 chat.send，并镜像事件给控制台。
func (g *Gateway) SendChat(convKey, content string) bool {
	ok := g.SendToPlugin(EvChatSend, map[string]any{"conv_key": convKey, "content": content})
	if ok {
		g.BroadcastConsole(EvChatSend, map[string]any{"conv_key": convKey, "content": content})
	}
	return ok
}
