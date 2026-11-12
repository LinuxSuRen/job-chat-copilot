package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

type fakeDelegate struct {
	mu        sync.Mutex
	messages  [][2]string // conv_key, content
	snapshots int
	delivers  []string
	anomalies []string
	jobLists  [][]ScannedJob
}

func (d *fakeDelegate) HandleChatMessage(convKey, direction, content, msgType, ts string) {
	d.mu.Lock()
	d.messages = append(d.messages, [2]string{convKey, content})
	d.mu.Unlock()
}
func (d *fakeDelegate) HandleChatSnapshot(data json.RawMessage) {
	d.mu.Lock()
	d.snapshots++
	d.mu.Unlock()
}
func (d *fakeDelegate) HandleDeliverResult(jobKey string, ok bool, failCode string) {
	d.mu.Lock()
	d.delivers = append(d.delivers, jobKey)
	d.mu.Unlock()
}
func (d *fakeDelegate) HandleAnomaly(typ string, payload json.RawMessage) {
	d.mu.Lock()
	d.anomalies = append(d.anomalies, typ)
	d.mu.Unlock()
}

func (d *fakeDelegate) HandleJobsList(keyword string, jobs []ScannedJob) {
	d.mu.Lock()
	d.jobLists = append(d.jobLists, jobs)
	d.mu.Unlock()
}

func newTestGateway(t *testing.T) (*Gateway, *fakeDelegate) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &fakeDelegate{}
	g, err := New(st, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g, d
}

func serve(t *testing.T, g *Gateway) *httptest.Server {
	t.Helper()
	mux := newTestMux(g)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestMux(g *Gateway) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/ext", g.ServePluginWS)
	mux.HandleFunc("/api/ws", g.ServeConsoleWS)
	return mux
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func readEvent(t *testing.T, ws *websocket.Conn) Envelope {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("bad envelope: %v", err)
	}
	return env
}

func writeEvent(t *testing.T, ws *websocket.Conn, event string, data any) {
	t.Helper()
	b, _ := json.Marshal(data)
	msg, _ := json.Marshal(Envelope{Event: event, Payload: b})
	if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Fatalf("write %s: %v", event, err)
	}
}

func dialExt(t *testing.T, srv *httptest.Server, origin string) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	ws, _, err := websocket.DefaultDialer.Dial(wsURL(srv, "/ws/ext"), h)
	if err != nil {
		t.Fatalf("dial /ws/ext: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func TestOriginCheck(t *testing.T) {
	g, _ := newTestGateway(t)
	srv := serve(t, g)
	// 恶意站点 Origin 被拒
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL(srv, "/ws/ext"), http.Header{"Origin": {"https://evil.example.com"}}); err == nil {
		t.Fatal("evil origin must be rejected")
	} else if resp.StatusCode != 403 {
		t.Fatalf("expect 403, got %d", resp.StatusCode)
	}
	// chrome-extension:// 放行；控制台同源放行
	for _, origin := range []string{"chrome-extension://abcdef1234", ""} {
		ws := dialExt(t, srv, origin)
		ws.Close()
	}
}

func TestPairingFlow(t *testing.T) {
	g, _ := newTestGateway(t)
	srv := serve(t, g)

	paired, code := g.PairingStatus()
	if paired || code == "" {
		t.Fatalf("fresh gateway should be unpaired with code, got %v %q", paired, code)
	}

	// 错误 token 被拒
	ws := dialExt(t, srv, "")
	writeEvent(t, ws, EvHello, map[string]any{"token": "wrong-token", "ext_version": "0.1.0"})
	ack := readEvent(t, ws)
	if ack.Event != EvHelloAck || !strings.Contains(string(ack.Payload), `"paired":false`) {
		t.Fatalf("wrong token should get paired:false, got %s %s", ack.Event, ack.Payload)
	}

	// 配对码首次连接（插件 options 页标准路径：pairing_code 字段）：下发 bind_token
	ws2 := dialExt(t, srv, "")
	writeEvent(t, ws2, EvHello, map[string]any{"token": "", "ext_version": "0.1.0", "pairing_code": code})
	ack2 := readEvent(t, ws2)
	if ack2.Event != EvHelloAck {
		t.Fatalf("expect hello_ack, got %s", ack2.Event)
	}
	var ackData struct {
		Paired    bool   `json:"paired"`
		BindToken string `json:"bind_token"`
	}
	json.Unmarshal(ack2.Payload, &ackData)
	if !ackData.Paired || ackData.BindToken == "" {
		t.Fatalf("pairing should succeed with bind_token: %s", ack2.Payload)
	}

	if p, c := g.PairingStatus(); !p || c != "" {
		t.Fatalf("after pairing expect paired with consumed code, got %v %q", p, c)
	}

	// 配对码一次性：再用会被拒
	ws3 := dialExt(t, srv, "")
	writeEvent(t, ws3, EvHello, map[string]any{"token": code})
	if ack := readEvent(t, ws3); !strings.Contains(string(ack.Payload), `"paired":false`) {
		t.Fatalf("used pairing code must be rejected: %s", ack.Payload)
	}

	// bind_token 重连成功
	ws4 := dialExt(t, srv, "")
	writeEvent(t, ws4, EvHello, map[string]any{"token": ackData.BindToken})
	if ack := readEvent(t, ws4); !strings.Contains(string(ack.Payload), `"paired":true`) {
		t.Fatalf("bind token must authenticate: %s", ack.Payload)
	}
}

func TestChatMessageRoutedToDelegate(t *testing.T) {
	g, d := newTestGateway(t)
	srv := serve(t, g)
	if g.SendToPlugin(EvChatSend, map[string]any{}) {
		t.Fatal("offline plugin send should fail")
	}
	ws := dialExt(t, srv, "")
	writeEvent(t, ws, EvHello, map[string]any{"token": g.localToken})
	readEvent(t, ws) // hello_ack

	writeEvent(t, ws, EvChatMessage, map[string]any{
		"conv_key": "ck-1", "direction": "in", "content": "何时到岗？", "msg_type": "text", "ts": "2026-11-12T12:00:00Z",
	})
	writeEvent(t, ws, EvChatSnapshot, map[string]any{"conversations": []any{}})
	writeEvent(t, ws, EvDeliverResult, map[string]any{"job_key": "jk-1", "ok": true})
	writeEvent(t, ws, EvPageAnomaly, map[string]any{"type": "captcha"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		n := len(d.messages) + d.snapshots + len(d.delivers) + len(d.anomalies)
		d.mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.messages) != 1 || d.messages[0] != [2]string{"ck-1", "何时到岗？"} {
		t.Fatalf("chat.message routing failed: %v", d.messages)
	}
	if d.snapshots != 1 || len(d.delivers) != 1 || len(d.anomalies) != 1 {
		t.Fatalf("event routing incomplete: %+v", d)
	}
}

func TestConsoleBusMirror(t *testing.T) {
	g, _ := newTestGateway(t)
	srv := serve(t, g)

	// 控制台先连
	console, _, err := websocket.DefaultDialer.Dial(wsURL(srv, "/api/ws"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { console.Close() })

	ws := dialExt(t, srv, "")
	writeEvent(t, ws, EvHello, map[string]any{"token": g.localToken})
	readEvent(t, ws) // hello_ack（插件侧）

	seen := map[string]bool{}
	console.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 1; i++ { // 先收到 plugin.connected
		_, raw, err := console.ReadMessage()
		if err != nil {
			t.Fatalf("console read: %v (seen=%v)", err, seen)
		}
		var env Envelope
		json.Unmarshal(raw, &env)
		seen[env.Event] = true
	}
	if !seen[EvPluginConnected] {
		t.Fatalf("console should see plugin.connected: %v", seen)
	}
	writeEvent(t, ws, EvChatMessage, map[string]any{"conv_key": "c", "direction": "in", "content": "x"})
	console.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 5; i++ {
		_, raw, err := console.ReadMessage()
		if err != nil {
			t.Fatalf("console read mirror: %v", err)
		}
		var env Envelope
		json.Unmarshal(raw, &env)
		if env.Event == EvChatMessage {
			seen[EvChatMessage] = true
			break
		}
	}
	if !seen[EvChatMessage] {
		t.Fatal("console must receive mirrored chat.message")
	}

	// 服务→插件下发 + review.stats / draft.ready 广播
	if !g.SendToPlugin(EvDeliverNext, map[string]any{"keyword": "go"}) {
		t.Fatal("SendToPlugin should succeed when online")
	}
	g.BroadcastConsole(EvDraftReady, map[string]any{"draft_id": 1, "conv_id": 2})
	g.BroadcastConsole(EvReviewStats, map[string]any{"pending_count": 3})
	g.BroadcastConsole(EvGuardAlert, map[string]any{"reason_code": "captcha"})
	console.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 10; i++ {
		_, raw, err := console.ReadMessage()
		if err != nil {
			t.Fatalf("console read broadcast: %v", err)
		}
		var env Envelope
		json.Unmarshal(raw, &env)
		seen[env.Event] = true
		if seen[EvDraftReady] && seen[EvReviewStats] && seen[EvGuardAlert] {
			break
		}
	}
	for _, want := range []string{EvDraftReady, EvReviewStats, EvGuardAlert} {
		if !seen[want] {
			t.Errorf("console missing %s (seen=%v)", want, seen)
		}
	}
}

func TestSendChat(t *testing.T) {
	g, _ := newTestGateway(t)
	srv := serve(t, g)
	if g.SendChat("ck", "hi") {
		t.Fatal("send with no plugin should fail")
	}
	ws := dialExt(t, srv, "")
	writeEvent(t, ws, EvHello, map[string]any{"token": g.localToken})
	readEvent(t, ws)
	deadline := time.Now().Add(2 * time.Second)
	for !g.PluginConnected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !g.SendChat("ck", "hi") {
		t.Fatal("send with plugin online should succeed")
	}
	// 插件侧应收到 chat.send
	found := false
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 3 && !found; i++ {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			break
		}
		var env Envelope
		json.Unmarshal(raw, &env)
		if env.Event == EvChatSend {
			found = true
		}
	}
	if !found {
		t.Fatal("plugin should receive chat.send")
	}
}
