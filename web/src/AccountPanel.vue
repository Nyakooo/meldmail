<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import type { Account, Diagnostic } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ accounts: Account[] }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const panel = ref<HTMLElement | null>(null)
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const editing = ref<number | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const diagnostics = ref<Diagnostic[]>([])
const proxy = ref('')
const currentProxy = ref('')
const form = reactive({ email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'global', proxy_url: '', status: 'active' })
function reset() {
 editing.value = null
 Object.assign(form, { email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'global', proxy_url: '', status: 'active' })
 diagnostics.value = []; error.value = ''; notice.value = ''
}
function edit(account: Account) {
 reset(); editing.value = account.id
 Object.assign(form, { email: account.email, display_name: account.display_name, provider: account.provider, auth_type: account.auth_type, imap_host: account.imap_host, imap_port: account.imap_port, imap_tls: account.imap_tls, smtp_host: account.smtp_host, smtp_port: account.smtp_port, smtp_tls: account.smtp_tls, proxy_mode: account.proxy_mode, status: account.status === 'paused' ? 'paused' : 'active' })
}
function payload() {
 const data: Record<string, string | number> = { ...form }
 if(editing.value!==null)data.id=editing.value
 if (form.provider !== 'custom' && editing.value === null) {
  for (const key of ['imap_host', 'imap_port', 'imap_tls', 'smtp_host', 'smtp_port', 'smtp_tls']) delete data[key]
 }
 if (!form.proxy_url && editing.value !== null) delete data.proxy_url
 return data
}
async function action(work: () => Promise<void>) {
 busy.value = true; error.value = ''; notice.value = ''
 try { await work() } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function save() {
 await action(async () => {
  await request<Account>(editing.value === null ? '/api/accounts' : `/api/accounts/${editing.value}`, { method: editing.value === null ? 'POST' : 'PUT', body: JSON.stringify(payload()) })
  form.password = ''; form.access_token = ''; form.proxy_url = ''; notice.value = '账号已保存'; emit('changed')
 })
}
async function test() {
 await action(async () => { const data = await request<{ diagnostics: Diagnostic[] }>('/api/accounts/test', { method: 'POST', body: JSON.stringify(payload()) }); diagnostics.value = data.diagnostics })
}
async function remove(account: Account) {
 if (!window.confirm(`删除 ${account.email}？其本地邮件索引也会删除，源邮箱中的邮件会保留。`)) return
 await action(async () => { await request<void>(`/api/accounts/${account.id}`, { method: 'DELETE' }); if (editing.value === account.id) reset(); emit('changed') })
}
async function saveProxy() {
 await action(async () => { const data = await request<{ url: string }>('/api/settings/proxy', { method: 'PUT', body: JSON.stringify({ url: proxy.value }) }); currentProxy.value = data.url; proxy.value = ''; notice.value = '默认代理已更新' })
}
function trapFocus(event: KeyboardEvent) {
 if (event.key !== 'Tab') return
 const controls = Array.from(panel.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input, select, [tabindex="0"]') ?? []).filter(element => element.offsetParent !== null)
 const first = controls[0]; const last = controls[controls.length - 1]
 if (!first || !last) { event.preventDefault(); return }
 if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last.focus() }
 else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
onBeforeUnmount(() => previousFocus?.focus())
onMounted(async () => { panel.value?.focus(); try { currentProxy.value = (await request<{ url: string }>('/api/settings/proxy')).url } catch (err) { error.value = errorMessage(err) } })
</script>
<template>
 <div class="modal-shade" @click.self="emit('close')">
  <section ref="panel" class="account-panel" tabindex="-1" @keydown="trapFocus" role="dialog" aria-modal="true" aria-labelledby="settings-title" @keydown.esc="emit('close')">
   <header class="panel-heading"><div><p class="eyebrow">YOUR CONNECTIONS</p><h2 id="settings-title">邮箱与连接</h2></div><button class="icon-button" aria-label="关闭设置" @click="emit('close')">✕</button></header>
   <p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="notice" class="notice" role="status">{{ notice }}</p>
   <div class="account-settings-grid">
    <aside><button class="secondary" @click="reset">＋ 添加邮箱</button><ul class="saved-accounts"><li v-for="account in props.accounts" :key="account.id"><button :class="{ active: editing === account.id }" @click="edit(account)"><strong>{{ account.display_name || account.email }}</strong><small>{{ account.email }}</small></button><button class="delete-button" :disabled="busy" :aria-label="`删除 ${account.email}`" @click="remove(account)">删除</button></li></ul></aside>
    <form class="account-form" @submit.prevent="save">
     <h3>{{ editing === null ? '连接新邮箱' : '编辑邮箱' }}</h3>
     <label>邮箱地址<input v-model="form.email" type="email" autocomplete="off" required maxlength="320"></label>
     <label>显示名称<input v-model="form.display_name" maxlength="256" placeholder="例如：工作邮箱"></label>
     <div class="form-row"><label>服务商<select v-model="form.provider"><option value="gmail">Gmail</option><option value="outlook">Outlook</option><option value="163">163</option><option value="126">126</option><option value="qq">QQ 邮箱</option><option value="custom">自定义 IMAP</option></select></label><label>认证<select v-model="form.auth_type"><option value="password">密码 / 授权码</option><option value="oauth2">OAuth2 Access Token</option></select></label></div>
     <label v-if="form.auth_type === 'password'">密码或应用授权码<input v-model="form.password" type="password" autocomplete="new-password" :placeholder="editing === null ? '输入邮箱授权码' : '留空保留现有凭据'"></label>
     <label v-else>Access Token<input v-model="form.access_token" type="password" autocomplete="off" :placeholder="editing === null ? '输入现有访问令牌' : '留空保留现有凭据'"></label>
     <template v-if="form.provider === 'custom' || editing !== null"><div class="form-row"><label>IMAP 主机<input v-model="form.imap_host" required></label><label class="port-field">端口<input v-model.number="form.imap_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.imap_tls"><option value="implicit">隐式 TLS</option><option value="starttls">STARTTLS</option></select></label></div><div class="form-row"><label>SMTP 主机<input v-model="form.smtp_host" required></label><label class="port-field">端口<input v-model.number="form.smtp_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.smtp_tls"><option value="implicit">隐式 TLS</option><option value="starttls">STARTTLS</option></select></label></div></template>
     <div class="form-row"><label>代理<select v-model="form.proxy_mode"><option value="global">使用默认代理</option><option value="direct">直接连接</option><option value="http">HTTP CONNECT</option><option value="https">HTTPS CONNECT</option><option value="socks5">SOCKS5</option></select></label><label>同步<select v-model="form.status"><option value="active">启用</option><option value="paused">暂停</option></select></label></div>
     <label v-if="!['global', 'direct'].includes(form.proxy_mode)">代理 URL<input v-model="form.proxy_url" type="password" autocomplete="off" :placeholder="editing === null ? 'socks5://user:password@host:port' : '留空保留当前代理'"></label>
     <ul v-if="diagnostics.length" class="diagnostics"><li v-for="d in diagnostics" :key="d.stage" :class="d.ok ? 'notice' : 'error'">{{ d.ok ? '✓' : '✕' }} {{ d.stage }} · {{ d.message }}</li></ul>
     <div class="form-actions"><button class="secondary" type="button" :disabled="busy" @click="test">测试连接</button><button class="primary" :disabled="busy">{{ busy ? '处理中…' : '保存邮箱' }}</button></div>
    </form>
   </div>
   <form class="proxy-settings" @submit.prevent="saveProxy"><h3>默认代理</h3><p>当前：{{ currentProxy || '直接连接' }}</p><label>新的代理 URL<input v-model="proxy" type="password" autocomplete="off" placeholder="留空并保存可改为直接连接"></label><button class="secondary" :disabled="busy">保存默认代理</button></form>
  </section>
 </div>
</template>
