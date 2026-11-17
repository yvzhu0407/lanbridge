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
  roomBusy: boolean
  roomRevision: number
  roomPollDue: number
  roomError: string
  healthError: string
  roomsError: string
  membersError: string
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
  chatOpen: boolean
  chatUnread: number
  toasts: ToastItem[]
}>({
  roomBusy: false,
  roomRevision: 0,
  roomPollDue: 0,
  roomError: '',
  healthError: '',
  roomsError: '',
  membersError: '',
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
  chatOpen: false,
  chatUnread: 0,
  toasts: [],
})

// 房间变更时统一丢弃旧房间数据；revision 阻止在途请求把旧结果写回来。
export function resetRoom(room: JoinStatus | null = null) {
  state.roomRevision++
  state.currentRoom = room
  state.joined = false
  state.assignedIp = ''
  state.members = []
  state.memberLatency = {}
  state.membersError = ''
  state.roomError = ''
  state.chatOpen = false
  state.chatUnread = 0
  state.roomPollDue = 0
  state.currentView = room ? 'room' : 'lobby'
}

export function syncRoomNetwork() {
  const room = state.currentRoom
  const net = state.health?.networks.find(n => n.network_id === room?.network_id)
  state.joined = !!(room?.authorized && state.health?.node_ready && net?.ready)
  state.assignedIp = state.joined ? (net?.ips[0] || '').split('/')[0] : ''
}

export function showToast(text: string, isErr = false) {
  const id = ++toastSeq
  state.toasts.push({ id, text, isErr })
  setTimeout(() => {
    const idx = state.toasts.findIndex(t => t.id === id)
    if (idx !== -1) state.toasts.splice(idx, 1)
  }, 3200)
}

// 应用内确认框。
//
// 不能用原生 confirm():WKWebView 的 WKUIDelegate 只实现了文件选择，
// 没实现 runJavaScriptConfirmPanel，于是在原生窗口里 confirm() 恒返回 false,
// "离开房间 / 解散房间 / 退出登录"全部静默失效。WebView2 同理不保证。
export const confirmState = reactive<{
  open: boolean
  title: string
  detail: string
  confirmText: string
  danger: boolean
  resolve: ((ok: boolean) => void) | null
}>({
  open: false,
  title: '',
  detail: '',
  confirmText: '确定',
  danger: false,
  resolve: null,
})

export function askConfirm(opts: {
  title: string
  detail?: string
  confirmText?: string
  danger?: boolean
}): Promise<boolean> {
  // 同一时间只留一个确认框:上一笔未决的直接按取消处理,避免 Promise 永久挂起
  confirmState.resolve?.(false)
  return new Promise<boolean>(resolve => {
    confirmState.open = true
    confirmState.title = opts.title
    confirmState.detail = opts.detail || ''
    confirmState.confirmText = opts.confirmText || '确定'
    confirmState.danger = !!opts.danger
    confirmState.resolve = resolve
  })
}

export function settleConfirm(ok: boolean) {
  const resolve = confirmState.resolve
  confirmState.open = false
  confirmState.resolve = null
  resolve?.(ok)
}

export function isSelfMember(m?: Member | null): boolean {
  if (!m) return false
  if (!m.zt_node_id && !m.assigned_ip && !m.name) return false
  const myNode = (state.nodeId || '').toLowerCase()
  if (myNode && m.zt_node_id && m.zt_node_id.toLowerCase() === myNode) return true
  if (state.assignedIp && m.assigned_ip && m.assigned_ip === state.assignedIp) return true
  if (state.me) {
    if (state.me.id && m.user_id === state.me.id) return true
    if (state.me.name && m.name === state.me.name) return true
    const meNode = (state.me.zt_node_id || '').toLowerCase()
    if (meNode && m.zt_node_id && m.zt_node_id.toLowerCase() === meNode) return true
  }
  return false
}
