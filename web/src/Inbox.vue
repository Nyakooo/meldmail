<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import AccountPanel from './AccountPanel.vue'
import type { Account, Email, EmailPage } from './types'
import { request, errorMessage } from './http'
const emit = defineEmits<{ logout: [] }>()
const accounts = ref<Account[]>([])
const emails = ref<Email[]>([])
const accountId = ref(0)
const unread = ref(false)
const query = ref('')
const folder = ref('inbox')
const cursor = ref('')
const selected = ref<Email | null>(null)
const settings = ref(false)
const loading = ref(false)
const syncing = ref(false)
const error = ref('')
const sidebarOpen = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | undefined
let sequence = 0
let activeRequest: AbortController | undefined
const accountName = (id: number) => { const a = accounts.value.find(a => a.id === id); return a?.display_name || a?.email || '邮箱' }
const heading = computed(() => folder.value === 'inbox' ? '统一收件箱' : '全部邮件')
function date(value: number) { return new Intl.DateTimeFormat('zh-CN', { month: 'short', day: 'numeric' }).format(new Date(value * 1000)) }
function fullDate(value: number) { return new Date(value * 1000).toLocaleString('zh-CN') }
async function loadAccounts() { accounts.value = await request<Account[]>('/api/accounts') }
async function loadEmails(append = false) {
 activeRequest?.abort(); const controller = new AbortController(); activeRequest = controller
 const current = ++sequence; loading.value = true; error.value = ''
 const params = new URLSearchParams({ limit: '50', folder: folder.value })
 if (accountId.value) params.set('account_id', String(accountId.value))
 if (unread.value) params.set('unread', 'true')
 if (query.value.trim()) params.set('q', query.value.trim())
 if (append && cursor.value) params.set('cursor', cursor.value)
 try {
  const page = await request<EmailPage>(`/api/emails?${params}`, { signal: controller.signal })
  if (current !== sequence) return
  emails.value = append ? [...emails.value, ...page.emails] : page.emails; cursor.value = page.next_cursor
 } catch (err) { if (!controller.signal.aborted) error.value = errorMessage(err) } finally { if (current === sequence) loading.value = false }
}
function filter() { selected.value = null; sidebarOpen.value = false; void loadEmails() }
function search() { clearTimeout(searchTimer); searchTimer = setTimeout(filter, 250) }
async function accountsChanged() { try { await loadAccounts(); if (accountId.value && !accounts.value.some(a => a.id === accountId.value)) accountId.value = 0; await loadEmails() } catch (err) { error.value = errorMessage(err) } }
async function sync() {
 syncing.value = true; error.value = ''
 try {
  const targets = accounts.value.filter(a => a.status !== 'paused' && (!accountId.value || a.id === accountId.value))
  // Requests are deliberately sequential here; server sync paths share a three-account limiter.
  for (const account of targets) await request<{ synced: number }>(`/api/accounts/${account.id}/sync`, { method: 'POST' })
  await loadAccounts(); await loadEmails()
 } catch (err) { error.value = errorMessage(err) } finally { syncing.value = false }
}
onMounted(async () => { try { await loadAccounts(); await loadEmails() } catch (err) { error.value = errorMessage(err) } })
onBeforeUnmount(() => { clearTimeout(searchTimer); activeRequest?.abort() })
</script>
<template>
 <div class="workspace">
  <aside class="sidebar" :class="{ open: sidebarOpen }">
   <a class="brand" href="/" aria-label="MeldMail 首页"><span class="brand-icon">M</span>MeldMail</a>
   <p class="eyebrow sidebar-caption">MAILBOX</p>
   <nav aria-label="邮件文件夹"><button :class="{ active: folder === 'inbox' && !unread }" @click="folder = 'inbox'; unread = false; filter()"><span>▤</span>统一收件箱</button><button :class="{ active: unread }" @click="unread = !unread; filter()"><span>◉</span>未读邮件</button><button :class="{ active: folder === 'all' && !unread }" @click="folder = 'all'; unread = false; filter()"><span>▦</span>全部邮件</button></nav>
   <div class="account-heading"><p class="eyebrow">CONNECTED ACCOUNTS</p><button class="icon-button" aria-label="添加邮箱" @click="settings = true">＋</button></div>
   <button class="account-link" :class="{ active: accountId === 0 }" @click="accountId = 0; filter()"><span class="account-dot all-dot"></span>所有邮箱</button>
   <button v-for="(account, index) in accounts" :key="account.id" class="account-link" :class="{ active: accountId === account.id }" @click="accountId = account.id; filter()"><span class="account-dot" :style="{ background: ['#397867', '#c89349', '#6579aa', '#ad6b89'][index % 4] }"></span><span class="truncate">{{ account.display_name || account.email }}</span><span v-if="account.status === 'error'" title="连接失败">!</span><span v-else-if="account.status === 'paused'" title="已暂停">Ⅱ</span></button>
   <div class="sidebar-footer"><button @click="settings = true">⚙ 邮箱与连接</button><button @click="emit('logout')">退出登录</button><small>你的邮件，留在你的邮箱。</small></div>
  </aside>
  <button v-if="sidebarOpen" class="sidebar-shade" aria-label="关闭导航" @click="sidebarOpen = false"></button>
  <section class="mail-workspace">
   <header class="workspace-toolbar"><button class="mobile-menu icon-button" aria-label="打开导航" @click="sidebarOpen = !sidebarOpen">☰</button><label class="search-box"><span aria-hidden="true">⌕</span><input v-model="query" type="search" maxlength="256" placeholder="搜索主题、发件人或验证码" aria-label="搜索邮件" @input="search"></label><button class="secondary sync-button" :disabled="syncing || !accounts.length" @click="sync">{{ syncing ? '同步中…' : '↻ 同步' }}</button></header>
   <div v-if="error" class="workspace-error error" role="alert">{{ error }} <button class="text-button" @click="loadEmails()">重试</button></div>
   <div class="mail-columns" :class="{ 'has-selection': selected }">
    <section class="message-list" aria-label="邮件列表">
     <header class="list-heading"><div><p class="eyebrow">{{ accountId ? accountName(accountId) : 'ALL YOUR MAIL, TOGETHER' }}</p><h1>{{ heading }}</h1></div><label class="unread-toggle"><input v-model="unread" type="checkbox" @change="filter">仅未读</label></header>
     <div class="mobile-account-filter"><select v-model="accountId" aria-label="筛选邮箱" @change="filter"><option :value="0">所有邮箱</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.display_name || a.email }}</option></select></div>
     <p v-if="loading && !emails.length" class="list-status" role="status">正在加载邮件…</p>
     <div v-else-if="!emails.length" class="empty-list"><div class="empty-symbol">✉</div><h2>{{ accounts.length ? '这里很安静' : '从连接一个邮箱开始' }}</h2><p>{{ query ? '没有找到匹配邮件，试试其他关键词。' : accounts.length ? '暂无符合条件的邮件。你可以同步邮箱，或调整筛选条件。' : '把工作与生活的来信，汇集在一个地方。' }}</p><button v-if="!accounts.length" class="primary" @click="settings = true">＋ 连接邮箱</button></div>
     <ul v-else class="email-items"><li v-for="email in emails" :key="email.id"><button class="email-item" :class="{ selected: selected?.id === email.id, unread: !email.is_read }" @click="selected = email"><div class="email-item-top"><span class="sender">{{ email.from_name || email.from_addr || '未知发件人' }}</span><time :datetime="new Date(email.date * 1000).toISOString()">{{ date(email.date) }}</time></div><div class="email-subject"><span v-if="!email.is_read" class="unread-dot"></span><strong>{{ email.subject || '（无主题）' }}</strong><span v-if="email.has_attachments" aria-label="有附件">↧</span><span v-if="email.is_starred" aria-label="已加星标">★</span></div><p>{{ email.snippet || '（暂无摘要）' }}</p><span class="account-tag">{{ accountName(email.account_id) }}</span></button></li></ul>
     <button v-if="cursor" class="load-more secondary" :disabled="loading" @click="loadEmails(true)">{{ loading ? '加载中…' : '加载更多' }}</button>
    </section>
    <section class="reader" aria-label="邮件预览">
     <template v-if="selected"><header class="reader-toolbar"><button class="text-button" @click="selected = null">← 返回列表</button><span>{{ accountName(selected.account_id) }}</span></header><article class="message-preview"><span class="eyebrow">邮件预览</span><h2>{{ selected.subject || '（无主题）' }}</h2><div class="sender-detail"><div class="sender-avatar">{{ (selected.from_name || selected.from_addr || '?').slice(0, 1).toUpperCase() }}</div><div><strong>{{ selected.from_name || selected.from_addr }}</strong><p>{{ selected.from_addr }}</p><time>{{ fullDate(selected.date) }}</time></div></div><p class="preview-snippet">{{ selected.snippet }}</p></article></template>
     <div v-else class="reader-empty"><div class="reader-art" aria-hidden="true"><span>✉</span><i></i></div><p class="eyebrow">A LITTLE MORE FOCUS</p><h2>给每一封来信，留一点空间。</h2><p>选择一封邮件，开始阅读。</p></div>
    </section>
   </div>
  </section>
  <AccountPanel v-if="settings" :accounts="accounts" @close="settings = false" @changed="accountsChanged" />
 </div>
</template>
