<script setup lang="ts">
import Icon from './Icon.vue'
import { t, locale } from './i18n'
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import type { Email, EmailContent, EmailUpdate, ComposeAction } from './types'
import { request, errorMessage } from './http'
import { dark } from './theme'
const props = defineProps<{ email: Email; accountName: string }>()
const emit = defineEmits<{ back: []; flags: [update: EmailUpdate]; compose: [action: ComposeAction] }>()
const content = ref<EmailContent | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const actionError = ref('')
let controller: AbortController | undefined
const bodyFrame = ref<HTMLIFrameElement | null>(null)
const frameHeight = ref(0)
let bodyObserver: ResizeObserver | undefined
function fitBody() {
 bodyObserver?.disconnect()
 const body = bodyFrame.value?.contentDocument?.body
 if (!body) return
 const resize = () => { frameHeight.value = Math.ceil(body.getBoundingClientRect().height) }
 bodyObserver = new ResizeObserver(resize)
 bodyObserver.observe(body)
 resize()
}
const frame = computed(() => `<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; form-action 'none'; base-uri 'none'"><style>html{color-scheme:${dark.value ? 'dark' : 'light'}}html{overflow:hidden}body{margin:0;display:flow-root;background:${dark.value ? '#25282d' : '#fff'};font:15px/1.8 system-ui,sans-serif;color:${dark.value ? '#e4eaf3' : '#202c3c'};padding:0;overflow-wrap:anywhere}body>:first-child{margin-top:0}body>:last-child{margin-bottom:0}table{max-width:100%;border-collapse:collapse}img{max-width:100%;height:auto}pre{white-space:pre-wrap}a{color:${dark.value ? '#8cc2ff' : '#0876f2'}}${dark.value ? 'body *{color:#e4eaf3!important;background-color:transparent!important;border-color:#465367!important}a,a *{color:#8cc2ff!important}' : ''}blockquote{border-left:3px solid #dfe8d8;margin-left:0;padding-left:16px}</style></head><body>${content.value?.html || ''}<style>html{height:auto!important;min-height:0!important;overflow:hidden!important}body{margin:0!important;height:auto!important;min-height:0!important;max-width:100%!important;box-sizing:border-box!important}table{max-width:100%!important}</style></body></html>`)
async function change(change: { is_read?: boolean; is_starred?: boolean; archive?: boolean; delete?: boolean; restore?: boolean }) {
 busy.value = true; actionError.value = ''
 try {
  const response = await request<{ email: Email }>(`/api/emails/${props.email.id}/flags`, { method: 'PATCH', body: JSON.stringify(change) })
  emit('flags', { ...response.email, archived: change.archive, deleted: change.delete, restored: change.restore, pending: true })
 } catch (err) { actionError.value = errorMessage(err) } finally { busy.value = false }
}
async function load() {
 bodyObserver?.disconnect(); frameHeight.value = 0
 controller?.abort(); const active = new AbortController(); controller = active
 const id = props.email.id; loading.value = true; content.value = null; error.value = ''; actionError.value = ''
 try {
  const response = await request<EmailContent>(`/api/emails/${id}`, { signal: active.signal })
  if (active.signal.aborted) return
  content.value = response
  if (!props.email.is_read && !props.email.pending_action) void change({ is_read: true })
 } catch (err) { if (!active.signal.aborted) error.value = errorMessage(err) } finally { if (!active.signal.aborted) loading.value = false }
}
function composeAction(action: ComposeAction) { emit('compose', action) }
function remove() { if (window.confirm(t('将这封邮件移到废纸篓？'))) void change({ delete: true }) }
function size(bytes: number) { return bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB` }
watch(() => [props.email.id, props.email.folder_id, props.email.uid], load, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); bodyObserver?.disconnect() })
</script>
<template>
 <header class="reader-toolbar"><button class="text-button" @click="emit('back')">{{ t("← 返回列表") }}</button><span>{{ accountName }}</span></header>
 <article class="message-preview full-message">
  <div class="message-actions" :aria-busy="busy">
   <button class="reader-icon" :title="email.is_read ? t('标为未读') : t('标为已读')" :aria-label="email.is_read ? t('标为未读') : t('标为已读')" :disabled="busy || !!email.pending_action" @click="change({ is_read: !email.is_read })"><Icon name="mail" /></button>
   <button class="reader-icon" :class="{ starred: email.is_starred }" :title="email.is_starred ? t('★ 取消星标') : t('☆ 星标')" :aria-label="email.is_starred ? t('★ 取消星标') : t('☆ 星标')" :disabled="busy || !!email.pending_action" @click="change({ is_starred: !email.is_starred })"><Icon name="star" /></button>
   <button v-if="email.folder_role !== 'trash'" class="reader-icon" :title="t('归档')" :aria-label="t('归档')" :disabled="busy || !!email.pending_action || email.folder_role === 'archive'" @click="change({ archive: true })"><Icon name="archive" /></button>
   <button v-if="email.folder_role !== 'trash'" class="reader-icon" :title="t('删除')" :aria-label="t('删除')" :disabled="busy || !!email.pending_action || email.folder_role === 'trash'" @click="remove"><Icon name="trash" /></button>
   <button v-if="email.folder_role === 'trash' || email.folder_role === 'archive'" class="restore-button" :disabled="busy || !!email.pending_action" @click="change({ restore: true })"><Icon name="restore" />{{ t('恢复到收件箱') }}</button>
  </div>
  <h2>{{ email.subject || t('（无主题）') }}</h2>
  <div class="sender-detail"><div class="sender-avatar">{{ (email.from_name || email.from_addr || '?').slice(0, 1).toUpperCase() }}</div><div><strong>{{ email.from_name || email.from_addr }}</strong><p>{{ email.from_addr }}</p><time>{{ new Date(email.date * 1000).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') }}</time></div></div>
  <p v-if="email.pending_action" class="pending-status" role="status">{{ t("移动已提交，正在确认服务商结果；邮件保留在此列表。") }}</p><p v-if="actionError" class="error" role="alert">{{ t(actionError) }}</p>
  <p v-if="loading" class="list-status" role="status">{{ t("正在读取邮件…") }}</p>
  <div v-else-if="error" class="message-error"><p class="error" role="alert">{{ t(error) }}</p><button class="secondary" @click="load">{{ t("重新读取") }}</button></div>
  <template v-else-if="content"><p v-if="content.cached" class="cache-status" role="status">✓ {{ content.local ? t('已从本地缓存读取') : t('正文已保存到本地，下次直接读取') }}</p><p v-if="content.cache_warning" class="error" role="status">{{ content.cache_warning }}</p><iframe v-if="content.html" ref="bodyFrame" class="email-frame" :style="{ height: frameHeight + 'px' }" @load="fitBody" :title="t('邮件正文')" :srcdoc="frame" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer"></iframe><pre v-else class="plain-message">{{ content.text || t('（这封邮件没有文本正文）') }}</pre>
   <section v-if="content.attachments.length" class="attachments"><h3>{{ t("附件 · ") }}{{ content.attachments.length }}</h3><a v-for="attachment in content.attachments" :key="attachment.part_id" :href="`/api/emails/${email.id}/attachments/${encodeURIComponent(attachment.part_id)}`" download><span>↓ {{ attachment.filename }}</span><small>{{ size(attachment.size) }}</small></a></section>


  </template>
 </article>
   <div class="reply-actions"><button class="secondary" :disabled="!content || loading" @click="composeAction('reply')"><Icon name="reply" />{{ t('回复') }}</button><button class="secondary" :disabled="!content || loading" @click="composeAction('forward')"><Icon name="forward" />{{ t('转发') }}</button></div>
</template>
