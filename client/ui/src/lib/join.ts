import { state, showToast, askConfirm, resetRoom } from '../store'
import { fetchRooms, joinRoom, joinZtNetwork } from '../api'
import type { Room } from '../types'

// 加入房间的唯一入口：大厅点「加入」、密码框提交、邀请链接都走这里。
//
// 早先 LobbyView 和 PassModal 各写了一份 joinRoom + joinZtNetwork，
// 邀请链接再写第三份就会出现"某条路径忘了 joinZtNetwork"这种问题。
export async function connectRoom(room: { room_id: number; network_id: string }, name = '') {
  resetRoom({ ...room, joined: true, authorized: false, name })
  try {
    await joinZtNetwork(room.network_id)
    showToast('已进入房间，正在确认网络连接')
  } catch (e: any) {
    state.roomError = `已进入房间，本机网络连接未完成：${e.message}`
    showToast(state.roomError, true)
  }
}

export async function confirmRoomSwitch(name: string): Promise<boolean> {
  if (!state.currentRoom) return true
  return askConfirm({
    title: `切换到「${name}」？`,
    detail: '成功加入后会离开当前房间，虚拟网络会重新建立。',
    confirmText: '切换',
  })
}

export async function joinRoomFlow(roomId: number, password = ''): Promise<boolean> {
  if (state.roomBusy) return false
  if (!state.me) {
    state.pendingJoinRoomId = roomId
    state.authModalOpen = true
    showToast('请先登录，登录后继续加入房间')
    return false
  }
  if (state.currentRoom?.room_id === roomId) {
    state.currentView = 'room'
    return true
  }
  if (!state.nodeId) {
    showToast('联机引擎还没就绪，请打开诊断查看', true)
    return false
  }
  state.roomBusy = true
  state.roomRevision++
  try {
    const name = state.rooms.find(r => r.id === roomId)?.name || `房间 #${roomId}`
    if (!await confirmRoomSwitch(name)) return false
    const res = await joinRoom(roomId, password, state.nodeId)
    await connectRoom(res, name)
    return true
  } catch (e: any) {
    showToast(`加入失败：${e.message}`, true)
    return false
  } finally {
    state.roomPollDue = 0
    state.roomBusy = false
  }
}

// 找房间信息：大厅列表可能还没刷新到这个房间，先拉一次。
// 返回 null 表示房间不存在或已关闭。
async function findRoom(roomId: number): Promise<Room | null> {
  let room = state.rooms.find(r => r.id === roomId) || null
  if (room) return room
  state.rooms = (await fetchRooms()) || []
  return state.rooms.find(r => r.id === roomId) || null
}

// 处理一条邀请（来自 lanbridge:// 链接或手动粘贴的房间号）。
//
// 房间号来自链接，不能假设它有效：一律用服务端房间列表核对，
// 是否要密码也以服务端返回的 password_required 为准。
export async function handleInvite(roomId: number): Promise<void> {
  if (state.roomBusy) return
  if (!state.me) {
    showToast('收到邀请，请先登录')
    state.pendingJoinRoomId = roomId
    state.authModalOpen = true
    return
  }

  if (state.currentRoom?.room_id === roomId) {
    showToast('你已经在邀请的这个房间里')
    state.currentView = 'room'
    return
  }

  let room: Room | null
  try {
    room = await findRoom(roomId)
  } catch (e: any) {
    showToast(`无法获取房间信息：${e.message}`, true)
    return
  }
  if (!room) {
    showToast(`房间 #${roomId} 不存在或已关闭`, true)
    return
  }

  if (room.password_required) {
    state.pendingJoinRoomId = roomId
    state.passModalOpen = true
    return
  }

  showToast('正在加入房间并建立虚拟网络…')
  await joinRoomFlow(roomId)
}
