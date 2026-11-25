<script setup lang="ts">
import { t, locale } from './i18n'
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
const statuses: Record<string, string> = { pending: t('等待你审批'), approved: t('正在执行'), executed: t('已发送'), rejected: t('已拒绝'), failed: t('失败 / 发送结果待确认') }
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
 try { const response = await request<{ status: string; audit_warning?: boolean }>(`/api/mcp/approvals/${encodeURIComponent(props.id)}/${decision}`, { method: 'POST' }); emit('changed'); if (response.audit_warning) { await load(); error.value = t('邮件已发送，但审计结果未能写入。请勿重复发送。') } else dialog.value?.close() } catch (err) { error.value = errorMessage(err); await load(); emit('changed') } finally { busy.value = false }
}
onMounted(() => { dialog.value?.showModal(); void load() })
onBeforeUnmount(() => { controller?.abort(); action.value = null; source.value = null; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="approval-review" aria-labelledby="approval-title" @close="emit('close')" @cancel.prevent="!busy && dialog?.close()">
  <header class="panel-heading"><div><p class="eyebrow">YOU HAVE THE FINAL SAY</p><h2 id="approval-title">{{ t("邮件发送审批") }}</h2></div><button class="icon-button" :disabled="busy" :aria-label="t('关闭审批')" @click="dialog?.close()">✕</button></header>
  <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
  <template v-if="action"><p class="approval-status">{{ t(statuses[action.status] || action.status) }} · {{ action.token_name }}</p><dl class="approval-details"><dt>{{ t("发件人") }}</dt><dd>{{ action.sender_email }}</dd><dt>{{ t("收件人") }}</dt><dd>{{ action.payload.to?.join(', ') || t('（无）') }}</dd><template v-if="action.payload.cc?.length"><dt>{{ t("抄送") }}</dt><dd>{{ action.payload.cc.join(', ') }}</dd></template><dt>{{ t("主题") }}</dt><dd>{{ action.payload.subject || t('（无主题）') }}</dd></dl>
   <p v-if="action.error_msg" class="error">{{ action.error_msg }}</p><section v-if="action.body_available" class="approval-body"><h3>{{ action.payload.html ? t('HTML 源码（不会执行）') : action.action_type === 'forward' ? t('转发说明') : t('正文') }}</h3><pre>{{ action.payload.html || action.payload.text || t('（空正文）') }}</pre></section>
   <section v-if="action.payload.email_id" class="approval-source"><p>{{ t("关联原邮件 #") }}{{ action.payload.email_id }}{{ action.action_type === 'forward' ? t('；批准后将包含其正文与附件。') : '' }}</p><button class="secondary" @click="previewSource">{{ t("查看原邮件") }}</button><div v-if="source"><h3>{{ source.subject }}</h3><pre>{{ source.text || source.html }}</pre><p v-for="attachment in source.attachments" :key="attachment.part_id">{{ t("附件：") }}{{ attachment.filename }}</p></div></section>
   <p class="compose-hint">{{ action.status === 'pending' ? t('审批截止说明').replace('{time}',new Date(action.expires_at * 1000).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US')) : t('正文不会保留在审批历史中。') }}</p>
   <footer v-if="action.status === 'pending' && action.body_available" class="form-actions"><button class="secondary" :disabled="busy" @click="decide('reject')">{{ t("拒绝") }}</button><button class="primary" :disabled="busy" @click="decide('approve')">{{ busy ? t('正在处理…') : t('批准并发送') }}</button></footer>
  </template><p v-else class="list-status">{{ t("正在读取审批请求…") }}</p>
 </dialog>
</template>
