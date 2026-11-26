<script setup lang="ts">
import Icon from './Icon.vue'
import DialogBack from './DialogBack.vue'
import { useSystemNotifications } from './systemNotifications'
import { t, locale } from './i18n'
import LanguageToggle from './LanguageToggle.vue'
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import AccountPanel from './AccountPanel.vue'
import ChangePassword from './ChangePassword.vue'
import DiagnosticLogs from './DiagnosticLogs.vue'
import SystemStats from './SystemStats.vue'
import ThemeSelect from './ThemeSelect.vue'
import MessageReader from './MessageReader.vue'
import Composer from './Composer.vue'
import MCPPanel from './MCPPanel.vue'
import ApprovalReview from './ApprovalReview.vue'
import type { Account, Email, EmailPage, EmailUpdate, ComposeAction, Approval, SystemStats as StatsData, CategoryCounts } from './types'
import { request, errorMessage } from './http'
const emit = defineEmits<{ logout: []; passwordChanged: [] }>()
const { notificationsEnabled, toggleNotifications, notify } = useSystemNotifications()
const settingsDialog = ref<HTMLDialogElement | null>(null)
const notificationFeedback = ref('')
const logsOpen = ref(false)
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
const settings = ref<'account' | 'transfer' | 'proxy' | null>(null)
const passwordOpen = ref(false)
const loading = ref(false)
const syncing = ref(false)
const error = ref('')
const sidebarOpen = ref(false)
const composing = ref<{ action: ComposeAction; original?: Email } | null>(null)
const notice = ref('')
const mcpTab = ref<'tokens' | 'approvals' | null>(null)
const reviewId = ref<string | null>(null)
const mcpRevision = ref(0)
const pendingApprovals = ref(0)
let statsTimer: ReturnType<typeof setInterval> | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let countRefreshTimer: ReturnType<typeof setTimeout> | undefined
let statsRequest: AbortController | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
const connected = ref(false)
const background = ref<{ syncing: number; prefetch: { account_id: number; subject: string } | null }>({ syncing: 0, prefetch: null })
const backgroundMessage = computed(() => {
 if (!connected.value) return t('正在连接后台状态…')
 const { prefetch, syncing } = background.value
 if (prefetch) return `${t('正在预取正文')} · ${accountName(prefetch.account_id)} · ${prefetch.subject || t('（无主题）')}`
 if (syncing) return t('正在同步邮件列表…')
 return t('后台空闲')
})
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
const clearingTrash = ref(false)
const trashAccount = computed(() => accounts.value.find(a => a.id === accountId.value)
  || (accounts.value.length === 1 ? accounts.value[0] : undefined))
async function emptyTrash() {
 const account = trashAccount.value
 if (!account || clearingTrash.value || !window.confirm(t('清空「{email}」的废纸篓？该邮箱废纸篓中的所有邮件将被永久删除，无法恢复。').replace('{email}', account.email))) return
 clearingTrash.value = true; error.value = ''
 try {
  await request<void>(`/api/accounts/${account.id}/trash`, { method: 'DELETE' })
  if (folder.value === 'trash') filter()
  void loadStats()
 } catch (err) { error.value = errorMessage(err) } finally { clearingTrash.value = false }
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
 statsRequest?.abort(); const controller = new AbortController(); statsRequest = controller; const revision = eventRevision
 try { const result = await request<StatsData>('/api/stats', { signal: controller.signal }); if (!controller.signal.aborted && revision === eventRevision) { stats.value = result; statsUnavailable.value = false } }
 catch { if (!controller.signal.aborted) statsUnavailable.value = true }
}
function visibleStats() { if (!document.hidden) void loadStats() }
function scheduleRefresh() {
 eventRevision++; pageDirty.value = true; clearTimeout(refreshTimer); clearTimeout(countRefreshTimer)
 countRefreshTimer = setTimeout(() => { void loadStats() }, 50)
 refreshTimer = setTimeout(() => { if (!navigating.value) void loadEmails() }, 400)
}
const countCategory = computed<keyof CategoryCounts>(() => unread.value ? 'unread' : folder.value as keyof CategoryCounts)
function accountCount(id: number) {
 const summary = id ? stats.value?.mail.accounts.find(account => account.id === id) : stats.value?.mail
 return summary?.categories[countCategory.value]
}
function accountStatus(account: Account) {
 return account.status === 'error' ? t('连接暂不可用') : account.status === 'paused' ? t('已暂停') : t('同步已启用')
}
function connectionDetails(account: Account) {
 settings.value = 'account'; settingsAccount.value = account.id; sidebarOpen.value = false
}
const settingsAccount = ref<number | null>(null)
function search() { activeRequest?.abort(); pageDirty.value = true; clearTimeout(searchTimer); searchTimer = setTimeout(filter, 250) }
async function accountsChanged() { try { previousCursors.value = []; pageCursor.value = ''; await loadAccounts(); if (accountId.value && !accounts.value.some(a => a.id === accountId.value)) accountId.value = 0; await loadEmails(); void loadStats() } catch (err) { error.value = errorMessage(err) } }
async function sync() {
 syncing.value = true; error.value = ''; notice.value = ''
 try {
  const targets = accounts.value.filter(a => a.status !== 'paused' && (!accountId.value || a.id === accountId.value))
  // Requests are deliberately sequential here; server sync paths share a three-account limiter.
  let alreadySyncing = false
  for (const account of targets) {
   const result = await request<{ status?: 'syncing' }>(`/api/accounts/${account.id}/sync`, { method: 'POST' })
   alreadySyncing ||= result.status === 'syncing'
  }
  await loadAccounts(); await loadEmails(); void loadStats()
  if (targets.length) notice.value = t(alreadySyncing ? '部分邮箱正在后台同步' : '同步完成')
 } catch (err) { notice.value = ''; error.value = errorMessage(err) } finally { syncing.value = false }
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
async function changeNotificationPreference() {
 notificationFeedback.value = (await toggleNotifications()) ?? ''
}
function openSettings() {
 notificationFeedback.value = ''
 settingsDialog.value?.showModal()
 sidebarOpen.value = false
}
async function returnToSettings() {
 settings.value = null
 mcpTab.value = null
 logsOpen.value = false
 passwordOpen.value = false
 await nextTick()
 openSettings()
}
function openMCP(tab: 'tokens' | 'approvals') {
 settingsDialog.value?.close()
 sidebarOpen.value = false
 mcpTab.value = tab
}
function startEvents() {
 events = new EventSource('/api/events')
 events.onopen = () => { connected.value = true; scheduleRefresh(); void refreshMCP() }
 events.onerror = () => {
  connected.value = false
  background.value = { syncing: 0, prefetch: null }
 }
 events.addEventListener('background_activity', (event: MessageEvent<string>) => {
  try { background.value = JSON.parse(event.data) } catch { background.value = { syncing: 0, prefetch: null } }
 })
 events.addEventListener('new_email', (event: MessageEvent<string>) => {
  try { const email = JSON.parse(event.data) as Email & { from_sync?: boolean }
   if (email.from_sync && email.folder_role === 'inbox' && !email.is_read && email.date >= Date.now() / 1000 - 300) {
    notify(t('MeldMail 有新邮件'), `mail-${email.account_id}`)
   }
   if (!email.folder_role) { scheduleRefresh(); return }
   if (selected.value?.id === email.id) selected.value = matches(email) ? email : null
   emails.value = emails.value.filter(item => item.id !== email.id)
   if (matches(email) && !pageCursor.value) { emails.value.push(email); emails.value.sort((a, b) => b.date - a.date || b.id - a.id); emails.value = emails.value.slice(0, 50) }
   scheduleRefresh()
  } catch { error.value = t('邮件更新失败，请重新加载列表') }
 })
 events.addEventListener('trash_changed', (event: MessageEvent<string>) => {
  try {
   const { account_id, email_id } = JSON.parse(event.data) as { account_id: number; email_id: number }
   if (selected.value?.id === email_id || (!email_id && selected.value?.folder_role === 'trash' && selected.value.account_id === account_id)) selected.value = null
   if (folder.value === 'trash' && (!accountId.value || accountId.value === account_id)) {
    if (email_id) emails.value = emails.value.filter(email => email.id !== email_id)
    scheduleRefresh()
   } else { void loadStats() }
  } catch { error.value = t('邮件状态更新失败，请重新加载列表') }
 })
 events.addEventListener('flag_updated', (event: MessageEvent<string>) => {
  try { applyFlags(JSON.parse(event.data) as EmailUpdate); scheduleRefresh() } catch { error.value = t('邮件状态更新失败，请重新加载列表') }
 })
 events.addEventListener('mcp_approval_required', (event: MessageEvent<string>) => {
  try { const update = JSON.parse(event.data) as { action_id: string; status: string }; void refreshMCP(); if (update.status === 'pending') { notify(t('MeldMail 有待审批的发信请求'), `approval-${update.action_id}`); if (!reviewId.value) reviewId.value = update.action_id } } catch { error.value = t('审批通知读取失败') }
 })
 events.addEventListener('sync_status', (event: MessageEvent<string>) => {
  try { const update = JSON.parse(event.data) as { account_id: number; status: string; last_error: string; mode?: string }; const account = accounts.value.find(a => a.id === update.account_id); if (account) { account.status = update.status; account.last_error = update.last_error } } catch { error.value = t('同步状态更新失败') }
 })
}
onMounted(async () => { statsTimer = setInterval(visibleStats, 15000); document.addEventListener('visibilitychange', visibleStats); void loadStats(); try { await loadAccounts(); await loadEmails(); startEvents() } catch (err) { error.value = errorMessage(err) } })
onBeforeUnmount(() => { clearTimeout(searchTimer); clearTimeout(refreshTimer); clearTimeout(countRefreshTimer); clearInterval(statsTimer); document.removeEventListener('visibilitychange', visibleStats); statsRequest?.abort(); activeRequest?.abort(); events?.close() })
</script>
<template>
 <div class="workspace">
  <aside class="sidebar" :class="{ open: sidebarOpen }">
   <button class="primary compose-button" :disabled="!accounts.length" @click="compose()">{{ t("＋ 撰写邮件") }}</button>
   <p class="eyebrow sidebar-caption">{{ t("邮件") }}</p>
   <nav :aria-label="t('邮件文件夹')"><button :class="{ active: folder === 'inbox' && !unread }" @click="folder = 'inbox'; unread = false; filter()"><Icon name="mail" />{{ t("统一收件箱") }}</button><button :class="{ active: unread }" @click="folder = 'all'; unread = true; filter()"><Icon name="unread" />{{ t("未读邮件") }}</button><button :class="{ active: folder === 'all' && !unread }" @click="folder = 'all'; unread = false; filter()"><Icon name="all" />{{ t("全部邮件") }}</button><button :class="{ active: folder === 'archive' }" @click="folder = 'archive'; unread = false; filter()"><Icon name="archive" />{{ t("已归档") }}</button><button :class="{ active: folder === 'trash' }" @click="folder = 'trash'; unread = false; filter()"><Icon name="trash" />{{ t("废纸篓") }}</button></nav>
   <div class="account-heading"><p class="eyebrow">{{ t("已连接邮箱") }}</p><button class="icon-button" :aria-label="t('添加邮箱')" @click="settings = 'account'">＋</button></div>
   <button class="account-link" :class="{ active: accountId === 0 }" @click="accountId = 0; filter()"><span class="account-dot all-dot"></span>{{ t("所有邮箱") }}<span class="account-count">{{ accountCount(0) ?? '—' }}</span></button>
   <div v-for="(account, index) in accounts" :key="account.id" class="account-row"><button class="account-link" :class="{ active: accountId === account.id }" @click="accountId = account.id; filter()"><span class="account-dot" :style="{ background: ['#397867', '#c89349', '#6579aa', '#ad6b89'][index % 4] }"></span><span class="truncate">{{ account.display_name || account.email }}</span><span class="account-count">{{ accountCount(account.id) ?? '—' }}</span></button><button v-if="account.status === 'error' || account.status === 'paused'" class="account-status" :class="account.status" :aria-label="accountStatus(account) + ' · ' + account.email" :title="accountStatus(account) + (account.last_error ? '：' + t(account.last_error) : '') + ' · ' + t('查看连接设置')" @click="connectionDetails(account)"><span class="status-dot"></span><span>{{ account.status === 'paused' ? t('已暂停') : t('待重连') }}</span></button></div>
   <div class="sidebar-footer">
    <p class="live-status" role="status"><span class="status-dot" :class="{ connected }"></span>{{ connected ? t('实时更新已连接') : t('正在连接实时更新') }}</p>
    <nav class="footer-actions" :aria-label="t('设置与账户')">
     <div class="settings-shortcuts">
      <button @click="openSettings"><Icon name="settings" /><span>{{ t('设置') }}</span></button>
      <button v-if="pendingApprovals" class="approval-shortcut" :aria-label="t('待审批') + ' · ' + pendingApprovals" @click="openMCP('approvals')"><span class="approval-badge">{{ pendingApprovals }}</span></button>
     </div>
     <button class="logout-button" @click="emit('logout')"><Icon name="logout" /><span>{{ t('退出登录') }}</span></button>
    </nav>
   </div>
  </aside>
  <button v-if="sidebarOpen" class="sidebar-shade" :aria-label="t('关闭导航')" @click="sidebarOpen = false"></button>
  <section class="mail-workspace">
   <header class="workspace-toolbar"><button class="mobile-menu icon-button" :aria-label="t('打开导航')" @click="sidebarOpen = !sidebarOpen">☰</button><label class="search-box"><Icon name="search" /><input v-model="query" type="search" maxlength="256" :placeholder="t('搜索主题、发件人或验证码')" :aria-label="t('搜索邮件')" @input="search"></label><button class="sync-button" :class="{ syncing }" :disabled="syncing || !accounts.length" :aria-busy="syncing" :aria-label="syncing ? t('同步中…') : t('同步')" :title="syncing ? t('同步中…') : t('同步')" @click="sync"><Icon name="refresh" /></button><span class="sync-progress" role="status"><template v-if="syncing">{{ t('同步中…') }}</template></span><ThemeSelect /><LanguageToggle /></header>
   <p v-if="notice" class="workspace-notice notice" role="status">{{ t(notice) }}</p>
   <div v-if="error" class="workspace-error error" role="alert">{{ t(error) }} <button class="text-button" @click="loadEmails()">{{ t("重试") }}</button></div>
   <div class="mail-columns" :class="{ 'has-selection': selected }">
    <section class="message-list" :aria-label="t('邮件列表')">
     <header class="list-heading">
      <div class="list-heading-title">
       <Icon :name="unread ? 'unread' : ({ inbox: 'mail', all: 'all', archive: 'archive', trash: 'trash' }[folder] || 'all')" />
       <h1>{{ heading }}</h1>
      </div>
      <span class="list-heading-count" :title="t('当前筛选共 ') + total + t(' 封')">
       {{ total.toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') }}{{ t(' 封') }}
      </span>
     </header>
     <div class="mobile-account-filter"><select v-model="accountId" :aria-label="t('筛选邮箱')" @change="filter"><option :value="0">{{ t("所有邮箱") }}</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.display_name || a.email }}</option></select></div>
     <div v-if="folder === 'trash' && accounts.length" class="trash-tools">
      <button v-if="trashAccount" class="secondary" :disabled="clearingTrash" @click="emptyTrash">{{ t('清空废纸篓') }}</button>
      <small v-else>{{ t('选择一个邮箱后可清空废纸篓。') }}</small>
     </div>
     <p v-if="loading && !emails.length" class="list-status" role="status">{{ t("正在加载邮件…") }}</p>
     <div v-else-if="!emails.length" class="empty-list"><div class="empty-symbol">✉</div><h2>{{ accounts.length ? t('这里很安静') : t('从连接一个邮箱开始') }}</h2><p>{{ query ? t('没有找到匹配邮件，试试其他关键词。') : accounts.length ? t('暂无符合条件的邮件。你可以同步邮箱，或调整筛选条件。') : t('把工作与生活的来信，汇集在一个地方。') }}</p><button v-if="!accounts.length" class="primary" @click="settings = 'account'">{{ t("＋ 连接邮箱") }}</button></div>
     <ul v-else class="email-items"><li v-for="email in emails" :key="email.id"><button class="email-item" :class="{ selected: selected?.id === email.id, unread: !email.is_read }" @click="selected = email"><div class="email-item-top"><span class="sender">{{ email.from_name || email.from_addr || t('未知发件人') }}</span><time :datetime="new Date(email.date * 1000).toISOString()">{{ date(email.date) }}</time></div><div class="email-subject"><span v-if="!email.is_read" class="unread-dot"></span><strong>{{ email.subject || t('（无主题）') }}</strong><span v-if="email.has_attachments" :aria-label="t('有附件')">↧</span><span v-if="email.is_starred" :aria-label="t('已加星标')">★</span></div><p>{{ email.snippet || t('（暂无摘要）') }}</p><span v-if="email.pending_action" class="pending-label">{{ t("正在确认移动 · ") }}</span><span class="account-tag">{{ accountName(email.account_id) }}</span></button></li></ul>
     <nav v-if="total || previousCursors.length" class="mail-pagination" :aria-label="t('邮件分页')"><button class="secondary" :disabled="loading || navigating || pageDirty || !previousCursors.length" @click="movePage(false)">{{ t("上一页") }}</button><span><strong>{{ t("第 ") }}{{ previousCursors.length + 1 }}{{ t(" 页") }}</strong><small>{{ t("每页 50 封") }}</small></span><button class="secondary" :disabled="loading || navigating || pageDirty || !cursor" @click="movePage(true)">{{ t("下一页") }}</button></nav>
    </section>
    <section class="reader" :aria-label="t('邮件预览')">
     <MessageReader v-if="selected" :email="selected" :account-name="accountName(selected.account_id)" @back="selected = null" @purged="filter(); void loadStats()" @flags="applyFlags" @compose="compose" />
     <div v-else class="reader-empty"><img src="/meldmail-mark.svg" alt="" class="empty-mark"><h2>{{ t("选择一封邮件") }}</h2><p>{{ t("在这里阅读、回复和处理来信。") }}</p></div>
    </section>
   </div>
   <div class="background-activity" role="status">
    <span class="status-dot" :class="{ active: connected && (background.prefetch || background.syncing) }" aria-hidden="true"></span>
    <span class="truncate" :title="backgroundMessage">{{ backgroundMessage }}</span>
   </div>
   <SystemStats :stats="stats" :unavailable="statsUnavailable" />
  </section>
  <nav class="mobile-bottom-nav" :aria-label="t('移动端导航')"><button :class="{active: folder === 'inbox' && !unread}" @click="folder = 'inbox'; unread = false; filter()">{{ t("收件箱") }}</button><button :class="{active: unread}" @click="folder = 'all'; unread = true; filter()">{{ t("未读") }}</button><button :class="{active: folder === 'all' && !unread}" @click="folder = 'all'; unread = false; filter()">{{ t("全部") }}</button><button @click="settings = 'account'">{{ t("邮箱") }}</button><button @click="openSettings">{{ t("设置") }}</button></nav>
  <button class="mobile-compose primary" :aria-label="t('撰写邮件')" :disabled="!accounts.length" @click="compose()">＋</button>
  <dialog ref="settingsDialog" class="composer settings-menu content-dialog" aria-labelledby="settings-menu-title">
   <header class="panel-heading">
    <h2 id="settings-menu-title">{{ t('设置') }}</h2>
    <button class="icon-button" :aria-label="t('关闭设置')" @click="settingsDialog?.close()">✕</button>
   </header>
   <div class="modal-scroll settings-menu-items">
    <button class="secondary" @click="settingsDialog?.close(); settings = 'transfer'"><Icon name="all" /><span>{{ t('导入 / 导出') }}</span></button>
    <button class="secondary" @click="settingsDialog?.close(); settings = 'proxy'"><Icon name="settings" /><span>{{ t('共用代理') }}</span></button>
    <button class="secondary" :aria-pressed="notificationsEnabled" @click="changeNotificationPreference">
     <Icon name="bell" /><span>{{ t('系统通知') }}</span><small>{{ notificationsEnabled ? t('已开启通知') : t('已关闭通知') }}</small>
    </button>
    <p v-if="notificationFeedback" class="settings-notice" role="status">{{ t(notificationFeedback) }}</p>
    <div class="settings-shortcuts">
     <button class="secondary" @click="openMCP('tokens')"><Icon name="code" /><span>{{ t('AI 与 MCP') }}</span></button>
     <button v-if="pendingApprovals" class="secondary" @click="openMCP('approvals')">{{ t('待审批') }} · {{ pendingApprovals }}</button>
    </div>
    <button class="secondary" @click="settingsDialog?.close(); passwordOpen = true"><Icon name="key" /><span>{{ t('修改登录密码') }}</span></button>
    <button class="secondary" @click="settingsDialog?.close(); logsOpen = true"><Icon name="code" /><span>{{ t('诊断日志') }}</span></button>
   </div>
  </dialog>
  <Composer v-if="composing" :accounts="accounts" :action="composing.action" :original="composing.original" @close="composing = null" @sent="notice = t('邮件已发送')" />
  <MCPPanel v-if="mcpTab" :initial-tab="mcpTab" :revision="mcpRevision" @close="mcpTab = null" @review="reviewId = $event" @changed="refreshMCP"><template #back><DialogBack @click="returnToSettings" /></template></MCPPanel>
  <ApprovalReview v-if="reviewId" :key="reviewId" :id="reviewId" @close="reviewId = null" @changed="refreshMCP"><template v-if="mcpTab" #back><DialogBack label="返回 AI 与 MCP" @click="reviewId = null" /></template></ApprovalReview>
  <DiagnosticLogs v-if="logsOpen" :accounts="accounts" @close="logsOpen = false"><template #back><DialogBack @click="returnToSettings" /></template></DiagnosticLogs>
  <ChangePassword v-if="passwordOpen" @close="passwordOpen = false" @changed="emit('passwordChanged')"><template #back><DialogBack @click="returnToSettings" /></template></ChangePassword>
  <AccountPanel v-if="settings" :section="settings" :accounts="accounts" :initial-account-id="settingsAccount" @close="settings = null; settingsAccount = null" @changed="accountsChanged"><template v-if="settings !== 'account'" #back><DialogBack @click="returnToSettings" /></template></AccountPanel>
 </div>
</template>

<style scoped>
.settings-shortcuts { display: flex; gap: 4px; }
.settings-shortcuts > button:first-child { flex: 1; }
.sidebar .footer-actions .approval-shortcut { flex: 0 0 auto; width: auto; padding-inline: 8px; }
.settings-menu.composer[open] { width: min(480px, calc(100vw - 32px)); }
.settings-menu-items { display: grid; align-content: start; gap: 6px; scrollbar-gutter: auto; padding: 0 2px; }
.settings-menu-items button { display: flex; align-items: center; gap: 12px; min-height: 44px; text-align: start; font-size: 13px; }
.settings-menu-items .ui-icon { width: 16px; height: 16px; flex: 0 0 16px; }
.settings-menu-items small { margin-inline-start: auto; color: var(--muted); }
.sync-progress:empty { display: none; }
.sync-progress { flex: none; font-size: 12px; color: var(--accent); white-space: nowrap; }
.workspace-toolbar .sync-button.syncing { opacity: 1; }
.settings-notice { margin: 0 0 4px; color: var(--muted); font-size: 12px; }
</style>
