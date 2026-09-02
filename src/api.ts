import type { ConfigData, Doctor, MemoryRecord, Project, RegistryAction, SearchHit, Source, Status, SyncRun } from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json', ...init?.headers }, ...init })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || `请求失败 (${response.status})`)
  return body as T
}

export const api = {
  status: () => request<Status>('/status'),
  projects: () => request<Project[]>('/projects'),
  sources: () => request<Source[]>('/sources'),
  runs: (limit = 50) => request<SyncRun[]>(`/sync/runs?limit=${limit}`),
  sync: (sourceId?: string) => request<SyncRun | SyncRun[]>('/sync', { method: 'POST', body: JSON.stringify({ sourceId }) }),
  search: (params: URLSearchParams) => request<SearchHit[]>(`/search?${params}`),
  memory: (id: string) => request<MemoryRecord>(`/memories/${encodeURIComponent(id)}`),
  doctor: () => request<Doctor>('/index/doctor'),
  rebuild: () => request<{rebuilt: boolean; documents: number}>('/index/rebuild', { method: 'POST' }),
  config: () => request<ConfigData>('/config'),
  validateConfig: (raw: string) => request<{valid: boolean; actions: RegistryAction[]}>('/config/validate', { method: 'POST', body: JSON.stringify({ raw }) }),
  saveConfig: (raw: string, expectedHash: string) => request<{saved: boolean; hash: string; actions: RegistryAction[]}>('/config', { method: 'PUT', body: JSON.stringify({ raw, expectedHash }) }),
}
