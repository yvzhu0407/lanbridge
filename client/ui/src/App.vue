<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { state, showToast, askConfirm, resetRoom, syncRoomNetwork } from './store'
import {
  fetchConfig,
  fetchMe,
  fetchRooms,
  fetchJoinStatus,
  fetchZtHealth,
  sendHeartbeat,
  joinZtNetwork,
  ackQuitPrompt,
  cancelQuitPrompt,
  quitApp,
  fetchPendingInvite,
} from './api'
import { handleInvite } from './lib/join'

import AppHeader from './components/AppHeader.vue'
import LobbyView from './components/LobbyView.vue'
import RoomView from './components/RoomView.vue'
import CreateRoomDrawer from './components/CreateRoomDrawer.vue'
import DiagnoseDrawer from './components/DiagnoseDrawer.vue'
import PassModal from './components/PassModal.vue'
import AuthModal from './components/AuthModal.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'

let ztTimer: any = null
let pollTimer: any = null
let autoRefreshTimer: any = null
let inviteTimer: any = null
let lastHeartbeat = 0
let retryNetwork = ''
let lastRejoin = 0
let lastHeartbeatRoom = 0

// 邀请链接轮询。
//
// 链接可能来自三处：冷启动时的命令行、已经运行的实例收到的 Apple Event /
// 转发请求、用户手动粘贴。后端把它们统一存成"待处理邀请"，
// 这里取到就去加入。私聊/群聊里的链接不会凭空出现，所以频率可以放低。
let inviteBusy = false

async function pollInvite() {
  if (inviteBusy || state.roomBusy || state.authModalOpen || state.passModalOpen) return
  inviteBusy = true
  try {
    const d = await fetchPendingInvite()
    if (d && d.room_id) {
      await handleInvite(Number(d.room_id))
    }
  } catch {
    // 后端不可用时静默跳过，下一轮再试
  } finally {
    inviteBusy = false
  }
}

let healthBusy = false
async function syncZT() {
  if (healthBusy) return
  healthBusy = true
  try {
    state.health = await fetchZtHealth()
    state.healthError = ''
    state.nodeId = state.health.node_ready ? state.health.node_id : ''
  } catch (e: any) {
    state.health = null
    state.nodeId = ''
    state.healthError = e.message
  } finally {
    syncRoomNetwork()
    healthBusy = false
  }
}

async function pollRoomStatus() {
  const revision = state.roomRevision
  const user = state.me
  try {
    const d = await fetchJoinStatus()
    if (state.roomBusy || revision !== state.roomRevision || user !== state.me) return 2000
    state.roomError = ''
    if (!d.joined) {
      if (state.currentRoom) {
        state.currentRoom.authorized = false
        syncRoomNetwork()
        state.roomBusy = true
        try {
          await joinZtNetwork('', 'all')
          resetRoom()
          showToast('已离开房间或房间已关闭')
        } catch (e: any) {
          state.roomError = `本地网络清理失败，将自动重试：${e.message}`
        } finally {
          state.roomBusy = false
        }
      }
      retryNetwork = ''
      return 5000
    }

    const changed = state.currentRoom?.room_id !== d.room_id || state.currentRoom?.network_id !== d.network_id
    const name = state.currentRoom?.room_id === d.room_id ? state.currentRoom.name : ''
    if (changed) resetRoom({ ...d, name: state.rooms.find(r => r.id === d.room_id)?.name || '' })
    else state.currentRoom = { ...d, name: name || state.rooms.find(r => r.id === d.room_id)?.name || '' }
    syncRoomNetwork()

    if (retryNetwork !== d.network_id) {
      retryNetwork = d.network_id
      lastRejoin = 0
    }
    const net = state.health?.networks.find(n => n.network_id === d.network_id)
    if (state.health?.node_ready && !net && Date.now() - lastRejoin >= 15000) {
      lastRejoin = Date.now()
      state.roomBusy = true
      try {
        await joinZtNetwork(d.network_id)
      } catch (e: any) {
        state.roomError = `本机网络尚未接入：${e.message}`
      } finally {
        state.roomBusy = false
      }
    }

    if (lastHeartbeatRoom !== d.room_id || Date.now() - lastHeartbeat >= 60000) {
      lastHeartbeatRoom = d.room_id
      lastHeartbeat = Date.now()
      try {
        await sendHeartbeat()
      } catch (e: any) {
        if (state.currentRoom?.room_id === d.room_id) state.roomError = `房间保活失败：${e.message}`
      }
    }
    return state.joined ? 15000 : 2000
  } catch (e: any) {
    if (revision === state.roomRevision && user === state.me && !state.roomBusy) {
      state.roomError = `房间状态同步失败：${e.message}`
    }
    return 5000
  }
}

let pollBusy = false
async function pollTick() {
  if (pollBusy || state.roomBusy || !state.me || Date.now() < state.roomPollDue) return
  pollBusy = true
  const revision = state.roomRevision
  try {
    const delay = await pollRoomStatus()
    state.roomPollDue = revision === state.roomRevision ? Date.now() + delay : 0
  } finally {
    pollBusy = false
  }
}

let roomsBusy = false
async function refreshRooms() {
  if (roomsBusy || !state.me) return
  roomsBusy = true
  const user = state.me
  try {
    const rooms = await fetchRooms()
    if (user === state.me) {
      state.rooms = rooms || []
      state.roomsError = ''
    }
  } catch (e: any) {
    if (user === state.me) state.roomsError = e.message
  } finally {
    roomsBusy = false
  }
}

// 关窗确认。
//
// 原生窗口(WebKit/WebView2)关窗前会先问后端，后端在房间里时拦住并注入
// 'lanbridge:close-request' 事件。这里负责把它变成一句人话：
// 先 ack(停掉后端 3 秒兜底)，再弹应用内确认框。
// 不能用原生 confirm()——WKWebView 没实现 runJavaScriptConfirmPanel，恒返回 false。
let closingGuard = false

async function onCloseRequest() {
  if (closingGuard) return
  closingGuard = true
  try {
    // 先 ack：界面已经接管，后端别急着关窗口
    await ackQuitPrompt().catch(() => {})
    const ok = await askConfirm({
      title: '离开当前房间并退出？',
      detail: '退出后虚拟网络会释放，下次需要在大厅重新加入房间。',
      confirmText: '离开并退出',
      danger: true,
    })
    if (ok) {
      // 清理在后端做(离开房间、退出网络、恢复网卡跃点)，完成后自己关窗。
      // 这个过程要几秒，先给个反馈，否则窗口像卡住了。
      showToast('正在退出…')
      await quitApp().catch(() => {})
    } else {
      // 用户取消：恢复原生窗口，下次关窗重新询问
      await cancelQuitPrompt().catch(() => {})
      showToast('已取消退出，仍在房间中')
    }
  } finally {
    closingGuard = false
  }
}

async function init() {
  // 健康检测可能要等待驱动启动，不能阻塞登录与大厅反馈。
  const healthTask = syncZT()
  try {
    state.config = await fetchConfig()
    if (state.config.logged_in) state.me = await fetchMe()
  } catch (e: any) {
    state.roomsError = e.message
  }
  await refreshRooms()
  await pollTick()
  await healthTask
}

onMounted(() => {
  // 关窗确认：原生层拦下关闭后发这个事件。
  // 必须在这里注册(早于任何轮询)，页面加载完成即具备响应能力。
  window.addEventListener('lanbridge:close-request', onCloseRequest)
  init()
  ztTimer = setInterval(syncZT, 8000)
  pollTimer = setInterval(pollTick, 1000)
  inviteTimer = setInterval(pollInvite, 1500)
  autoRefreshTimer = setInterval(() => {
    if (state.currentView === 'lobby') refreshRooms()
  }, 5000)
})

onUnmounted(() => {
  window.removeEventListener('lanbridge:close-request', onCloseRequest)
  if (ztTimer) clearInterval(ztTimer)
  if (pollTimer) clearInterval(pollTimer)
  if (autoRefreshTimer) clearInterval(autoRefreshTimer)
  if (inviteTimer) clearInterval(inviteTimer)
})
</script>

<template>
  <div class="app-root">
    <AppHeader />

    <div v-if="state.roomError && state.currentRoom" class="room-status-notice" role="status">
      {{ state.roomError }}
    </div>
    <main class="main-content">
      <LobbyView v-if="state.currentView === 'lobby'" />
      <RoomView v-else />
    </main>

    <!-- 弹窗 / 抽屉 -->
    <CreateRoomDrawer />
    <DiagnoseDrawer />
    <PassModal />
    <AuthModal />
    <ConfirmDialog />

    <!-- 全局 Toast -->
    <div class="toast-container">
      <div
        v-for="t in state.toasts"
        :key="t.id"
        class="toast"
        :class="{ 'toast-err': t.isErr }"
      >
        <span>{{ t.text }}</span>
      </div>
    </div>
  </div>
</template>

<style>
@import './styles/tokens.css';
@import './styles/base.css';

.app-root {
  height: 100vh;
  width: 100vw;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg);
}

.room-status-notice {
  padding: 8px 12px;
  color: var(--fg);
  background: var(--warn-subtle);
  border-bottom: 1px solid var(--border);
  font-size: 12px;
}

.main-content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
