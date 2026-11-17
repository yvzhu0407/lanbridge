<script setup lang="ts">
import { ref, computed, onUnmounted, watch } from 'vue'
import { state, showToast, isSelfMember } from '../store'
import { fetchMembers, pingPeer } from '../api'
import MemberRow from './MemberRow.vue'

const refreshing = ref(false)
let pollTimer: any = null
let disposed = false

const othersCount = computed(() => {
  return state.members.filter(m => !isSelfMember(m)).length
})

async function reloadMembersAndPing(manual = false) {
  if (disposed || refreshing.value || !state.currentRoom) return
  refreshing.value = true
  const roomId = state.currentRoom.room_id
  const netId = state.currentRoom.network_id
  const revision = state.roomRevision

  try {
    const list = await fetchMembers(roomId)
    if (disposed || revision !== state.roomRevision || state.currentRoom?.room_id !== roomId) return
    state.membersError = ''
    state.members = list || []
    const keys = new Set(state.members.map(m => m.zt_node_id || m.assigned_ip))
    for (const key of Object.keys(state.memberLatency)) {
      if (!keys.has(key)) delete state.memberLatency[key]
    }

    const promises = state.members.map(async m => {
      if (isSelfMember(m)) return
      const key = m.zt_node_id || m.assigned_ip
      if (!key) return
      try {
        const res = await pingPeer(m.assigned_ip, m.zt_node_id, netId)
        if (!disposed && revision === state.roomRevision && state.currentRoom?.room_id === roomId) state.memberLatency[key] = res
      } catch {
        if (disposed || revision !== state.roomRevision || state.currentRoom?.room_id !== roomId) return
        state.memberLatency[key] = {
          state: 'no_tunnel',
          state_text: '探测失败',
          reachable: false,
          latency: -1,
        }
      }
    })
    await Promise.allSettled(promises)
    if (manual && revision === state.roomRevision) showToast(`成员测速已更新 (共 ${state.members.length} 人)`)
  } catch (e: any) {
    if (!disposed && revision === state.roomRevision) {
      state.membersError = `成员信息获取失败：${e.message}`
      state.members = []
      state.memberLatency = {}
      if (manual) showToast(state.membersError, true)
    }
  } finally {
    refreshing.value = false
    if (state.currentRoom && state.currentRoom.room_id !== roomId) reloadMembersAndPing()
  }
}

// 进房间即自动拉一次，之后每 15 秒刷新成员与延迟。
// 旧实现只绑在「测速」按钮上，进房后成员表一直是空的。
const REFRESH_INTERVAL = 15000

function startPolling() {
  stopPolling()
  reloadMembersAndPing()
  pollTimer = setInterval(() => reloadMembersAndPing(), REFRESH_INTERVAL)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(
  () => state.currentRoom?.room_id,
  id => {
    if (id) startPolling()
    else stopPolling()
  },
  { immediate: true },
)

onUnmounted(() => {
  disposed = true
  stopPolling()
})
</script>

<template>
  <div class="member-list-panel">
    <div class="member-list-head">
      <div class="head-left">
        <h4 class="head-title">房间成员</h4>
        <span class="member-count-badge">{{ state.members.length }} 人</span>
      </div>

      <div class="head-right">
        <span v-if="othersCount === 0" class="only-self-hint">
          让好友在大厅加入当前房间
        </span>
        <button
          class="btn btn-sm btn-ghost"
          :class="{ spinning: refreshing }"
          :disabled="refreshing"
          @click="reloadMembersAndPing(true)"
          title="重新测速全部成员"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="23 4 23 10 17 10"></polyline>
            <polyline points="1 20 1 14 7 14"></polyline>
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path>
          </svg>
          <span>测速</span>
        </button>
      </div>
    </div>

    <!-- 成员表格 (内部滚动) -->
    <p v-if="state.membersError" class="member-error" role="alert">{{ state.membersError }}，请点击测速重试。</p>
    <div class="member-table-wrap">
      <table class="member-table">
        <thead>
          <tr>
            <th>玩家</th>
            <th style="width: 130px;">联机地址</th>
            <th style="width: 120px;">延迟 / 状态</th>
            <th style="width: 48px; text-align: right;">操作</th>
          </tr>
        </thead>
        <tbody>
          <MemberRow
            v-for="m in state.members"
            :key="m.zt_node_id || m.assigned_ip || m.name"
            :member="m"
          />

          <tr v-if="state.members.length === 0">
            <td colspan="4" class="empty-cell">
              暂无在线成员
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.member-error { padding: 8px 12px; color: var(--err); }

.member-list-panel {
  flex: 1;
  min-height: 0;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.member-list-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 10px;
  border-bottom: 1px solid var(--border);
  background: var(--card-subtle);
  flex: none;
  min-height: 30px;
}

.head-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.head-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--fg);
}

.member-count-badge {
  font-size: 10.5px;
  color: var(--fg-subtle);
  background: var(--border-subtle);
  padding: 0 5px;
  border-radius: 8px;
  line-height: 15px;
}

.head-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.only-self-hint {
  font-size: 11px;
  color: var(--fg-faint);
}

.member-table-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.member-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

.member-table thead {
  position: sticky;
  top: 0;
  background: var(--card-subtle);
  border-bottom: 1px solid var(--border);
  z-index: 2;
}

.member-table th {
  padding: 4px 10px;
  font-weight: 600;
  color: var(--fg-subtle);
  font-size: 10.5px;
  text-transform: uppercase;
  text-align: left;
}

.member-table td {
  padding: 5px 10px;
  border-bottom: 1px solid var(--border-subtle);
  vertical-align: middle;
}

.empty-cell {
  text-align: center;
  padding: 28px 10px;
  color: var(--fg-faint);
  font-size: 11.5px;
}
</style>
