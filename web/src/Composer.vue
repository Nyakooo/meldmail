<script setup lang="ts">
import { t } from './i18n'
import { ref, onMounted, onBeforeUnmount } from 'vue'
import type { Account, Email, ComposeAction } from './types'
import { request, errorMessage } from './http'
const props = defineProps<{ accounts: Account[]; action: ComposeAction; original?: Email }>()
const emit = defineEmits<{ close: []; sent: [] }>()
const accountId = ref(props.original?.account_id || props.accounts[0]?.id || 0)
const to = ref(props.action === 'reply' ? props.original?.from_addr || '' : '')
const cc = ref('')
const subject = ref(props.original ? `${props.action === 'reply' ? 'Re:' : 'Fwd:'} ${props.original.subject}` : '')
const text = ref('')
const busy = ref(false)
const error = ref('')
const dialog = ref<HTMLDialogElement | null>(null)
let disposed = false
function closed() { if (!disposed) emit('close') }
const recipients = (value: string) => value.split(/[,;，；]/).map(value => value.trim()).filter(Boolean)
function close() { if (busy.value) return; if (text.value && !window.confirm(t('放弃这封尚未发送的邮件？'))) return; dialog.value?.close() }
async function send() {
 busy.value = true; error.value = ''
 try { await request('/api/emails/send', { method: 'POST', body: JSON.stringify({ account_id: accountId.value, to: recipients(to.value), cc: recipients(cc.value), subject: subject.value, text: text.value, action: props.action, email_id: props.original?.id }) }); text.value = ''; emit('sent'); dialog.value?.close() } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
onMounted(() => dialog.value?.showModal())
onBeforeUnmount(() => { disposed = true; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="composer" aria-labelledby="compose-title" @close="closed" @cancel.prevent="close">
  <header class="panel-heading"><div><p class="eyebrow">A NOTE FROM YOU</p><h2 id="compose-title">{{ action === 'reply' ? t('回复邮件') : action === 'forward' ? t('转发邮件') : t('撰写邮件') }}</h2></div><button class="icon-button" :disabled="busy" :aria-label="t('关闭撰写窗口')" @click="close">✕</button></header>
  <form @submit.prevent="send"><div class="modal-scroll compose-fields"><label>{{ t("发件邮箱") }}<select v-model="accountId" :disabled="action !== 'send' || busy"><option v-for="account in accounts" :key="account.id" :value="account.id">{{ account.display_name || account.email }} · {{ account.email }}</option></select></label><label>{{ t("收件人") }}<input v-model="to" required :disabled="busy" :placeholder="t('name@example.com，多个地址用逗号分隔')"></label><label>{{ t("抄送") }}<input v-model="cc" :disabled="busy" :placeholder="t('可选')"></label><label>{{ t("主题") }}<input v-model="subject" :disabled="busy" maxlength="8192" :placeholder="t('（无主题）')"></label><label>{{ t("正文") }}<textarea v-model="text" :disabled="busy" rows="12" :placeholder="action === 'forward' ? t('添加一段说明…') : t('写下你想说的…')"></textarea></label>
   <p v-if="action === 'forward'" class="compose-hint">{{ t("原邮件正文与附件将随信转发。") }}</p><p v-if="error" class="error" role="alert">{{ t(error) }}</p></div><footer class="form-actions"><button class="secondary" type="button" :disabled="busy" @click="close">{{ t("取消") }}</button><button class="primary" :disabled="busy || !accountId">{{ busy ? t('正在发送…') : t('发送邮件 →') }}</button></footer>
  </form>
 </dialog>
</template>
