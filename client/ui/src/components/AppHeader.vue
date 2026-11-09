<script setup lang="ts">
import { computed } from 'vue'
import { state, showToast } from '../store'
import { copyToClipboard } from '../lib/format'

const hasIssues = computed(() => {
  return (state.health?.issues && state.health.issues.length > 0) || false
})

const isRecovering = computed(() => {
  return state.health?.recovering || false
})

const nodeShort = computed(() => {
  if (!state.nodeId) return '离线'
  return `${state.nodeId.slice(0, 8)}…`
})

async function copyNodeId() {
  if (!state.nodeId) {
    showToast('ZeroTier 尚未就绪', true)
    return
  }
  const ok = await copyToClipboard(state.nodeId)
  showToast(ok ? '已复制 ZeroTier 节点 ID' : '复制失败', !ok)
}

function handleBrandClick() {
  if (state.currentRoom) {
    state.currentView = 'room'
  } else {
    state.currentView = 'lobby'
  }
}
</script>

<template>
  <header class="app-header">
    <div class="header-left">
      <div class="brand-btn" @click="handleBrandClick">
        <div class="brand-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"></path>
            <line x1="4" y1="22" x2="4" y2="15"></line>
          </svg>
        </div>
        <span class="brand-text">桥联</span>
      </div>

      <nav class="nav-pills">
        <button
          class="nav-pill"
          :class="{ active: state.currentView === 'lobby' }"
          @click="state.currentView = 'lobby'"
        >
          大厅
        </button>
        <button
          v-if="state.currentRoom"
          class="nav-pill"
          :class="{ active: state.currentView === 'room' }"
          @click="state.currentView = 'room'"
        >
          <span class="dot-online"></span>
          房间
        </button>
      </nav>
    </div>

    <div class="header-right">
      <div class="node-pill" @click="copyNodeId" title="点击复制 Node ID">
        <span
          class="dot"
          :class="{
            'dot-ok': state.nodeId && !hasIssues,
            'dot-warn dot-pulse': isRecovering,
            'dot-err': !state.nodeId || hasIssues
          }"
        ></span>
        <span class="mono">{{ state.nodeId ? `Node: ${nodeShort}` : 'ZeroTier 离线' }}</span>
      </div>

      <div class="header-divider"></div>

      <button
        class="diagnose-btn"
        :class="{ 'has-alert': hasIssues }"
        @click="state.diagnoseOpen = true"
        title="打开网络与诊断"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10"></circle>
          <line x1="12" y1="8" x2="12" y2="12"></line>
          <line x1="12" y1="16" x2="12.01" y2="16"></line>
        </svg>
        <span>诊断</span>
        <span v-if="hasIssues" class="alert-dot"></span>
      </button>

      <div class="user-pill" @click="state.authModalOpen = true">
        <span class="user-name">{{ state.me?.name || '未登录' }}</span>
      </div>
    </div>
  </header>
</template>

<style scoped>
.app-header {
  height: 44px;
  flex: none;
  background: var(--card);
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  z-index: 20;
}

.header-left, .header-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.brand-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}

.brand-icon {
  width: 22px;
  height: 22px;
  border-radius: 5px;
  background: linear-gradient(135deg, #1e3a8a, #2563eb);
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  box-shadow: 0 1px 3px rgba(37, 99, 235, 0.25);
}

.brand-text {
  font-weight: 700;
  font-size: 13.5px;
  letter-spacing: -0.2px;
  color: var(--fg);
}

.nav-pills {
  display: inline-flex;
  background: var(--card-subtle);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 2px;
  gap: 2px;
  margin-left: 6px;
}

.nav-pill {
  border: none;
  background: transparent;
  color: var(--fg-subtle);
  font-size: 12px;
  font-weight: 500;
  padding: 3px 12px;
  border-radius: 4px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  transition: all 0.15s ease;
}

.nav-pill:hover {
  color: var(--fg);
}

.nav-pill.active {
  background: var(--card);
  color: var(--fg);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

.dot-online {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ok);
}

.node-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--card-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 3px 8px;
  font-size: 11.5px;
  color: var(--fg-subtle);
  cursor: pointer;
  transition: all 0.15s ease;
}

.node-pill:hover {
  border-color: var(--accent);
  color: var(--fg);
}

.header-divider {
  width: 1px;
  height: 16px;
  background: var(--border);
}

/* 诊断按钮：默认灰字无边框，视觉不显眼；有异常时加红点 */
.diagnose-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: none;
  background: transparent;
  color: var(--fg-subtle);
  font-size: 12px;
  padding: 4px 8px;
  border-radius: var(--radius);
  cursor: pointer;
  position: relative;
  transition: all 0.15s ease;
}

.diagnose-btn:hover {
  background: var(--card-subtle);
  color: var(--fg);
}

.diagnose-btn.has-alert {
  color: var(--err);
}

.alert-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--err);
  position: absolute;
  top: 4px;
  right: 2px;
}

.user-pill {
  cursor: pointer;
  padding: 3px 8px;
  border-radius: var(--radius);
  color: var(--fg-muted);
  font-size: 12px;
  background: var(--card-subtle);
  border: 1px solid var(--border);
  transition: all 0.15s ease;
}

.user-pill:hover {
  border-color: var(--fg-subtle);
  color: var(--fg);
}
</style>
