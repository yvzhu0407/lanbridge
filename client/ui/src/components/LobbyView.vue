<script setup lang="ts">
import { ref, computed } from 'vue'
import { state, showToast } from '../store'
import { fetchRooms, joinRoom, joinZtNetwork } from '../api'
import type { Room } from '../types'

const search = ref('')
const refreshing = ref(false)

const filteredRooms = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return state.rooms
  return state.rooms.filter(r =>
    r.name.toLowerCase().includes(q) || String(r.id).includes(q)
  )
})

async function reloadRooms(manual = false) {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const list = await fetchRooms()
    state.rooms = list || []
    if (manual) showToast(`大厅已刷新 (共 ${state.rooms.length} 间)`)
  } catch (e: any) {
    if (manual) showToast(`刷新失败: ${e.message}`, true)
  } finally {
    setTimeout(() => {
      refreshing.value = false
    }, 300)
  }
}

function handleJoinClick(room: Room) {
  if (state.currentRoom && state.currentRoom.room_id === room.id) {
    state.currentView = 'room'
    return
  }

  if (room.password_required) {
    state.pendingJoinRoomId = room.id
    state.passModalOpen = true
  } else {
    executeJoin(room.id, '')
  }
}

async function executeJoin(roomId: number, pass: string) {
  if (!state.nodeId) {
    showToast('ZeroTier 本地节点尚未就绪', true)
    return
  }
  showToast('正在加入房间并建立虚拟网络…')
  try {
    const res = await joinRoom(roomId, pass, state.nodeId)
    await joinZtNetwork(res.network_id)
    state.currentView = 'room'
    await reloadRooms()
    showToast('已加入房间，正在等待网络连接')
  } catch (e: any) {
    showToast(e.message, true)
  }
}
</script>

<template>
  <div class="lobby-view">
    <!-- 当前房间快捷提示条 -->
    <div v-if="state.currentRoom" class="active-room-banner" @click="state.currentView = 'room'">
      <div class="banner-content">
        <span class="dot dot-ok"></span>
        <span>你正在房间 <strong>{{ state.currentRoom.name || `#${state.currentRoom.room_id}` }}</strong> 中</span>
        <span class="banner-ip mono">({{ state.assignedIp || '正在获取 IP…' }})</span>
      </div>
      <button class="btn btn-sm btn-primary">返回房间 →</button>
    </div>

    <!-- 顶栏操作区 -->
    <div class="lobby-toolbar">
      <div class="search-box">
        <svg class="search-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="11" cy="11" r="8"></circle>
          <line x1="21" y1="21" x2="16.65" y2="16.65"></line>
        </svg>
        <input
          v-model="search"
          type="text"
          class="input search-input"
          placeholder="搜索房间名或 ID…"
        >
      </div>

      <div class="toolbar-actions">
        <button
          class="btn btn-ghost"
          :class="{ spinning: refreshing }"
          :disabled="refreshing"
          @click="reloadRooms(true)"
          title="刷新大厅房间列表"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="23 4 23 10 17 10"></polyline>
            <polyline points="1 20 1 14 7 14"></polyline>
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path>
          </svg>
          <span>刷新</span>
        </button>

        <button class="btn btn-primary" @click="state.createDrawerOpen = true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <line x1="12" y1="5" x2="12" y2="19"></line>
            <line x1="5" y1="12" x2="19" y2="12"></line>
          </svg>
          <span>创建房间</span>
        </button>
      </div>
    </div>

    <!-- 房间列表表格 (内部滚动，整页不滚) -->
    <div class="rooms-table-wrap">
      <table class="rooms-table">
        <thead>
          <tr>
            <th style="width: 70px;">ID</th>
            <th>房间名称</th>
            <th style="width: 100px; text-align: center;">玩家数</th>
            <th style="width: 100px; text-align: center;">权限</th>
            <th style="width: 120px; text-align: right;">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="r in filteredRooms"
            :key="r.id"
            :class="{ 'is-current': state.currentRoom && state.currentRoom.room_id === r.id }"
          >
            <td class="mono room-id">#{{ r.id }}</td>
            <td>
              <div class="room-name-cell">
                <span class="room-title">{{ r.name }}</span>
                <span
                  v-if="state.currentRoom && state.currentRoom.room_id === r.id"
                  class="badge badge-ok"
                >
                  当前所在
                </span>
              </div>
            </td>
            <td style="text-align: center;" class="mono font-bold">
              {{ r.user_count }} 人
            </td>
            <td style="text-align: center;">
              <span v-if="r.password_required" class="badge badge-warn">
                🔒 密码
              </span>
              <span v-else class="badge badge-subtle">
                公开
              </span>
            </td>
            <td style="text-align: right;">
              <button
                v-if="state.currentRoom && state.currentRoom.room_id === r.id"
                class="btn btn-sm"
                @click="state.currentView = 'room'"
              >
                进入房间 →
              </button>
              <button
                v-else
                class="btn btn-sm"
                :class="r.password_required ? '' : 'btn-primary'"
                @click="handleJoinClick(r)"
              >
                {{ state.currentRoom ? '切换' : '加入' }}
              </button>
            </td>
          </tr>

          <tr v-if="filteredRooms.length === 0">
            <td colspan="5" class="empty-cell">
              <div class="empty-state">
                <p>暂无活动房间</p>
                <span>点击右上角「＋ 创建房间」即可一键开局</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="lobby-footer">
      <span class="footer-count">共 {{ state.rooms.length }} 间房间</span>
    </div>
  </div>
</template>

<style scoped>
.lobby-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 12px;
  background: var(--bg);
}

.active-room-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px;
  background: var(--accent-subtle);
  border: 1px solid var(--accent-border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: all 0.15s ease;
}

.active-room-banner:hover {
  background: var(--card-subtle);
}

.banner-content {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  color: var(--fg);
}

.banner-ip {
  color: var(--fg-subtle);
  font-size: 11.5px;
}

.lobby-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.search-box {
  position: relative;
  width: 280px;
}

.search-icon {
  position: absolute;
  left: 9px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--fg-faint);
  pointer-events: none;
}

.search-input {
  padding-left: 30px;
  font-size: 12px;
}

.toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rooms-table-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.rooms-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  text-align: left;
}

.rooms-table thead {
  position: sticky;
  top: 0;
  background: var(--card-subtle);
  border-bottom: 1px solid var(--border);
  z-index: 5;
}

.rooms-table th {
  padding: 8px 12px;
  font-weight: 600;
  color: var(--fg-subtle);
  font-size: 11px;
  text-transform: uppercase;
}

.rooms-table td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-subtle);
  vertical-align: middle;
}

.rooms-table tbody tr:hover {
  background: var(--card-hover);
}

.rooms-table tbody tr.is-current {
  background: var(--accent-subtle);
}

.room-id {
  color: var(--fg-faint);
}

.room-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.room-title {
  font-weight: 600;
  color: var(--fg);
}

.font-bold {
  font-weight: 600;
}

.empty-cell {
  text-align: center;
  padding: 60px 20px;
  color: var(--fg-faint);
}

.empty-state p {
  font-size: 14px;
  font-weight: 500;
  color: var(--fg-muted);
  margin-bottom: 4px;
}

.empty-state span {
  font-size: 12px;
  color: var(--fg-faint);
}

.lobby-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  font-size: 11.5px;
  color: var(--fg-faint);
}
</style>
