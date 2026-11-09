import { reactive } from 'vue'
import type {
  ClientConfig,
  HealthReport,
  JoinStatus,
  MeInfo,
  Member,
  ProbeResult,
  Room,
} from './types'

export interface ToastItem {
  id: number
  text: string
  isErr?: boolean
}

let toastSeq = 0

export const state = reactive<{
  currentView: 'lobby' | 'room'
  me: MeInfo | null
  config: ClientConfig | null
  nodeId: string
  assignedIp: string
  joined: boolean
  currentRoom: JoinStatus | null
  health: HealthReport | null
  rooms: Room[]
  members: Member[]
  memberLatency: Record<string, ProbeResult | null>
  diagnoseOpen: boolean
  createDrawerOpen: boolean
  authModalOpen: boolean
  passModalOpen: boolean
  pendingJoinRoomId: number | null
  toasts: ToastItem[]
}>({
  currentView: 'lobby',
  me: null,
  config: null,
  nodeId: '',
  assignedIp: '',
  joined: false,
  currentRoom: null,
  health: null,
  rooms: [],
  members: [],
  memberLatency: {},
  diagnoseOpen: false,
  createDrawerOpen: false,
  authModalOpen: false,
  passModalOpen: false,
  pendingJoinRoomId: null,
  toasts: [],
})

export function showToast(text: string, isErr = false) {
  const id = ++toastSeq
  state.toasts.push({ id, text, isErr })
  setTimeout(() => {
    const idx = state.toasts.findIndex(t => t.id === id)
    if (idx !== -1) state.toasts.splice(idx, 1)
  }, 3200)
}

export function isSelfMember(m?: Member | null): boolean {
  if (!m) return false
  if (!m.zt_node_id && !m.assigned_ip && !m.name) return false
  const myNode = (state.nodeId || '').toLowerCase()
  if (myNode && m.zt_node_id && m.zt_node_id.toLowerCase() === myNode) return true
  if (state.assignedIp && m.assigned_ip && m.assigned_ip === state.assignedIp) return true
  if (state.me) {
    if (state.me.name && m.name === state.me.name) return true
    const meNode = (state.me.zt_node_id || '').toLowerCase()
    if (meNode && m.zt_node_id && m.zt_node_id.toLowerCase() === meNode) return true
  }
  return false
}
