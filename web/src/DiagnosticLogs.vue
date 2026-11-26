<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { errorMessage, request } from './http'
import { t } from './i18n'
import type { Account } from './types'

defineProps<{ accounts: Account[] }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const output = ref<HTMLPreElement | null>(null)
const accountId = ref(0)
const entries = ref<Array<{ account_id?: number }>>([])
const busy = ref(false)
const maximized = ref(false)
const cleared = ref(false)
const error = ref('')
const notice = ref('')
const controller = new AbortController()
const visibleLogs = computed(() => entries.value
 .filter(entry => accountId.value === 0 || entry.account_id === accountId.value)
 .map(entry => JSON.stringify(entry))
 .join('\n'))

async function refresh() {
 busy.value = true
 error.value = ''
 notice.value = ''
 try {
  entries.value = await request('/api/settings/logs', { signal: controller.signal })
  cleared.value = false
  if (output.value) output.value.scrollTop = 0
 } catch (err) {
  if (!controller.signal.aborted) error.value = errorMessage(err)
 } finally {
  busy.value = false
 }
}

function clear() {
 entries.value = []
 cleared.value = true
 notice.value = ''
}

async function copy() {
 try {
  await navigator.clipboard.writeText(visibleLogs.value)
  notice.value = '已复制日志'
 } catch {
  const selection = window.getSelection()
  if (!output.value || !selection) return
  const range = document.createRange()
  range.selectNodeContents(output.value)
  selection.removeAllRanges()
  selection.addRange(range)
  notice.value = '日志已选中，请手动复制'
 }
}

onMounted(() => {
 dialog.value?.showModal()
 void refresh()
})
onBeforeUnmount(() => controller.abort())
</script>

<template>
 <dialog ref="dialog" class="composer diagnostic-dialog" :class="{ maximized }" aria-labelledby="diagnostic-title" @close="emit('close')">
  <header class="panel-heading">
   <h2 id="diagnostic-title">{{ t('诊断日志') }}</h2>
   <div class="diagnostic-actions">
    <button class="secondary" @click="maximized = !maximized">{{ t(maximized ? '还原窗口' : '最大化查看') }}</button>
    <button class="icon-button" :aria-label="t('关闭诊断日志')" @click="dialog?.close()">✕</button>
   </div>
  </header>
  <div class="diagnostic-actions">
   <select v-model="accountId" :aria-label="t('筛选邮箱')" @change="notice = ''">
    <option :value="0">{{ t('所有邮箱') }}</option>
    <option v-for="account in accounts" :key="account.id" :value="account.id">{{ account.display_name || account.email }}</option>
   </select>
   <button class="secondary" :disabled="busy" @click="refresh">{{ busy ? t('正在读取…') : t('刷新日志') }}</button>
   <button class="secondary" :disabled="!visibleLogs" @click="copy">{{ t('复制日志') }}</button>
   <button class="secondary" :disabled="busy || !entries.length" @click="clear">{{ t('清屏') }}</button>
  </div>
  <p class="diagnostic-caption">{{ t('最近 100 条 · 最新优先') }}</p>
  <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
  <pre ref="output" class="diagnostic-output" tabindex="0" :aria-label="t('诊断日志')" :aria-busy="busy">{{ visibleLogs || (busy ? t('正在读取…') : error || cleared ? '' : t('暂无匹配的诊断日志')) }}</pre>
  <div class="diagnostic-notice" role="status">{{ t(notice) }}</div>
 </dialog>
</template>

<style scoped>
.diagnostic-dialog.maximized[open] {
 width: 100vw;
 max-width: 100vw;
 height: 100dvh;
 max-height: 100dvh;
 border-radius: 0;
}
.diagnostic-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.diagnostic-actions select { max-width: 100%; min-width: 0; }
.diagnostic-caption, .diagnostic-notice { color: var(--muted); font-size: 12px; }
.diagnostic-notice { min-height: 1.5em; margin-top: 8px; }
.diagnostic-output {
 flex: 1;
 min-height: 0;
 overflow: auto;
 overscroll-behavior: contain;
 margin: 0;
 padding: 12px;
 border-radius: 6px;
 background: #101418;
 color: #d8e2ed;
 font: 12px/1.6 ui-monospace, monospace;
 white-space: pre-wrap;
 overflow-wrap: anywhere;
}
</style>
