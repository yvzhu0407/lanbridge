<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { state, showToast } from './store'
import {
  fetchConfig,
  fetchMe,
  fetchRooms,
  fetchJoinStatus,
  fetchZtHealth,
  fetchZtStatus,
  sendHeartbeat,
  joinZtNetwork,
} from './api'

import AppHeader from './components/AppHeader.vue'
import LobbyView from './components/LobbyView.vue'
import RoomView from './components/RoomView.vue'
import CreateRoomDrawer from './components/CreateRoomDrawer.vue'
import DiagnoseDrawer from './components/DiagnoseDrawer.vue'
import PassModal from './components/PassModal.vue'
import AuthModal from './components/AuthModal.vue'

let ztTimer: any = null
let pollTimer: any = null
let autoRefreshTimer: any = null
let lastHeartbeat = 0
let rejoinTried = false

async function syncZT() {
  try {
    const d = await fetchZtStatus()
    state.nodeId = d.node_id || ''

    const nets = d.networks || []
    const net = nets[0]
    if (net && net.ips && net.ips.length) {
      state.assignedIp = net.ips[0].split('/')[0]
    }
  } catch {
    state.nodeId = ''
  }

  try {
    const health = await fetchZtHealth()
    state.health = health
  } catch {}
}

async function pollRoomStatus() {
  let nextDelay = 30000
  try {
    const d = await fetchJoinStatus()
    if (!d.joined) {
      if (state.joined) {
        showToast('房间已解散或断开')
      }
      state.joined = false
      state.currentRoom = null
      state.assignedIp = ''
      if (state.currentView === 'room') {
        state.currentView = 'lobby'
      }
      return
    }

    state.currentRoom = d
    nextDelay = d.authorized ? 30000 : 2000

    // 拉取用户详情获取房间名称和身份
    try {
      const me = await fetchMe()
      state.me = me
      if (me.room_name && state.currentRoom) {
        state.currentRoom.name = me.room_name
      }
    } catch {}

    // 本地网络就绪判定：status=OK、网卡存在、拿到分配 IP
    try {
      const health = await fetchZtHealth()
      state.health = health
      const net = (health.networks || []).find(n => n.network_id === d.network_id)
      if (d.authorized && net && net.ready) {
        state.assignedIp = (net.ips || [])[0] ? net.ips[0].split('/')[0] : ''
        state.joined = true
      } else if (d.authorized && !net && !rejoinTried) {
        // 服务端在房间但本地没有此网络：自动补 join 一次
        rejoinTried = true
        joinZtNetwork(d.network_id).catch(() => {})
        nextDelay = 2000
      }
    } catch {}

    // 业务心跳保活
    if (Date.now() - lastHeartbeat > 60000) {
      lastHeartbeat = Date.now()
      sendHeartbeat().catch(() => {})
    }
  } catch {
    // 异常时不加速轮询
  } finally {
    if (pollTimer) clearTimeout(pollTimer)
    if (state.currentRoom) {
      pollTimer = setTimeout(pollRoomStatus, nextDelay)
    }
  }
}

async function init() {
  await syncZT()

  try {
    const cfg = await fetchConfig()
    state.config = cfg
    if (cfg.logged_in) {
      state.me = await fetchMe()
    }
  } catch {}

  try {
    state.rooms = (await fetchRooms()) || []
  } catch {}

  await pollRoomStatus()

  if (state.joined) {
    state.currentView = 'room'
  } else {
    state.currentView = 'lobby'
  }

  // 定时任务
  ztTimer = setInterval(syncZT, 8000)
  autoRefreshTimer = setInterval(() => {
    if (state.currentView === 'lobby') {
      fetchRooms().then(rooms => { state.rooms = rooms || [] }).catch(() => {})
    }
  }, 5000)
}

onMounted(() => {
  init()
})

onUnmounted(() => {
  if (ztTimer) clearInterval(ztTimer)
  if (pollTimer) clearTimeout(pollTimer)
  if (autoRefreshTimer) clearInterval(autoRefreshTimer)
})
</script>

<template>
  <div class="app-root">
    <AppHeader />

    <main class="main-content">
      <LobbyView v-if="state.currentView === 'lobby'" />
      <RoomView v-else />
    </main>

    <!-- 弹窗 / 抽屉 -->
    <CreateRoomDrawer />
    <DiagnoseDrawer />
    <PassModal />
    <AuthModal />

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

.main-content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
