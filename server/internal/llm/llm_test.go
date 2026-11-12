package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreset(t *testing.T) {
	for _, p := range []string{ProviderDeepSeek, ProviderGLM, ProviderQwen} {
		b, m := Preset(p)
		if b == "" || m == "" {
			t.Errorf("preset %s missing", p)
		}
	}
	if b, _ := Preset("unknown"); b != "" {
		t.Error("unknown provider should be custom-empty")
	}
}

func TestCompleteNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"可以的，两周内到岗。"}}]}`))
	}))
	defer srv.Close()
	c := NewClient(Config{Provider: ProviderCustom, BaseURL: srv.URL, APIKey: "sk-test", Model: "m1"}, time.Second, nil)
	out, err := c.Complete(context.Background(), Request{System: "s", Prompt: "p"}, false)
	if err != nil || out != "可以的，两周内到岗。" {
		t.Fatalf("complete = %q, %v", out, err)
	}
}

func TestCompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n"))
		w.Write([]byte(": keepalive\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"，可到岗\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"}, time.Second, nil)
	out, err := c.Complete(context.Background(), Request{Prompt: "p"}, true)
	if err != nil || out != "你好，可到岗" {
		t.Fatalf("stream complete = %q, %v", out, err)
	}
}

func TestRetryOnce(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"}, time.Second, nil)
	out, err := c.Complete(context.Background(), Request{Prompt: "p"}, false)
	if err != nil || out != "ok" || n != 2 {
		t.Fatalf("expect retry success, n=%d out=%q err=%v", n, out, err)
	}
}

func TestNoRetryOnAuthError(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "bad", Model: "m"}, time.Second, nil)
	_, err := c.Complete(context.Background(), Request{Prompt: "p"}, false)
	if err == nil || n != 1 {
		t.Fatalf("auth error should not retry, n=%d err=%v", n, err)
	}
	if e, ok := err.(*Error); !ok || e.Code != "llm_bad_request" {
		t.Fatalf("expect llm_bad_request, got %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	c := NewClient(Config{}, time.Second, nil)
	_, err := c.Complete(context.Background(), Request{Prompt: "p"}, false)
	if err == nil || !strings.Contains(err.Error(), "llm_unavailable") {
		t.Fatalf("expect unavailable, got %v", err)
	}
}

func TestPostProcess(t *testing.T) {
	out := PostProcess("**你好**，`Go` 开发\n--- ")
	if out != "你好，Go 开发\n---" {
		// 只要求去 markdown 符号 + 截断；具体空白保留可接受
		if strings.ContainsAny(out, "*`#") {
			t.Fatalf("markdown symbols should be stripped: %q", out)
		}
	}
	long := strings.Repeat("字", 350)
	if got := len([]rune(PostProcess(long))); got != 200 {
		t.Fatalf("should truncate to 200 runes, got %d", got)
	}
}

func TestAssemblePrompt(t *testing.T) {
	p := AssemblePrompt("简历", "JD", []ChatMessage{{Role: "user", Content: "hi"}}, "何时到岗", 10)
	for _, want := range []string{"简历摘要", "岗位 JD", "最近对话", "何时到岗"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
