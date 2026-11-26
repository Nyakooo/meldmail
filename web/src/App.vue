<script setup lang="ts">
import { t, locale } from './i18n'
import LanguageToggle from './LanguageToggle.vue'
import { ref, onMounted, onBeforeUnmount } from 'vue'
import Inbox from './Inbox.vue'
import ThemeSelect from './ThemeSelect.vue'
import { request, errorMessage } from './http'
import './style.css'
const password = ref('')
const authenticated = ref(false)
const ready = ref(false)
const error = ref('')
const notice = ref('')
const busy = ref(false)
function expired() { authenticated.value = false }
function passwordChanged() { authenticated.value = false; notice.value = t('登录密码已修改，请使用新密码重新登录。'); error.value = '' }
onMounted(async () => { window.addEventListener('session-expired', expired); try { authenticated.value = (await fetch('/api/auth/me')).ok } catch { error.value = t('无法连接服务') } finally { ready.value = true } })
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
 <p v-if="!ready" class="app-loading" role="status">{{ t("正在打开 MeldMail…") }}</p>
 <Inbox v-else-if="authenticated" @logout="logout" @password-changed="passwordChanged" />
 <main v-else class="login-page"><div class="login-theme"><LanguageToggle /><ThemeSelect /></div><div class="login-brand"><img class="brand-icon" src="/meldmail-mark.svg" alt="">MeldMail</div><section class="login-card"><h2>{{ t("登录 MeldMail") }}</h2><p v-if="notice" class="notice" role="status">{{ t(notice) }}</p><form @submit.prevent="login"><label for="password">{{ t("登录密码") }}</label><input id="password" v-model="password" type="password" autocomplete="current-password" required maxlength="72"><button class="primary" :disabled="busy">{{ busy ? t('登录中…') : t('登录工作台 →') }}</button></form><p v-if="error" class="error" role="alert">{{ t(error) }}</p></section></main>
</template>
