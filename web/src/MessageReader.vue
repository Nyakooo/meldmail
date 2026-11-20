<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import type { Email, EmailContent, EmailUpdate, ComposeAction } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ email: Email; accountName: string }>()
const emit = defineEmits<{ back: []; flags: [update: EmailUpdate]; compose: [action: ComposeAction] }>()
const content = ref<EmailContent | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const actionError = ref('')
let controller: AbortController | undefined
const frame = computed(() => `<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; form-action 'none'; base-uri 'none'"><style>body{font:15px/1.8 system-ui,sans-serif;color:#34483b;padding:12px;overflow-wrap:anywhere}table{max-width:100%;border-collapse:collapse}td,th{padding:5px}pre{white-space:pre-wrap}a{color:#285e49}blockquote{border-left:3px solid #dfe8d8;margin-left:0;padding-left:16px}</style></head><body>${content.value?.html || ''}</body></html>`)
async function change(change: { is_read?: boolean; is_starred?: boolean; archive?: boolean; delete?: boolean }) {
 busy.value = true; actionError.value = ''
 try {
  const response = await request<{ email: Email }>(`/api/emails/${props.email.id}/flags`, { method: 'PATCH', body: JSON.stringify(change) })
  emit('flags', { ...response.email, archived: change.archive, deleted: change.delete })
 } catch (err) { actionError.value = errorMessage(err) } finally { busy.value = false }
}
async function load() {
 controller?.abort(); const active = new AbortController(); controller = active
 const id = props.email.id; loading.value = true; content.value = null; error.value = ''; actionError.value = ''
 try {
  const response = await request<EmailContent>(`/api/emails/${id}`, { signal: active.signal })
  if (active.signal.aborted) return
  content.value = response
  if (!props.email.is_read) void change({ is_read: true })
 } catch (err) { if (!active.signal.aborted) error.value = errorMessage(err) } finally { if (!active.signal.aborted) loading.value = false }
}
function remove() { if (window.confirm('将这封邮件移到废纸篓？')) void change({ delete: true }) }
function size(bytes: number) { return bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB` }
watch(() => props.email.id, load, { immediate: true })
onBeforeUnmount(() => controller?.abort())
</script>
<template>
 <header class="reader-toolbar"><button class="text-button" @click="emit('back')">← 返回列表</button><span>{{ accountName }}</span></header>
 <article class="message-preview full-message">
  <div class="message-actions"><button class="primary" :disabled="!content || loading" @click="emit('compose', 'reply')">回复</button><button class="secondary" :disabled="!content || loading" @click="emit('compose', 'forward')">转发</button><button class="secondary" :disabled="busy" @click="change({ is_read: !email.is_read })">{{ email.is_read ? '标为未读' : '标为已读' }}</button><button class="secondary" :disabled="busy" @click="change({ is_starred: !email.is_starred })">{{ email.is_starred ? '★ 取消星标' : '☆ 星标' }}</button><button class="secondary" :disabled="busy || email.folder_role === 'archive'" @click="change({ archive: true })">归档</button><button class="secondary" :disabled="busy || email.folder_role === 'trash'" @click="remove">删除</button></div>
  <h2>{{ email.subject || '（无主题）' }}</h2>
  <div class="sender-detail"><div class="sender-avatar">{{ (email.from_name || email.from_addr || '?').slice(0, 1).toUpperCase() }}</div><div><strong>{{ email.from_name || email.from_addr }}</strong><p>{{ email.from_addr }}</p><time>{{ new Date(email.date * 1000).toLocaleString('zh-CN') }}</time></div></div>
  <p v-if="actionError" class="error" role="alert">{{ actionError }}</p>
  <p v-if="loading" class="list-status" role="status">正在读取邮件…</p>
  <div v-else-if="error" class="message-error"><p class="error" role="alert">{{ error }}</p><button class="secondary" @click="load">重新读取</button></div>
  <template v-else-if="content"><iframe v-if="content.html" class="email-frame" title="邮件正文" :srcdoc="frame" sandbox="allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer"></iframe><pre v-else class="plain-message">{{ content.text || '（这封邮件没有文本正文）' }}</pre>
   <section v-if="content.attachments.length" class="attachments"><h3>附件 · {{ content.attachments.length }}</h3><a v-for="attachment in content.attachments" :key="attachment.part_id" :href="`/api/emails/${email.id}/attachments/${encodeURIComponent(attachment.part_id)}`" download><span>↓ {{ attachment.filename }}</span><small>{{ size(attachment.size) }}</small></a></section>

  </template>
 </article>
</template>
