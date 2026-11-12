// Package orchestrator 实现投递任务状态机与应答任务。
//
// 投递回路（§5.1 jobs.scan/jobs.list 契约）：
//
//	任务启动 → 下发 jobs.scan{keyword} → 插件上报 jobs.list → 落库 +
//	按任务 filters 过滤 → 投递队列 → 串行 deliver.next{job_key, job_title, greeting}
//	（间隔由 guard 下发，招呼语在服务端完成模板轮换 + JD 变量替换）→
//	deliver.result 精确记账；队列耗尽且扫描无新增 → done。
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linuxsuren/job-chat-copilot/internal/gateway"
	"github.com/linuxsuren/job-chat-copilot/internal/guard"
	"github.com/linuxsuren/job-chat-copilot/internal/llm"
	"github.com/linuxsuren/job-chat-copilot/internal/review"
	"github.com/linuxsuren/job-chat-copilot/internal/rule"
	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

// DeliverTaskSpec 为 POST /api/tasks/deliver 的请求体。
type DeliverTaskSpec struct {
	Keyword    string  `json:"keyword"`
	Filters    Filters `json:"filters"`
	DailyLimit int     `json:"daily_limit,omitempty"`
}

// Filters 投递过滤策略。
type Filters struct {
	ExcludeKeywords []string `json:"exclude_keywords"`
	SalaryMin       int      `json:"salary_min,omitempty"` // 单位 K
	SalaryMax       int      `json:"salary_max,omitempty"` // 单位 K
}

// settingAutoMode 全自动模式开关（settings.auto_mode，默认 false 人工审核优先）。
const settingAutoMode = "auto_mode"

// Sender 是编排器对网关的最小依赖（gateway.Gateway 满足；测试可替换）。
type Sender interface {
	SendToPlugin(event string, data any) bool
	SendChat(convKey, content string) bool
	BroadcastConsole(event string, data any)
	PluginConnected() bool
}

// Orchestrator 任务编排器。
type Orchestrator struct {
	mu     sync.Mutex
	store  *store.Store
	gw     Sender
	guard  *guard.Guard
	review *review.Service
	logger *slog.Logger

	lib     *rule.Library
	llmCfg  llm.Config
	autoLLM bool // 全自动：rule 命中直发

	// 招呼语模板轮换
	greetings []string
	greetSeq  int
	strategy  string // order / random

	// 投递任务状态
	state    string // idle / running / paused / done
	cancel   context.CancelFunc
	lastSpec *DeliverTaskSpec
	pauseErr error

	queue         []store.Job // 待投递队列（已过滤，未沟通过）
	queued        map[string]bool
	scanSignal    chan struct{} // jobs.list 到达通知（容量 1）
	scanExhausted bool          // 上一次扫描未带来可投递新职位
	scanCount     int

	// 可注入（测试用）
	wait      func(ctx context.Context, d time.Duration) error
	waited    []time.Duration // 测试观测：guard 下发的人化间隔记录
	pollEvery time.Duration
}

// 状态常量。
const (
	StateIdle    = "idle"
	StateRunning = "running"
	StatePaused  = "paused"
	StateDone    = "done"
)

// DefaultPollInterval chat.poll 兜底扫描周期。
const DefaultPollInterval = 60 * time.Second

// New 创建编排器。
func New(st *store.Store, gw Sender, g *guard.Guard, rv *review.Service, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	o := &Orchestrator{
		store: st, gw: gw, guard: g, review: rv, logger: logger,
		state: StateIdle, scanSignal: make(chan struct{}, 1),
		queued: make(map[string]bool), pollEvery: DefaultPollInterval,
	}
	o.wait = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	o.reloadRules()
	if v, err := st.GetSetting(settingAutoMode); err == nil {
		o.autoLLM = v == "true"
	}
	if v, err := st.GetSetting("greeting"); err == nil {
		var gc struct {
			Templates []string `json:"templates"`
			Strategy  string   `json:"strategy"`
		}
		if json.Unmarshal([]byte(v), &gc) == nil {
			o.greetings, o.strategy = gc.Templates, gc.Strategy
		}
	}
	return o
}

// reloadRules 从库里重建规则。
func (o *Orchestrator) reloadRules() {
	rules, err := o.store.ListRules()
	if err != nil {
		o.logger.Error("load rules failed", "err", err)
		return
	}
	lib := rule.NewLibrary(nil)
	rs := make([]rule.Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			rs = append(rs, rule.Rule{ID: r.ID, Pattern: r.Pattern, Answer: r.Answer})
		}
	}
	lib.Reload(rs)
	o.lib = lib
}

// ReloadRules 供规则 CRUD 后刷新。
func (o *Orchestrator) ReloadRules() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reloadRules()
}

// SetLLMConfig 更新 LLM 配置。
func (o *Orchestrator) SetLLMConfig(cfg llm.Config) {
	o.mu.Lock()
	o.llmCfg = cfg
	o.mu.Unlock()
}

// SetAutoMode 设置全自动模式。
func (o *Orchestrator) SetAutoMode(auto bool) {
	o.mu.Lock()
	o.autoLLM = auto
	o.mu.Unlock()
}

// SetGreeting 更新招呼语模板与轮换策略。
func (o *Orchestrator) SetGreeting(templates []string, strategy string) {
	o.mu.Lock()
	o.greetings = templates
	o.strategy = strategy
	o.mu.Unlock()
}

// State 返回投递任务状态。
func (o *Orchestrator) State() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

// ---------- 插件事件入口（gateway Delegate） ----------

// HandleJobsList 处理 jobs.list：落库 + 过滤 + 入队，并唤醒投递循环。
func (o *Orchestrator) HandleJobsList(keyword string, jobs []gateway.ScannedJob) {
	o.mu.Lock()
	spec := o.lastSpec
	o.mu.Unlock()
	if spec == nil {
		o.store.AddTaskLog("deliver", "jobs.list", "no active task, ignored")
		return
	}
	added := 0
	for _, j := range jobs {
		if j.JobKey == "" {
			continue
		}
		// 已存在的职位：仅刷新元数据，状态不回退（未沟通过才可投）
		existing, err := o.store.GetJob(j.JobKey)
		if errors.Is(err, store.ErrNotFound) {
			id, err := o.store.UpsertJob(store.Job{
				JobKey: j.JobKey, Title: j.Title, Company: j.Company,
				Salary: j.Salary, JDText: j.JDText, Status: "pending",
			})
			if err != nil {
				o.logger.Warn("insert scanned job failed", "job_key", j.JobKey, "err", err)
				continue
			}
			existing = store.Job{ID: id, JobKey: j.JobKey, Title: j.Title, Company: j.Company, Salary: j.Salary, JDText: j.JDText, Status: "pending"}
		} else if err != nil {
			o.logger.Warn("get scanned job failed", "job_key", j.JobKey, "err", err)
			continue
		} else {
			_, _ = o.store.UpsertJob(store.Job{
				JobKey: existing.JobKey, Title: j.Title, Company: j.Company,
				Salary: j.Salary, JDText: j.JDText, Status: existing.Status, DeliveredAt: existing.DeliveredAt,
			})
		}
		if existing.Status != "" && existing.Status != "pending" {
			continue // 已投过/已失败，不再入队
		}
		if !passFilters(spec.Filters, existing) {
			continue
		}
		o.mu.Lock()
		if !o.queued[j.JobKey] {
			o.queued[j.JobKey] = true
			o.queue = append(o.queue, existing)
			added++
		}
		o.mu.Unlock()
	}
	o.mu.Lock()
	if added == 0 {
		o.scanExhausted = true // 本次扫描无可投递新职位
	}
	o.scanCount++
	o.mu.Unlock()
	o.store.AddTaskLog("deliver", "jobs.list", fmt.Sprintf("keyword=%s reported=%d enqueued=%d", keyword, len(jobs), added))
	select {
	case o.scanSignal <- struct{}{}:
	default:
	}
}

// passFilters 按任务过滤策略判断职位是否可投。
func passFilters(f Filters, j store.Job) bool {
	blob := j.Title + " " + j.Company
	for _, kw := range f.ExcludeKeywords {
		if kw != "" && strings.Contains(blob, kw) {
			return false
		}
	}
	minK, maxK, ok := parseSalaryK(j.Salary)
	if !ok {
		return true // "面议" 等无法解析的不过滤（best effort）
	}
	if f.SalaryMin > 0 && minK < f.SalaryMin {
		return false
	}
	if f.SalaryMax > 0 && maxK > f.SalaryMax {
		return false
	}
	return true
}

var salaryRe = regexp.MustCompile(`(\d+)\s*[-–~]\s*(\d+)\s*[kK]`)

// parseSalaryK 解析 "20-30K"、"20-30k·14薪" 形式的薪资范围（单位 K）。
func parseSalaryK(s string) (min, max int, ok bool) {
	m := salaryRe.FindStringSubmatch(s)
	if m == nil {
		single := regexp.MustCompile(`^(\d+)\s*[kK]`).FindStringSubmatch(strings.TrimSpace(s))
		if single == nil {
			return 0, 0, false
		}
		v, _ := strconv.Atoi(single[1])
		return v, v, true
	}
	min, _ = strconv.Atoi(m[1])
	max, _ = strconv.Atoi(m[2])
	return min, max, true
}

// HandleChatSnapshot 会话列表兜底扫描结果：尽力更新 hr 名称等元数据。
func (o *Orchestrator) HandleChatSnapshot(data json.RawMessage) {
	var snap struct {
		Conversations []struct {
			ConvKey string `json:"conv_key"`
			HRName  string `json:"hr_name"`
		} `json:"conversations"`
	}
	if json.Unmarshal(data, &snap) != nil {
		return
	}
	for _, c := range snap.Conversations {
		if c.ConvKey == "" {
			continue
		}
		if _, err := o.store.GetOrCreateConversation(c.ConvKey, c.HRName, nil); err != nil {
			o.logger.Warn("snapshot upsert conversation failed", "conv_key", c.ConvKey, "err", err)
		}
	}
	o.store.AddTaskLog("answer", "chat.snapshot", fmt.Sprintf("%d conversations", len(snap.Conversations)))
}

// HandleDeliverResult 投递动作结果回执：精确记账到 job_key。
func (o *Orchestrator) HandleDeliverResult(jobKey string, ok bool, failCode string) {
	status := "delivered"
	if !ok {
		status = "failed"
	}
	if err := o.store.UpdateJobStatus(jobKey, status, strNowIf(ok)); err != nil {
		o.logger.Error("record deliver result failed", "job_key", jobKey, "err", err)
	}
	o.mu.Lock()
	delete(o.queued, jobKey)
	o.mu.Unlock()
	o.store.AddTaskLog("deliver", "deliver.result", fmt.Sprintf("job_key=%s ok=%v fail_code=%s", jobKey, ok, failCode))
}

func strNowIf(ok bool) *string {
	if !ok {
		return nil
	}
	s := time.Now().UTC().Format(time.RFC3339)
	return &s
}

// HandleAnomaly 异常上报：captcha / logged_out 触发全局暂停与控制台告警。
func (o *Orchestrator) HandleAnomaly(typ string, payload json.RawMessage) {
	detail := string(payload)
	_ = o.store.AddAuditEvent(typ, detail)
	switch typ {
	case "captcha", "logged_out":
		o.logger.Warn("page anomaly, pausing globally", "type", typ)
		o.guard.Pause(typ)
		o.pauseDeliver("anomaly:" + typ)
		_ = o.store.AddTaskLog("guard", "pause", typ)
	default:
		o.logger.Info("page anomaly reported", "type", typ, "detail", detail)
	}
}

// HandleChatMessage 新消息事件：落库 → 规则/LLM 生成草稿 → 审核或直发。
func (o *Orchestrator) HandleChatMessage(convKey, direction, content, msgType, ts string) {
	conv, err := o.store.GetOrCreateConversation(convKey, "", nil)
	if err != nil {
		o.logger.Error("upsert conversation failed", "conv_key", convKey, "err", err)
		return
	}
	if _, err := o.store.InsertMessage(conv.ID, direction, content, msgType, "", ts); err != nil {
		o.logger.Error("insert message failed", "err", err)
		return
	}
	if direction != "in" {
		return // 只应答 HR 发来的消息
	}
	o.answer(conv.ID, content)
}

// answer 应答回路：规则优先，未命中走 LLM；全自动模式下 rule 直发。
func (o *Orchestrator) answer(convID int64, content string) {
	o.mu.Lock()
	lib, auto := o.lib, o.autoLLM
	o.mu.Unlock()

	if lib == nil {
		lib = rule.NewLibrary(nil)
	}
	if r, ok := lib.Match(content); ok {
		if auto {
			// 全自动：白名单类（rule 命中）直发
			conv, err := o.store.GetConversation(convID)
			if err == nil && o.gw.SendChat(conv.ConvKey, r.Answer) {
				o.store.InsertMessage(convID, "out", r.Answer, "text", "", "")
				o.store.AddTaskLog("answer", "auto_send", "rule hit: "+r.Pattern)
				return
			}
			// 插件离线：落入审核队列，人工可见
		}
		if _, err := o.review.Create(convID, "rule", r.Answer); err != nil {
			o.logger.Error("create rule draft failed", "err", err)
		}
		return
	}

	// LLM 路径
	if err := o.guard.AllowLLMReply(); err != nil {
		o.logger.Warn("llm reply rejected by guard", "err", err)
		o.gw.BroadcastConsole(gateway.EvGuardAlert, map[string]any{"reason_code": "manual_answer", "conv_id": convID})
		return
	}
	o.mu.Lock()
	cfg := o.llmCfg
	o.mu.Unlock()
	client := llm.NewClient(cfg, 90*time.Second, o.logger)

	resume, _ := o.store.GetResumeContext()
	hist := o.history(convID, 10)
	prompt := llm.AssemblePrompt(resume, "", hist, content, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	reply, err := client.Complete(ctx, llm.Request{System: llm.SystemPrompt(), Prompt: prompt}, false)
	cancel()
	if err != nil {
		// 失败/超时转人工：控制台提醒，不生成草稿
		o.logger.Warn("llm failed, fallback to human", "err", err)
		o.gw.BroadcastConsole(gateway.EvGuardAlert, map[string]any{"reason_code": "llm_failed", "conv_id": convID, "message": "请人工回复该消息"})
		return
	}
	o.guard.RecordLLMReply()
	if _, err := o.review.Create(convID, "llm", llm.PostProcess(reply)); err != nil {
		o.logger.Error("create llm draft failed", "err", err)
	}
}

// history 取最近 N 条对话转为 LLM 消息（时间升序，role: in→user, out→assistant）。
func (o *Orchestrator) history(convID int64, n int) []llm.ChatMessage {
	msgs, err := o.store.ListMessages(convID)
	if err != nil {
		return nil
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	out := make([]llm.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		if m.Direction == "out" {
			role = "assistant"
		}
		out = append(out, llm.ChatMessage{Role: role, Content: m.Content})
	}
	return out
}

// ---------- 投递任务状态机 ----------

// StartDeliver 启动（或替换）投递任务：先下发 jobs.scan，收到 jobs.list 后
// 过滤入队并串行投递（间隔由 guard 下发）。
func (o *Orchestrator) StartDeliver(spec DeliverTaskSpec) error {
	spec.Keyword = strings.TrimSpace(spec.Keyword)
	if spec.Keyword == "" {
		return fmt.Errorf("keyword required")
	}
	o.mu.Lock()
	if o.cancel != nil {
		o.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	o.state = StateRunning
	o.lastSpec = &spec
	o.queue = nil
	o.queued = make(map[string]bool)
	o.scanExhausted = false
	o.scanCount = 0
	o.mu.Unlock()
	o.store.AddTaskLog("deliver", "start", spec.Keyword)

	go o.deliverLoop(ctx, spec)
	return nil
}

// deliverLoop 投递主循环：请求扫描 → 等待 jobs.list → 逐职位串行投递。
func (o *Orchestrator) deliverLoop(ctx context.Context, spec DeliverTaskSpec) {
	for {
		o.mu.Lock()
		st := o.state
		o.mu.Unlock()
		if st != StateRunning {
			return
		}
		job, ok := o.popJob()
		if !ok {
			// 队列空：扫描无新增 → 任务完成
			o.mu.Lock()
			exhausted := o.scanExhausted
			o.mu.Unlock()
			if exhausted && o.scanCount > 0 {
				o.finishDone()
				return
			}
			if !o.requestScan(spec.Keyword) {
				o.pauseDeliver("plugin offline")
				return
			}
			// 等待 jobs.list 到达（超时视为无新增；插件翻页扫描暂不实现，TODO）
			timer := time.NewTimer(60 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-o.scanSignal:
				timer.Stop()
			case <-timer.C:
				o.mu.Lock()
				o.scanExhausted = true
				o.mu.Unlock()
			}
			continue
		}

		if err := o.guard.AllowDeliver(); err != nil {
			o.logger.Warn("deliver blocked by guard", "err", err)
			o.pauseDeliver(err.Error())
			o.gw.BroadcastConsole(gateway.EvGuardAlert, map[string]any{"reason_code": "guard", "message": "投递任务已暂停，请检查护栏状态"})
			return
		}
		if err := ctx.Err(); err != nil {
			return
		}
		greeting := o.renderGreeting(job)
		payload := map[string]any{
			"job_key":   job.JobKey,
			"job_title": job.Title,
			"company":   job.Company,
			"greeting":  greeting,
		}
		if !o.gw.SendToPlugin(gateway.EvDeliverNext, payload) {
			o.logger.Warn("plugin offline, deliver paused")
			o.pauseDeliver("plugin offline")
			return
		}
		o.guard.RecordDeliver()
		o.store.AddTaskLog("deliver", "deliver.next", fmt.Sprintf("job_key=%s title=%s", job.JobKey, job.Title))
		// deliver.result 通过 HandleDeliverResult 异步回执落库；状态机只负责节奏与串行
		_ = o.wait(ctx, o.guard.NextDelay())
	}
}

// popJob 取出队首职位；队列空返回 ok=false。
func (o *Orchestrator) popJob() (store.Job, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.queue) == 0 {
		return store.Job{}, false
	}
	j := o.queue[0]
	o.queue = o.queue[1:]
	delete(o.queued, j.JobKey)
	return j, true
}

// requestScan 下发 jobs.scan；插件离线返回 false。
func (o *Orchestrator) requestScan(keyword string) bool {
	ok := o.gw.SendToPlugin(gateway.EvJobsScan, map[string]any{"keyword": keyword})
	if ok {
		o.store.AddTaskLog("deliver", "jobs.scan", keyword)
	}
	return ok
}

// finishDone 标记任务完成。
func (o *Orchestrator) finishDone() {
	o.mu.Lock()
	if o.state == StateRunning {
		o.state = StateDone
	}
	o.mu.Unlock()
	o.store.AddTaskLog("deliver", "done", "")
}

// pauseDeliver 将投递任务置为 paused。
func (o *Orchestrator) pauseDeliver(reason string) {
	o.mu.Lock()
	if o.state == StateRunning {
		o.state = StatePaused
		o.pauseErr = fmt.Errorf("%s", reason)
	}
	o.mu.Unlock()
	o.store.AddTaskLog("deliver", "pause", reason)
}

// Pause 全局暂停：guard 暂停 + 下发 guard.pause 给插件。
func (o *Orchestrator) Pause() {
	o.guard.Pause(guard.ReasonManual)
	o.gw.SendToPlugin(gateway.EvGuardPause, map[string]any{"reason_code": guard.ReasonManual})
	o.gw.BroadcastConsole(gateway.EvGuardPause, map[string]any{"reason_code": guard.ReasonManual})
	o.pauseDeliver(guard.ReasonManual)
}

// Resume 恢复：guard 恢复 + 下发 config.sync{paused:false} + 继续任务。
func (o *Orchestrator) Resume() {
	o.guard.Resume()
	o.SyncConfig()
	if o.lastSpec != nil {
		_ = o.StartDeliver(*o.lastSpec)
	}
	o.store.AddTaskLog("task", "resume", "")
}

// SyncConfig 下发 config.sync（含 paused 与人化间隔参数，§5.1 / 插件 ConfigSyncPayload）。
func (o *Orchestrator) SyncConfig() {
	snap := o.guard.Snapshot()
	cfg := o.guard.Config()
	payload := map[string]any{
		"paused":          snap.Paused,
		"min_interval_ms": cfg.MinIntervalSeconds * 1000,
		"jitter_ratio":    0.5, // 人化抖动比例：间隔 ± 50%（40s±20s）
		"daily_limit":     cfg.DailyLimitDeliver,
	}
	o.gw.SendToPlugin(gateway.EvConfigSync, payload)
	o.gw.BroadcastConsole(gateway.EvConfigSync, map[string]any{
		"paused":          snap.Paused,
		"min_interval_ms": cfg.MinIntervalSeconds * 1000,
	})
}

// StartPoller 启动 chat.poll 定时触发器（DOM 会话兜底扫描，每 60s；
// guard 暂停期间跳过）。随 ctx 结束退出。
func (o *Orchestrator) StartPoller(ctx context.Context) {
	go func() {
		t := time.NewTicker(o.pollEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if o.guard.Snapshot().Paused {
					continue
				}
				o.gw.SendToPlugin(gateway.EvChatPoll, map[string]any{})
			}
		}
	}()
}

// ---------- 招呼语渲染 ----------

// renderGreeting 渲染下一条招呼语：模板轮换（order 顺序 / random 随机）+
// JD 变量替换（{title} {company} {jd}，{jd} 截断 100 字）。
func (o *Orchestrator) renderGreeting(job store.Job) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	tmpl := "您好，看到这个岗位和我的经历比较匹配，希望能进一步沟通。"
	if len(o.greetings) > 0 {
		if o.strategy == "random" {
			tmpl = o.greetings[rand.IntN(len(o.greetings))]
		} else {
			tmpl = o.greetings[o.greetSeq%len(o.greetings)]
			o.greetSeq++
		}
	}
	return renderGreetingVars(tmpl, job)
}

// renderGreetingVars 模板变量替换：{title} {company} {jd}（{jd} 压缩空白并截断 100 字）。
func renderGreetingVars(tmpl string, job store.Job) string {
	jd := strings.Join(strings.Fields(job.JDText), " ")
	if r := []rune(jd); len(r) > 100 {
		jd = string(r[:100])
	}
	r := strings.NewReplacer(
		"{title}", job.Title,
		"{company}", job.Company,
		"{jd}", jd,
	)
	return r.Replace(tmpl)
}
