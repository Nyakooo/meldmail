<script setup lang="ts">
import { t } from './i18n'
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { request, errorMessage } from './http'
const emit = defineEmits<{ close: []; changed: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const current = ref('')
const next = ref('')
const confirmation = ref('')
const busy = ref(false)
const error = ref('')
function close() { if (!busy.value) dialog.value?.close() }
async function save() {
 error.value = ''
 if (next.value !== confirmation.value) { error.value = t('两次输入的新密码不一致'); return }
 if (new TextEncoder().encode(next.value).length > 72) { error.value = t('新密码不能超过 72 字节（中文和表情会占用多个字节）'); return }
 busy.value = true
 try {
  await request('/api/auth/password', { method: 'POST', body: JSON.stringify({ current_password: current.value, new_password: next.value }) })
  current.value = ''; next.value = ''; confirmation.value = ''; emit('changed')
 } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
onMounted(() => dialog.value?.showModal())
onBeforeUnmount(() => { current.value = ''; next.value = ''; confirmation.value = ''; if (dialog.value?.open) dialog.value.close() })
</script>
<template>
 <dialog ref="dialog" class="password-dialog composer content-dialog" aria-labelledby="change-password-title" @close="emit('close')" @cancel.prevent="close">
  <header class="panel-heading"><div class="dialog-title"><slot name="back" /><h2 id="change-password-title">{{ t("修改登录密码") }}</h2></div><button class="icon-button" :disabled="busy" :aria-label="t('关闭修改密码')" @click="close">✕</button></header>
  <p>{{ t("修改的是 MeldMail 管理员密码，不影响邮箱授权码或已有邮件。") }}</p>
  <form @submit.prevent="save"><div class="modal-scroll compose-fields">
   <label>{{ t("当前登录密码") }}<input v-model="current" type="password" autocomplete="current-password" required maxlength="72" :disabled="busy" autofocus></label>
   <label>{{ t("新登录密码") }}<input v-model="next" type="password" autocomplete="new-password" required maxlength="72" :disabled="busy"></label>
   <label>{{ t("确认新密码") }}<input v-model="confirmation" type="password" autocomplete="new-password" required maxlength="72" :disabled="busy"></label>
   <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
   <p class="compose-hint">{{ t("保存后所有网页会话退出，请使用新密码重新登录。密码会保留到重启后；.env 中的密码只用于首次初始化，不会覆盖这里的设置。") }}</p>
   </div><footer class="form-actions"><button type="button" class="secondary" :disabled="busy" @click="close">{{ t("取消") }}</button><button class="primary" :disabled="busy">{{ busy ? t('正在修改…') : t('保存新密码') }}</button></footer>
  </form>
 </dialog>
</template>
