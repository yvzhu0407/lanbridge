<script setup lang="ts">
import { computed } from 'vue'
import { state, showToast, isSelfMember } from '../store'
import { copyToClipboard } from '../lib/format'
import { leaveRoom, dismissRoom, joinZtNetwork } from '../api'

const isOwner = computed(() => {
  if (!state.currentRoom || !state.me) return false
  const r = state.rooms.find(item => item.id === state.currentRoom?.room_id)
  if (r && r.owner_id && state.me.id) {
    return r.owner_id === state.me.id
  }
  return state.me.room_id === state.currentRoom.room_id
})

const myIp = computed(() => {
  return state.assignedIp || state.currentRoom?.ip || ''
})

const summaryState = computed(() => {
  // 1. 本机网络未就绪判定
  const health = state.health
  const net = health?.networks?.find(n => n.network_id === state.currentRoom?.network_id)
  if (!health || (net && !net.ready) || (!state.assignedIp && !state.joined)) {
    return {
      status: 'err' as const,
      title: '你这台机器没连上',
      hint: net?.reason || '虚拟网卡未建出或尚未分配到 IP，请查看右上角「诊断」',
    }
  }

  // 2. 成员探测统计
  const otherMembers = state.members.filter(m => !isSelfMember(m))
  if (otherMembers.length === 0) {
    return {
      status: 'ok' as const,
      title: '可以联机',
      hint: '当前房间仅有你一人，把联机地址发给好友即可加入对局',
    }
  }

  let reachableCount = 0
  let pendingCount = 0
  for (const m of otherMembers) {
    const key = m.zt_node_id || m.assigned_ip
    const probe = state.memberLatency[key]
    if (probe === undefined || probe === null) {
      pendingCount++
    } else if (probe.reachable && probe.latency >= 0) {
      reachableCount++
    }
  }

  if (pendingCount === otherMembers.length) {
    return {
      status: 'faint' as const,
      title: '正在检测…',
      hint: '正在探测各成员网络延迟与连通状态',
    }
  }

  if (reachableCount === otherMembers.length) {
    return {
      status: 'ok' as const,
      title: `可以联机 · 全部成员可达 (${reachableCount + 1}/${otherMembers.length + 1}人)`,
      hint: '虚拟网络与数据通道畅通，可以直接开始游戏',
    }
  }

  return {
    status: 'warn' as const,
    title: `部分成员可达 · ${reachableCount + 1}/${otherMembers.length + 1} 人可达`,
    hint: '部分成员尚未完成链路连接，可点击该成员查看排查建议',
  }
})

async function copyIp() {
  if (!myIp.value) {
    showToast('联机 IP 尚未分配', true)
    return
  }
  const ok = await copyToClipboard(myIp.value)
  showToast(ok ? '已复制联机 IP 到剪贴板' : '复制失败', !ok)
}

async function copyNwid() {
  const nwid = state.currentRoom?.network_id
  if (!nwid) return
  const ok = await copyToClipboard(nwid)
  showToast(ok ? '已复制虚拟网络 ID' : '复制失败', !ok)
}

async function handleLeave() {
  if (!confirm('确定离开当前房间？')) return
  try {
    await leaveRoom()
    await joinZtNetwork('', 'all')
  } catch {}
  state.currentRoom = null
  state.joined = false
  state.assignedIp = ''
  state.currentView = 'lobby'
  showToast('已离开房间')
}

async function handleDismiss() {
  if (!state.currentRoom) return
  if (!confirm('解散房间将销毁虚拟网络，所有成员都将断开。确认解散？')) return
  try {
    await dismissRoom(state.currentRoom.room_id)
    await joinZtNetwork('', 'all')
    state.currentRoom = null
    state.joined = false
    state.assignedIp = ''
    state.currentView = 'lobby'
    showToast('房间已解散')
  } catch (e: any) {
    showToast(e.message, true)
  }
}
</script>

<template>
  <div class="room-summary-card">
    <div class="summary-top">
      <div class="summary-conclusion">
        <div class="conclusion-badge" :class="`badge-${summaryState.status}`">
          <span class="dot" :class="`dot-${summaryState.status}`"></span>
          <span class="conclusion-title">{{ summaryState.title }}</span>
        </div>
        <div class="conclusion-hint">{{ summaryState.hint }}</div>
      </div>

      <div class="summary-actions">
        <button
          v-if="isOwner"
          class="btn btn-sm btn-danger"
          @click="handleDismiss"
        >
          解散房间
        </button>
        <button
          class="btn btn-sm btn-ghost"
          @click="handleLeave"
        >
          离开房间
        </button>
      </div>
    </div>

    <div class="summary-meta-row">
      <div class="meta-item ip-item">
        <span class="meta-label">你的联机地址:</span>
        <span class="meta-value mono ip-value">{{ myIp || '正在获取…' }}</span>
        <button
          class="btn btn-sm"
          :disabled="!myIp"
          @click="copyIp"
        >
          复制地址
        </button>
      </div>

      <div class="meta-item">
        <span class="meta-label">房间:</span>
        <span class="meta-value">{{ state.currentRoom?.name || `#${state.currentRoom?.room_id}` }}</span>
      </div>

      <div class="meta-item">
        <span class="meta-label">网络 ID:</span>
        <span
          class="meta-value mono link-value"
          @click="copyNwid"
          title="点击复制网络 ID"
        >
          {{ state.currentRoom?.network_id }}
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.room-summary-card {
  flex: none;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.summary-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.summary-conclusion {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.conclusion-badge {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  font-weight: 600;
  padding: 4px 10px;
  border-radius: var(--radius);
  width: fit-content;
}

.conclusion-title {
  letter-spacing: -0.2px;
}

.conclusion-hint {
  font-size: 11.5px;
  color: var(--fg-subtle);
  padding-left: 2px;
}

.summary-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.summary-meta-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 18px;
  padding-top: 8px;
  border-top: 1px solid var(--border-subtle);
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}

.meta-label {
  color: var(--fg-subtle);
}

.meta-value {
  color: var(--fg);
  font-weight: 500;
}

.ip-value {
  color: var(--accent);
  font-weight: 700;
  font-size: 13px;
}

.link-value {
  cursor: pointer;
  color: var(--fg-muted);
}

.link-value:hover {
  color: var(--accent);
  text-decoration: underline;
}
</style>
