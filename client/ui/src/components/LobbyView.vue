<script setup lang="ts">
import { ref, computed } from 'vue'
import { state, showToast } from '../store'
import { fetchRooms, parseInvite } from '../api'
import { handleInvite } from '../lib/join'

const search = ref('')
const refreshing = ref(false)
// 邀请码输入框：链接被聊天软件吞掉时，用户可以把房间号直接粘进来
const inviteInput = ref('')

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
    state.roomsError = ''
    if (manual) showToast(`大厅已刷新 (共 ${state.rooms.length} 间)`)
  } catch (e: any) {
    state.roomsError = e.message
    if (manual) showToast(`刷新失败: ${e.message}`, true)
  } finally {
    setTimeout(() => {
      refreshing.value = false
    }, 300)
  }
}

function openCreate() {
  if (state.roomBusy) return
  state.createDrawerOpen = true
  if (!state.me) state.authModalOpen = true
}

// 手动提交邀请：链接或房间号都收，解析和后端共用同一套规则。
async function submitInviteCode() {
  const id = parseInvite(inviteInput.value)
  if (!id) {
    showToast('没认出邀请链接或房间号', true)
    return
  }
  inviteInput.value = ''
  await handleInvite(id)
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

      <div class="search-box invite-box">
        <input
          v-model="inviteInput"
          type="text"
          class="input search-input"
          placeholder="粘贴邀请链接或房间号…"
          @keydown.enter="submitInviteCode"
        >
        <button class="btn btn-sm" :disabled="!inviteInput.trim() || state.roomBusy" @click="submitInviteCode">
          加入
        </button>
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

        <button class="btn btn-primary" :disabled="state.roomBusy" @click="openCreate">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <line x1="12" y1="5" x2="12" y2="19"></line>
            <line x1="5" y1="12" x2="19" y2="12"></line>
          </svg>
          <span>创建房间</span>
        </button>
      </div>
    </div>

    <!-- 房间列表表格 (内部滚动，整页不滚) -->
    <p v-if="state.roomsError" class="lobby-notice" role="alert">房间列表获取失败：{{ state.roomsError }}。可点击刷新重试。</p>
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
                :disabled="state.roomBusy" @click="handleInvite(r.id)"
              >
                {{ state.currentRoom ? '切换' : '加入' }}
              </button>
            </td>
          </tr>

          <tr v-if="filteredRooms.length === 0">
            <td colspan="5" class="empty-cell">
              <div class="empty-state">
                <template v-if="state.roomsError">
                  <p>暂时无法获取房间</p>
                  <span>请检查连接后刷新，当前列表不代表没有房间</span>
                </template>
                <template v-else-if="!state.me">
                  <p>登录后查看和加入房间</p>
                  <button class="btn btn-primary" @click="state.authModalOpen = true">登录 / 注册</button>
                </template>
                <template v-else-if="search.trim()">
                  <p>没有匹配的房间</p>
                  <span>试试其他房间名或 ID</span>
                </template>
                <template v-else>
                  <p>暂无活动房间</p>
                  <span>创建房间，或粘贴朋友的邀请链接加入</span>
                </template>
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
.lobby-notice {
  padding: 8px 10px;
  color: var(--err);
  background: var(--err-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.lobby-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 10px 12px;
  gap: 8px;
  background: var(--bg);
}

.active-room-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 5px 10px;
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
  gap: 7px;
  font-size: 12px;
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
  width: 240px;
}

/* 邀请输入框：输入 + 加入按钮并排，宽度略宽以容纳完整链接 */
.invite-box {
  display: flex;
  align-items: center;
  gap: 6px;
  width: auto;
  min-width: 260px;
}

.invite-box .search-input {
  flex: 1;
  min-width: 0;
  /* 这一侧没有搜索图标，留出 30px 会让文字看起来缩进莫名其妙 */
  padding-left: 9px;
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
  font-size: 12px;
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
  padding: 5px 10px;
  font-weight: 600;
  color: var(--fg-subtle);
  font-size: 10.5px;
  text-transform: uppercase;
}

.rooms-table td {
  padding: 6px 10px;
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
  padding: 36px 16px;
  color: var(--fg-faint);
}

.empty-state p {
  font-size: 13px;
  font-weight: 500;
  color: var(--fg-muted);
  margin-bottom: 2px;
}

.empty-state span {
  font-size: 11.5px;
  color: var(--fg-faint);
}

.lobby-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  font-size: 10.5px;
  color: var(--fg-faint);
}
</style>
