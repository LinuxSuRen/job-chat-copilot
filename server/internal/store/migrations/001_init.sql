-- job-chat-copilot 初始 schema（设计文档 §4.5）
CREATE TABLE settings (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);

CREATE TABLE resume_context (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE rule (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern TEXT NOT NULL,
    answer TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE job (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    salary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    delivered_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_job_status ON job(status);

CREATE TABLE conversation (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conv_key TEXT NOT NULL UNIQUE,
    hr_name TEXT NOT NULL DEFAULT '',
    job_id INTEGER REFERENCES job(id),
    last_msg_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE message (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conv_id INTEGER NOT NULL REFERENCES conversation(id),
    direction TEXT NOT NULL,          -- in: HR 发来 / out: 我方发出
    content TEXT NOT NULL,
    msg_type TEXT NOT NULL DEFAULT 'text',
    raw_hint TEXT NOT NULL DEFAULT '',
    sent_at TEXT NOT NULL
);
CREATE INDEX idx_message_conv ON message(conv_id, id);

CREATE TABLE draft (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conv_id INTEGER NOT NULL REFERENCES conversation(id),
    source TEXT NOT NULL CHECK (source IN ('rule','llm','human')),
    content TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','approved','rejected','sent','failed'))
        DEFAULT 'pending',
    created_at TEXT NOT NULL,
    sent_at TEXT
);
CREATE INDEX idx_draft_status ON draft(status);

CREATE TABLE task_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_type TEXT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE audit_event (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
