<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string,string> = {
 bell:'M4 17h16l-2-3V9a6 6 0 0 0-12 0v5z M10 21h4',
 code:'M8 7l-5 5 5 5 M16 7l5 5-5 5 M14 4l-4 16', key:'M14 10a4 4 0 1 0-3 4l4 4h3v3h3v-4l-7-7', logout:'M9 4H4v16h5 M9 12h12 M16 7l5 5-5 5',
 restore:'M9 4l-5 5 5 5 M4 9h9a7 7 0 0 1 0 14', mail:'M3 5h18v14H3z M3 5l9 7 9-7', unread:'M5 3h14v18H5z M8 7h8 M8 11h8', all:'M4 4h16v16H4z M4 9h16 M9 4v16', archive:'M3 4h18v4H3z M5 8v12h14V8 M10 12h4', trash:'M3 6h18 M9 6V3h6v3 M6 6l1 15h10l1-15 M10 10v7 M14 10v7', star:'M12 3l2.8 5.8 6.4.9-4.6 4.5 1.1 6.3-5.7-3-5.7 3 1.1-6.3-4.6-4.5 6.4-.9z', compose:'M14 4l6 6 M3 21l4-1 14-14-5-5L2 15z', refresh:'M20 7v5h-5 M20 12a8 8 0 1 0-2.34 5.66', sun:'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M12 2v2 M12 20v2 M2 12h2 M20 12h2 M5 5l1.5 1.5 M17.5 17.5L19 19 M5 19l1.5-1.5 M17.5 6.5L19 5', moon:'M20 15a8 8 0 0 1-11-11 9 9 0 1 0 11 11', settings:'M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1z M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6', search:'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14 M15 15l6 6', back:'M20 12H4 M10 6l-6 6 6 6', reply:'M10 5l-7 7 7 7 M3 12h9c5 0 8 2 9 7', forward:'M14 5l7 7-7 7 M21 12h-9c-5 0-8 2-9 7'
}
</script>
<template><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] || paths.mail" /></svg></template>
