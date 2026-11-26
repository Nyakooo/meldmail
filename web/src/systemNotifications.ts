import { ref } from 'vue'

const preference = 'meldmail-system-notifications'
const supported = () => window.isSecureContext && 'Notification' in window

export function useSystemNotifications() {
 const notificationsEnabled = ref(false)
 let requesting = false
 try {
  notificationsEnabled.value = supported() && Notification.permission === 'granted' && localStorage.getItem(preference) === 'on'
 } catch { /* This browser may block local storage. */ }

 function setEnabled(enabled: boolean) {
  notificationsEnabled.value = enabled
  try { localStorage.setItem(preference, enabled ? 'on' : 'off') } catch { /* Keep the setting for this session. */ }
 }

 async function toggleNotifications() {
  if (requesting) return
  if (notificationsEnabled.value) {
   setEnabled(false)
   return
  }
  if (!supported()) return '系统通知需要 HTTPS 和受支持的桌面浏览器。'
  requesting = true
  try {
   const permission = await Notification.requestPermission()
   if (permission === 'denied') return '请在浏览器站点设置中允许通知。'
   if (permission !== 'granted') return '未授予通知权限。'
   setEnabled(true)
  } catch {
   return '无法启用系统通知。'
  } finally {
   requesting = false
  }
 }

 function notify(title: string, tag: string) {
  if (!notificationsEnabled.value || Notification.permission !== 'granted' || document.visibilityState !== 'hidden') return
  try {
   const notification = new Notification(title, { tag })
   notification.onclick = () => {
    window.focus()
    notification.close()
   }
  } catch { /* In-page updates continue if the browser cannot display system notifications. */ }
 }

 return { notificationsEnabled, toggleNotifications, notify }
}
