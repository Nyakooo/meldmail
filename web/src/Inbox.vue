<script setup lang="ts">
import Icon from './Icon.vue'
import { t, locale } from './i18n'
import LanguageToggle from './LanguageToggle.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import AccountPanel from './AccountPanel.vue'
import ChangePassword from './ChangePassword.vue'
import SystemStats from './SystemStats.vue'
import ThemeSelect from './ThemeSelect.vue'
import MessageReader from './MessageReader.vue'
import Composer from './Composer.vue'
import MCPPanel from './MCPPanel.vue'
import ApprovalReview from './ApprovalReview.vue'
import type { Account, Email, EmailPage, EmailUpdate, ComposeAction, Approval, SystemStats as StatsData } from './types'
import { request, errorMessage } from './http'
const emit = defineEmits<{ logout: []; passwordChanged: [] }>()
const accounts = ref<Account[]>([])
const emails = ref<Email[]>([])
const accountId = ref(0)
const unread = ref(false)
const query = ref('')
const folder = ref('inbox')
const cursor = ref('')
const pageCursor = ref('')
const previousCursors = ref<string[]>([])
const total = ref(0)
const navigating = ref(false)
const pageDirty = ref(false)
const stats = ref<StatsData | null>(null)
const statsUnavailable = ref(false)
const selected = ref<Email | null>(null)
const settings = ref(false)
const initialSync = ref<number | null>(null)
const passwordOpen = ref(false)
const loading = ref(false)
const syncing = ref(false)
const error = ref('')
const sidebarOpen = ref(false)
const composing = ref<{ action: ComposeAction; original?: Email } | null>(null)
const notice = ref('')
const mcpOpen = ref(false)
const reviewId = ref<string | null>(null)
const mcpRevision = ref(0)
const pendingApprovals = ref(0)
let statsTimer: ReturnType<typeof setInterval> | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let statsRequest: AbortController | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
const connected = ref(false)
let events: EventSource | undefined
let sequence = 0
let activeRequest: AbortController | undefined
let eventRevision = 0
const accountName = (id: number) => { const a = accounts.value.find(a => a.id === id); return a?.display_name || a?.email || t('邮箱') }
const heading = computed(() => unread.value ? t('未读邮件') : ({ inbox: t('统一收件箱'), all: t('全部邮件'), archive: t('已归档'), trash: t('废纸篓') }[folder.value] || t('邮件')))
function date(value: number) { return new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric' }).format(new Date(value * 1000)) }
function fullDate(value: number) { return new Date(value * 1000).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US') }
async function loadAccounts() { accounts.value = await request<Account[]>('/api/accounts') }
async function loadEmails(targetCursor = pageCursor.value) {
 activeRequest?.abort(); const controller = new AbortController(); activeRequest = controller
 const current = ++sequence; const revision = eventRevision; loading.value = true; error.value = ''
 const params = new URLSearchParams({ limit: '50', folder: folder.value })
 if (accountId.value) params.set('account_id', String(accountId.value))
 if (unread.value) params.set('unread', 'true')
 if (query.value.trim()) params.set('q', query.value.trim())
 if (targetCursor) params.set('cursor', targetCursor)
 try {
  const page = await request<EmailPage>(`/api/emails?${params}`, { signal: controller.signal })
  if (current !== sequence) return false
  emails.value = page.emails; cursor.value = page.next_cursor; pageCursor.value = targetCursor; total.value = page.total ?? page.emails.length; pageDirty.value = revision !== eventRevision
  if (pageDirty.value && !navigating.value) scheduleRefresh()
  return true
 } catch (err) { if (!controller.signal.aborted) error.value = errorMessage(err); return false } finally { if (current === sequence) loading.value = false }
}
function filter() { previousCursors.value = []; pageCursor.value = ''; selected.value = null; sidebarOpen.value = false; void loadEmails() }
async function movePage(next: boolean) {
 if (loading.value || navigating.value || pageDirty.value || (next ? !cursor.value : !previousCursors.value.length)) return
 const oldCursor = pageCursor.value
 const target = next ? cursor.value : previousCursors.value[previousCursors.value.length - 1] || ''
 navigating.value = true; selected.value = null
 try { if (await loadEmails(target)) { if (next) previousCursors.value.push(oldCursor); else previousCursors.value.pop() } }
 finally { navigating.value = false; if (pageDirty.value) scheduleRefresh() }
}
async function loadStats() {
 statsRequest?.abort(); const controller = new AbortController(); statsRequest = controller
 try { const result = await request<StatsData>('/api/stats', { signal: controller.signal }); if (!controller.signal.aborted) { stats.value = result; statsUnavailable.value = false } }
 catch { if (!controller.signal.aborted) statsUnavailable.value = true }
}
function visibleStats() { if (!document.hidden) void loadStats() }
function scheduleRefresh() {
 eventRevision++; pageDirty.value = true; clearTimeout(refreshTimer)
 refreshTimer = setTimeout(() => { if (!navigating.value) void loadEmails(); void loadStats() }, 400)
}
function accountCount(id: number) { return stats.value?.mail.accounts.find(account => account.id === id)?.total }
function search() { activeRequest?.abort(); pageDirty.value = true; clearTimeout(searchTimer); searchTimer = setTimeout(filter, 250) }
async function accountSaved(account: Account) {
 settings.value = false; sidebarOpen.value = false; accountId.value = account.id; folder.value = 'inbox'; unread.value = false; query.value = ''; selected.value = null
 previousCursors.value = []; pageCursor.value = ''; emails.value = []; total.value = 0
 initialSync.value = account.status === 'paused' ? null : account.id
 notice.value = account.status === 'paused' ? t('邮箱已保存，当前暂停同步。将同步设为“启用”后开始收信。') : t('邮箱已保存，正在同步…')
 try { await loadAccounts(); await loadEmails(); void loadStats()
  const current = accounts.value.find(item => item.id === account.id)
  if (initialSync.value === account.id && current?.status === 'error') { initialSync.value = null; notice.value = ''; error.value = current.last_error || t('连接失败') }
  else if (initialSync.value === account.id && current && current.last_sync_at > account.last_sync_at) { initialSync.value = null; notice.value = t('同步完成') }
 } catch (err) { error.value = errorMessage(err) }
}
async function accountsChanged() { try { previousCursors.value = []; pageCursor.value = ''; await loadAccounts(); if (accountId.value && !accounts.value.some(a => a.id === accountId.value)) accountId.value = 0; await loadEmails(); void loadStats() } catch (err) { error.value = errorMessage(err) } }
async function sync() {
 syncing.value = true; error.value = ''
 try {
  const targets = accounts.value.filter(a => a.status !== 'paused' && (!accountId.value || a.id === accountId.value))
  // Requests are deliberately sequential here; server sync paths share a three-account limiter.
  for (const account of targets) await request<{ synced: number }>(`/api/accounts/${account.id}/sync`, { method: 'POST' })
  await loadAccounts(); await loadEmails(); void loadStats()
 } catch (err) { error.value = errorMessage(err) } finally { syncing.value = false }
}
function matches(email: Email): boolean {
 if (accountId.value && email.account_id !== accountId.value || unread.value && email.is_read) return false
 if (email.folder_role && (folder.value === 'all' ? email.folder_role === 'trash' : email.folder_role !== folder.value)) return false
 const text = query.value.trim().toLowerCase()
 return !text || [email.subject, email.from_name, email.from_addr, email.snippet].some(value => value.toLowerCase().includes(text))
}
function applyFlags(update: EmailUpdate) {
 if (update.deleted || update.archived || update.restored) {
  if (update.pending === false && update.folder_role && typeof update.subject === 'string') {
   const moved = { ...update, pending_action: '' } as Email
   emails.value = emails.value.filter(email => email.id !== moved.id)
   if (matches(moved)) { emails.value.push(moved); emails.value.sort((a, b) => b.date - a.date || b.id - a.id) }
   if (selected.value?.id === moved.id) { if (matches(moved)) Object.assign(selected.value, moved); else selected.value = null }
   return
  }
  if (update.deleted || update.restored || folder.value === 'inbox') { emails.value = emails.value.filter(email => email.id !== update.id); if (selected.value?.id === update.id) selected.value = null; return }
 }
 const existing = emails.value.find(email => email.id === update.id)
 if (existing) { Object.assign(existing, update); if (!matches(existing)) emails.value = emails.value.filter(email => email.id !== update.id) }
 if (selected.value?.id === update.id) Object.assign(selected.value, update)
}
function compose(action: ComposeAction = 'send') { composing.value = { action, original: action === 'send' ? undefined : selected.value || undefined } }
async function refreshMCP() { try { const actions = await request<Approval[]>('/api/mcp/approvals'); pendingApprovals.value = actions.filter(action => action.status === 'pending').length; mcpRevision.value++ } catch (err) { error.value = errorMessage(err) } }
function startEvents() {
 events = new EventSource('/api/events')
 events.onopen = () => { connected.value = true; scheduleRefresh(); void refreshMCP() }
 events.onerror = () => { connected.value = false }
 events.addEventListener('new_email', (event: MessageEvent<string>) => {
  try { const email = JSON.parse(event.data) as Email
   if (!email.folder_role) { scheduleRefresh(); return }
   if (selected.value?.id === email.id) selected.value = matches(email) ? email : null
   emails.value = emails.value.filter(item => item.id !== email.id)
   if (matches(email) && !pageCursor.value) { emails.value.push(email); emails.value.sort((a, b) => b.date - a.date || b.id - a.id); emails.value = emails.value.slice(0, 50) }
   scheduleRefresh()
  } catch { error.value = t('邮件更新失败，请重新加载列表') }
 })
 events.addEventListener('flag_updated', (event: MessageEvent<string>) => {
  try { applyFlags(JSON.parse(event.data) as EmailUpdate); scheduleRefresh() } catch { error.value = t('邮件状态更新失败，请重新加载列表') }
 })
 events.addEventListener('mcp_approval_required', (event: MessageEvent<string>) => {
  try { const update = JSON.parse(event.data) as { action_id: string; status: string }; void refreshMCP(); if (update.status === 'pending' && !reviewId.value) reviewId.value = update.action_id } catch { error.value = t('审批通知读取失败') }
 })
 events.addEventListener('sync_status', (event: MessageEvent<string>) => {
  try { const update = JSON.parse(event.data) as { account_id: number; status: string; last_error: string; mode?: string }; const account = accounts.value.find(a => a.id === update.account_id); if (account) { account.status = update.status; account.last_error = update.last_error } if (initialSync.value === update.account_id) { if (update.status === 'error') { initialSync.value = null; notice.value = ''; error.value = update.last_error || t('连接失败') } else if (update.mode === 'idle') { initialSync.value = null; notice.value = t('同步完成'); scheduleRefresh() } } } catch { error.value = t('同步状态更新失败') }
 })
}
onMounted(async () => { statsTimer = setInterval(visibleStats, 15000); document.addEventListener('visibilitychange', visibleStats); void loadStats(); try { await loadAccounts(); await loadEmails(); startEvents() } catch (err) { error.value = errorMessage(err) } })
onBeforeUnmount(() => { clearTimeout(searchTimer); clearTimeout(refreshTimer); clearInterval(statsTimer); document.removeEventListener('visibilitychange', visibleStats); statsRequest?.abort(); activeRequest?.abort(); events?.close() })
</script>
<template>
 <div class="workspace">
  <aside class="sidebar" :class="{ open: sidebarOpen }">
   <button class="primary compose-button" :disabled="!accounts.length" @click="compose()">{{ t("＋ 撰写邮件") }}</button>
   <p class="eyebrow sidebar-caption">{{ t("邮件") }}</p>
   <nav :aria-label="t('邮件文件夹')"><button :class="{ active: folder === 'inbox' && !unread }" @click="folder = 'inbox'; unread = false; filter()"><Icon name="mail" />{{ t("统一收件箱") }}</button><button :class="{ active: unread }" @click="folder = 'all'; unread = true; filter()"><Icon name="unread" />{{ t("未读邮件") }}</button><button :class="{ active: folder === 'all' && !unread }" @click="folder = 'all'; unread = false; filter()"><Icon name="all" />{{ t("全部邮件") }}</button><button :class="{ active: folder === 'archive' }" @click="folder = 'archive'; unread = false; filter()"><Icon name="archive" />{{ t("已归档") }}</button><button :class="{ active: folder === 'trash' }" @click="folder = 'trash'; unread = false; filter()"><Icon name="trash" />{{ t("废纸篓") }}</button></nav>
   <div class="account-heading"><p class="eyebrow">{{ t("已连接邮箱") }}</p><button class="icon-button" :aria-label="t('添加邮箱')" @click="settings = true">＋</button></div>
   <button class="account-link" :class="{ active: accountId === 0 }" @click="accountId = 0; filter()"><span class="account-dot all-dot"></span>{{ t("所有邮箱") }}<span class="account-count">{{ stats?.mail.total ?? '—' }}</span></button>
   <button v-for="(account, index) in accounts" :key="account.id" class="account-link" :class="{ active: accountId === account.id }" @click="accountId = account.id; filter()"><span class="account-dot" :style="{ background: ['#397867', '#c89349', '#6579aa', '#ad6b89'][index % 4] }"></span><span class="truncate">{{ account.display_name || account.email }}</span><span class="account-count">{{ accountCount(account.id) ?? '—' }}</span><span v-if="account.status === 'error'" :title="t('连接失败')">!</span><span v-else-if="account.status === 'paused'" :title="t('已暂停')">Ⅱ</span></button>
   <div class="sidebar-footer"><small role="status">{{ connected ? t('● 实时更新已连接') : t('○ 正在连接实时更新') }}</small><button @click="settings = true">{{ t("⚙ 邮箱与连接") }}</button><button @click="mcpOpen = true; sidebarOpen = false">{{ t("◇ AI 与 MCP ") }}<span v-if="pendingApprovals" class="approval-badge">{{ pendingApprovals }}</span></button><button @click="passwordOpen = true; sidebarOpen = false">{{ t("修改登录密码") }}</button><button @click="emit('logout')">{{ t("退出登录") }}</button></div>
  </aside>
  <button v-if="sidebarOpen" class="sidebar-shade" :aria-label="t('关闭导航')" @click="sidebarOpen = false"></button>
  <section class="mail-workspace">
   <header class="workspace-toolbar"><button class="mobile-menu icon-button" :aria-label="t('打开导航')" @click="sidebarOpen = !sidebarOpen">☰</button><label class="search-box"><Icon name="search" /><input v-model="query" type="search" maxlength="256" :placeholder="t('搜索主题、发件人或验证码')" :aria-label="t('搜索邮件')" @input="search"></label><button class="sync-button" :class="{ syncing }" :disabled="syncing || !accounts.length" :aria-busy="syncing" @click="sync"><Icon name="refresh" /><span>{{ syncing ? t('同步中…') : t('同步') }}</span></button><ThemeSelect /><LanguageToggle /><button class="toolbar-settings icon-button" :aria-label="t('邮箱与连接')" @click="settings = true"><Icon name="settings" /></button></header>
   <p v-if="notice" class="workspace-notice notice" role="status">{{ t(notice) }}</p>
   <div v-if="error" class="workspace-error error" role="alert">{{ t(error) }} <button class="text-button" @click="loadEmails()">{{ t("重试") }}</button></div>
   <div class="mail-columns" :class="{ 'has-selection': selected }">
    <section class="message-list" :aria-label="t('邮件列表')">
     <header class="list-heading"><div><p class="eyebrow">{{ accountId ? accountName(accountId) : t('所有邮箱') }}</p><h1>{{ heading }}</h1><small class="filtered-total">{{ t("当前筛选共 ") }}{{ total }}{{ t(" 封") }}</small></div></header>
     <div class="mobile-account-filter"><select v-model="accountId" :aria-label="t('筛选邮箱')" @change="filter"><option :value="0">{{ t("所有邮箱") }}</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.display_name || a.email }}</option></select></div>
     <p v-if="loading && !emails.length" class="list-status" role="status">{{ t("正在加载邮件…") }}</p>
     <div v-else-if="!emails.length" class="empty-list"><div class="empty-symbol">✉</div><h2>{{ initialSync === accountId ? t('正在同步邮件…') : accounts.length ? t('这里很安静') : t('从连接一个邮箱开始') }}</h2><p>{{ query ? t('没有找到匹配邮件，试试其他关键词。') : accounts.length ? t('暂无符合条件的邮件。你可以同步邮箱，或调整筛选条件。') : t('把工作与生活的来信，汇集在一个地方。') }}</p><button v-if="!accounts.length" class="primary" @click="settings = true">{{ t("＋ 连接邮箱") }}</button></div>
     <ul v-else class="email-items"><li v-for="email in emails" :key="email.id"><button class="email-item" :class="{ selected: selected?.id === email.id, unread: !email.is_read }" @click="selected = email"><div class="email-item-top"><span class="sender">{{ email.from_name || email.from_addr || t('未知发件人') }}</span><time :datetime="new Date(email.date * 1000).toISOString()">{{ date(email.date) }}</time></div><div class="email-subject"><span v-if="!email.is_read" class="unread-dot"></span><strong>{{ email.subject || t('（无主题）') }}</strong><span v-if="email.has_attachments" :aria-label="t('有附件')">↧</span><span v-if="email.is_starred" :aria-label="t('已加星标')">★</span></div><p>{{ email.snippet || t('（暂无摘要）') }}</p><span v-if="email.pending_action" class="pending-label">{{ t("正在确认移动 · ") }}</span><span class="account-tag">{{ accountName(email.account_id) }}</span></button></li></ul>
     <nav v-if="total || previousCursors.length" class="mail-pagination" :aria-label="t('邮件分页')"><button class="secondary" :disabled="loading || navigating || pageDirty || !previousCursors.length" @click="movePage(false)">{{ t("上一页") }}</button><span><strong>{{ t("第 ") }}{{ previousCursors.length + 1 }}{{ t(" 页") }}</strong><small>{{ t("每页 50 封") }}</small></span><button class="secondary" :disabled="loading || navigating || pageDirty || !cursor" @click="movePage(true)">{{ t("下一页") }}</button></nav>
    </section>
    <section class="reader" :aria-label="t('邮件预览')">
     <MessageReader v-if="selected" :email="selected" :account-name="accountName(selected.account_id)" @back="selected = null" @flags="applyFlags" @compose="compose" />
     <div v-else class="reader-empty"><img src="/meldmail-mark.svg" alt="" class="empty-mark"><h2>{{ t("选择一封邮件") }}</h2><p>{{ t("在这里阅读、回复和处理来信。") }}</p></div>
    </section>
   </div>
   <SystemStats :stats="stats" :unavailable="statsUnavailable" />
  </section>
  <nav class="mobile-bottom-nav" :aria-label="t('移动端导航')"><button :class="{active: folder === 'inbox' && !unread}" @click="folder = 'inbox'; unread = false; filter()">{{ t("收件箱") }}</button><button :class="{active: unread}" @click="folder = 'all'; unread = true; filter()">{{ t("未读") }}</button><button :class="{active: folder === 'all' && !unread}" @click="folder = 'all'; unread = false; filter()">{{ t("全部") }}</button><button @click="settings = true">{{ t("邮箱") }}</button><button @click="sidebarOpen = true">{{ t("设置") }}</button></nav>
  <button class="mobile-compose primary" :aria-label="t('撰写邮件')" :disabled="!accounts.length" @click="compose()">＋</button>
  <Composer v-if="composing" :accounts="accounts" :action="composing.action" :original="composing.original" @close="composing = null" @sent="notice = t('邮件已发送')" />
  <MCPPanel v-if="mcpOpen" :revision="mcpRevision" @close="mcpOpen = false" @review="reviewId = $event" @changed="refreshMCP" />
  <ApprovalReview v-if="reviewId" :key="reviewId" :id="reviewId" @close="reviewId = null" @changed="refreshMCP" />
  <ChangePassword v-if="passwordOpen" @close="passwordOpen = false" @changed="emit('passwordChanged')" />
  <AccountPanel v-if="settings" :accounts="accounts" @close="settings = false" @changed="accountsChanged" @saved="accountSaved" />
 </div>
</template>
