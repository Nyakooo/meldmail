<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import type { Approval, EmailContent } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ id: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const action = ref<Approval | null>(null)
const source = ref<EmailContent | null>(null)
const busy = ref(false)
const error = ref('')
const statuses: Record<string, string> = { pending: '等待你审批', approved: '正在执行', executed: '已发送', rejected: '已拒绝', failed: '失败 / 发送结果待确认' }
let controller: AbortController | undefined
async function load() {
 controller?.abort(); controller = new AbortController()
 try { action.value = await request<Approval>(`/api/mcp/approvals/${encodeURIComponent(props.id)}`, { signal: controller.signal }) } catch (err) { if (!controller.signal.aborted) error.value = errorMessage(err) }
}
async function previewSource() {
 const id = action.value?.payload.email_id; if (!id) return
 try { const active = controller; const content = await request<EmailContent>(`/api/emails/${id}`, { signal: active?.signal }); if (!active?.signal.aborted) source.value = content } catch (err) { error.value = errorMessage(err) }
}
async function decide(decision: 'approve' | 'reject') {
 busy.value = true; error.value = ''
 try { await request(`/api/mcp/approvals/${encodeURIComponent(props.id)}/${decision}`, { method: 'POST' }); emit('changed'); dialog.value?.close() } catch (err) { error.value = errorMessage(err); await load(); emit('changed') } finally { busy.value = false }
}
onMounted(() => { dialog.value?.showModal(); void load() })
onBeforeUnmount(() => { controller?.abort(); action.value = null; source.value = null; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="approval-review" aria-labelledby="approval-title" @close="emit('close')" @cancel.prevent="!busy && dialog?.close()">
  <header class="panel-heading"><div><p class="eyebrow">YOU HAVE THE FINAL SAY</p><h2 id="approval-title">邮件发送审批</h2></div><button class="icon-button" :disabled="busy" aria-label="关闭审批" @click="dialog?.close()">✕</button></header>
  <p v-if="error" class="error" role="alert">{{ error }}</p>
  <template v-if="action"><p class="approval-status">{{ statuses[action.status] || action.status }} · {{ action.token_name }}</p><dl class="approval-details"><dt>发件人</dt><dd>{{ action.sender_email }}</dd><dt>收件人</dt><dd>{{ action.payload.to?.join(', ') || '（无）' }}</dd><template v-if="action.payload.cc?.length"><dt>抄送</dt><dd>{{ action.payload.cc.join(', ') }}</dd></template><dt>主题</dt><dd>{{ action.payload.subject || '（无主题）' }}</dd></dl>
   <p v-if="action.error_msg" class="error">{{ action.error_msg }}</p><section v-if="action.body_available" class="approval-body"><h3>{{ action.payload.html ? 'HTML 源码（不会执行）' : action.action_type === 'forward' ? '转发说明' : '正文' }}</h3><pre>{{ action.payload.text || action.payload.html || '（空正文）' }}</pre></section>
   <section v-if="action.payload.email_id" class="approval-source"><p>关联原邮件 #{{ action.payload.email_id }}{{ action.action_type === 'forward' ? '；批准后将包含其正文与附件。' : '' }}</p><button class="secondary" @click="previewSource">查看原邮件</button><div v-if="source"><h3>{{ source.subject }}</h3><pre>{{ source.text || source.html }}</pre><p v-for="attachment in source.attachments" :key="attachment.part_id">附件：{{ attachment.filename }}</p></div></section>
   <p class="compose-hint">{{ action.status === 'pending' ? `审批截止：${new Date(action.expires_at * 1000).toLocaleString('zh-CN')}。正文只在内存中等待审批。` : '正文不会保留在审批历史中。' }}</p>
   <footer v-if="action.status === 'pending' && action.body_available" class="form-actions"><button class="secondary" :disabled="busy" @click="decide('reject')">拒绝</button><button class="primary" :disabled="busy" @click="decide('approve')">{{ busy ? '正在处理…' : '批准并发送' }}</button></footer>
  </template><p v-else class="list-status">正在读取审批请求…</p>
 </dialog>
</template>
