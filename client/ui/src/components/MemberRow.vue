<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Member } from '../types'
import { state, showToast, isSelfMember } from '../store'
import { formatMemberStatus, copyToClipboard } from '../lib/format'
import { pingPeer } from '../api'

const props = defineProps<{
  member: Member
}>()

const isMe = computed(() => isSelfMember(props.member))
const menuOpen = ref(false)
const pinging = ref(false)

const probeKey = computed(() => props.member.zt_node_id || props.member.assigned_ip)
const probe = computed(() => state.memberLatency[probeKey.value])

const badge = computed(() => formatMemberStatus(probe.value, isMe.value))

async function copyMemberIp() {
  if (!props.member.assigned_ip) return
  const ok = await copyToClipboard(props.member.assigned_ip)
  showToast(ok ? `已复制 ${props.member.name} 的 IP` : '复制失败', !ok)
}

async function copyNodeId() {
  if (!props.member.zt_node_id) return
  const ok = await copyToClipboard(props.member.zt_node_id)
  showToast(ok ? `已复制节点 ID: ${props.member.zt_node_id}` : '复制失败', !ok)
  menuOpen.value = false
}

async function doPing() {
  menuOpen.value = false
  if (isMe.value) return
  pinging.value = true
  const netId = state.currentRoom?.network_id || ''
  try {
    const res = await pingPeer(props.member.assigned_ip, props.member.zt_node_id, netId)
    state.memberLatency[probeKey.value] = res
    if (res.reachable) {
      showToast(`测速成功: ${res.latency} ms (经 ${res.via === 'tcp' ? 'TCP 8890' : 'ICMP'})`)
    } else {
      showToast(`${res.state_text}${res.tunnel !== undefined && res.tunnel >= 0 ? ` (隧道 ${res.tunnel}ms)` : ''}`, true)
    }
  } catch (e: any) {
    state.memberLatency[probeKey.value] = {
      state: 'no_tunnel',
      state_text: '探测失败',
      reachable: false,
      latency: -1,
    }
    showToast(`探测失败: ${e.message}`, true)
  } finally {
    pinging.value = false
  }
}
</script>

<template>
  <tr class="member-row" :class="{ 'is-me': isMe }">
    <!-- 玩家信息 -->
    <td>
      <div class="player-cell">
        <div class="player-avatar">
          {{ member.name ? member.name.slice(0, 1).toUpperCase() : '?' }}
        </div>
        <span class="player-name">{{ member.name }}</span>
        <span v-if="member.is_owner" class="badge badge-warn">房主</span>
        <span v-if="isMe" class="badge badge-ok">你</span>
      </div>
    </td>

    <!-- 联机地址 -->
    <td>
      <div class="ip-cell">
        <span class="mono ip-text">{{ member.assigned_ip || '--' }}</span>
        <button
          v-if="member.assigned_ip"
          class="btn btn-sm btn-ghost btn-copy"
          @click="copyMemberIp"
          title="复制 IP"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
          </svg>
        </button>
      </div>
    </td>

    <!-- 合并状态与延迟徽标 -->
    <td>
      <div
        class="status-badge"
        :class="`badge-${badge.colorClass}`"
        :title="badge.tooltip"
      >
        <span class="dot" :class="`dot-${badge.colorClass}`"></span>
        <span>{{ badge.label }}</span>
        <span v-if="badge.tooltip && badge.colorClass === 'faint'" class="info-icon">ⓘ</span>
      </div>
    </td>

    <!-- 操作 (行内 ⋯ 菜单) -->
    <td style="text-align: right;">
      <div v-if="!isMe" class="action-dropdown-wrap">
        <button
          class="btn btn-sm btn-ghost btn-icon"
          @click.stop="menuOpen = !menuOpen"
          title="更多操作"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="1"></circle>
            <circle cx="19" cy="12" r="1"></circle>
            <circle cx="5" cy="12" r="1"></circle>
          </svg>
        </button>

        <div v-if="menuOpen" class="action-menu" @click.stop>
          <button class="menu-item" @click="doPing" :disabled="pinging">
            {{ pinging ? '测速中…' : '重新测速' }}
          </button>
          <button class="menu-item" @click="copyNodeId">
            复制节点 ID
          </button>
        </div>
      </div>
      <span v-else class="self-tag">本机</span>
    </td>
  </tr>
</template>

<style scoped>
.member-row {
  transition: background 0.15s ease;
}

.member-row:hover {
  background: var(--card-hover);
}

.member-row.is-me {
  background: var(--card-subtle);
}

.player-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.player-avatar {
  width: 24px;
  height: 24px;
  border-radius: 4px;
  background: var(--card);
  border: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  font-size: 11px;
  color: var(--fg-subtle);
  flex: none;
}

.player-name {
  font-weight: 600;
  color: var(--fg);
}

.ip-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ip-text {
  color: var(--accent);
  font-weight: 600;
  font-size: 12px;
}

.btn-copy {
  padding: 2px 4px;
  opacity: 0.7;
}

.btn-copy:hover {
  opacity: 1;
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11.5px;
  font-weight: 500;
  cursor: default;
}

.info-icon {
  font-size: 11px;
  opacity: 0.75;
}

.self-tag {
  font-size: 11.5px;
  color: var(--fg-faint);
  padding-right: 4px;
}

.action-dropdown-wrap {
  position: relative;
  display: inline-block;
}

.action-menu {
  position: absolute;
  right: 0;
  top: 100%;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
  z-index: 30;
  min-width: 110px;
  padding: 4px 0;
  display: flex;
  flex-direction: column;
}

.menu-item {
  background: transparent;
  border: none;
  text-align: left;
  padding: 6px 12px;
  font-size: 12px;
  color: var(--fg);
  cursor: pointer;
  transition: background 0.12s ease;
  white-space: nowrap;
}

.menu-item:hover:not(:disabled) {
  background: var(--card-subtle);
  color: var(--accent);
}

.menu-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
