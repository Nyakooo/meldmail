export interface Account {
 id: number; email: string; display_name: string; provider: string; auth_type: string;
 imap_host: string; imap_port: number; imap_tls: string; smtp_host: string; smtp_port: number; smtp_tls: string;
 proxy_mode: string; status: string; last_error: string; last_sync_at: number;
}
export interface Email {
 id: number; account_id: number; folder_id: number; folder_role: string; uid: number; subject: string; from_name: string; from_addr: string;
 to_addrs: string; cc_addrs: string; snippet: string; has_attachments: boolean; is_read: boolean; is_starred: boolean; date: number;
}
export interface EmailPage { emails: Email[]; next_cursor: string; total: number }
export interface Diagnostic { stage: string; ok: boolean; message: string }
export interface Attachment { part_id: string; filename: string; content_type: string; size: number }
export interface EmailContent extends Email { cached?: boolean; local?: boolean; cache_warning?: string; html: string; text: string; attachments: Attachment[] }
export interface EmailUpdate extends Partial<Email> { id: number; deleted?: boolean; archived?: boolean; pending?: boolean }
export type ComposeAction = 'send' | 'reply' | 'forward'
export interface MCPToken { id: number; name: string; scope: 'readonly' | 'readwrite'; require_approval: boolean; enabled: boolean; last_used_at: number; created_at: number }
export interface Approval { id: string; token_id: number; token_name: string; account_id: number; action_type: string; status: string; error_msg: string; created_at: number; expires_at: number; sender_email: string; body_available: boolean; payload: { account_id: number; email_id?: number; action: string; to: string[]; cc?: string[]; subject: string; text?: string; html?: string } }
export interface AuditEntry { id: number; token_id: number | null; tool_name: string; args_summary: string; status: string; created_at: number }
export interface AccountCount { id: number; email: string; display_name: string; total: number; unread: number }
export interface SystemStats {
 mail: { total: number; unread: number; accounts: AccountCount[] };
 resources: {
  timestamp: number;
  memory: { used_bytes: number; limit_bytes: number | null; source: string };
  cpu: { percent: number | null; limit_cores: number | null; source: string; interval_seconds: number };
  disk: { database_bytes: number; wal_bytes: number; shm_bytes: number; data_bytes: number; available: boolean; volume_total_bytes: number; volume_available_bytes: number; volume_known: boolean };
 };
}
