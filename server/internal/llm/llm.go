// Package llm 实现 OpenAI 兼容 /chat/completions 客户端：
// DeepSeek / GLM / Qwen 三预设 + 自定义配置，net/http 实现，
// 支持 SSE 流式解析（内部聚合为完整文本使用），重试 1 次，超时/失败返回错误由调用方转人工。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// Provider 预设标识。
const (
	ProviderDeepSeek = "deepseek"
	ProviderGLM      = "glm"
	ProviderQwen     = "qwen"
	ProviderCustom   = "custom"
)

// Preset 返回预设的 base_url 与默认 model；未知 provider 返回 custom 空预设。
func Preset(provider string) (baseURL, model string) {
	switch provider {
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1", "deepseek-chat"
	case ProviderGLM:
		return "https://open.bigmodel.cn/api/paas/v4", "glm-4-flash"
	case ProviderQwen:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen-plus"
	default:
		return "", ""
	}
}

// Config 为 LLM 网关配置（settings.llm）。
type Config struct {
	Provider    string  `json:"provider"`
	BaseURL     string  `json:"base_url"`
	APIKey      string  `json:"api_key"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature,omitempty"`
}

// Normalize 补全预设默认值（custom 或显式覆盖时用填写的值）。
func (c Config) Normalize() Config {
	if c.Provider == "" {
		c.Provider = ProviderCustom
	}
	pb, pm := Preset(c.Provider)
	if c.BaseURL == "" {
		c.BaseURL = pb
	}
	if c.Model == "" {
		c.Model = pm
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return c
}

// Ready 判断是否具备调用条件。
func (c Config) Ready() bool {
	n := c.Normalize()
	return n.BaseURL != "" && n.APIKey != "" && n.Model != ""
}

// ChatMessage 一条对话消息。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request 一次补全请求：System 为人设约束，History 为最近对话（role: user/assistant）。
type Request struct {
	System  string
	Prompt  string // 本轮用户消息（含上下文组装结果）
	History []ChatMessage
}

// Client 是 OpenAI 兼容客户端。
type Client struct {
	cfg     Config
	http    *http.Client
	retries int
	logger  *slog.Logger
}

// NewClient 创建客户端；timeout 单次请求超时（整体含 1 次重试）。
func NewClient(cfg Config, timeout time.Duration, logger *slog.Logger) *Client {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{cfg: cfg.Normalize(), http: &http.Client{Timeout: timeout}, retries: 1, logger: logger}
}

type completionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type completionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Error 为 LLM 网关稳定错误。
type Error struct {
	Code    string // llm_unavailable / llm_timeout / llm_bad_request
	Message string
	Cause   error
}

func (e *Error) Error() string { return "[" + e.Code + "] " + e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// Complete 调用 /chat/completions 生成回复（内部重试 1 次）。流式与非流式均可，
// 由 stream 参数决定；内部聚合成完整文本返回。
func (c *Client) Complete(ctx context.Context, req Request, stream bool) (string, error) {
	if !c.cfg.Ready() {
		return "", &Error{Code: "llm_unavailable", Message: "大模型服务未配置，请先在设置中完成配置"}
	}
	msgs := make([]ChatMessage, 0, len(req.History)+2)
	if req.System != "" {
		msgs = append(msgs, ChatMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.History...)
	msgs = append(msgs, ChatMessage{Role: "user", Content: req.Prompt})

	body, _ := json.Marshal(completionRequest{
		Model:       c.cfg.Model,
		Messages:    msgs,
		Temperature: c.cfg.Temperature,
		Stream:      stream,
	})

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			// 指数退避 + 抖动，避免立即重试加重限流
			select {
			case <-time.After(time.Duration(500+rand.Int64N(500)) * time.Millisecond):
			case <-ctx.Done():
				return "", &Error{Code: "llm_timeout", Message: "大模型响应超时，请稍后重试", Cause: ctx.Err()}
			}
		}
		out, retryable, err := c.once(ctx, body, stream)
		if err == nil {
			return out, nil
		}
		lastErr = err
		var le *Error
		if !retryable || (errorsAs(err, &le) && (le.Code == "llm_bad_request")) {
			break
		}
		c.logger.Warn("llm attempt failed, may retry", "attempt", attempt+1, "err", err)
	}
	return "", lastErr
}

func errorsAs(err error, target **Error) bool {
	if e, ok := err.(*Error); ok {
		*target = e
		return true
	}
	return false
}

// once 执行单次请求；retryable 表示该错误值得重试（网络/429/5xx）。
func (c *Client) once(ctx context.Context, body []byte, stream bool) (out string, retryable bool, err error) {
	url := c.cfg.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, &Error{Code: "llm_unavailable", Message: "请求构造失败", Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", true, &Error{Code: "llm_unavailable", Message: "大模型服务暂时不可用，请稍后重试", Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", false, &Error{Code: "llm_bad_request", Message: "大模型鉴权失败，请检查配置", Cause: fmt.Errorf("status %d: %s", resp.StatusCode, b)}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", true, &Error{Code: "llm_unavailable", Message: "大模型服务繁忙，请稍后重试", Cause: fmt.Errorf("status %d: %s", resp.StatusCode, b)}
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", false, &Error{Code: "llm_bad_request", Message: "大模型请求被拒绝", Cause: fmt.Errorf("status %d: %s", resp.StatusCode, b)}
	}

	if stream {
		text, err := c.aggregateSSE(resp.Body)
		if err != nil {
			return "", true, &Error{Code: "llm_unavailable", Message: "大模型响应中断，请稍后重试", Cause: err}
		}
		return text, false, nil
	}

	var cr completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", true, &Error{Code: "llm_unavailable", Message: "大模型响应解析失败", Cause: err}
	}
	if cr.Error != nil {
		return "", false, &Error{Code: "llm_bad_request", Message: "大模型返回错误", Cause: fmt.Errorf("%s", cr.Error.Message)}
	}
	if len(cr.Choices) == 0 {
		return "", true, &Error{Code: "llm_unavailable", Message: "大模型返回为空，请稍后重试"}
	}
	return cr.Choices[0].Message.Content, false, nil
}

// aggregateSSE 聚合流式增量为完整文本。
func (c *Client) aggregateSSE(r io.Reader) (string, error) {
	var sb strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 跳过无法解析的块（如注释行）
		}
		for _, ch := range chunk.Choices {
			sb.WriteString(ch.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		return sb.String(), err
	}
	return sb.String(), nil
}

// SystemPrompt 返回 §4.4 的求职者人设约束。
func SystemPrompt() string {
	return "你是一名正在找工作的求职者，通过招聘平台与 HR 沟通。" +
		"严格约束：1) 只基于提供的简历事实回答，不得编造经历；" +
		"2) 简历中没有的信息，回复\"这个我确认后答复您\"类话术，不要猜测；" +
		"3) 不主动承诺具体薪资数字，除非用户预设中已给出；" +
		"4) 口吻自然、礼貌、简洁，符合中文聊天习惯；" +
		"5) 绝不透露或暗示你在使用任何自动回复工具。"
}

// AssemblePrompt 组装本轮输入：简历摘要 + 岗位 JD + 最近 N 条对话 + 本轮消息。
func AssemblePrompt(resumeContext, jobJD string, history []ChatMessage, current string, recentN int) string {
	if recentN <= 0 {
		recentN = 10
	}
	var b strings.Builder
	if resumeContext != "" {
		b.WriteString("【我的简历摘要】\n" + resumeContext + "\n\n")
	}
	if jobJD != "" {
		b.WriteString("【岗位 JD】\n" + jobJD + "\n\n")
	}
	if len(history) > recentN {
		history = history[len(history)-recentN:]
	}
	if len(history) > 0 {
		b.WriteString("【最近对话】\n")
		for _, m := range history {
			who := "HR"
			if m.Role == "assistant" {
				who = "我"
			}
			b.WriteString(who + "：" + m.Content + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("【HR 最新消息】\n" + current + "\n\n请以我的身份直接给出回复内容（不要任何解释、不要 markdown）。")
	return b.String()
}

// PostProcess 聊天场景输出后处理：去除 markdown 符号、按 rune 截断到 ≤200 字。
func PostProcess(s string) string {
	replacer := strings.NewReplacer("**", "", "*", "", "`", "", "#", "", "~~", "")
	s = replacer.Replace(s)
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}
