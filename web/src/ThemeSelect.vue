<script setup lang="ts">
import Icon from './Icon.vue'
import { theme, dark } from './theme'
import { locale } from './i18n'
</script>
<template><button class="theme-toggle icon-button" :aria-label="locale === 'zh' ? (dark ? '切换为浅色' : '切换为深色') : (dark ? 'Switch to light' : 'Switch to dark')" :title="locale === 'zh' ? '切换主题' : 'Switch theme'" :aria-pressed="dark" @click="theme = dark ? 'light' : 'dark'"><Icon :name="dark ? 'moon' : 'sun'" /></button></template>
