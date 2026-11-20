<script setup lang="ts">
import { ref, onMounted } from 'vue'
const password = ref('')
const authenticated = ref(false)
const error = ref('')
const busy = ref(false)
onMounted(async () => { try { authenticated.value = (await fetch('/api/auth/me')).ok } catch { error.value = '无法连接服务' } })
async function login() {
 busy.value = true; error.value = ''
 try {
  const response = await fetch('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: password.value }) })
  if (!response.ok) { error.value = response.status === 429 ? '尝试次数过多，请稍后再试' : '登录失败，请检查管理员密码'; return }
  authenticated.value = true; password.value = ''
 } catch { error.value = '无法连接服务' } finally { busy.value = false }
}
async function logout() {
 try { const response = await fetch('/api/auth/logout', { method: 'POST' }); if (response.ok) authenticated.value = false; else error.value = '退出失败' } catch { error.value = '无法连接服务' }
}
</script>
<template>
 <main>
  <h1>MeldMail</h1><p class="subtitle">轻量、私有的多邮箱工作台</p>
  <section v-if="!authenticated">
   <h2>管理员登录</h2>
   <form @submit.prevent="login"><label for="password">管理员密码</label><input id="password" v-model="password" type="password" autocomplete="current-password" required maxlength="72"><button :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button></form>
  </section>
  <section v-else><h2>欢迎回来</h2><p>工程底座已就绪。邮箱接入将在下一阶段开放。</p><button @click="logout">退出登录</button></section>
  <p v-if="error" role="alert">{{ error }}</p>
 </main>
</template>
<style>
:root{font-family:system-ui,sans-serif;color:#173a36;background:#f2f6f4}body{margin:0}main{max-width:440px;margin:12vh auto;padding:24px}h1{font-size:36px;letter-spacing:-1px}.subtitle{color:#58736d}section{background:white;padding:28px;border:1px solid #d9e4df;border-radius:16px;margin-top:36px}h2{font-size:20px}label,input,button{display:block;box-sizing:border-box;width:100%}input{margin:10px 0 20px;padding:12px;border:1px solid #91aaa2;border-radius:6px;font-size:16px}button{padding:12px;background:#156450;color:white;border:0;border-radius:6px;font-size:16px;cursor:pointer}button:disabled{opacity:.6}[role=alert]{color:#ad2929}
</style>
