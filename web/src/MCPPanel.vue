<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import type { MCPToken, Approval, AuditEntry } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ revision: number }>()
const emit = defineEmits<{ close: []; review: [id: string]; changed: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const tab = ref('tokens')
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
const statuses: Record<string, string> = { pending: '待审批', approved: '正在执行', executed: '已执行', rejected: '已拒绝', failed: '失败 / 待确认', success: '成功', denied: '已拒绝', error: '失败' }
async function refresh() { try { [tokens.value, approvals.value, audit.value] = await Promise.all([request<MCPToken[]>('/api/mcp/tokens'), request<Approval[]>('/api/mcp/approvals'), request<AuditEntry[]>('/api/mcp/audit')]) } catch (err) { error.value = errorMessage(err) } }
async function create() {
 busy.value = true; error.value = ''; secret.value = ''
 try { const response = await request<{ token: string; metadata: MCPToken }>('/api/mcp/tokens', { method: 'POST', body: JSON.stringify({ name: name.value, scope: scope.value, require_approval: approval.value }) }); secret.value = response.token; name.value = ''; await refresh(); emit('changed') } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function revoke(token: MCPToken) {
 if (!window.confirm(`撤销 ${token.name}？其尚未批准的发信请求也会被拒绝。`)) return
 busy.value = true; error.value = ''
 try { await request<void>(`/api/mcp/tokens/${token.id}`, { method: 'DELETE' }); await refresh(); emit('changed') } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function copy() { try { await navigator.clipboard.writeText(secret.value) } catch { error.value = '无法访问剪贴板，请手动复制令牌' } }
watch(() => props.revision, refresh)
onMounted(() => { dialog.value?.showModal(); void refresh() })
onBeforeUnmount(() => { secret.value = ''; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="mcp-panel" aria-labelledby="mcp-title" @close="emit('close')" @cancel.prevent="dialog?.close()">
  <header class="panel-heading"><div><p class="eyebrow">CONTROLLED ACCESS</p><h2 id="mcp-title">AI 与 MCP</h2></div><button class="icon-button" aria-label="关闭 MCP 设置" @click="dialog?.close()">✕</button></header>
  <p class="mcp-endpoint">Streamable HTTP · <code>{{ endpoint }}</code></p>
  <nav class="mcp-tabs" aria-label="MCP 设置分类"><button :class="{ active: tab === 'tokens' }" @click="tab = 'tokens'">访问令牌</button><button :class="{ active: tab === 'approvals' }" @click="tab = 'approvals'">审批记录</button><button :class="{ active: tab === 'audit' }" @click="tab = 'audit'">审计日志</button></nav>
  <p v-if="error" class="error" role="alert">{{ error }}</p>
  <section v-if="tab === 'tokens'">
   <form class="token-form" @submit.prevent="create"><label>令牌名称<input v-model="name" required maxlength="80" placeholder="例如：个人助理"></label><label>权限<select v-model="scope" aria-label="权限"><option value="readonly">只读 · 列表、搜索与阅读</option><option value="readwrite">读写 · 状态修改与发信</option></select></label><label class="inline-checkbox"><input v-model="approval" type="checkbox" :disabled="scope === 'readonly'">发信需要我审批</label><p v-if="scope === 'readwrite' && !approval" class="compose-hint">此令牌可以直接发送邮件，无需再次批准。</p><button class="primary" :disabled="busy">创建令牌</button></form>
   <section v-if="secret" class="token-secret"><strong>新令牌只显示这一次，请妥善保存。</strong><label>新访问令牌<input :value="secret" type="password" readonly autocomplete="off" aria-label="新访问令牌"></label><button class="secondary" @click="copy">复制令牌</button></section>
   <ul class="mcp-records"><li v-for="token in tokens" :key="token.id"><div><strong>{{ token.name }}</strong><p>{{ token.scope === 'readonly' ? '只读' : '读写' }} · {{ token.require_approval ? '发信需审批' : '直接发信' }} · {{ token.enabled ? '有效' : '已撤销' }}</p></div><button v-if="token.enabled" class="text-button" :disabled="busy" :aria-label="`撤销 ${token.name}`" @click="revoke(token)">撤销</button></li></ul>
  </section>
  <section v-else-if="tab === 'approvals'"><p v-if="!approvals.length" class="list-status">暂无审批记录</p><ul class="mcp-records"><li v-for="action in approvals" :key="action.id"><div><strong>{{ action.payload.subject || '（无主题）' }}</strong><p>{{ action.token_name }} · {{ statuses[action.status] || action.status }} · {{ new Date(action.created_at * 1000).toLocaleString('zh-CN') }}</p><small v-if="action.error_msg" class="error">{{ action.error_msg }}</small></div><button class="secondary" @click="emit('review', action.id)">{{ action.status === 'pending' ? '审核' : '查看' }}</button></li></ul></section>
  <section v-else><p v-if="!audit.length" class="list-status">暂无审计记录</p><ul class="mcp-records audit-records"><li v-for="entry in audit" :key="entry.id"><div><strong>{{ entry.tool_name }}</strong><p>{{ statuses[entry.status] || entry.status }} · Token #{{ entry.token_id || '—' }} · {{ new Date(entry.created_at * 1000).toLocaleString('zh-CN') }}</p><code>{{ entry.args_summary }}</code></div></li></ul></section>
 </dialog>
</template>
