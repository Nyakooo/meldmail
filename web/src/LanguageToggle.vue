<script setup lang="ts">
import { locale, toggleLanguage } from './i18n'
</script>
<template><button class="language-toggle" :aria-label="locale === 'zh' ? 'Switch to English' : '切换为中文'" @click="toggleLanguage">{{ locale === 'zh' ? 'EN' : '中文' }}</button></template>
