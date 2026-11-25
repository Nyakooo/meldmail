<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { t } from './i18n'
import { request, errorMessage } from './http'
defineProps<{ count: number }>()
const emit = defineEmits<{ imported: [] }>()
const exportPassword = ref('')
const importPassword = ref('')
const importing = ref(false)
const exporting = ref(false)
const bundle = ref<unknown>(null)
const filename = ref('')
const error = ref('')
const result = ref<{ added: number; updated: number; duplicates: number } | null>(null)
let active = true
const controller = new AbortController()
async function choose(event: Event) {
 const input = event.target as HTMLInputElement
 const file = input.files?.[0]
 bundle.value = null; filename.value = ''; error.value = ''; result.value = null
 if (!file) return
 try {
  if (file.size > 1024 * 1024) throw new Error(t('配置文件不能超过 1 MiB'))
  const parsed: unknown = JSON.parse(await file.text())
  if (!active) return
  if (!parsed || typeof parsed !== 'object' || !('format' in parsed) || parsed.format !== 'meldmail-config') throw new Error(t('请选择 MeldMail 导出的加密配置文件'))
  bundle.value = parsed; filename.value = file.name
 } catch { if (active) error.value = t('配置文件无效或超过大小限制') }
 finally { input.value = '' }
}
async function exportConfig() {
 exporting.value = true; error.value = ''
 try {
  const data = await request<unknown>('/api/accounts/export', { method: 'POST', body: JSON.stringify({ password: exportPassword.value }), signal: controller.signal })
  if (!active) return
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'meldmail-config.json'; document.body.append(anchor); anchor.click(); anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  exportPassword.value = ''
 } catch (err) { if (active) error.value = errorMessage(err) } finally { if (active) exporting.value = false }
}
async function importConfig() {
 importing.value = true; error.value = ''; result.value = null
 try {
  const data = await request<{ added: number; updated: number; duplicates: number }>('/api/accounts/import', { method: 'POST', body: JSON.stringify({ password: importPassword.value, bundle: bundle.value }), signal: controller.signal })
  if (!active) return
  result.value = data; importPassword.value = ''; bundle.value = null; filename.value = ''; emit('imported')
 } catch (err) { if (active) error.value = errorMessage(err) } finally { if (active) importing.value = false }
}
onBeforeUnmount(() => { active = false; controller.abort(); exportPassword.value = ''; importPassword.value = ''; bundle.value = null })
</script>
<template>
 <section class="configuration-transfer">
  <header><h3>{{ t('配置迁移') }}</h3><p class="field-hint">{{ t('复用现有邮箱授权码和代理；相同邮箱自动合并，邮件内容不会导出。') }}</p></header>
  <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
  <p v-if="result" class="notice" role="status">{{ t('导入完成') }} · {{ t('新增') }} {{ result.added }} · {{ t('更新') }} {{ result.updated }} · {{ t('去重') }} {{ result.duplicates }}</p>
  <div class="transfer-grid">
   <form class="transfer-card" @submit.prevent="exportConfig"><h3>{{ t('导出配置') }}</h3><p>{{ t('使用独立密码保护配置文件，在另一台 MeldMail 导入时填写此密码。') }}</p><p class="transfer-scope">{{ t('导出范围') }} · {{ count }} {{ t('个邮箱') }}</p><label>{{ t('导出密码') }}<input v-model="exportPassword" type="password" minlength="12" maxlength="128" autocomplete="new-password" required :disabled="exporting || importing" :placeholder="t('至少 12 个字符')"></label><button class="primary" :disabled="!count || exporting || importing">{{ exporting ? t('正在导出…') : t('下载加密配置') }}</button></form>
   <form class="transfer-card" @submit.prevent="importConfig"><h3>{{ t('导入配置') }}</h3><p>{{ t('导入后直接使用已有凭据。相同邮箱会更新连接配置，不会清空已有邮件。') }}</p><label class="config-file-button secondary">{{ filename || t('选择加密配置文件') }}<input type="file" accept=".json,application/json" :disabled="exporting || importing" @change="choose"></label><label>{{ t('导入密码') }}<input v-model="importPassword" type="password" minlength="12" maxlength="128" autocomplete="off" required :disabled="exporting || importing" :placeholder="t('填写导出时设置的密码')"></label><button class="primary" :disabled="!bundle || exporting || importing">{{ importing ? t('正在导入…') : t('导入并合并') }}</button></form>
  </div>
  <p class="field-hint">{{ t('授权码或令牌需仍有效；导入不会延长 OAuth 令牌的有效期。') }}</p>
 </section>
</template>
