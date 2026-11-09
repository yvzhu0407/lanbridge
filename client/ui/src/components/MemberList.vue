<script setup lang="ts">
import { ref, computed } from 'vue'
import { state, showToast, isSelfMember } from '../store'
import { fetchMembers, fetchZtPeers, pingPeer } from '../api'
import type { Member } from '../types'
import MemberRow from './MemberRow.vue'

const refreshing = ref(false)

const othersCount = computed(() => {
  return state.members.filter(m => !isSelfMember(m)).length
})

async function reloadMembersAndPing(manual = false) {
  if (refreshing.value || !state.currentRoom) return
  refreshing.value = true
  const roomId = state.currentRoom.room_id

  try {
    let list: Member[] = await fetchMembers(roomId).catch(() => [])
    if (!list || list.length === 0) {
      list = []
      if (state.me) {
        list.push({
          user_id: state.me.id || 1,
          name: state.me.name || '我',
          zt_node_id: state.nodeId || '',
          assigned_ip: state.assignedIp || '',
          is_owner: true,
        })
      }
      try {
        const peers = await fetchZtPeers()
        for (const p of peers || []) {
          if (p.address && p.address !== state.nodeId && p.role === 'LEAF') {
            list.push({
              user_id: 0,
              name: `玩家 (${p.address.slice(0, 6)})`,
              zt_node_id: p.address,
              assigned_ip: '',
              is_owner: false,
            })
          }
        }
      } catch {}
    }
    state.members = list

    // 并发测速
    const netId = state.currentRoom.network_id || ''
    const promises = state.members.map(async m => {
      if (isSelfMember(m)) return
      const key = m.zt_node_id || m.assigned_ip
      if (!key) return
      try {
        const res = await pingPeer(m.assigned_ip, m.zt_node_id, netId)
        state.memberLatency[key] = res
      } catch {
        state.memberLatency[key] = {
          state: 'no_tunnel',
          state_text: '探测失败',
          reachable: false,
          latency: -1,
        }
      }
    })
    await Promise.allSettled(promises)
    if (manual) showToast(`成员测速已更新 (共 ${state.members.length} 人)`)
  } catch (e: any) {
    if (manual) showToast(`刷新失败: ${e.message}`, true)
  } finally {
    setTimeout(() => {
      refreshing.value = false
    }, 300)
  }
}
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
    <div class="member-table-wrap">
      <table class="member-table">
        <thead>
          <tr>
            <th>玩家</th>
            <th style="width: 140px;">联机地址</th>
            <th style="width: 130px;">延迟 / 状态</th>
            <th style="width: 60px; text-align: right;">操作</th>
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
  padding: 8px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--card-subtle);
  flex: none;
}

.head-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.head-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--fg);
}

.member-count-badge {
  font-size: 11px;
  color: var(--fg-subtle);
  background: var(--border-subtle);
  padding: 1px 6px;
  border-radius: 10px;
}

.head-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.only-self-hint {
  font-size: 11.5px;
  color: var(--warn);
}

.member-table-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.member-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}

.member-table thead {
  position: sticky;
  top: 0;
  background: var(--card-subtle);
  border-bottom: 1px solid var(--border);
  z-index: 2;
}

.member-table th {
  padding: 7px 12px;
  font-weight: 600;
  color: var(--fg-subtle);
  font-size: 11px;
  text-transform: uppercase;
  text-align: left;
}

.member-table td {
  padding: 8px 12px;
  border-bottom: 1px solid var(--border-subtle);
  vertical-align: middle;
}

.empty-cell {
  text-align: center;
  padding: 40px 10px;
  color: var(--fg-faint);
}
</style>
