<script setup lang="ts">
import { computed } from 'vue'
import { state, showToast, isSelfMember, askConfirm, resetRoom } from '../store'
import { copyToClipboard } from '../lib/format'
import { leaveRoom, dismissRoom, joinZtNetwork, inviteLink } from '../api'

const isOwner = computed(() => {
  if (!state.currentRoom || !state.me) return false
  const r = state.rooms.find(item => item.id === state.currentRoom?.room_id)
  if (r && r.owner_id && state.me.id) {
    return r.owner_id === state.me.id
  }
  return state.members.some(m => isSelfMember(m) && m.is_owner === true)
})

const myIp = computed(() => state.joined ? state.assignedIp : '')

const roomLabel = computed(() => {
  const name = state.currentRoom?.name
  if (name) return name
  const id = state.currentRoom?.room_id
  return id ? `房间 #${id}` : ''
})

// 一句话结论：这是整个房间页最重要的信息，其余都是它的注脚
const net = computed(() => state.health?.networks?.find(n => n.network_id === state.currentRoom?.network_id))

const verdict = computed(() => {
  const n = net.value
  if (!state.health || !state.health.node_ready) {
    return { status: 'err' as const, text: state.healthError ? '连接状态未知' : '联机引擎未就绪', hint: '打开诊断查看' }
  }
  if (!state.joined || !n?.ready) {
    const pending = !n || n.status === 'REQUESTING_CONFIGURATION' || (n.status === 'OK' && !n.ready)
    return {
      status: pending ? 'faint' as const : 'err' as const,
      text: pending ? '正在连接虚拟网络' : '这台机器没连上',
      hint: n?.user_reason || '正在等待网络就绪',
    }
  }
  if (state.roomError || state.membersError) {
    return { status: 'warn' as const, text: '本机网络已就绪', hint: '房间信息同步异常' }
  }

  const others = state.members.filter(m => !isSelfMember(m))
  if (others.length === 0) {
    return { status: 'ok' as const, text: '本机网络已就绪', hint: '点击邀请，让朋友加入房间' }
  }

  let reachable = 0
  let pending = 0
  for (const m of others) {
    const p = state.memberLatency[m.zt_node_id || m.assigned_ip]
    if (p === undefined || p === null) pending++
    else if (p.reachable && p.latency >= 0) reachable++
  }

  const total = others.length + 1
  if (pending === others.length) {
    return { status: 'faint' as const, text: '正在检测…', hint: '' }
  }
  if (reachable === others.length) {
    return { status: 'ok' as const, text: '成员网络互通', hint: `${reachable + 1}/${total} 人可达` }
  }
  return { status: 'warn' as const, text: '部分人连不上', hint: `${reachable + 1}/${total} 人可达` }
})

async function copyIp() {
  if (!myIp.value) {
    showToast('联机 IP 尚未分配', true)
    return
  }
  const ok = await copyToClipboard(myIp.value)
  showToast(ok ? '已复制联机地址' : '复制失败', !ok)
}

function toggleChat() {
  state.chatOpen = !state.chatOpen
}

// 复制邀请链接。房间名要和链接写在一起：对方可能只截走其中一行，
// 光有 lanbridge://join/81 看不出这是什么房间。
// 房间号单独留一行，聊天软件吞掉自定义协议时还能手抄加入。
async function copyInvite() {
  const id = state.currentRoom?.room_id
  if (!id) return
  const name = state.currentRoom?.name || `房间 #${id}`
  const text = [
    `来和我联机：${name}`,
    `${inviteLink(id)}（房间号 ${id} · ${name}）`,
  ].join('\n')
  const ok = await copyToClipboard(text)
  showToast(ok ? '邀请已复制，发给朋友即可加入' : '复制失败', !ok)
}

async function leaveOrDismiss(dismiss: boolean) {
  if (state.roomBusy || !state.currentRoom) return
  state.roomBusy = true
  state.roomRevision++
  try {
    const ok = await askConfirm(dismiss ? {
      title: '解散这个房间？',
      detail: '解散会销毁虚拟网络，房间内所有成员都会断开。',
      confirmText: '解散', danger: true,
    } : {
      title: '离开当前房间？',
      detail: '离开后需要在大厅重新加入才能继续联机。',
      confirmText: '离开',
    })
    if (!ok) return
    if (dismiss) await dismissRoom(state.currentRoom.room_id)
    else await leaveRoom()
    state.currentRoom.authorized = false
    state.joined = false
    state.assignedIp = ''
    try {
      await joinZtNetwork('', 'all')
    } catch (e: any) {
      state.roomError = `已退出房间，本地网络清理未完成，将自动重试：${e.message}`
      showToast(state.roomError, true)
      return
    }
    resetRoom()
    showToast(dismiss ? '房间已解散' : '已离开房间')
  } catch (e: any) {
    showToast(`${dismiss ? '解散' : '离开'}失败：${e.message}`, true)
  } finally {
    state.roomBusy = false
    state.roomPollDue = 0
  }
}

const handleLeave = () => leaveOrDismiss(false)
const handleDismiss = () => leaveOrDismiss(true)

</script>

<template>
  <div class="summary-bar">
    <!-- 左：结论 -->
    <div class="verdict" :class="`v-${verdict.status}`" :title="verdict.hint">
      <span class="dot" :class="`dot-${verdict.status}`"></span>
      <span class="verdict-text">{{ verdict.text }}</span>
      <span v-if="verdict.hint" class="verdict-hint">{{ verdict.hint }}</span>
    </div>

    <!-- 中：我的联机地址（核心操作） -->
    <button class="ip-chip" :disabled="!myIp" title="这是我在这台机器上的联机地址，点击复制" @click="copyIp">
      <span class="ip-label">我的地址</span>
      <span class="mono ip-text">{{ myIp || '分配中…' }}</span>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
      </svg>
    </button>

    <!-- 右：房间标识 + 操作 -->
    <div class="actions">
      <span v-if="roomLabel" class="room-label">{{ roomLabel }}</span>
      <span v-if="state.currentRoom?.room_id" class="room-id mono">#{{ state.currentRoom.room_id }}</span>
      <button class="btn btn-sm btn-ghost" @click="copyInvite">邀请</button>
      <button class="btn btn-sm btn-ghost" @click="toggleChat">
        聊天
        <span v-if="!state.chatOpen && state.chatUnread > 0" class="unread-dot">{{ state.chatUnread }}</span>
      </button>
      <button v-if="isOwner" class="btn btn-sm btn-ghost danger" :disabled="state.roomBusy" @click="handleDismiss">解散</button>
      <button class="btn btn-sm btn-ghost" :disabled="state.roomBusy" @click="handleLeave">离开</button>
    </div>
  </div>
</template>

<style scoped>
.summary-bar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 10px;
  min-height: 38px;
}

.verdict {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  font-weight: 600;
  flex: none;
}

.verdict-text { letter-spacing: -0.2px; }
.v-ok .verdict-text { color: var(--ok); }
.v-warn .verdict-text { color: var(--warn); }
.v-err .verdict-text { color: var(--err); }
.v-faint .verdict-text { color: var(--fg-subtle); }

.verdict-hint {
  font-size: 11.5px;
  font-weight: 400;
  color: var(--fg-faint);
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ip-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--accent-subtle);
  border: 1px solid var(--accent-border);
  border-radius: 4px;
  padding: 3px 8px;
  cursor: pointer;
  color: var(--accent);
  transition: all 0.15s ease;
}

.ip-chip:hover:not(:disabled) {
  background: var(--card);
}

.ip-label {
  font-size: 10.5px;
  color: var(--fg-subtle);
  font-weight: 500;
  padding-right: 5px;
  border-right: 1px solid var(--accent-border);
}

.ip-chip:disabled {
  opacity: 0.6;
  cursor: default;
}

.ip-text {
  font-size: 12.5px;
  font-weight: 700;
  letter-spacing: -0.2px;
}

.actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
}

.room-label {
  font-size: 11.5px;
  color: var(--fg-faint);
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 房间号：同房间的人靠这个对上号，比网络 hash 好认得多 */
.room-id {
  font-size: 11px;
  color: var(--fg-subtle);
  background: var(--card-subtle);
  border-radius: 4px;
  padding: 1px 5px;
  margin-right: 4px;
}

.danger { color: var(--err); }
.danger:hover:not(:disabled) { background: var(--err-subtle); }

.unread-dot {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 15px;
  height: 15px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--warn);
  color: #fff;
  font-size: 10px;
  font-weight: 600;
}
</style>
