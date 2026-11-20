export async function request<T>(path: string, init?: RequestInit): Promise<T> {
 const response = await fetch(path, { ...init, headers: { 'Content-Type': 'application/json', ...init?.headers } })
 if (!response.ok) {
  if (response.status === 401) window.dispatchEvent(new Event('session-expired'))
  throw new Error((await response.text()).trim() || `请求失败 (${response.status})`)
 }
 if (response.status === 204) return undefined as T
 return response.json() as Promise<T>
}
export function errorMessage(error: unknown): string { return error instanceof Error ? error.message : '请求失败，请稍后重试' }
