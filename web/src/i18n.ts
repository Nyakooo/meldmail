import { ref } from 'vue'
import english from './en.json'
export const locale = ref<'zh' | 'en'>('zh')
try { if (localStorage.getItem('meldmail-language') === 'en') locale.value = 'en' } catch { /* Use Chinese by default. */ }
const messages: Record<string,string> = {'删除邮箱确认':'删除 {email}？其本地邮件索引也会删除，源邮箱中的邮件会保留。','撤销令牌确认':'撤销 {name}？其尚未批准的发信请求也会被拒绝。','审批截止说明':'审批截止：{time}。正文只在内存中等待审批。'}
const chinese = Object.fromEntries(Object.entries(english).map(([key,value]) => [value,key]))
export function t(value: string): string { if (locale.value === 'zh') return messages[value] ?? chinese[value] ?? value; return locale.value === 'en' ? (english as Record<string, string>)[value] ?? value : value }
export function toggleLanguage() { locale.value = locale.value === 'zh' ? 'en' : 'zh'; document.documentElement.lang = locale.value === 'zh' ? 'zh-CN' : 'en'; try { localStorage.setItem('meldmail-language',locale.value) } catch { /* The current choice still applies. */ } }
document.documentElement.lang = locale.value === 'zh' ? 'zh-CN' : 'en'
