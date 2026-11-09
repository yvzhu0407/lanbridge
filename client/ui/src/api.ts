import type {
  ClientConfig,
  HealthReport,
  JoinStatus,
  MeInfo,
  Member,
  ProbeResult,
  Room,
  ZtStatus,
} from './types'

export async function api<T = any>(path: string, body?: any): Promise<T> {
  const opt: RequestInit = body !== undefined
    ? {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }
    : { method: 'GET' }

  const res = await fetch(path, opt)
  const json = await res.json().catch(() => ({ ok: false, error: '网络响应非有效 JSON' }))
  if (!json.ok) {
    throw new Error(json.error || `HTTP ${res.status}`)
  }
  return json.data as T
}

export const fetchConfig = () => api<ClientConfig>('/api/config')
export const fetchMe = () => api<MeInfo>('/api/me')
export const login = (name: string, password: string) => api('/api/login', { name, password })
export const register = (name: string, password: string) => api('/api/register', { name, password })

export const fetchRooms = () => api<Room[]>('/api/rooms')

// 协议层保留 game 字段（服务端 rooms.game NOT NULL），界面不再暴露游戏选择。
export const createRoom = (name: string, password: string, zt_node_id: string) =>
  api<{ room_id: number; network_id: string }>('/api/room/create', {
    game: 'civ6',
    name,
    password,
    zt_node_id,
  })

export const joinRoom = (room_id: number, password: string, zt_node_id: string) =>
  api<{ room_id: number; network_id: string }>('/api/room/join', {
    room_id,
    password,
    zt_node_id,
  })

export const leaveRoom = () => api('/api/room/leave', {})
export const dismissRoom = (room_id: number) => api('/api/room/dismiss', { room_id })
export const sendHeartbeat = () => api('/api/room/heartbeat', {})

export const fetchJoinStatus = () => api<JoinStatus>('/api/join/status')
export const fetchMembers = (room_id: number) => api<Member[]>(`/api/room/members?room_id=${room_id}`)

export const fetchZtStatus = () => api<ZtStatus>('/api/zt/status')
export const fetchZtPeers = () => api<any[]>('/api/zt/peers')
export const fetchZtHealth = () => api<HealthReport>('/api/zt/health')
export const joinZtNetwork = (network_id: string, leave?: string) =>
  api('/api/zt/join', { network_id, ...(leave ? { leave } : {}) })

export const pingPeer = (ip: string, node_id: string, net_id: string) =>
  api<ProbeResult>(
    `/api/ping?ip=${encodeURIComponent(ip || '')}&node_id=${encodeURIComponent(node_id || '')}&net=${encodeURIComponent(net_id || '')}`,
  )

export const fetchLogs = (view: '' | 'user' | 'dev' = '') =>
  api<{ logs: string; path: string; error?: string }>(`/api/logs${view ? `?view=${view}` : ''}`)
