<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
type Theme = 'system' | 'light' | 'dark'
const choice = ref<Theme>('system')
let media: MediaQueryList | undefined
function apply() { document.documentElement.dataset.theme = choice.value === 'system' ? (media?.matches ? 'dark' : 'light') : choice.value }
function change() { apply(); try { localStorage.setItem('meldmail-theme', choice.value) } catch { /* Theme still applies when storage is unavailable. */ } }
onMounted(() => {
 media = window.matchMedia('(prefers-color-scheme: dark)')
 try { const saved = localStorage.getItem('meldmail-theme'); if (saved === 'light' || saved === 'dark') choice.value = saved } catch { /* Use system preference. */ }
 apply(); media.addEventListener('change', apply)
})
onBeforeUnmount(() => media?.removeEventListener('change', apply))
</script>
<template><select v-model="choice" class="theme-select" aria-label="外观主题" @change="change"><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></template>
