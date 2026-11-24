<script setup lang="ts">
import type { SystemStats } from './types'
defineProps<{ stats: SystemStats | null; unavailable: boolean }>()
function bytes(value: number) { if (value >= 1073741824) return `${(value / 1073741824).toFixed(2)} GiB`; if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MiB`; return `${(value / 1024).toFixed(1)} KiB` }
</script>
<template>
 <section class="system-strip" aria-label="运行和邮件统计">
  <div><small>已同步邮件 · 含归档和废纸篓</small><strong>{{ stats ? `${stats.mail.total} 封` : '—' }}</strong><span v-if="stats">未读 {{ stats.mail.unread }} 封</span></div>
  <div><small>{{ stats?.resources.memory.source === 'container' ? '容器内存' : stats?.resources.memory.source === 'go_runtime' ? 'Go 运行时内存' : '进程内存' }}</small><strong>{{ stats ? bytes(stats.resources.memory.used_bytes) : '—' }}</strong><span v-if="stats?.resources.memory.limit_bytes">上限 {{ bytes(stats.resources.memory.limit_bytes) }}</span><span v-if="unavailable" role="status">统计暂不可用，显示上次数据</span></div>
  <div :title="'SQLite 数据库、WAL 和共享内存文件；不包含 Docker 镜像'"><small>本地数据占用</small><strong>{{ stats?.resources.disk.available ? bytes(stats.resources.disk.data_bytes) : '—' }}</strong><span v-if="stats?.resources.disk.volume_known">磁盘可用 {{ bytes(stats.resources.disk.volume_available_bytes) }} / {{ bytes(stats.resources.disk.volume_total_bytes) }}</span></div>
 </section>
</template>
