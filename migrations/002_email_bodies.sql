-- On-demand, sanitized reading cache. No raw MIME or attachment bytes.
CREATE TABLE IF NOT EXISTS email_bodies (
    email_id INTEGER PRIMARY KEY REFERENCES emails(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 128),
    html TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    attachments_json TEXT NOT NULL DEFAULT '[]',
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0 AND size_bytes <= 5505024),
    stored_at INTEGER NOT NULL,
    last_access INTEGER NOT NULL,
    CHECK (length(CAST(html AS BLOB)) + length(CAST(text AS BLOB)) <= 5242880),
    CHECK (length(CAST(attachments_json AS BLOB)) <= 262144),
    CHECK (size_bytes = length(CAST(html AS BLOB)) + length(CAST(text AS BLOB)) + length(CAST(attachments_json AS BLOB)))
);
CREATE INDEX IF NOT EXISTS idx_email_bodies_access ON email_bodies(last_access DESC, email_id DESC);

CREATE TRIGGER IF NOT EXISTS emails_body_source_changed
AFTER UPDATE OF account_id, folder_id, uid ON emails
WHEN old.account_id <> new.account_id OR old.folder_id <> new.folder_id OR old.uid <> new.uid
BEGIN
    DELETE FROM email_bodies WHERE email_id = old.id;
END;

CREATE TRIGGER IF NOT EXISTS accounts_body_source_changed
AFTER UPDATE OF email, provider, imap_host, imap_port, imap_tls ON accounts
WHEN old.email <> new.email OR old.provider <> new.provider OR old.imap_host <> new.imap_host
    OR old.imap_port <> new.imap_port OR old.imap_tls <> new.imap_tls
BEGIN
    DELETE FROM email_bodies WHERE email_id IN (SELECT id FROM emails WHERE account_id = new.id);
END;
