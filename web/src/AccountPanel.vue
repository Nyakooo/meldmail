<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import type { Account, Diagnostic } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ accounts: Account[] }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const panel = ref<HTMLElement | null>(null)
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const editing = ref<number | null>(null)
const savedAuthType = ref('')
const busy = ref(false)
const error = ref('')
const notice = ref('')
const diagnostics = ref<Diagnostic[]>([])
const proxy = ref('')
const currentProxy = ref('')
const form = reactive({ email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'global', proxy_url: '', status: 'active' })
function reset() {
 editing.value = null
 savedAuthType.value = ''
 Object.assign(form, { email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'global', proxy_url: '', status: 'active' })
 diagnostics.value = []; error.value = ''; notice.value = ''
}
function edit(account: Account) {
 reset(); editing.value = account.id
 savedAuthType.value = account.auth_type
 Object.assign(form, { email: account.email, display_name: account.display_name, provider: account.provider, auth_type: account.auth_type, imap_host: account.imap_host, imap_port: account.imap_port, imap_tls: account.imap_tls, smtp_host: account.smtp_host, smtp_port: account.smtp_port, smtp_tls: account.smtp_tls, proxy_mode: account.proxy_mode, status: account.status === 'paused' ? 'paused' : 'active' })
}
function payload() {
 const data: Record<string, string | number> = { ...form }
 if (form.provider === 'gmail' && form.auth_type === 'password') data.password = form.password.replace(/\s/g, '')
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
 if (!validateCredentials()) return
 await action(async () => {
  const created = editing.value === null
  const saved = await request<Account>(created ? '/api/accounts' : `/api/accounts/${editing.value}`, { method: created ? 'POST' : 'PUT', body: JSON.stringify(payload()) })
  edit(saved)
  notice.value = form.status === 'paused' ? '邮箱已保存，当前暂停同步。将同步设为“启用”后开始收信。' : (created ? '邮箱已保存，正在后台同步最近 100 封邮件。关闭设置即可查看收件箱。' : '邮箱设置已更新，正在后台同步。关闭设置即可查看收件箱。'); emit('changed')
 })
}
function validateCredentials() {
 if (!form.email.trim()) { error.value = '请先填写完整邮箱地址'; return false }
 const credential = form.auth_type === 'password' ? String(payload().password).trim() : form.access_token.trim()
 if ((editing.value === null || form.auth_type !== savedAuthType.value) && !credential) {
  error.value = form.auth_type === 'oauth2' ? '请填写新的 Access Token；切换认证方式后不能沿用原凭据。' : form.provider === 'qq' ? '请填写 QQ 邮箱授权码；在 QQ 邮箱网页设置中开启 IMAP/SMTP 服务后生成，不是 QQ 登录密码。' : '请填写邮箱授权码或应用专用密码。'
  notice.value = ''; return false
 }
 return true
}
async function test() {
 if (!validateCredentials()) return
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
 const controls = Array.from(panel.value?.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input, select, summary, [tabindex="0"]') ?? []).filter(element => element.offsetParent !== null)
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
     <div class="form-row"><label>服务商<select v-model="form.provider"><option value="gmail">Gmail</option><option value="outlook">Outlook</option><option value="163">163</option><option value="126">126</option><option value="qq">QQ 邮箱</option><option value="custom">自定义 IMAP</option></select></label><label>认证<select v-model="form.auth_type"><option value="password">密码 / 授权码</option><option value="oauth2">OAuth2 Access Token</option></select></label></div>
     <section v-if="form.provider === 'gmail'" class="connection-guide" aria-labelledby="gmail-guide-title">
      <h4 id="gmail-guide-title">Gmail 接入 · 跟着这 3 步填写</h4>
      <p>当前使用应用专用密码接入；暂不提供“使用 Google 账号登录”按钮。</p>
      <ol>
       <li>打开 <a href="https://myaccount.google.com/signinoptions/two-step-verification" target="_blank" rel="noopener noreferrer">Google 两步验证 ↗</a>，登录要接入的账号并开启两步验证。</li>
       <li>进入 <a href="https://myaccount.google.com/apppasswords" target="_blank" rel="noopener noreferrer">应用专用密码 ↗</a>，创建名为 MeldMail 的密码，复制生成的 16 位密码。</li>
       <li>下方填写完整 Gmail 地址和应用专用密码，点击“测试连接”，通过后点击“保存邮箱”。</li>
      </ol>
      <p>个人 Gmail 的 IMAP 已默认开启。服务器会自动配置，无需手填。</p>
      <details><summary>找不到应用专用密码，或连接失败？</summary>
       <p>先确认已开启两步验证。工作／学校账号可能受组织策略限制；高级保护或仅使用安全密钥的账号也可能没有此入口。请查看 <a href="https://support.google.com/mail/answer/185833?hl=zh-Hans" target="_blank" rel="noopener noreferrer">Google 官方帮助 ↗</a>。</p>
       <p>认证失败：确认使用的是刚生成的应用专用密码；更改 Google 登录密码后，需要重新生成。连接超时：请检查运行 MeldMail 的服务器能否访问 Google，并按需设置下方代理。</p>
       <p>自动配置：收信 imap.gmail.com:993，发信 smtp.gmail.com:465，均使用 TLS。</p>
      </details>
     </section>
     <p v-if="form.auth_type === 'oauth2'" class="field-hint">此选项需要你自行取得有效的 Access Token，暂不提供授权跳转或自动续期。Gmail 首次接入请优先选择“密码 / 授权码”，按上方步骤生成应用专用密码。</p>
     <label>邮箱地址<input v-model="form.email" type="email" autocomplete="off" required maxlength="320" :placeholder="form.provider === 'gmail' ? '例如：yourname@gmail.com' : '输入完整邮箱地址'"></label>
     <label>显示名称（可选）<input v-model="form.display_name" maxlength="256" placeholder="例如：工作邮箱"></label>
     <label v-if="form.auth_type === 'password'">{{ form.provider === 'gmail' ? 'Google 应用专用密码' : '密码或应用授权码' }}<input v-model="form.password" type="password" autocomplete="new-password" aria-describedby="mail-password-hint" :placeholder="editing === null ? (form.provider === 'gmail' ? '粘贴 Google 生成的 16 位密码' : '输入邮箱授权码') : '留空保留现有凭据'"></label>
     <label v-else>Access Token<input v-model="form.access_token" type="password" autocomplete="off" :placeholder="editing === null ? '输入现有访问令牌' : '留空保留现有凭据'"></label>
     <p v-if="form.auth_type === 'password'" id="mail-password-hint" class="field-hint">{{ form.provider === 'gmail' ? '填写应用专用密码，不是 Google 登录密码。粘贴时的分组空格会自动去除。' : '请先在邮箱服务商处开启 IMAP / SMTP，并按要求生成授权码。' }}</p>
     <template v-if="form.provider === 'custom' || editing !== null"><div class="form-row"><label>IMAP 主机<input v-model="form.imap_host" required></label><label class="port-field">端口<input v-model.number="form.imap_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.imap_tls"><option value="implicit">隐式 TLS</option><option value="starttls">STARTTLS</option></select></label></div><div class="form-row"><label>SMTP 主机<input v-model="form.smtp_host" required></label><label class="port-field">端口<input v-model.number="form.smtp_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.smtp_tls"><option value="implicit">隐式 TLS</option><option value="starttls">STARTTLS</option></select></label></div></template>
     <div class="form-row"><label>代理<select v-model="form.proxy_mode"><option value="global">使用默认代理</option><option value="direct">直接连接</option><option value="http">HTTP CONNECT</option><option value="https">HTTPS CONNECT</option><option value="socks5">SOCKS5</option></select></label><label>同步<select v-model="form.status"><option value="active">启用</option><option value="paused">暂停</option></select></label></div>
     <p class="field-hint">{{ form.proxy_mode === 'global' ? (currentProxy ? `当前默认代理：${currentProxy}` : '默认代理尚未设置，当前会直接连接邮箱。') : form.proxy_mode === 'direct' ? '将由运行 MeldMail 的服务器直接连接邮箱。' : '此代理只用于当前邮箱的收信和发信。' }}{{ form.provider === 'gmail' ? ' 若该服务器无法直连 Google，请选择可用代理；浏览器能打开 Gmail 并不代表服务器也能连接。' : '' }}</p>
     <label v-if="!['global', 'direct'].includes(form.proxy_mode)">代理 URL<input v-model="form.proxy_url" type="password" autocomplete="off" :placeholder="editing === null ? 'socks5://user:password@host:port' : '留空保留当前代理'"></label>
     <p v-if="!['global', 'direct'].includes(form.proxy_mode)" class="field-hint">例如 HTTP 使用 http://代理地址:端口，SOCKS5 使用 socks5://代理地址:端口。Docker 中的 127.0.0.1 指向容器自身；宿主机代理需使用容器可访问的地址。</p>
     <ul v-if="diagnostics.length" class="diagnostics"><li v-for="d in diagnostics" :key="d.stage" :class="d.ok ? 'notice' : 'error'">{{ d.ok ? '✓' : '✕' }} {{ d.stage }} · {{ d.message }}</li></ul>
     <div class="form-actions"><button class="secondary" type="button" :disabled="busy" @click="test">测试连接</button><button class="primary" :disabled="busy">{{ busy ? '处理中…' : '保存邮箱' }}</button></div>
    </form>
   </div>
   <form class="proxy-settings" @submit.prevent="saveProxy"><h3>默认代理</h3><p>当前：{{ currentProxy || '直接连接' }}</p><label>新的代理 URL<input v-model="proxy" type="password" autocomplete="off" placeholder="留空并保存可改为直接连接"></label><button class="secondary" :disabled="busy">保存默认代理</button></form>
  </section>
 </div>
</template>
