export interface Account {
 id: number; email: string; display_name: string; provider: string; auth_type: string;
 imap_host: string; imap_port: number; imap_tls: string; smtp_host: string; smtp_port: number; smtp_tls: string;
 proxy_mode: string; status: string; last_error: string; last_sync_at: number;
}
export interface Email {
 id: number; account_id: number; folder_id: number; uid: number; subject: string; from_name: string; from_addr: string;
 to_addrs: string; cc_addrs: string; snippet: string; has_attachments: boolean; is_read: boolean; is_starred: boolean; date: number;
}
export interface EmailPage { emails: Email[]; next_cursor: string }
export interface Diagnostic { stage: string; ok: boolean; message: string }
export interface Attachment { part_id: string; filename: string; content_type: string; size: number }
export interface EmailContent extends Email { html: string; text: string; attachments: Attachment[] }
export interface EmailUpdate extends Partial<Email> { id: number; deleted?: boolean; archived?: boolean; pending?: boolean }
export type ComposeAction = 'send' | 'reply' | 'forward'
