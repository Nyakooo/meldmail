<script setup lang="ts">
import { t } from './i18n'
import type { SystemStats } from './types'
defineProps<{ stats: SystemStats | null; unavailable: boolean }>()
function bytes(value: number) { if (value >= 1073741824) return `${(value / 1073741824).toFixed(2)} GiB`; if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MiB`; return `${(value / 1024).toFixed(1)} KiB` }
</script>
<template>
 <section class="system-strip" :aria-label="t('运行和邮件统计')">
  <div><small>{{ t("已同步邮件 · 含归档和废纸篓") }}</small><strong>{{ stats ? `${stats.mail.total}${t(' 封')}` : '—' }}</strong><span v-if="stats">{{ t("未读 ") }}{{ stats.mail.unread }}{{ t(" 封") }}</span></div>
  <div><small>{{ stats?.resources.memory.source === 'container' ? t('容器内存') : stats?.resources.memory.source === 'go_runtime' ? t('Go 运行时内存') : t('进程内存') }}</small><strong>{{ stats ? bytes(stats.resources.memory.used_bytes) : '—' }}</strong><span v-if="stats?.resources.memory.limit_bytes">{{ t("上限 ") }}{{ bytes(stats.resources.memory.limit_bytes) }}</span><span v-if="unavailable" role="status">{{ t("统计暂不可用，显示上次数据") }}</span></div>
  <div :title="t('SQLite 数据库、WAL 和共享内存文件；不包含 Docker 镜像')"><small>{{ t("本地数据占用") }}</small><strong>{{ stats?.resources.disk.available ? bytes(stats.resources.disk.data_bytes) : '—' }}</strong><span v-if="stats?.resources.disk.volume_known">{{ t("磁盘可用 ") }}{{ bytes(stats.resources.disk.volume_available_bytes) }} / {{ bytes(stats.resources.disk.volume_total_bytes) }}</span></div>
 </section>
</template>
