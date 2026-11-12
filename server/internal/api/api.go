// Package api 实现控制台 REST（§5.2）：统一 snake_case、
// 分页 page(从1)+page_size → data/page/total/page_size、
// 错误只返回 {code, message}（稳定 code，技术根因进日志）。
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/linuxsuren/job-chat-copilot/internal/browser"
	"github.com/linuxsuren/job-chat-copilot/internal/gateway"
	"github.com/linuxsuren/job-chat-copilot/internal/guard"
	"github.com/linuxsuren/job-chat-copilot/internal/llm"
	"github.com/linuxsuren/job-chat-copilot/internal/orchestrator"
	"github.com/linuxsuren/job-chat-copilot/internal/review"
	"github.com/linuxsuren/job-chat-copilot/internal/rule"
	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

// Server 聚合 REST 所需依赖。
type Server struct {
	Store *store.Store
	GW    *gateway.Gateway
	Guard *guard.Guard
	Orch  *orchestrator.Orchestrator
	Rev   *review.Service
	Brow  *browser.Manager
	Log   *slog.Logger
}

// Settings 为 GET/PUT /api/settings 的结构（与控制台契约一致）。
type Settings struct {
	LLM      llm.Config      `json:"llm"`
	Guard    guard.Config    `json:"guard"`
	Greeting GreetingConfig  `json:"greeting"`
	AutoMode bool            `json:"auto_mode,omitempty"`
}

// GreetingConfig 招呼语模板配置（多模板轮换，M4 策略：order/ random）。
type GreetingConfig struct {
	Templates []string `json:"templates"`
	Strategy  string   `json:"strategy"` // order / random
}

// settings 存储键
const (
	keyLLM      = "llm"
	keyGuard    = "guard"
	keyGreeting = "greeting"
	keyAutoMode = "auto_mode"
)

// LoadSettings 从 store 读取并补默认值。
func (s *Server) LoadSettings() Settings {
	out := Settings{Guard: guard.DefaultConfig(), Greeting: GreetingConfig{Templates: []string{}, Strategy: "order"}}
	if v, err := s.Store.GetSetting(keyLLM); err == nil {
		_ = json.Unmarshal([]byte(v), &out.LLM)
	}
	if v, err := s.Store.GetSetting(keyGuard); err == nil {
		_ = json.Unmarshal([]byte(v), &out.Guard)
	}
	if v, err := s.Store.GetSetting(keyGreeting); err == nil {
		_ = json.Unmarshal([]byte(v), &out.Greeting)
	}
	if v, err := s.Store.GetSetting(keyAutoMode); err == nil {
		out.AutoMode = v == "true"
	}
	out.Guard = guard.Sanitize(out.Guard)
	return out
}

// Register 注册全部 REST 路由（含 WS 端点）。
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/resume-context", s.getResume)
	mux.HandleFunc("PUT /api/resume-context", s.putResume)
	mux.HandleFunc("GET /api/rules", s.listRules)
	mux.HandleFunc("POST /api/rules", s.createRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.deleteRule)
	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/conversations", s.listConversations)
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.listMessages)
	mux.HandleFunc("GET /api/drafts", s.listDrafts)
	mux.HandleFunc("PUT /api/drafts/{id}", s.putDraft)
	mux.HandleFunc("POST /api/tasks/deliver", s.postDeliver)
	mux.HandleFunc("POST /api/tasks/{action}", s.postTaskAction)
	mux.HandleFunc("GET /api/pairing", s.getPairing)
	mux.HandleFunc("GET /api/stats", s.getStats)
	mux.HandleFunc("GET /api/supervise/screenshot", s.getScreenshot)
	mux.HandleFunc("/ws/ext", s.GW.ServePluginWS)
	mux.HandleFunc("/api/ws", s.GW.ServeConsoleWS)
}

// ---------- 通用助手 ----------

type errResp struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) fail(w http.ResponseWriter, status int, code, msg string, cause error) {
	if cause != nil {
		s.Log.Warn("api error", "code", code, "cause", cause)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errResp{Code: code, Message: msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func parsePage(r *http.Request) store.Page {
	q := r.URL.Query()
	p := store.Page{Page: 1, PageSize: 20}
	if v, err := strconv.Atoi(q.Get("page")); err == nil {
		p.Page = v
	}
	if v, err := strconv.Atoi(q.Get("page_size")); err == nil {
		p.PageSize = v
	}
	return p
}

type pageResp struct {
	Data     any   `json:"data"`
	Page     int   `json:"page"`
	Total    int   `json:"total"`
	PageSize int   `json:"page_size"`
}

func respondPage(w http.ResponseWriter, items any, total int, p store.Page) {
	limit, offset := p.Normalize()
	_ = offset
	writeJSON(w, pageResp{Data: items, Page: p.Page, Total: total, PageSize: limit})
}

// ---------- settings ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.LoadSettings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, 400, "param_invalid", "请求参数格式不正确", err)
		return
	}
	cur := s.LoadSettings()
	// llm：api_key 为空表示保留原值
	if in.LLM.APIKey == "" {
		in.LLM.APIKey = cur.LLM.APIKey
	}
	in.Guard = guard.Sanitize(in.Guard)
	if b, err := json.Marshal(in.LLM); err == nil {
		s.Store.SetSetting(keyLLM, string(b))
	}
	if b, err := json.Marshal(in.Guard); err == nil {
		s.Store.SetSetting(keyGuard, string(b))
	}
	if b, err := json.Marshal(in.Greeting); err == nil {
		s.Store.SetSetting(keyGreeting, string(b))
	}
	s.Store.SetSetting(keyAutoMode, strconv.FormatBool(in.AutoMode))

	// 应用副作用
	s.Guard.UpdateConfig(in.Guard)
	s.Orch.SetLLMConfig(in.LLM.Normalize())
	s.Orch.SetAutoMode(in.AutoMode)
	s.Orch.SetGreeting(in.Greeting.Templates, in.Greeting.Strategy)
	s.Orch.SyncConfig() // 人化间隔参数变更即下发插件

	writeJSON(w, s.LoadSettings())
}

// ---------- resume ----------

func (s *Server) getResume(w http.ResponseWriter, r *http.Request) {
	content, err := s.Store.GetResumeContext()
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, map[string]string{"content": ""})
		return
	}
	if err != nil {
		s.fail(w, 500, "internal", "服务暂时不可用，请稍后重试", err)
		return
	}
	writeJSON(w, map[string]string{"content": content})
}

func (s *Server) putResume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, 400, "param_invalid", "请求参数格式不正确", err)
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		s.fail(w, 400, "param_invalid", "简历上下文不能为空", nil)
		return
	}
	if err := s.Store.SaveResumeContext(in.Content); err != nil {
		s.fail(w, 500, "internal", "保存失败，请稍后重试", err)
		return
	}
	writeJSON(w, map[string]string{"content": in.Content})
}

// ---------- rules ----------

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Store.ListRules()
	if err != nil {
		s.fail(w, 500, "internal", "读取规则失败，请稍后重试", err)
		return
	}
	if rules == nil {
		rules = []store.Rule{}
	}
	total := len(rules)
	respondPage(w, rules, total, parsePage(r))
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pattern string `json:"pattern"`
		Answer  string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, 400, "param_invalid", "请求参数格式不正确", err)
		return
	}
	if !rule.Validate(in.Pattern, in.Answer) {
		s.fail(w, 400, "rule_invalid", "规则不合法：正则表达式或答案有误", errors.New(in.Pattern))
		return
	}
	id, err := s.Store.CreateRule(in.Pattern, in.Answer)
	if err != nil {
		s.fail(w, 500, "internal", "保存规则失败，请稍后重试", err)
		return
	}
	s.Orch.ReloadRules()
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.DeleteRule(id); errors.Is(err, store.ErrNotFound) {
		s.fail(w, 404, "not_found", "规则不存在", err)
		return
	} else if err != nil {
		s.fail(w, 500, "internal", "删除失败，请稍后重试", err)
		return
	}
	s.Orch.ReloadRules()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- jobs / conversations / messages ----------

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.Store.ListJobs(r.URL.Query().Get("status"), parsePage(r))
	if err != nil {
		s.fail(w, 500, "internal", "读取投递记录失败，请稍后重试", err)
		return
	}
	if items == nil {
		items = []store.Job{}
	}
	respondPage(w, items, total, parsePage(r))
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.Store.ListConversations(parsePage(r))
	if err != nil {
		s.fail(w, 500, "internal", "读取会话列表失败，请稍后重试", err)
		return
	}
	if items == nil {
		items = []store.Conversation{}
	}
	respondPage(w, items, total, parsePage(r))
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := s.Store.GetConversation(id); errors.Is(err, store.ErrNotFound) {
		s.fail(w, 404, "not_found", "会话不存在", err)
		return
	}
	msgs, err := s.Store.ListMessages(id)
	if err != nil {
		s.fail(w, 500, "internal", "读取消息失败，请稍后重试", err)
		return
	}
	if msgs == nil {
		msgs = []store.Message{}
	}
	writeJSON(w, map[string]any{"data": msgs})
}

// ---------- drafts ----------

func (s *Server) listDrafts(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.Store.ListDrafts(r.URL.Query().Get("status"), parsePage(r))
	if err != nil {
		s.fail(w, 500, "internal", "读取草稿失败，请稍后重试", err)
		return
	}
	if items == nil {
		items = []store.Draft{}
	}
	respondPage(w, items, total, parsePage(r))
}

func (s *Server) putDraft(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Action  string `json:"action"` // approve / reject
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.fail(w, 400, "param_invalid", "请求参数格式不正确", err)
		return
	}
	switch in.Action {
	case "approve":
		final, err := s.Rev.Approve(id, in.Content)
		if errors.Is(err, review.ErrInvalidTransition) {
			s.fail(w, 409, "draft_state", "草稿当前状态不允许该操作", err)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, 404, "not_found", "草稿不存在", err)
			return
		}
		if err != nil {
			s.fail(w, 500, "internal", "审核操作失败，请稍后重试", err)
			return
		}
		if final == "failed" {
			s.fail(w, 503, "plugin_offline", "插件不在线，发送失败请稍后重试", nil)
			return
		}
		writeJSON(w, map[string]any{"status": final})
	case "reject":
		if err := s.Rev.Reject(id); errors.Is(err, review.ErrInvalidTransition) {
			s.fail(w, 409, "draft_state", "草稿当前状态不允许该操作", err)
			return
		} else if errors.Is(err, store.ErrNotFound) {
			s.fail(w, 404, "not_found", "草稿不存在", err)
			return
		} else if err != nil {
			s.fail(w, 500, "internal", "审核操作失败，请稍后重试", err)
			return
		}
		writeJSON(w, map[string]any{"status": "rejected"})
	default:
		s.fail(w, 400, "param_invalid", "action 必须是 approve 或 reject", nil)
	}
}

// ---------- tasks ----------

func (s *Server) postDeliver(w http.ResponseWriter, r *http.Request) {
	var spec orchestrator.DeliverTaskSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		s.fail(w, 400, "param_invalid", "请求参数格式不正确", err)
		return
	}
	if err := s.Orch.StartDeliver(spec); err != nil {
		s.fail(w, 400, "param_invalid", "投递任务启动失败：请填写搜索关键词", err)
		return
	}
	writeJSON(w, map[string]any{"state": s.Orch.State()})
}

func (s *Server) postTaskAction(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("action") {
	case "pause":
		s.Orch.Pause()
	case "resume":
		s.Orch.Resume()
	default:
		s.fail(w, 404, "not_found", "不支持的任务操作", nil)
		return
	}
	writeJSON(w, map[string]any{"state": s.Orch.State()})
}

// ---------- pairing / stats / screenshot ----------

func (s *Server) getPairing(w http.ResponseWriter, r *http.Request) {
	paired, code := s.GW.PairingStatus()
	writeJSON(w, map[string]any{"paired": paired, "pairing_code": code})
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	delivered, err1 := s.Store.CountDeliveredToday()
	replied, err2 := s.Store.CountRepliedToday()
	if err1 != nil || err2 != nil {
		s.fail(w, 500, "internal", "读取统计失败，请稍后重试", errors.Join(err1, err2))
		return
	}
	g := s.Guard.Snapshot()
	writeJSON(w, map[string]any{
		"today_delivered": delivered,
		"today_replied":   replied,
		"guard": map[string]any{
			"paused":              g.Paused,
			"reason_code":         g.ReasonCode,
			"daily_limit_deliver": g.DailyLimitDeliver,
			"daily_limit_reply":   g.DailyLimitReply,
		},
		"plugin_connected": s.GW.PluginConnected(),
	})
}

func (s *Server) getScreenshot(w http.ResponseWriter, r *http.Request) {
	img, err := s.Brow.Screenshot()
	if errors.Is(err, browser.ErrNotRunning) {
		s.fail(w, 503, "browser_not_running", "浏览器未启动，请先启动浏览器", err)
		return
	}
	if err != nil {
		s.fail(w, 500, "screenshot_failed", "截图失败，请稍后重试", err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(img)
}
