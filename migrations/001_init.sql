CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL,                     -- gmail | 163 | qq | outlook | custom
    auth_type TEXT NOT NULL DEFAULT 'password', -- password | oauth2
    imap_host TEXT NOT NULL,
    imap_port INTEGER NOT NULL DEFAULT 993,
    imap_tls TEXT NOT NULL DEFAULT 'implicit',  -- implicit | starttls
    smtp_host TEXT NOT NULL,
    smtp_port INTEGER NOT NULL DEFAULT 465,
    smtp_tls TEXT NOT NULL DEFAULT 'implicit',  -- implicit | starttls
    credentials_enc BLOB NOT NULL,              -- AES-256-GCM 加密的 JSON
    proxy_mode TEXT NOT NULL DEFAULT 'direct',  -- global | direct | http | https | socks5
    proxy_url_enc BLOB,                         -- AES-256-GCM 加密的代理 URL
    status TEXT NOT NULL DEFAULT 'active',      -- active | error | paused
    last_error TEXT NOT NULL DEFAULT '',
    last_sync_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS folders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'other',         -- inbox | sent | archive | trash | drafts | other
    uid_validity INTEGER NOT NULL DEFAULT 0,
    last_uid INTEGER NOT NULL DEFAULT 0,
    unread_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(account_id, name)
);

CREATE TABLE IF NOT EXISTS emails (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    message_id TEXT NOT NULL DEFAULT '',
    in_reply_to TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    from_addr TEXT NOT NULL DEFAULT '',
    to_addrs TEXT NOT NULL DEFAULT '[]',
    cc_addrs TEXT NOT NULL DEFAULT '[]',
    snippet TEXT NOT NULL DEFAULT '',           -- 纯文本摘要，最多 500 字符
    has_attachments INTEGER NOT NULL DEFAULT 0,
    is_read INTEGER NOT NULL DEFAULT 0,
    is_starred INTEGER NOT NULL DEFAULT 0,
    date INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(account_id, folder_id, uid)
);

CREATE INDEX IF NOT EXISTS idx_emails_date ON emails(date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_emails_account_date ON emails(account_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_emails_unread ON emails(is_read, date DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS emails_fts USING fts5(
    subject, from_name, from_addr, snippet,
    content='emails', content_rowid='id', tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS emails_ai AFTER INSERT ON emails BEGIN
    INSERT INTO emails_fts(rowid, subject, from_name, from_addr, snippet)
    VALUES (new.id, new.subject, new.from_name, new.from_addr, new.snippet);
END;

CREATE TRIGGER IF NOT EXISTS emails_ad AFTER DELETE ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, from_name, from_addr, snippet)
    VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.snippet);
END;

CREATE TRIGGER IF NOT EXISTS emails_au AFTER UPDATE OF subject, from_name, from_addr, snippet ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, from_name, from_addr, snippet)
    VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.snippet);
    INSERT INTO emails_fts(rowid, subject, from_name, from_addr, snippet)
    VALUES (new.id, new.subject, new.from_name, new.from_addr, new.snippet);
END;

CREATE TABLE IF NOT EXISTS mcp_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,            -- SHA-256
    scope TEXT NOT NULL DEFAULT 'readonly',     -- readonly | readwrite
    require_approval INTEGER NOT NULL DEFAULT 1,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pending_actions (
    id TEXT PRIMARY KEY,
    token_id INTEGER NOT NULL REFERENCES mcp_tokens(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL,                  -- send | reply | forward
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',     -- pending | approved | rejected | executed | failed
    error_msg TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id INTEGER,
    tool_name TEXT NOT NULL,
    args_summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,                       -- success | denied | pending | error
    created_at INTEGER NOT NULL
);
