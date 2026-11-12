package review

import (
	"testing"

	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

type fakeSender struct {
	sent     []string
	available bool
}

func (f *fakeSender) SendChat(convKey, content string) bool {
	if !f.available {
		return false
	}
	f.sent = append(f.sent, convKey+"|"+content)
	return true
}

func setup(t *testing.T) (*Service, *fakeSender, int64) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	conv, err := st.GetOrCreateConversation("conv-1", "HR", nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{available: true}
	var ready []int64
	svc := NewService(st, fs, nil, func(id, _ int64) { ready = append(ready, id) }, nil)
	return svc, fs, conv.ID
}

func TestApproveSends(t *testing.T) {
	svc, fs, convID := setup(t)
	id, err := svc.Create(convID, "rule", "原始答案")
	if err != nil {
		t.Fatal(err)
	}
	// approve 时修改内容
	final, err := svc.Approve(id, "修改后的答案")
	if err != nil || final != "sent" {
		t.Fatalf("approve = %s, %v", final, err)
	}
	if len(fs.sent) != 1 || fs.sent[0] != "conv-1|修改后的答案" {
		t.Fatalf("sender got %v", fs.sent)
	}
	// 消息落库
	msgs, _ := svc.store.ListMessages(convID)
	if len(msgs) != 1 || msgs[0].Content != "修改后的答案" {
		t.Fatalf("outgoing message should be recorded: %+v", msgs)
	}
	// 已 sent 不能再 approve
	if _, err := svc.Approve(id, ""); err != ErrInvalidTransition {
		t.Fatalf("re-approve should fail: %v", err)
	}
}

func TestApproveFailedWhenPluginDown(t *testing.T) {
	svc, fs, convID := setup(t)
	fs.available = false
	id, _ := svc.Create(convID, "llm", "答案")
	final, err := svc.Approve(id, "")
	if err != nil || final != "failed" {
		t.Fatalf("approve with plugin down = %s, %v", final, err)
	}
}

func TestReject(t *testing.T) {
	svc, _, convID := setup(t)
	id, _ := svc.Create(convID, "llm", "答案")
	if err := svc.Reject(id); err != nil {
		t.Fatal(err)
	}
	// rejected 终态：不能再 approve / reject
	if _, err := svc.Approve(id, ""); err != ErrInvalidTransition {
		t.Fatalf("approve rejected draft: %v", err)
	}
	if err := svc.Reject(id); err != ErrInvalidTransition {
		t.Fatalf("reject twice: %v", err)
	}
}

func TestDraftReadyCallback(t *testing.T) {
	var events []int64
	st, _ := store.OpenMemory()
	defer st.Close()
	conv, _ := st.GetOrCreateConversation("c", "h", nil)
	svc := NewService(st, &fakeSender{}, nil, func(id, _ int64) { events = append(events, id) }, nil)
	id1, _ := svc.Create(conv.ID, "rule", "a")
	id2, _ := svc.Create(conv.ID, "llm", "b")
	if len(events) != 2 || events[0] != id1 || events[1] != id2 {
		t.Fatalf("draft.ready events = %v", events)
	}
}
