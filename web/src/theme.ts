import { ref, watch } from 'vue'
export type Theme = 'system' | 'light' | 'dark'
export const theme = ref<Theme>('system')
const media = window.matchMedia('(prefers-color-scheme: dark)')
export const dark = ref(false)
try { const saved = localStorage.getItem('meldmail-theme'); if (saved === 'light' || saved === 'dark') theme.value = saved } catch { /* Fall back to the system preference. */ }
function apply() { dark.value = theme.value === 'dark' || (theme.value === 'system' && media.matches); document.documentElement.dataset.theme = dark.value ? 'dark' : 'light' }
apply()
media.addEventListener('change', apply)
watch(theme, () => { apply(); try { localStorage.setItem('meldmail-theme', theme.value) } catch { /* The selected theme still applies. */ } }, { flush: 'sync' })
