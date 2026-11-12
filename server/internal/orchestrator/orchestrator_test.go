package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linuxsuren/job-chat-copilot/internal/gateway"
	"github.com/linuxsuren/job-chat-copilot/internal/guard"
	"github.com/linuxsuren/job-chat-copilot/internal/review"
	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

// fakeGW 实现 Sender，记录下发事件。
type fakeGW struct {
	mu     sync.Mutex
	online bool
	events []struct {
		event string
		data  map[string]any
	}
}

func (f *fakeGW) SendToPlugin(event string, data any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online {
		return false
	}
	f.events = append(f.events, struct {
		event string
		data  map[string]any
	}{event, data.(map[string]any)})
	return true
}
func (f *fakeGW) SendChat(convKey, content string) bool   { return f.online }
func (f *fakeGW) BroadcastConsole(event string, data any) {}
func (f *fakeGW) PluginConnected() bool                   { return f.online }

func (f *fakeGW) count(event string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if e.event == event {
			n++
		}
	}
	return n
}

func (f *fakeGW) first(event string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.event == event {
			return e.data, true
		}
	}
	return nil, false
}

func newOrch(t *testing.T, gw *fakeGW) *Orchestrator {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	g := guard.New(guard.Config{
		MinIntervalSeconds: 40, DailyLimitDeliver: 50, DailyLimitReply: 200,
		ActiveStart: "09:00", ActiveEnd: "20:00",
	}, nil)
	g.Now(func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local) })
	gw.online = true
	rv := review.NewService(st, gw, nil, nil, nil)
	o := New(st, gw, g, rv, nil)
	// 测试内不真实 sleep：记录 guard 下发的间隔
	o.wait = func(ctx context.Context, d time.Duration) error {
		o.mu.Lock()
		o.waited = append(o.waited, d)
		o.mu.Unlock()
		return ctx.Err()
	}
	return o
}

func TestParseSalaryK(t *testing.T) {
	cases := []struct {
		in       string
		min, max int
		ok       bool
	}{
		{"20-30K", 20, 30, true},
		{"20-30k·14薪", 20, 30, true},
		{"15-25K·13薪", 15, 25, true},
		{"25K", 25, 25, true},
		{"面议", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		min, max, ok := parseSalaryK(c.in)
		if ok != c.ok || min != c.min || max != c.max {
			t.Errorf("parseSalaryK(%q) = (%d,%d,%v), want (%d,%d,%v)", c.in, min, max, ok, c.min, c.max, c.ok)
		}
	}
}

func TestPassFilters(t *testing.T) {
	f := Filters{ExcludeKeywords: []string{"外包"}, SalaryMin: 15, SalaryMax: 35}
	if passFilters(f, store.Job{Title: "Go 后端", Company: "某公司", Salary: "20-30K"}) != true {
		t.Error("normal job should pass")
	}
	if passFilters(f, store.Job{Title: "Go 外包", Company: "x", Salary: "20-30K"}) != false {
		t.Error("exclude keyword should filter out")
	}
	if passFilters(f, store.Job{Title: "Go", Company: "x", Salary: "10-12K"}) != false {
		t.Error("below salary_min should filter out")
	}
	if passFilters(f, store.Job{Title: "Go", Company: "x", Salary: "40-50K"}) != false {
		t.Error("above salary_max should filter out")
	}
	if passFilters(f, store.Job{Title: "Go", Company: "x", Salary: "面议"}) != true {
		t.Error("unparseable salary should pass (best effort)")
	}
}

func TestRenderGreetingVars(t *testing.T) {
	jd := strings.Repeat("岗位职责", 60) // 240 字
	out := renderGreetingVars("您好，我是应聘{title}的候选人，{company}团队的岗位很感兴趣。JD 摘要：{jd}",
		store.Job{Title: "Go 后端", Company: "某科技", JDText: jd})
	if !strings.Contains(out, "Go 后端") || !strings.Contains(out, "某科技") {
		t.Fatalf("vars not replaced: %q", out)
	}
	// {jd} 截断 100 字：总输出应明显小于 240
	idx := strings.Index(out, "JD 摘要：")
	if got := len([]rune(out[idx+len("JD 摘要："):])); got != 100 {
		t.Fatalf("jd should be truncated to 100 runes, got %d", got)
	}
}

func TestRenderGreetingRotation(t *testing.T) {
	o := newOrch(t, &fakeGW{})
	o.SetGreeting([]string{"A-{title}", "B-{title}"}, "order")
	j1 := o.renderGreeting(store.Job{Title: "一"})
	j2 := o.renderGreeting(store.Job{Title: "二"})
	j3 := o.renderGreeting(store.Job{Title: "三"})
	if j1 != "A-一" || j2 != "B-二" || j3 != "A-三" {
		t.Fatalf("order rotation broken: %q %q %q", j1, j2, j3)
	}
}

func TestDeliverLoopScanFilterSerial(t *testing.T) {
	gw := &fakeGW{}
	o := newOrch(t, gw)
	o.SetGreeting([]string{"你好，{title}-{company}"}, "order")

	err := o.StartDeliver(DeliverTaskSpec{
		Keyword: "golang",
		Filters: Filters{ExcludeKeywords: []string{"外包"}, SalaryMin: 15, SalaryMax: 35},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 启动即下发 jobs.scan
	waitFor(t, time.Second, func() bool { return gw.count(gateway.EvJobsScan) >= 1 })

	// 混合职位上报：1 个正常、1 个命中排除词、1 个薪资过低、1 个已投递过
	o.store.UpsertJob(store.Job{JobKey: "done-1", Title: "已投", Company: "旧", Salary: "20-30K", Status: "delivered", JDText: ""})
	o.HandleJobsList("golang", []gateway.ScannedJob{
		{JobKey: "ok-1", Title: "Go 后端", Company: "好公司", Salary: "20-30K", JDText: "负责服务端"},
		{JobKey: "bad-1", Title: "Go 外包", Company: "外包公司", Salary: "20-30K"},
		{JobKey: "bad-2", Title: "Go 实习", Company: "小公司", Salary: "8-12K"},
		{JobKey: "done-1", Title: "已投", Company: "旧", Salary: "20-30K"},
	})

	// 只有 ok-1 收到 deliver.next，greeting 已完成变量替换
	waitFor(t, time.Second, func() bool { return gw.count(gateway.EvDeliverNext) >= 1 })
	d, _ := gw.first(gateway.EvDeliverNext)
	if d["job_key"] != "ok-1" || d["job_title"] != "Go 后端" {
		t.Fatalf("deliver.next = %v", d)
	}
	if d["greeting"] != "你好，Go 后端-好公司" {
		t.Fatalf("greeting not rendered: %v", d["greeting"])
	}
	if n := gw.count(gateway.EvDeliverNext); n > 1 {
		// 队列中仅 1 个可投职位；等待第二个 scan 前不应多发
		t.Fatalf("unexpected extra deliver.next: %d", n)
	}

	// 回执记账
	o.HandleDeliverResult("ok-1", true, "")
	j, err := o.store.GetJob("ok-1")
	if err != nil || j.Status != "delivered" {
		t.Fatalf("deliver.result accounting failed: %+v %v", j, err)
	}

	// 再次上报（全部不可投）→ scanExhausted → 任务 done
	o.HandleJobsList("golang", []gateway.ScannedJob{
		{JobKey: "ok-1", Title: "Go 后端", Company: "好公司", Salary: "20-30K"},
		{JobKey: "done-1", Title: "已投", Company: "旧", Salary: "20-30K"},
	})
	waitFor(t, time.Second, func() bool { return o.State() == StateDone })

	// 串行节奏：每个动作之间记录了 guard 下发的人化间隔（≥ 最小间隔）
	o.mu.Lock()
	waits := len(o.waited)
	o.mu.Unlock()
	if waits < 1 {
		t.Fatalf("guard interval not applied, waits=%d", waits)
	}
	if gw.count(gateway.EvDeliverNext) != 1 {
		t.Fatalf("filtered jobs must not be delivered, got %d", gw.count(gateway.EvDeliverNext))
	}
}

func TestSerialTwoJobsOrder(t *testing.T) {
	gw := &fakeGW{}
	o := newOrch(t, gw)
	o.SetGreeting([]string{"G"}, "order")
	_ = o.StartDeliver(DeliverTaskSpec{Keyword: "go"})
	waitFor(t, time.Second, func() bool { return gw.count(gateway.EvJobsScan) >= 1 })
	o.HandleJobsList("go", []gateway.ScannedJob{
		{JobKey: "a", Title: "A", Company: "c", Salary: "20-30K"},
		{JobKey: "b", Title: "B", Company: "c", Salary: "20-30K"},
	})
	waitFor(t, time.Second, func() bool { return gw.count(gateway.EvDeliverNext) >= 2 })
	// 串行：两个动作之间必须经过 guard 间隔（wait 被记录）
	o.mu.Lock()
	waits := append([]time.Duration(nil), o.waited...)
	o.mu.Unlock()
	if len(waits) < 2 {
		t.Fatalf("expect >=2 interval waits (one per action), got %v", waits)
	}
	if waits[0] < 40*time.Second {
		t.Fatalf("first interval should be >= min interval 40s, got %v", waits[0])
	}
}

func TestDeliverPauseWhenPluginOffline(t *testing.T) {
	gw := &fakeGW{online: false}
	o := newOrch(t, gw)
	gw.online = false
	_ = o.StartDeliver(DeliverTaskSpec{Keyword: "go"})
	waitFor(t, time.Second, func() bool { return o.State() == StatePaused })
}

func TestPoller(t *testing.T) {
	gw := &fakeGW{}
	o := newOrch(t, gw)
	o.pollEvery = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.StartPoller(ctx)
	waitFor(t, time.Second, func() bool { return gw.count(gateway.EvChatPoll) >= 2 })
}

func TestHandleChatSnapshotUpserts(t *testing.T) {
	gw := &fakeGW{}
	o := newOrch(t, gw)
	o.HandleChatSnapshot(json.RawMessage(`{"conversations":[{"conv_key":"c1","hr_name":"HR甲"}]}`))
	conv, err := o.store.GetOrCreateConversation("c1", "", nil)
	if err != nil || conv.HRName != "HR甲" {
		t.Fatalf("snapshot upsert failed: %+v %v", conv, err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
