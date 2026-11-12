-- jobs.list 上报包含 jd_text（§5.1），职位表补充 JD 文本列（仅服务端排程/变量替换用）
ALTER TABLE job ADD COLUMN jd_text TEXT NOT NULL DEFAULT '';
