<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import Inbox from './Inbox.vue'
import { request, errorMessage } from './http'
import './style.css'
const password = ref('')
const authenticated = ref(false)
const ready = ref(false)
const error = ref('')
const notice = ref('')
const busy = ref(false)
function expired() { authenticated.value = false }
function passwordChanged() { authenticated.value = false; notice.value = '登录密码已修改，请使用新密码重新登录。'; error.value = '' }
onMounted(async () => { window.addEventListener('session-expired', expired); try { authenticated.value = (await fetch('/api/auth/me')).ok } catch { error.value = '无法连接服务' } finally { ready.value = true } })
onBeforeUnmount(() => window.removeEventListener('session-expired', expired))
async function login() {
 busy.value = true; error.value = ''
 try { await request('/api/auth/login', { method: 'POST', body: JSON.stringify({ password: password.value }) }); authenticated.value = true; notice.value = ''; password.value = '' } catch (err) { error.value = errorMessage(err) } finally { busy.value = false }
}
async function logout() {
 try { await request<void>('/api/auth/logout', { method: 'POST' }); authenticated.value = false } catch (err) { error.value = errorMessage(err) }
}
</script>
<template>
 <p v-if="!ready" class="app-loading" role="status">正在打开 MeldMail…</p>
 <Inbox v-else-if="authenticated" @logout="logout" @password-changed="passwordChanged" />
 <main v-else class="login-page"><div class="login-brand"><img class="brand-icon" src="/meldmail-mark.svg" alt="">MeldMail</div><p class="eyebrow">YOUR MAIL. YOUR SPACE.</p><h1>所有来信，<br>从容相见。</h1><p class="login-subtitle">一个轻量、私有的多邮箱工作台。</p><section class="login-card"><h2>管理员登录</h2><p v-if="notice" class="notice" role="status">{{ notice }}</p><form @submit.prevent="login"><label for="password">管理员密码</label><input id="password" v-model="password" type="password" autocomplete="current-password" required maxlength="72"><button class="primary" :disabled="busy">{{ busy ? '登录中…' : '登录工作台 →' }}</button></form><p v-if="error" class="error" role="alert">{{ error }}</p></section></main>
</template>
