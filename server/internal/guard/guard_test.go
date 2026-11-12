package guard

import (
	"strings"
	"testing"
	"time"
)

func fakeGuard(t *testing.T, now time.Time) *Guard {
	t.Helper()
	g := New(DefaultConfig(), nil)
	g.Now(func() time.Time { return now })
	return g
}

func TestSanitizeClamps(t *testing.T) {
	c := Sanitize(Config{MinIntervalSeconds: 5, DailyLimitDeliver: 999, DailyLimitReply: 9999, ActiveStart: "bad", ActiveEnd: "25:00"})
	if c.MinIntervalSeconds != MinIntervalSecondsFloor {
		t.Errorf("min interval should clamp to floor, got %d", c.MinIntervalSeconds)
	}
	if c.DailyLimitDeliver != MaxDailyDeliver {
		t.Errorf("daily deliver should clamp to %d, got %d", MaxDailyDeliver, c.DailyLimitDeliver)
	}
	if c.DailyLimitReply != MaxDailyReply {
		t.Errorf("daily reply should clamp to %d, got %d", MaxDailyReply, c.DailyLimitReply)
	}
	if c.ActiveStart != "09:00" || c.ActiveEnd != "20:00" {
		t.Errorf("invalid clocks should fall back to defaults, got %q-%q", c.ActiveStart, c.ActiveEnd)
	}
	// 调严方向应保留
	c2 := Sanitize(Config{MinIntervalSeconds: 120, DailyLimitDeliver: 10, DailyLimitReply: 20, ActiveStart: "10:00", ActiveEnd: "18:00"})
	if c2.MinIntervalSeconds != 120 || c2.DailyLimitDeliver != 10 || c2.DailyLimitReply != 20 {
		t.Errorf("stricter config should be kept, got %+v", c2)
	}
}

func TestDailyDeliverLimit(t *testing.T) {
	g := fakeGuard(t, time.Date(2026, 11, 12, 10, 0, 0, 0, time.Local))
	g.UpdateConfig(Config{MinIntervalSeconds: 40, DailyLimitDeliver: 2, DailyLimitReply: 200, ActiveStart: "09:00", ActiveEnd: "20:00"})
	for i := 0; i < 2; i++ {
		if err := g.AllowDeliver(); err != nil {
			t.Fatalf("deliver %d should be allowed: %v", i, err)
		}
		g.RecordDeliver()
	}
	err := g.AllowDeliver()
	if err == nil || !strings.Contains(err.Error(), "guard_daily_limit") {
		t.Fatalf("third deliver should hit daily limit, got %v", err)
	}
	// 跨天后恢复
	g.Now(func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.Local) })
	if err := g.AllowDeliver(); err != nil {
		t.Fatalf("next day should reset, got %v", err)
	}
}

func TestPauseResume(t *testing.T) {
	paused := []string{}
	g := New(DefaultConfig(), func(reason string) { paused = append(paused, reason) })
	g.Now(func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local) })
	g.Pause(ReasonCaptcha)
	if err := g.AllowDeliver(); err == nil {
		t.Fatal("paused guard must reject deliver")
	}
	snap := g.Snapshot()
	if !snap.Paused || snap.ReasonCode != ReasonCaptcha {
		t.Fatalf("snapshot should reflect pause: %+v", snap)
	}
	if len(paused) != 1 || paused[0] != ReasonCaptcha {
		t.Fatalf("onPause callback should fire once, got %v", paused)
	}
	g.Pause(ReasonLoggedOut) // 重复暂停不重复告警
	if len(paused) != 1 {
		t.Fatalf("repeat pause should not re-alert, got %v", paused)
	}
	g.Resume()
	if err := g.AllowDeliver(); err != nil {
		t.Fatalf("after resume deliver should be allowed, got %v", err)
	}
}

func TestActiveHours(t *testing.T) {
	g := fakeGuard(t, time.Date(2026, 11, 12, 8, 0, 0, 0, time.Local)) // 08:00 < 09:00
	if err := g.AllowDeliver(); err == nil {
		t.Fatal("outside active hours should reject")
	}
	g.Now(func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local) })
	if err := g.AllowDeliver(); err != nil {
		t.Fatalf("inside active hours should allow, got %v", err)
	}
}

func TestNextDelayHumanized(t *testing.T) {
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local)
	g := fakeGuard(t, base)
	if d := g.NextDelay(); d != 0 {
		t.Fatalf("first action should have no delay, got %v", d)
	}
	g.RecordDeliver()
	g.Now(func() time.Time { return base.Add(10 * time.Second) })
	for i := 0; i < 50; i++ {
		d := g.NextDelay()
		// 已过 10s，min 40s → 剩余 30s + [0,20s) 抖动
		if d < 30*time.Second || d >= 50*time.Second {
			t.Fatalf("delay out of range: %v", d)
		}
	}
	g.Now(func() time.Time { return base.Add(2 * time.Minute) })
	if d := g.NextDelay(); d >= 20*time.Second {
		t.Fatalf("past due should only carry jitter, got %v", d)
	}
}

func TestLLMReplyLimit(t *testing.T) {
	g := fakeGuard(t, time.Date(2026, 11, 12, 12, 0, 0, 0, time.Local))
	g.UpdateConfig(Config{MinIntervalSeconds: 40, DailyLimitReply: 1, DailyLimitDeliver: 50, ActiveStart: "00:00", ActiveEnd: "23:59"})
	if err := g.AllowLLMReply(); err != nil {
		t.Fatalf("first reply allowed: %v", err)
	}
	g.RecordLLMReply()
	if err := g.AllowLLMReply(); err == nil {
		t.Fatal("second reply should hit limit")
	}
}
