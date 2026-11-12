package store

import (
	"testing"
)

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	s := mustOpen(t)
	// 再开一个同路径文件库，验证迁移可重复执行
	s.Close()
	s2, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	// 再次手动跑 migrate 不应报错
	if err := s2.migrate(); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

func TestSettingsCRUD(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.GetSetting("llm"); err != ErrNotFound {
		t.Fatalf("expect ErrNotFound, got %v", err)
	}
	if err := s.SetSetting("llm", `{"provider":"deepseek"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("llm", `{"provider":"glm"}`); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetSetting("llm")
	if err != nil || v != `{"provider":"glm"}` {
		t.Fatalf("upsert failed: %v %s", err, v)
	}
}

func TestConversationMessageFlow(t *testing.T) {
	s := mustOpen(t)
	jobID, err := s.UpsertJob(Job{JobKey: "job-abc", Title: "Go 后端", Company: "某公司", Salary: "20-30K"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetOrCreateConversation("conv-1", "HR小张", &jobID)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := s.GetOrCreateConversation("conv-1", "", nil)
	if c2.ID != c.ID {
		t.Fatalf("get-or-create must be idempotent: %d vs %d", c.ID, c2.ID)
	}
	if _, err := s.InsertMessage(c.ID, "in", "什么时候到岗？", "text", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMessage(c.ID, "out", "两周内", "text", "", ""); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.ListMessages(c.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("expect 2 messages, got %d %v", len(msgs), err)
	}
	if msgs[0].Direction != "in" || msgs[1].Direction != "out" {
		t.Fatal("message order should follow insert order")
	}
	got, err := s.GetConversation(c.ID)
	if err != nil || got.LastMsgAt == nil {
		t.Fatalf("last_msg_at should be updated: %+v %v", got, err)
	}
	if n, _ := s.CountRepliedToday(); n != 1 {
		t.Fatalf("today replied = %d, want 1", n)
	}
}

func TestDraftLifecycle(t *testing.T) {
	s := mustOpen(t)
	c, _ := s.GetOrCreateConversation("conv-9", "HR", nil)
	id, err := s.CreateDraft(c.ID, "rule", "测试答案")
	if err != nil {
		t.Fatal(err)
	}
	// 非法迁移
	if err := s.UpdateDraftStatus(id, "sent"); err == nil {
		t.Fatal("pending->sent must be rejected")
	}
	if err := s.UpdateDraftContent(id, "改后内容"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDraftStatus(id, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDraftContent(id, "再改"); err == nil {
		t.Fatal("only pending draft content can be edited")
	}
	if err := s.UpdateDraftStatus(id, "rejected"); err == nil {
		t.Fatal("approved->rejected must be rejected")
	}
	if err := s.UpdateDraftStatus(id, "sent"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.GetDraft(id)
	if d.Status != "sent" || d.SentAt == nil {
		t.Fatalf("draft should be sent with sent_at: %+v", d)
	}
	// rejected 后终态
	id2, _ := s.CreateDraft(c.ID, "llm", "x")
	_ = s.UpdateDraftStatus(id2, "rejected")
	if err := s.UpdateDraftStatus(id2, "approved"); err == nil {
		t.Fatal("rejected is terminal")
	}
}

func TestListJobsPagination(t *testing.T) {
	s := mustOpen(t)
	for i := 0; i < 5; i++ {
		if _, err := s.UpsertJob(Job{JobKey: "j" + string(rune('a'+i)), Status: "delivered"}); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := s.ListJobs("", Page{Page: 2, PageSize: 2})
	if err != nil || total != 5 || len(items) != 2 {
		t.Fatalf("page2: total=%d len=%d err=%v", total, len(items), err)
	}
	if items[0].ID <= items[1].ID {
		t.Fatal("jobs should be ordered by id desc")
	}
	filtered, total, _ := s.ListJobs("delivered", Page{})
	if total != 5 || len(filtered) != 5 {
		t.Fatalf("filter by status failed: %d", total)
	}
	if n, _ := s.CountDeliveredToday(); n != 5 {
		t.Fatalf("delivered today = %d, want 5", n)
	}
}

func TestDraftNotFound(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.GetDraft(42); err != ErrNotFound {
		t.Fatalf("expect ErrNotFound, got %v", err)
	}
}

func TestRules(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.CreateRule(`到岗`, "两周内"); err != nil {
		t.Fatal(err)
	}
	rules, _ := s.ListRules()
	if len(rules) != 1 || rules[0].Pattern != `到岗` || !rules[0].Enabled {
		t.Fatalf("rules should exist: %+v", rules)
	}
	if err := s.DeleteRule(999); err != ErrNotFound {
		t.Fatalf("delete missing rule: %v", err)
	}
}
