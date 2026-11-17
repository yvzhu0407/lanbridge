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

// ApiError 带上 HTTP 状态码：老服务端缺路由时返回的是纯文本 404，
// JSON 解析失败后就没有状态码可判了，所以不能只靠 message 判断。
export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

export async function api<T = any>(path: string, body?: any, timeout = 40000): Promise<T> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeout)
  const opt: RequestInit = body !== undefined
    ? {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }
    : { method: 'GET' }

  try {
    const res = await fetch(path, { ...opt, signal: controller.signal })
    const json = await res.json().catch(() => null)
    if (!json) throw new ApiError(`请求失败（HTTP ${res.status}）`, res.status)
    if (!res.ok || !json.ok) {
      throw new ApiError(json.error || `请求失败（HTTP ${res.status}）`, res.status)
    }
    return json.data as T
  } catch (e: any) {
    if (controller.signal.aborted) throw new ApiError('请求超时，请检查连接后重试', 408)
    if (e instanceof ApiError) throw e
    throw new ApiError('无法连接本地服务，请确认桥联正在运行', 0)
  } finally {
    clearTimeout(timer)
  }
}

export const fetchConfig = () => api<ClientConfig>('/api/config')
export const fetchMe = () => api<MeInfo>('/api/me')
export const login = (name: string, password: string) => api('/api/login', { name, password })
export const logout = () => api('/api/logout', {})
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
// 老服务端缺少心跳路由时明确提示升级，不能用重新加入代替保活：旧实现会先离房。
let heartbeatUnavailable = false
export const sendHeartbeat = async () => {
  if (heartbeatUnavailable) throw new ApiError('房间服务器不支持保活，请更新服务端', 404)
  try {
    return await api('/api/room/heartbeat', {})
  } catch (e: any) {
    if (e?.status === 404) {
      heartbeatUnavailable = true
      throw new ApiError('房间服务器不支持保活，请更新服务端', 404)
    }
    throw e
  }
}

export const fetchJoinStatus = () => api<JoinStatus>('/api/join/status')
export const fetchMembers = (room_id: number) => api<Member[]>(`/api/room/members?room_id=${room_id}`)

export const fetchZtStatus = () => api<ZtStatus>('/api/zt/status')
export const fetchZtPeers = () => api<any[]>('/api/zt/peers')
export const fetchZtHealth = () => api<HealthReport>('/api/zt/health')
export const joinZtNetwork = (network_id: string, leave?: string) =>
  api('/api/zt/join', { network_id, ...(leave ? { leave } : {}) }, 60000)

export const pingPeer = (ip: string, node_id: string, net_id: string) =>
  api<ProbeResult>(
    `/api/ping?ip=${encodeURIComponent(ip || '')}&node_id=${encodeURIComponent(node_id || '')}&net=${encodeURIComponent(net_id || '')}`,
  )

export const fetchLogs = (view: '' | 'user' | 'dev' = '') =>
  api<{ logs: string; path: string; error?: string }>(`/api/logs${view ? `?view=${view}` : ''}`)

// 关窗退出：原生窗口先拦下关闭，界面确认后走 quit。
// ack 用来告诉后端"确认框已经弹出来了"，避免 3 秒兜底把窗口直接关掉。
export const ackQuitPrompt = () => api('/api/app/quit/ack', {})
export const cancelQuitPrompt = () => api('/api/app/quit/cancel', {})
export const quitApp = () => api('/api/app/quit', {})

// 邀请链接：GET 取走一条待处理邀请(没有时返回空对象)，
// POST 提交用户手动粘贴的链接或房间号。
export const fetchPendingInvite = () => api<{ room_id?: number }>('/api/invite')
export const submitInvite = (url: string) => api<{ room_id: number }>('/api/invite', { url })

// 邀请链接格式与解析规则和后端 invite.go 保持一致：
// 复制的是链接，手抄的房间号也能用。
export function inviteLink(roomId: number): string {
  return `lanbridge://join/${roomId}`
}

export function parseInvite(text: string): number | null {
  const s = (text || '').trim()
  if (!s) return null
  if (s.toLowerCase().includes('lanbridge://')) {
    const m = s.match(/lanbridge:\/\/join\/(\d+)/i)
    return m ? Number(m[1]) : null
  }
  const m = s.match(/#?\s*(\d{1,9})/)
  const id = m ? Number(m[1]) : 0
  return id > 0 ? id : null
}
