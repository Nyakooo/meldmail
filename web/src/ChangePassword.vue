<script setup lang="ts">
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
 if (next.value !== confirmation.value) { error.value = '两次输入的新密码不一致'; return }
 if (new TextEncoder().encode(next.value).length > 72) { error.value = '新密码不能超过 72 字节（中文和表情会占用多个字节）'; return }
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
 <dialog ref="dialog" class="password-dialog composer" aria-labelledby="change-password-title" @close="emit('close')" @cancel.prevent="close">
  <header class="panel-heading"><h2 id="change-password-title">修改登录密码</h2><button class="icon-button" :disabled="busy" aria-label="关闭修改密码" @click="close">✕</button></header>
  <p>修改的是 MeldMail 管理员密码，不影响邮箱授权码或已有邮件。</p>
  <form @submit.prevent="save">
   <label>当前登录密码<input v-model="current" type="password" autocomplete="current-password" required maxlength="72" :disabled="busy" autofocus></label>
   <label>新登录密码<input v-model="next" type="password" autocomplete="new-password" required maxlength="72" :disabled="busy"></label>
   <label>确认新密码<input v-model="confirmation" type="password" autocomplete="new-password" required maxlength="72" :disabled="busy"></label>
   <p v-if="error" class="error" role="alert">{{ error }}</p>
   <p class="compose-hint">保存后所有网页会话退出，请使用新密码重新登录。密码会保留到重启后；.env 中的密码只用于首次初始化，不会覆盖这里的设置。</p>
   <footer class="form-actions"><button type="button" class="secondary" :disabled="busy" @click="close">取消</button><button class="primary" :disabled="busy">{{ busy ? '正在修改…' : '保存新密码' }}</button></footer>
  </form>
 </dialog>
</template>
