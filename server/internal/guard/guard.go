// Package guard 实现风控护栏：操作最小间隔、每日上限、活跃时段、
// 验证码/掉登录全局暂停。护栏可调严、不可关闭（代码层面强制下限）。
package guard

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// 护栏硬边界（代码层面下限/上限，settings 只能在此区间内调严）。
const (
	MinIntervalSecondsFloor = 20  // 最小间隔不得低于 20s
	MaxDailyDeliver         = 50  // 每日投递上限不得高于 50
	MaxDailyReply           = 200 // 每日 LLM 回复上限不得高于 200
)

// 护栏暂停码（稳定，控制台展示用）。
const (
	ReasonManual      = "manual"
	ReasonCaptcha     = "captcha"
	ReasonLoggedOut   = "logged_out"
	ReasonInactiveDay = "inactive_hours"
	ReasonDailyLimit  = "daily_limit"
)

// Config 为护栏可调参数（来自 settings.guard）。
type Config struct {
	MinIntervalSeconds int    `json:"min_interval_seconds"`
	DailyLimitDeliver  int    `json:"daily_limit_deliver"`
	DailyLimitReply    int    `json:"daily_limit_reply"`
	ActiveStart        string `json:"active_start"` // "09:00"
	ActiveEnd          string `json:"active_end"`   // "20:00"
}

// DefaultConfig 返回设计文档 §4.3 的默认护栏参数。
func DefaultConfig() Config {
	return Config{
		MinIntervalSeconds: 40,
		DailyLimitDeliver:  MaxDailyDeliver,
		DailyLimitReply:    MaxDailyReply,
		ActiveStart:        "09:00",
		ActiveEnd:          "20:00",
	}
}

// Sanitize 将配置收敛到合法区间：只允许调严（间隔更长、上限更低）。
func Sanitize(c Config) Config {
	d := DefaultConfig()
	if c.MinIntervalSeconds < MinIntervalSecondsFloor {
		c.MinIntervalSeconds = MinIntervalSecondsFloor
	}
	if c.DailyLimitDeliver < 1 {
		c.DailyLimitDeliver = 1
	}
	if c.DailyLimitDeliver > d.DailyLimitDeliver {
		c.DailyLimitDeliver = d.DailyLimitDeliver
	}
	if c.DailyLimitReply < 1 {
		c.DailyLimitReply = 1
	}
	if c.DailyLimitReply > d.DailyLimitReply {
		c.DailyLimitReply = d.DailyLimitReply
	}
	if !validClock(c.ActiveStart) {
		c.ActiveStart = d.ActiveStart
	}
	if !validClock(c.ActiveEnd) {
		c.ActiveEnd = d.ActiveEnd
	}
	return c
}

func validClock(s string) bool {
	_, err := ParseClock(s)
	return err == nil
}

// ParseClock 解析 "HH:MM"。
func ParseClock(s string) (time.Duration, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h > 23 || m > 59 || h < 0 || m < 0 {
		return 0, fmt.Errorf("invalid clock %q", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

// Error 为护栏拒绝的稳定错误。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return "[" + e.Code + "] " + e.Message }

func errPaused(reason string) error {
	return &Error{Code: "guard_paused", Message: "护栏已暂停（" + reason + "），请人工处理后恢复"}
}

// Snapshot 供 /api/stats 使用。
type Snapshot struct {
	Paused            bool   `json:"paused"`
	ReasonCode        string `json:"reason_code"`
	DailyLimitDeliver int    `json:"daily_limit_deliver"`
	DailyLimitReply   int    `json:"daily_limit_reply"`
	DeliveredToday    int    `json:"-"`
	LLMRepliesToday   int    `json:"-"`
}

// Guard 并发安全。OnPause 用于向控制台推送告警事件。
type Guard struct {
	mu      sync.Mutex
	cfg     Config
	paused  bool
	reason  string
	day     string
	deliver int
	llm     int
	lastAt  time.Time
	now     func() time.Time
	onPause func(reason string)
}

// New 创建护栏；onPause 可为 nil。
func New(cfg Config, onPause func(reason string)) *Guard {
	return &Guard{cfg: Sanitize(cfg), onPause: onPause, now: time.Now}
}

// UpdateConfig 调整参数（内部仍会 Sanitize，只允许调严）。
func (g *Guard) UpdateConfig(cfg Config) {
	g.mu.Lock()
	g.cfg = Sanitize(cfg)
	g.mu.Unlock()
}

// Config 返回当前生效配置。
func (g *Guard) Config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg
}

// Pause 全局暂停，reason 为稳定暂停码。
func (g *Guard) Pause(reason string) {
	g.mu.Lock()
	already := g.paused
	g.paused = true
	g.reason = reason
	cb := g.onPause
	g.mu.Unlock()
	if !already && cb != nil {
		cb(reason)
	}
}

// Resume 恢复（仅人工通过控制台触发）。
func (g *Guard) Resume() {
	g.mu.Lock()
	g.paused = false
	g.reason = ""
	g.mu.Unlock()
}

// Snapshot 返回当前护栏状态与今日计数。
func (g *Guard) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay()
	return Snapshot{
		Paused:            g.paused,
		ReasonCode:        g.reason,
		DailyLimitDeliver: g.cfg.DailyLimitDeliver,
		DailyLimitReply:   g.cfg.DailyLimitReply,
		DeliveredToday:    g.deliver,
		LLMRepliesToday:   g.llm,
	}
}

// rollDay 在跨天后清零计数。调用方需持锁。
func (g *Guard) rollDay() {
	today := g.now().Format("2006-01-02")
	if g.day != today {
		g.day = today
		g.deliver, g.llm = 0, 0
	}
}

// AllowDeliver 校验当前是否允许发起一次投递动作。
func (g *Guard) AllowDeliver() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay()
	if g.paused {
		return errPaused(g.reason)
	}
	if !g.inActiveHours() {
		return &Error{Code: "guard_inactive_hours", Message: "当前不在活跃时段，任务已自动暂停"}
	}
	if g.deliver >= g.cfg.DailyLimitDeliver {
		return &Error{Code: "guard_daily_limit", Message: "已达每日投递上限，任务自动暂停"}
	}
	return nil
}

// NextDelay 返回下一次投递前应等待的时间（min_interval + [0, min_interval/2) 随机抖动，
// 默认即 40s ± 20s 人化间隔）。首次动作不等待。
func (g *Guard) NextDelay() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	jitter := time.Duration(g.cfg.MinIntervalSeconds/2) * time.Second
	base := time.Duration(g.cfg.MinIntervalSeconds) * time.Second
	if g.lastAt.IsZero() {
		return 0
	}
	wait := base - g.now().Sub(g.lastAt)
	if wait < 0 {
		wait = 0
	}
	return wait + time.Duration(rand.Int64N(int64(jitter)+1))
}

// RecordDeliver 记录一次投递动作。
func (g *Guard) RecordDeliver() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay()
	g.deliver++
	g.lastAt = g.now()
}

// AllowLLMReply 校验当日 LLM 回复额度。
func (g *Guard) AllowLLMReply() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay()
	if g.paused {
		return errPaused(g.reason)
	}
	if g.llm >= g.cfg.DailyLimitReply {
		return &Error{Code: "guard_daily_limit", Message: "已达每日回复上限，请人工作答"}
	}
	return nil
}

// RecordLLMReply 记录一次 LLM 回复。
func (g *Guard) RecordLLMReply() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay()
	g.llm++
}

// inActiveHours 判断当前时间是否在活跃时段。调用方需持锁。
func (g *Guard) inActiveHours() bool {
	start, err1 := ParseClock(g.cfg.ActiveStart)
	end, err2 := ParseClock(g.cfg.ActiveEnd)
	if err1 != nil || err2 != nil {
		return false
	}
	cur := time.Duration(g.now().Hour())*time.Hour + time.Duration(g.now().Minute())*time.Minute
	if start <= end {
		return cur >= start && cur < end
	}
	// 跨午夜时段
	return cur >= start || cur < end
}

// Now 可注入时钟（测试用）。
func (g *Guard) Now(f func() time.Time) {
	g.mu.Lock()
	g.now = f
	g.mu.Unlock()
}
