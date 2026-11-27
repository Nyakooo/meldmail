<script setup lang="ts">
import Icon from './Icon.vue'
import { t, locale } from './i18n'
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import type { Email, EmailContent, EmailUpdate, ComposeAction } from './types'
import { request, errorMessage } from './http'
import { dark } from './theme'
import { applyMessagePalette, repairLightLinks } from './messagePalette'
const props = defineProps<{ email: Email; accountName: string }>()
const emit = defineEmits<{ back: []; purged: []; flags: [update: EmailUpdate]; compose: [action: ComposeAction] }>()
const content = ref<EmailContent | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const actionError = ref('')
let controller: AbortController | undefined
const bodyFrame = ref<HTMLIFrameElement | null>(null)
const frameHeight = ref(0)
const frameReady = ref(false)
let bodyObserver: ResizeObserver | undefined
function fitBody() {
 bodyObserver?.disconnect()
 const body = bodyFrame.value?.contentDocument?.body
 if (!body) return
 body.querySelectorAll<HTMLAnchorElement>('a[href]').forEach(link => {
  const href = link.getAttribute('href') || ''
  if (/^https?:\/\//i.test(href)) {
   link.target = '_blank'
   link.rel = 'noopener noreferrer nofollow'
  }
 })
 const palette = body.ownerDocument.querySelector<HTMLStyleElement>('head > style[data-reader-dark]')
 if (palette?.media === 'not all') {
  applyMessagePalette(body)
  palette.media = 'all'
 } else if (!palette && !dark.value) {
  repairLightLinks(body)
 }
 const resize = () => { frameHeight.value = Math.ceil(body.getBoundingClientRect().height) }
 bodyObserver = new ResizeObserver(resize)
 bodyObserver.observe(body)
 resize()
 frameReady.value = true
}
const originalColors = ref(false)
const useDarkBody = computed(() => dark.value && !originalColors.value)
const frame = computed(() => {
 const html = content.value?.html || ''
 return `<!doctype html>
<html><head>
 <meta charset="utf-8">
 <meta name="referrer" content="no-referrer">
 <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; form-action 'none'; base-uri 'none'">
 <style>
 html { color-scheme: light; background: #fff; }
 body { display: flow-root; background: inherit; color: #20242b; font: 15px/1.75 system-ui, sans-serif; overflow-wrap: anywhere; }
 body > :first-child { margin-top: 0; }
 body > :last-child { margin-bottom: 0; }
 table { border-collapse: collapse; }
 img { max-width: 100%; height: auto; }
 pre { white-space: pre-wrap; }
 a { color: #0876d4; text-decoration: underline; text-underline-offset: 2px; }
 blockquote { border-inline-start: 2px solid #bac1ca; margin-inline: 0; padding-inline-start: 14px; }
 @layer reader {
  html, body { height: auto !important; min-height: 0 !important; }
  html { overflow: hidden !important; }
  body { margin: 0 !important; max-width: 100% !important; box-sizing: border-box !important; }
  table { max-width: 100% !important; }
 }
 </style>
 ${useDarkBody.value ? `<style data-reader-dark media="not all">
 @layer reader {
  html { color-scheme: dark !important; }
  html, body { background: #151515 !important; color: #efeeeb !important; }
  body a { text-decoration: underline !important; }
  body a[data-reader-action], body a[data-reader-action] * { text-decoration: none !important; }
  body a[data-reader-action]:is(:hover, :focus-visible) { outline: 2px solid #d7e4fa !important; outline-offset: 2px; }
 }
 </style>` : ''}
</head><body>${html}</body></html>`
})
watch(useDarkBody, () => { frameReady.value = false }, { flush: 'sync' })
async function change(change: { is_read?: boolean; is_starred?: boolean; archive?: boolean; delete?: boolean; restore?: boolean }) {
 busy.value = true; actionError.value = ''
 try {
  const response = await request<{ email: Email }>(`/api/emails/${props.email.id}/flags`, { method: 'PATCH', body: JSON.stringify(change) })
  const pendingAction = change.delete ? 'delete' : change.archive ? 'archive' : change.restore ? 'restore' : response.email.pending_action
  emit('flags', { ...response.email, pending_action: pendingAction, archived: change.archive, deleted: change.delete, restored: change.restore, pending: true })
 } catch (err) { actionError.value = errorMessage(err) } finally { busy.value = false }
}
async function load() {
 bodyObserver?.disconnect(); frameHeight.value = 0; frameReady.value = false; originalColors.value = false
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
async function purge() {
 if (!window.confirm(t('永久删除这封邮件？删除后无法恢复。'))) return
 busy.value = true; actionError.value = ''
 try {
  await request<void>(`/api/emails/${props.email.id}`, { method: 'DELETE' })
  emit('purged')
 } catch (err) { actionError.value = errorMessage(err) } finally { busy.value = false }
}
function size(bytes: number) { return bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB` }
watch(() => [props.email.id, props.email.folder_id, props.email.uid], load, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); bodyObserver?.disconnect() })
</script>
<template>
 <header class="reader-toolbar">
  <div class="reader-navigation">
   <button class="text-button" @click="emit('back')"><Icon name="back" />{{ t('返回列表') }}</button>
   <span class="truncate">{{ accountName }}</span>
  </div>
  <div class="message-actions" :aria-busy="busy">
   <button class="reader-icon" :title="email.is_read ? t('标为未读') : t('标为已读')" :aria-label="email.is_read ? t('标为未读') : t('标为已读')" :disabled="busy || !!email.pending_action" @click="change({ is_read: !email.is_read })"><Icon name="mail" /></button>
   <button class="reader-icon" :class="{ starred: email.is_starred }" :title="email.is_starred ? t('★ 取消星标') : t('☆ 星标')" :aria-label="email.is_starred ? t('★ 取消星标') : t('☆ 星标')" :disabled="busy || !!email.pending_action" @click="change({ is_starred: !email.is_starred })"><Icon name="star" /></button>
   <button v-if="email.folder_role !== 'trash'" class="reader-icon" :title="t('归档')" :aria-label="t('归档')" :disabled="busy || !!email.pending_action || email.folder_role === 'archive'" @click="change({ archive: true })"><Icon name="archive" /></button>
   <button v-if="email.folder_role !== 'trash'" class="reader-icon" :title="t('删除')" :aria-label="t('删除')" :disabled="busy || !!email.pending_action || email.folder_role === 'trash'" @click="remove"><Icon name="trash" /></button>
   <button v-if="email.folder_role === 'trash'" class="reader-icon" :title="t('彻底删除')" :aria-label="t('彻底删除')" :disabled="busy || !!email.pending_action" @click="purge"><Icon name="trash" /></button>
   <button v-if="email.folder_role === 'trash' || email.folder_role === 'archive'" class="restore-button" :disabled="busy || !!email.pending_action" @click="change({ restore: true })"><Icon name="restore" />{{ t('恢复到收件箱') }}</button>
  </div>
 </header>
 <article class="message-preview full-message">
  <div class="reader-page">
  <h2>{{ email.subject || t('（无主题）') }}</h2>
  <div class="sender-detail">
   <div class="sender-avatar" aria-hidden="true">{{ (email.from_name || email.from_addr || '?').slice(0, 1).toUpperCase() }}</div>
   <div class="reader-sender"><strong>{{ email.from_name || email.from_addr || t('未知发件人') }}</strong><p v-if="email.from_name && email.from_addr">{{ email.from_addr }}</p></div>
   <time :datetime="new Date(email.date * 1000).toISOString()">{{ new Date(email.date * 1000).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') }}</time>
  </div>
  <p v-if="email.pending_action" class="pending-status" role="status">{{ t("移动已提交，正在确认服务商结果；邮件保留在此列表。") }}</p><p v-if="actionError" class="error" role="alert">{{ t(actionError) }}</p>
  <p v-if="loading" class="list-status" role="status">{{ t("正在读取邮件…") }}</p>
  <div v-else-if="error" class="message-error"><p class="error" role="alert">{{ t(error) }}</p><button class="secondary" @click="load">{{ t("重新读取") }}</button></div>
  <template v-else-if="content">
   <div v-if="content.cached || (dark && content.html)" class="reader-body-tools">
    <p v-if="content.cached" class="cache-status" role="status">✓ {{ content.local ? t('已从本地缓存读取') : t('正文已保存到本地，下次直接读取') }}</p>
    <button v-if="dark && content.html" class="secondary reader-color-toggle" :aria-pressed="originalColors" @click="originalColors = !originalColors">{{ originalColors ? t('使用深色正文') : t('显示原始配色') }}</button>
   </div>
   <p v-if="content.cache_warning" class="error" role="status">{{ content.cache_warning }}</p>
   <div v-if="content.html" class="reader-html" :class="{ 'original-colors': !useDarkBody }">
    <iframe ref="bodyFrame" class="email-frame" :style="{ height: frameHeight + 'px', visibility: frameReady ? 'visible' : 'hidden' }" @load="fitBody" :title="t('邮件正文')" :srcdoc="frame" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer"></iframe>
   </div>
   <pre v-else class="plain-message">{{ content.text || t('（这封邮件没有文本正文）') }}</pre>
   <section v-if="content.attachments.length" class="attachments"><h3>{{ t("附件 · ") }}{{ content.attachments.length }}</h3><a v-for="attachment in content.attachments" :key="attachment.part_id" :href="`/api/emails/${email.id}/attachments/${encodeURIComponent(attachment.part_id)}`" download><span>↓ {{ attachment.filename }}</span><small>{{ size(attachment.size) }}</small></a></section>
  </template>
  </div>
 </article>
   <div class="reply-actions"><button class="secondary" :disabled="!content || loading" @click="composeAction('reply')"><Icon name="reply" />{{ t('回复') }}</button><button class="secondary" :disabled="!content || loading" @click="composeAction('forward')"><Icon name="forward" />{{ t('转发') }}</button></div>
</template>
