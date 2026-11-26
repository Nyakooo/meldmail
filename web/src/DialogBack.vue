<script setup lang="ts">
import Icon from './Icon.vue'
import { t } from './i18n'

withDefaults(defineProps<{ label?: string }>(), { label: '返回设置' })
</script>

<template>
 <button type="button" class="icon-button dialog-back" :aria-label="t(label)" :title="t(label)">
  <Icon name="back" />
 </button>
</template>
