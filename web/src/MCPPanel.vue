<script setup lang="ts">
import { t } from './i18n'
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import type { MCPToken, Approval, AuditEntry } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ revision: number; initialTab: 'tokens' | 'approvals' }>()
const emit = defineEmits<{ close: []; review: [id: string]; changed: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const tab = ref<string>(props.initialTab)
const tokens = ref<MCPToken[]>([])
const approvals = ref<Approval[]>([])
const audit = ref<AuditEntry[]>([])
const name = ref('')
const scope = ref<'readonly' | 'readwrite'>('readonly')
const approval = ref(true)
const secret = ref('')
const busy = ref(false)
const error = ref('')
const endpoint = `${window.location.origin}/mcp`
const statuses: Record<string, string> = { pending: t('待审批'), approved: t('正在执行'), executed: t('已执行'), rejected: t('已拒绝'), failed: t('失败 / 待确认'), success: t('成功'), denied: t('已拒绝'), error: t('失败') }
async function refresh() { try { [tokens.value, approvals.value, audit.value] = await Promise.all([request<MCPToken[]>('/api/mcp/tokens'), request<Approval[]>('/api/mcp/approvals'), request<AuditEntry[]>('/api/mcp/audit')]) } catch (err) { error.value = errorMessage(err) } }
async function create() {
 busy.value = true; error.value = ''; secret.value = ''
 try { const response = await request<{ token: string; metadata: MCPToken }>('/api/mcp/tokens', { method: 'POST', body: JSON.stringify({ name: name.value, scope: scope.value, require_approval: approval.value }) }); secret.value = response.token; name.value = ''; await refresh(); emit('changed') } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function revoke(token: MCPToken) {
 if (!window.confirm(t('撤销令牌确认').replace('{name}',token.name))) return
 busy.value = true; error.value = ''
 try { await request<void>(`/api/mcp/tokens/${token.id}`, { method: 'DELETE' }); await refresh(); emit('changed') } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function deleteRevoked(token: MCPToken) {
 if (!window.confirm(t('永久删除已撤销的令牌「{name}」及其审批记录？审计日志会保留。').replace('{name}', token.name))) return
 busy.value = true; error.value = ''
 try { await request<void>(`/api/mcp/tokens/${token.id}/revoked`, { method: 'DELETE' }); await refresh(); emit('changed') }
 catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function copy() { try { await navigator.clipboard.writeText(secret.value) } catch { error.value = t('无法访问剪贴板，请手动复制令牌') } }
watch(() => props.revision, refresh)
onMounted(() => { dialog.value?.showModal(); void refresh() })
onBeforeUnmount(() => { secret.value = ''; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="mcp-panel content-dialog" aria-labelledby="mcp-title" @close="emit('close')" @cancel.prevent="dialog?.close()">
  <header class="panel-heading"><div><p class="eyebrow">CONTROLLED ACCESS</p><div class="dialog-title"><slot name="back" /><h2 id="mcp-title">{{ t("AI 与 MCP") }}</h2></div></div><button class="icon-button" :aria-label="t('关闭 MCP 设置')" @click="dialog?.close()">✕</button></header>
  <p class="mcp-endpoint">Streamable HTTP · <code>{{ endpoint }}</code></p>
  <nav class="mcp-tabs" :aria-label="t('MCP 设置分类')"><button :class="{ active: tab === 'tokens' }" @click="tab = 'tokens'">{{ t("访问令牌") }}</button><button :class="{ active: tab === 'approvals' }" @click="tab = 'approvals'">{{ t("审批记录") }}</button><button :class="{ active: tab === 'audit' }" @click="tab = 'audit'">{{ t("审计日志") }}</button></nav>
  <div class="modal-scroll mcp-content"><p v-if="error" class="error" role="alert">{{ t(error) }}</p>
  <section v-if="tab === 'tokens'" class="mcp-token-layout">
   <form class="token-form" @submit.prevent="create"><label>{{ t("令牌名称") }}<input v-model="name" required maxlength="80" :placeholder="t('例如：个人助理')"></label><label>{{ t("权限") }}<select v-model="scope" :aria-label="t('权限')"><option value="readonly">{{ t("只读 · 列表、搜索与阅读") }}</option><option value="readwrite">{{ t("读写 · 状态修改与发信") }}</option></select></label><label class="inline-checkbox"><input v-model="approval" type="checkbox" :disabled="scope === 'readonly'">{{ t("发信需要我审批") }}</label><p v-if="scope === 'readwrite' && !approval" class="compose-hint">{{ t("此令牌可以直接发送邮件，无需再次批准。") }}</p><button class="primary" :disabled="busy">{{ t("创建令牌") }}</button></form>
   <section v-if="secret" class="token-secret"><strong>{{ t("新令牌只显示这一次，请妥善保存。") }}</strong><label>{{ t("新访问令牌") }}<input :value="secret" type="password" readonly autocomplete="off" :aria-label="t('新访问令牌')"></label><button class="secondary" @click="copy">{{ t("复制令牌") }}</button></section>
   <ul v-if="tokens.length" class="mcp-records"><li v-for="token in tokens" :key="token.id"><div><strong>{{ token.name }}</strong><p>{{ token.scope === 'readonly' ? t('只读') : t('读写') }} · {{ token.require_approval ? t('发信需审批') : t('直接发信') }} · {{ token.enabled ? t('有效') : t('已撤销') }}</p></div><button v-if="token.enabled" class="text-button" :disabled="busy" :aria-label="t('撤销') + ' ' + token.name" @click="revoke(token)">{{ t("撤销") }}</button><button v-else class="text-button" :disabled="busy" :aria-label="t('删除令牌') + ' ' + token.name" @click="deleteRevoked(token)">{{ t('删除令牌') }}</button></li></ul>
  </section>
  <section v-else-if="tab === 'approvals'"><p v-if="!approvals.length" class="list-status">{{ t("暂无审批记录") }}</p><ul class="mcp-records"><li v-for="action in approvals" :key="action.id"><div><strong>{{ action.payload.subject || t('（无主题）') }}</strong><p>{{ action.token_name }} · {{ t(statuses[action.status] || action.status) }} · {{ new Date(action.created_at * 1000).toLocaleString('zh-CN') }}</p><small v-if="action.error_msg" class="error">{{ action.error_msg }}</small></div><button class="secondary" @click="emit('review', action.id)">{{ action.status === 'pending' ? t('审核') : t('查看') }}</button></li></ul></section>
  <section v-else><p v-if="!audit.length" class="list-status">{{ t("暂无审计记录") }}</p><ul class="mcp-records audit-records"><li v-for="entry in audit" :key="entry.id"><div><strong>{{ entry.tool_name }}</strong><p>{{ t(statuses[entry.status] || entry.status) }} · Token #{{ entry.token_id || '—' }} · {{ new Date(entry.created_at * 1000).toLocaleString('zh-CN') }}</p><code>{{ entry.args_summary }}</code></div></li></ul></section>
  </div>
 </dialog>
</template>

<style scoped>
.mcp-panel { --dialog-width: 640px; }
.mcp-endpoint { margin: 0; padding: 10px 12px; border-radius: 6px; background: var(--canvas); }
.mcp-token-layout { grid-template-columns: minmax(0, 1fr); gap: 16px; }
.mcp-token-layout > .mcp-records { border-top: 1px solid var(--line); }
.mcp-content { scrollbar-gutter: auto; }
</style>
