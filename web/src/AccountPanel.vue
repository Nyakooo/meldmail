<script setup lang="ts">
import AccountTransfer from './AccountTransfer.vue'
import Icon from './Icon.vue'
import { t } from './i18n'
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import type { Account, Diagnostic } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ accounts: Account[]; initialAccountId?: number | null; section: 'account' | 'transfer' | 'proxy' }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const panel = ref<HTMLElement | null>(null)
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const editing = ref<number | null>(null)
const savedAuthType = ref('')
const busy = ref(false)
const error = ref('')
const notice = ref('')
const saveToast = ref(false)
let toastTimer: ReturnType<typeof setTimeout> | undefined
const diagnostics = ref<Diagnostic[]>([])
const proxy = ref('')
const currentProxy = ref('')
const proxyManaged = ref(false)
const accountBody = ref<HTMLElement | null>(null)
const proxyLoading = ref(false)
let proxyRequest: AbortController | undefined
function cancelProxyRead() { proxyRequest?.abort(); proxyRequest = undefined; proxyLoading.value = false }
async function loadProxy(path: string, target: 'account' | 'proxy') {
 cancelProxyRead(); const active = new AbortController(); proxyRequest = active; proxyLoading.value = true
 const account = editing.value
 try {
  const data = await request<{ url: string }>(path, { signal: active.signal })
  if (active.signal.aborted || proxyRequest !== active) return
  if (target === 'account' && props.section === 'account' && editing.value === account) form.proxy_url = data.url
  if (target === 'proxy' && props.section === 'proxy') proxy.value = data.url
 } catch (err) { if (!active.signal.aborted) error.value = errorMessage(err) }
 finally { if (proxyRequest === active) { proxyLoading.value = false; proxyRequest = undefined } }
}

const form = reactive({ email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'direct', proxy_url: '', status: 'active' })
const hasSavedCredential = computed(() => editing.value !== null && form.auth_type === savedAuthType.value)
const credentialMask = '••••••••'
function reset() {
 cancelProxyRead()
 void nextTick(() => { if (accountBody.value) accountBody.value.scrollTop = 0 })
 editing.value = null
 savedAuthType.value = ''
 Object.assign(form, { email: '', display_name: '', provider: 'gmail', auth_type: 'password', password: '', access_token: '', imap_host: '', imap_port: 993, imap_tls: 'implicit', smtp_host: '', smtp_port: 465, smtp_tls: 'implicit', proxy_mode: 'direct', proxy_url: '', status: 'active' })
 diagnostics.value = []; error.value = ''; notice.value = ''
}
function edit(account: Account) {
 reset(); editing.value = account.id
 savedAuthType.value = account.auth_type
 Object.assign(form, { email: account.email, display_name: account.display_name, provider: account.provider, auth_type: account.auth_type, imap_host: account.imap_host, imap_port: account.imap_port, imap_tls: account.imap_tls, smtp_host: account.smtp_host, smtp_port: account.smtp_port, smtp_tls: account.smtp_tls, proxy_mode: account.proxy_mode, status: account.status === 'paused' ? 'paused' : 'active' })
 if (!['direct', 'global'].includes(account.proxy_mode)) void loadProxy(`/api/accounts/${account.id}/proxy`, 'account')
}
function payload() {
 const data: Record<string, string | number> = { ...form }
 if (form.provider === 'gmail' && form.auth_type === 'password') data.password = form.password.replace(/\s/g, '')
 if(editing.value!==null)data.id=editing.value
 if (form.provider !== 'custom' && editing.value === null) {
  for (const key of ['imap_host', 'imap_port', 'imap_tls', 'smtp_host', 'smtp_port', 'smtp_tls']) delete data[key]
 }
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
  emit('changed')
  clearTimeout(toastTimer)
  saveToast.value = true
  toastTimer = setTimeout(() => { saveToast.value = false }, 3500)
 })
}
function validateCredentials() {
 if (!form.email.trim()) { error.value = t('请先填写完整邮箱地址'); return false }
 const credential = form.auth_type === 'password' ? String(payload().password).trim() : form.access_token.trim()
 if (!hasSavedCredential.value && !credential) {
  error.value = form.auth_type === 'oauth2' ? t('请填写新的 Access Token；切换认证方式后不能沿用原凭据。') : form.provider === 'qq' ? t('请填写 QQ 邮箱授权码；在 QQ 邮箱网页设置中开启 IMAP/SMTP 服务后生成，不是 QQ 登录密码。') : t('请填写邮箱授权码或应用专用密码。')
  notice.value = ''; return false
 }
 return true
}
async function test() {
 if (!validateCredentials()) return
 await action(async () => { const data = await request<{ diagnostics: Diagnostic[] }>('/api/accounts/test', { method: 'POST', body: JSON.stringify(payload()) }); diagnostics.value = data.diagnostics })
}
async function remove(account: Account) {
 if (!window.confirm(t('删除邮箱确认').replace('{email}',account.email))) return
 await action(async () => { await request<void>(`/api/accounts/${account.id}`, { method: 'DELETE' }); if (editing.value === account.id) reset(); emit('changed') })
}
async function refreshProxy() {
 try { const data = await request<{ url: string; managed: boolean }>('/api/settings/proxy'); currentProxy.value = data.url; proxyManaged.value = data.managed }
 catch (err) { error.value = errorMessage(err) }
}
async function saveProxy() {
 await action(async () => { const data = await request<{ url: string }>('/api/settings/proxy', { method: 'PUT', body: JSON.stringify({ url: proxy.value }) }); currentProxy.value = data.url; notice.value = t('共用代理已更新') })
 if (document.activeElement === document.body) panel.value?.focus()
}
function trapFocus(event: KeyboardEvent) {
 if (event.key !== 'Tab') return
 const controls = Array.from(panel.value?.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input, select, summary, [tabindex="0"]') ?? []).filter(element => element.offsetParent !== null)
 const first = controls[0]; const last = controls[controls.length - 1]
 if (!first || !last) { event.preventDefault(); return }
 if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last.focus() }
 else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
onBeforeUnmount(() => { clearTimeout(toastTimer); cancelProxyRead(); previousFocus?.focus() })
onMounted(() => {
 if (props.section === 'account' && props.initialAccountId) {
  const account = props.accounts.find(item => item.id === props.initialAccountId)
  if (account) edit(account)
 }
 panel.value?.focus()
 if (props.section !== 'transfer') void refreshProxy()
 if (props.section === 'proxy') void loadProxy('/api/settings/proxy/edit', 'proxy')
})
</script>
<template>
 <div class="modal-shade" @click.self="emit('close')">
  <section ref="panel" class="account-panel" :class="{ 'content-dialog': section !== 'account', 'proxy-dialog': section === 'proxy', 'transfer-dialog': section === 'transfer' }" tabindex="-1" @keydown="trapFocus" role="dialog" aria-modal="true" aria-labelledby="settings-title" @keydown.esc="emit('close')">
   <header class="panel-heading"><div><p class="eyebrow">YOUR CONNECTIONS</p><div class="dialog-title"><slot name="back" /><h2 id="settings-title">{{ t(section === 'account' ? '邮箱与连接' : section === 'transfer' ? '导入 / 导出' : '共用代理') }}</h2></div></div><button class="icon-button" :aria-label="t('关闭设置')" @click="emit('close')">✕</button></header>
   <p v-if="error" class="error" role="alert">{{ t(error) }}</p><p v-if="notice" class="notice" role="status">{{ t(notice) }}</p>
   <div class="account-settings-grid" :class="{ standalone: section !== 'account' }">
    <aside v-if="section === 'account'"><button class="secondary" @click="reset">{{ t("＋ 添加邮箱") }}</button><ul class="saved-accounts"><li v-for="account in props.accounts" :key="account.id" :class="{ selected: editing === account.id }"><button :class="{ active: editing === account.id }" @click="edit(account)"><strong>{{ account.display_name || account.email }}</strong><small>{{ account.email }}</small></button><button class="delete-button" :disabled="busy" :aria-label="t('删除') + ' ' + account.email" :title="t('删除') + ' ' + account.email" @click="remove(account)"><Icon name="trash" /></button></li></ul></aside>
    <div class="account-settings-content"><AccountTransfer v-if="section === 'transfer'" :count="accounts.length" @imported="emit('changed')" />
    <form v-else-if="section === 'proxy'" class="proxy-settings global-proxy-panel" @submit.prevent="saveProxy"><div class="modal-scroll">
     <p class="field-hint">{{ t('仅影响主动选择“使用共用代理”的邮箱；其他邮箱按各自的连接方式连接。') }}</p>
     <p class="connection-status-detail">{{ t('当前共用代理：') }}{{ currentProxy || t('未设置') }}</p>
     <label>{{ t('共用代理 URL') }}<input v-model="proxy" :readonly="proxyManaged" :disabled="busy || proxyLoading" type="text" inputmode="url" autocapitalize="none" :spellcheck="false" autocomplete="off" :placeholder="t('留空可清除共用代理')"></label>
     <p v-if="proxyManaged" class="field-hint">{{ t('共用代理由 .env 管理，请修改 MELDMAIL_HOST_PROXY_PORT 后重新部署。') }}</p><p v-else class="field-hint">{{ t('填写后，可让需要代理的邮箱主动选择复用。清空后，选择共用代理的邮箱将恢复直连。') }}</p>
     </div><div class="form-actions"><button v-if="!proxyManaged" class="primary" :disabled="busy || proxyLoading">{{ t('保存共用代理') }}</button></div>
    </form><form v-else class="account-form" @submit.prevent="save"><div ref="accountBody" class="modal-scroll account-form-body">
     <p v-if="editing && accounts.find(account => account.id === editing)?.status === 'error'" class="connection-status-detail" role="status">{{ t("连接暂不可用") }} · {{ t(accounts.find(account => account.id === editing)?.last_error || '请测试连接并检查网络或授权码') }}</p>
     <h3>{{ editing === null ? t('连接新邮箱') : t('编辑邮箱') }}</h3>
     <div class="form-row"><label>{{ t("服务商") }}<select v-model="form.provider"><option value="gmail">Gmail</option><option value="outlook">Outlook</option><option value="163">163</option><option value="126">126</option><option value="qq">{{ t("QQ 邮箱") }}</option><option value="custom">{{ t("自定义 IMAP") }}</option></select></label><label>{{ t("认证") }}<select v-model="form.auth_type"><option value="password">{{ t("密码 / 授权码") }}</option><option value="oauth2">OAuth2 Access Token</option></select></label></div>
     <details v-if="form.provider === 'gmail'" class="connection-guide" aria-labelledby="gmail-guide-title">
      <summary id="gmail-guide-title">{{ t("Gmail 接入 · 跟着这 3 步填写") }}</summary>
      <p>{{ t("当前使用应用专用密码接入；暂不提供“使用 Google 账号登录”按钮。") }}</p>
      <ol>
       <li>{{ t("打开 ") }}<a href="https://myaccount.google.com/signinoptions/two-step-verification" target="_blank" rel="noopener noreferrer">{{ t("Google 两步验证 ↗") }}</a>{{ t("，登录要接入的账号并开启两步验证。") }}</li>
       <li>{{ t("进入 ") }}<a href="https://myaccount.google.com/apppasswords" target="_blank" rel="noopener noreferrer">{{ t("应用专用密码 ↗") }}</a>{{ t("，创建名为 MeldMail 的密码，复制生成的 16 位密码。") }}</li>
       <li>{{ t("下方填写完整 Gmail 地址和应用专用密码，点击“测试连接”，通过后点击“保存邮箱”。") }}</li>
      </ol>
      <p>{{ t("个人 Gmail 的 IMAP 已默认开启。服务器会自动配置，无需手填。") }}</p>
      <details><summary>{{ t("找不到应用专用密码，或连接失败？") }}</summary>
       <p>{{ t("先确认已开启两步验证。工作／学校账号可能受组织策略限制；高级保护或仅使用安全密钥的账号也可能没有此入口。请查看 ") }}<a href="https://support.google.com/mail/answer/185833?hl=zh-Hans" target="_blank" rel="noopener noreferrer">{{ t("Google 官方帮助 ↗") }}</a>。</p>
       <p>{{ t("认证失败：确认使用的是刚生成的应用专用密码；更改 Google 登录密码后，需要重新生成。连接超时：请检查运行 MeldMail 的服务器能否访问 Google，并按需设置下方代理。") }}</p>
       <p>{{ t("自动配置：收信 imap.gmail.com:993，发信 smtp.gmail.com:465，均使用 TLS。") }}</p>
      </details>
     </details>
     <p v-if="form.auth_type === 'oauth2'" class="field-hint">{{ t("此选项需要你自行取得有效的 Access Token，暂不提供授权跳转或自动续期。Gmail 首次接入请优先选择“密码 / 授权码”，按上方步骤生成应用专用密码。") }}</p>
     <div class="account-fields"><label>{{ t("邮箱地址") }}<input v-model="form.email" type="email" autocomplete="off" required maxlength="320" :placeholder="form.provider === 'gmail' ? t('例如：yourname@gmail.com') : t('输入完整邮箱地址')"></label>
     <label>{{ t("显示名称（可选）") }}<input v-model="form.display_name" maxlength="256" :placeholder="t('例如：工作邮箱')"></label>
     <label v-if="form.auth_type === 'password'">{{ form.provider === 'gmail' ? t('Google 应用专用密码') : t('密码或应用授权码') }}<input v-model="form.password" type="password" autocomplete="new-password" aria-describedby="mail-password-hint" :placeholder="hasSavedCredential ? credentialMask : (form.provider === 'gmail' ? t('粘贴 Google 生成的 16 位密码') : t('输入邮箱授权码'))"></label>
     <label v-else>Access Token<input v-model="form.access_token" type="password" autocomplete="off" :aria-describedby="hasSavedCredential ? 'mail-password-hint' : undefined" :placeholder="hasSavedCredential ? credentialMask : t('输入现有访问令牌')"></label>
     <p v-if="hasSavedCredential" id="mail-password-hint" class="field-hint">{{ t('留空保留现有凭据') }}</p>
     <p v-else-if="form.auth_type === 'password'" id="mail-password-hint" class="field-hint">{{ form.provider === 'gmail' ? t('填写应用专用密码，不是 Google 登录密码。粘贴时的分组空格会自动去除。') : t('请先在邮箱服务商处开启 IMAP / SMTP，并按要求生成授权码。') }}</p>
     <template v-if="form.provider === 'custom' || editing !== null"><div class="form-row server-row"><label>{{ t("IMAP 主机") }}<input v-model="form.imap_host" required></label><label class="port-field">{{ t("端口") }}<input v-model.number="form.imap_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.imap_tls"><option value="implicit">{{ t("隐式 TLS") }}</option><option value="starttls">STARTTLS</option></select></label></div><div class="form-row server-row"><label>{{ t("SMTP 主机") }}<input v-model="form.smtp_host" required></label><label class="port-field">{{ t("端口") }}<input v-model.number="form.smtp_port" type="number" min="1" max="65535" required></label><label>TLS<select v-model="form.smtp_tls"><option value="implicit">{{ t("隐式 TLS") }}</option><option value="starttls">STARTTLS</option></select></label></div></template>
     <div class="form-row connection-row"><label>{{ t("连接方式") }}<select v-model="form.proxy_mode" :disabled="proxyLoading"><option value="direct">{{ t("直接连接（推荐）") }}</option><option v-if="currentProxy || form.proxy_mode === 'global'" value="global">{{ currentProxy ? t("使用共用代理") : t("共用代理未设置（当前直连）") }}</option><option value="http">{{ t("HTTP 代理") }}</option><option value="https">{{ t("HTTPS 代理") }}</option><option value="socks5">{{ t("SOCKS5 代理") }}</option></select></label><label>{{ t("同步") }}<select v-model="form.status"><option value="active">{{ t("启用") }}</option><option value="paused">{{ t("暂停") }}</option></select></label></div>
     <p class="field-hint">{{ form.proxy_mode === 'global' ? (currentProxy ? t('此邮箱使用共用代理：') + currentProxy : t('共用代理尚未配置，此邮箱当前使用直连。')) : form.proxy_mode === 'direct' ? t('此邮箱直接连接，不受共用代理设置影响。') : t('此代理只用于当前邮箱的收信和发信。') }}</p>
     <label v-if="!['global', 'direct'].includes(form.proxy_mode)" class="account-proxy-url">{{ t("代理 URL") }}<input v-model="form.proxy_url" :disabled="proxyLoading" required type="text" inputmode="url" autocapitalize="none" :spellcheck="false" autocomplete="off" placeholder="socks5://user:password@host:port"></label>
     <p v-if="!['global', 'direct'].includes(form.proxy_mode)" class="field-hint">{{ t("例如 HTTP 使用 http://代理地址:端口，SOCKS5 使用 socks5://代理地址:端口。Docker 中的 127.0.0.1 指向容器自身；宿主机代理需使用容器可访问的地址。") }}</p>
     <ul v-if="diagnostics.length" class="diagnostics"><li v-for="d in diagnostics" :key="d.stage" :class="d.ok ? 'notice' : 'error'">{{ d.ok ? '✓' : '✕' }} {{ d.stage }} · {{ t(d.message) }}</li></ul>
     </div></div><div class="form-actions"><button class="secondary" type="button" :disabled="busy || proxyLoading" @click="test">{{ t("测试连接") }}</button><button class="primary" :disabled="busy || proxyLoading">{{ busy ? t('处理中…') : t('保存邮箱') }}</button></div>
    </form></div>
   </div>

   <div class="account-toast" role="status" aria-live="polite"><span v-if="saveToast">{{ t('邮箱已保存') }}</span></div>
  </section>
 </div>
</template>

<style scoped>
.account-panel { position: relative; }
.account-toast { position: absolute; inset: auto 16px 68px; display: flex; justify-content: center; pointer-events: none; }
.account-toast > span { padding: 8px 16px; border-radius: 8px; background: var(--text); color: var(--surface); box-shadow: var(--modal-shadow); }
.account-panel.proxy-dialog { --dialog-width: 520px; }
.account-panel.transfer-dialog { --dialog-width: 880px; }
.account-settings-grid.standalone { display: flex; flex-direction: column; }
.global-proxy-panel { align-items: stretch; }
.global-proxy-panel > .modal-scroll { scrollbar-gutter: auto; }
.global-proxy-panel .field-hint:first-child { margin-top: 0; }
.global-proxy-panel .form-actions { justify-content: flex-end; }
.account-settings-grid > aside { grid-template-columns: minmax(0, 1fr); }
</style>
