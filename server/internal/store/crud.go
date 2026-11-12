package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ---------- settings ----------

// GetSetting 读取配置值；不存在返回 ErrNotFound。
func (s *Store) GetSetting(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting 写入配置值（upsert）。
func (s *Store) SetSetting(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO settings(k,v) VALUES(?,?)
		ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// ---------- resume context ----------

// GetResumeContext 返回当前（最新一条）简历上下文。
func (s *Store) GetResumeContext() (string, error) {
	var content string
	err := s.db.QueryRow(`SELECT content FROM resume_context ORDER BY id DESC LIMIT 1`).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return content, err
}

// SaveResumeContext 新增一版简历上下文。
func (s *Store) SaveResumeContext(content string) error {
	_, err := s.db.Exec(`INSERT INTO resume_context(content, created_at) VALUES(?,?)`, content, nowUTC())
	return err
}

// ---------- rule ----------

type Rule struct {
	ID      int64  `json:"id"`
	Pattern string `json:"pattern"`
	Answer  string `json:"answer"`
	Enabled bool   `json:"enabled"`
}

// ListRules 返回全部规则（按 id 升序）。
func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT id, pattern, answer, enabled FROM rule ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var en int
		if err := rows.Scan(&r.ID, &r.Pattern, &r.Answer, &en); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRule 新增规则。
func (s *Store) CreateRule(pattern, answer string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO rule(pattern, answer, enabled, created_at, updated_at)
		VALUES(?,?,1,?,?)`, pattern, answer, nowUTC(), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteRule 删除规则。
func (s *Store) DeleteRule(id int64) error {
	res, err := s.db.Exec(`DELETE FROM rule WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- job ----------

type Job struct {
	ID          int64   `json:"id"`
	JobKey      string  `json:"job_key"`
	Title       string  `json:"title"`
	Company     string  `json:"company"`
	Salary      string  `json:"salary"`
	Status      string  `json:"status"` // pending / delivered / failed / skipped
	JDText      string  `json:"jd_text"`
	DeliveredAt *string `json:"delivered_at"`
}

// GetJob 按 job_key 查询；不存在返回 ErrNotFound。
func (s *Store) GetJob(jobKey string) (Job, error) {
	var j Job
	err := s.db.QueryRow(`SELECT id, job_key, title, company, salary, status, jd_text, delivered_at FROM job WHERE job_key=?`, jobKey).
		Scan(&j.ID, &j.JobKey, &j.Title, &j.Company, &j.Salary, &j.Status, &j.JDText, &j.DeliveredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// UpdateJobStatus 按 job_key 更新投递状态（不覆盖职位元数据）。
func (s *Store) UpdateJobStatus(jobKey, status string, deliveredAt *string) error {
	d := any(nil)
	if deliveredAt != nil {
		d = *deliveredAt
	}
	res, err := s.db.Exec(`UPDATE job SET status=?, delivered_at=? WHERE job_key=?`, status, d, jobKey)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertJob 按 job_key 插入或更新职位（外部可见标识为 job_key）。
func (s *Store) UpsertJob(j Job) (int64, error) {
	if j.Status == "" {
		j.Status = "pending"
	}
	delivered := any(nil)
	if j.DeliveredAt != nil {
		delivered = *j.DeliveredAt
	} else if j.Status == "delivered" {
		now := nowUTC()
		j.DeliveredAt = &now
		delivered = now
	}
	res, err := s.db.Exec(`INSERT INTO job(job_key,title,company,salary,status,jd_text,delivered_at,created_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(job_key) DO UPDATE SET
			title=excluded.title, company=excluded.company, salary=excluded.salary,
			jd_text=CASE WHEN excluded.jd_text != '' THEN excluded.jd_text ELSE job.jd_text END,
			status=excluded.status, delivered_at=excluded.delivered_at`,
		j.JobKey, j.Title, j.Company, j.Salary, j.Status, j.JDText, delivered, nowUTC())
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return res.LastInsertId()
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM job WHERE job_key=?`, j.JobKey).Scan(&id)
	return id, err
}

// Page 分页参数。
type Page struct {
	Page     int // 从 1 开始
	PageSize int
}

// Normalize 收敛分页参数。
func (p Page) Normalize() (limit, offset int) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p.PageSize, (p.Page - 1) * p.PageSize
}

// ListJobs 按 status 过滤分页；status 为空返回全部。total 为符合条件总数。
func (s *Store) ListJobs(status string, p Page) (items []Job, total int, err error) {
	where, args := "", []any{}
	if status != "" {
		where = "WHERE status=?"
		args = append(args, status)
	}
	if err = s.db.QueryRow(`SELECT COUNT(1) FROM job `+where, args...).Scan(&total); err != nil {
		return
	}
	limit, offset := p.Normalize()
	q := `SELECT id, job_key, title, company, salary, status, jd_text, delivered_at FROM job ` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, limit, offset)...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.JobKey, &j.Title, &j.Company, &j.Salary, &j.Status, &j.JDText, &j.DeliveredAt); err != nil {
			return
		}
		items = append(items, j)
	}
	return items, total, rows.Err()
}

// CountDeliveredToday 统计今日投递成功数。
func (s *Store) CountDeliveredToday() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM job WHERE status='delivered' AND delivered_at LIKE ?`, todayPrefix()+"%").Scan(&n)
	return n, err
}

// ---------- conversation ----------

type Conversation struct {
	ID        int64   `json:"id"`
	ConvKey   string  `json:"conv_key"`
	HRName    string  `json:"hr_name"`
	JobID     *int64  `json:"job_id"`
	LastMsgAt *string `json:"last_msg_at"`
}

// GetOrCreateConversation 按 conv_key 获取会话，不存在则创建。
func (s *Store) GetOrCreateConversation(convKey, hrName string, jobID *int64) (Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(`SELECT id, conv_key, hr_name, job_id, last_msg_at FROM conversation WHERE conv_key=?`, convKey).
		Scan(&c.ID, &c.ConvKey, &c.HRName, &c.JobID, &c.LastMsgAt)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := s.db.Exec(`INSERT INTO conversation(conv_key, hr_name, job_id, created_at) VALUES(?,?,?,?)`, convKey, hrName, jobID, nowUTC())
		if ierr != nil {
			return c, ierr
		}
		id, _ := res.LastInsertId()
		c = Conversation{ID: id, ConvKey: convKey, HRName: hrName, JobID: jobID}
		return c, nil
	}
	if err == nil && hrName != "" && c.HRName == "" {
		s.db.Exec(`UPDATE conversation SET hr_name=? WHERE id=?`, hrName, c.ID)
		c.HRName = hrName
	}
	return c, err
}

// GetConversation 按 id 查询。
func (s *Store) GetConversation(id int64) (Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(`SELECT id, conv_key, hr_name, job_id, last_msg_at FROM conversation WHERE id=?`, id).
		Scan(&c.ID, &c.ConvKey, &c.HRName, &c.JobID, &c.LastMsgAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// ListConversations 分页返回会话（按最近消息排序）。
func (s *Store) ListConversations(p Page) (items []Conversation, total int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(1) FROM conversation`).Scan(&total); err != nil {
		return
	}
	limit, offset := p.Normalize()
	rows, err := s.db.Query(`SELECT id, conv_key, hr_name, job_id, last_msg_at FROM conversation
		ORDER BY COALESCE(last_msg_at, created_at) DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c Conversation
		if err = rows.Scan(&c.ID, &c.ConvKey, &c.HRName, &c.JobID, &c.LastMsgAt); err != nil {
			return
		}
		items = append(items, c)
	}
	return items, total, rows.Err()
}

// ---------- message ----------

type Message struct {
	ID        int64  `json:"id"`
	ConvID    int64  `json:"conv_id"`
	Direction string `json:"direction"` // in / out
	Content   string `json:"content"`
	MsgType   string `json:"msg_type"`
	SentAt    string `json:"sent_at"`
}

// InsertMessage 落库一条消息并刷新会话 last_msg_at。
func (s *Store) InsertMessage(convID int64, direction, content, msgType, rawHint, sentAt string) (int64, error) {
	if msgType == "" {
		msgType = "text"
	}
	if sentAt == "" {
		sentAt = nowUTC()
	}
	res, err := s.db.Exec(`INSERT INTO message(conv_id, direction, content, msg_type, raw_hint, sent_at)
		VALUES(?,?,?,?,?,?)`, convID, direction, content, msgType, rawHint, sentAt)
	if err != nil {
		return 0, err
	}
	s.db.Exec(`UPDATE conversation SET last_msg_at=? WHERE id=?`, sentAt, convID)
	return res.LastInsertId()
}

// ListMessages 按 id 升序返回会话消息。
func (s *Store) ListMessages(convID int64) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, conv_id, direction, content, msg_type, sent_at FROM message
		WHERE conv_id=? ORDER BY id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Direction, &m.Content, &m.MsgType, &m.SentAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountRepliedToday 统计今日我方发出消息数（today_replied 口径）。
func (s *Store) CountRepliedToday() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM message WHERE direction='out' AND sent_at LIKE ?`, todayPrefix()+"%").Scan(&n)
	return n, err
}

// ---------- draft ----------

type Draft struct {
	ID        int64   `json:"id"`
	ConvID    int64   `json:"conv_id"`
	Source    string  `json:"source"` // rule / llm / human
	Content   string  `json:"content"`
	Status    string  `json:"status"` // pending / approved / rejected / sent / failed
	CreatedAt string  `json:"created_at"`
	SentAt    *string `json:"sent_at"`
}

// CreateDraft 新建待审草稿。
func (s *Store) CreateDraft(convID int64, source, content string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO draft(conv_id, source, content, status, created_at)
		VALUES(?,?,?,'pending',?)`, convID, source, content, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetDraft 按 id 查询。
func (s *Store) GetDraft(id int64) (Draft, error) {
	var d Draft
	err := s.db.QueryRow(`SELECT id, conv_id, source, content, status, created_at, sent_at FROM draft WHERE id=?`, id).
		Scan(&d.ID, &d.ConvID, &d.Source, &d.Content, &d.Status, &d.CreatedAt, &d.SentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// ListDrafts 按 status 过滤分页。
func (s *Store) ListDrafts(status string, p Page) (items []Draft, total int, err error) {
	where, args := "", []any{}
	if status != "" {
		where = "WHERE status=?"
		args = append(args, status)
	}
	if err = s.db.QueryRow(`SELECT COUNT(1) FROM draft `+where, args...).Scan(&total); err != nil {
		return
	}
	limit, offset := p.Normalize()
	rows, err := s.db.Query(`SELECT id, conv_id, source, content, status, created_at, sent_at FROM draft `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var d Draft
		if err = rows.Scan(&d.ID, &d.ConvID, &d.Source, &d.Content, &d.Status, &d.CreatedAt, &d.SentAt); err != nil {
			return
		}
		items = append(items, d)
	}
	return items, total, rows.Err()
}

// CountPendingDrafts 返回待审草稿数。
func (s *Store) CountPendingDrafts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM draft WHERE status='pending'`).Scan(&n)
	return n, err
}

// UpdateDraftStatus 状态机迁移：pending→approved/rejected，approved→sent/failed。
func (s *Store) UpdateDraftStatus(id int64, next string) error {
	d, err := s.GetDraft(id)
	if err != nil {
		return err
	}
	allowed := map[string][]string{
		"pending":   {"approved", "rejected"},
		"approved":  {"sent", "failed"},
		"rejected":  {},
		"sent":      {},
		"failed":    {},
	}
	ok := false
	for _, n := range allowed[d.Status] {
		if n == next {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("invalid transition %s -> %s", d.Status, next)
	}
	sentAt := any(nil)
	if next == "sent" {
		v := nowUTC()
		sentAt = v
	}
	_, err = s.db.Exec(`UPDATE draft SET status=?, sent_at=? WHERE id=?`, next, sentAt, id)
	return err
}

// UpdateDraftContent 审核通过时允许修改内容（仅 pending 可改）。
func (s *Store) UpdateDraftContent(id int64, content string) error {
	res, err := s.db.Exec(`UPDATE draft SET content=? WHERE id=? AND status='pending'`, content, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- task log / audit ----------

// AddTaskLog 记录任务动作日志。
func (s *Store) AddTaskLog(taskType, action, detail string) error {
	_, err := s.db.Exec(`INSERT INTO task_log(task_type, action, detail, created_at) VALUES(?,?,?,?)`, taskType, action, detail, nowUTC())
	return err
}

// AddAuditEvent 记录关键审计事件（验证码/暂停等）。
func (s *Store) AddAuditEvent(typ, detail string) error {
	_, err := s.db.Exec(`INSERT INTO audit_event(type, detail, created_at) VALUES(?,?,?)`, typ, detail, nowUTC())
	return err
}
