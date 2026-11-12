// Package review 实现审核队列：草稿 CRUD、approve（可改内容）/reject、
// approve 后经网关下发 chat.send。
package review

import (
	"errors"
	"log/slog"

	"github.com/linuxsuren/job-chat-copilot/internal/store"
)

// ErrInvalidTransition 暴露给 API 层映射稳定错误码。
var ErrInvalidTransition = errors.New("invalid draft transition")

// Sender 下发接口：由 gateway 提供，返回是否送达插件（异步发送结果由 deliver/chat 事件回执）。
type Sender interface {
	SendChat(convKey, content string) bool
}

// Service 审核队列服务。
type Service struct {
	store   *store.Store
	sender  Sender
	logger  *slog.Logger
	onReady func(draftID, convID int64) // 草稿就绪回调（draft.ready 事件）
	onCount func(pending int)           // review.stats 推送回调
}

// NewService 创建审核服务；onReady/onCount 可为 nil（网关注入）。
func NewService(st *store.Store, sender Sender, logger *slog.Logger, onReady func(draftID, convID int64), onCount func(pending int)) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: st, sender: sender, logger: logger, onReady: onReady, onCount: onCount}
}

// Create 创建待审草稿并触发 draft.ready 事件与 pending 统计推送。
func (s *Service) Create(convID int64, source, content string) (int64, error) {
	id, err := s.store.CreateDraft(convID, source, content)
	if err != nil {
		return 0, err
	}
	if s.onReady != nil {
		s.onReady(id, convID)
	}
	s.notifyCount()
	return id, nil
}

// Approve 审核通过：content 非空时覆盖草稿内容（仅 pending 可改），随后下发发送。
// 返回最终状态 sent / failed。
func (s *Service) Approve(id int64, content string) (string, error) {
	if content != "" {
		if err := s.store.UpdateDraftContent(id, content); err != nil {
			return "", err
		}
	}
	if err := s.store.UpdateDraftStatus(id, "approved"); err != nil {
		return "", ErrInvalidTransition
	}
	d, err := s.store.GetDraft(id)
	if err != nil {
		return "", err
	}
	conv, err := s.store.GetConversation(d.ConvID)
	if err != nil {
		return "", err
	}
	sent := false
	if s.sender != nil {
		sent = s.sender.SendChat(conv.ConvKey, d.Content)
	}
	final := "failed"
	if sent {
		final = "sent"
		// 我方发出的消息同步落库，保证会话时间线完整
		if _, err := s.store.InsertMessage(d.ConvID, "out", d.Content, "text", "", ""); err != nil {
			s.logger.Error("record outgoing message failed", "err", err)
		}
	} else {
		s.logger.Warn("chat.send not delivered, draft marked failed", "draft_id", id)
	}
	if err := s.store.UpdateDraftStatus(id, final); err != nil {
		return final, err
	}
	s.notifyCount()
	return final, nil
}

// Reject 驳回草稿。
func (s *Service) Reject(id int64) error {
	if err := s.store.UpdateDraftStatus(id, "rejected"); err != nil {
		return ErrInvalidTransition
	}
	s.notifyCount()
	return nil
}

func (s *Service) notifyCount() {
	if s.onCount == nil {
		return
	}
	if n, err := s.store.CountPendingDrafts(); err == nil {
		s.onCount(n)
	}
}
