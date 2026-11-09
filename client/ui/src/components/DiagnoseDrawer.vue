<script setup lang="ts">
import { ref } from 'vue'
import { state } from '../store'
import DiagnoseFriendly from './DiagnoseFriendly.vue'
import DiagnoseDev from './DiagnoseDev.vue'

const currentTab = ref<'friendly' | 'dev'>('friendly')

function handleClose() {
  state.diagnoseOpen = false
}
</script>

<template>
  <div v-if="state.diagnoseOpen" class="drawer-backdrop" @click.self="handleClose">
    <div class="drawer-pane">
      <div class="drawer-header">
        <div class="header-left">
          <h3 class="drawer-title">网络与运行诊断</h3>
          <div class="tab-switch">
            <button
              class="tab-btn"
              :class="{ active: currentTab === 'friendly' }"
              @click="currentTab = 'friendly'"
            >
              问题诊断
            </button>
            <button
              class="tab-btn"
              :class="{ active: currentTab === 'dev' }"
              @click="currentTab = 'dev'"
            >
              开发者日志
            </button>
          </div>
        </div>

        <button class="btn btn-ghost btn-icon" @click="handleClose" title="关闭诊断">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>

      <div class="drawer-body">
        <DiagnoseFriendly v-if="currentTab === 'friendly'" />
        <DiagnoseDev v-else />
      </div>
    </div>
  </div>
</template>

<style scoped>
.drawer-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(2px);
  z-index: 100;
  display: flex;
  justify-content: flex-end;
}

.drawer-pane {
  width: min(520px, 92vw);
  height: 100%;
  background: var(--card);
  border-left: 1px solid var(--border);
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.15);
  display: flex;
  flex-direction: column;
  animation: slide-in 0.22s cubic-bezier(0.16, 1, 0.3, 1);
}

@keyframes slide-in {
  from { transform: translateX(100%); }
  to { transform: translateX(0); }
}

.drawer-header {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: none;
  background: var(--card-subtle);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 14px;
}

.drawer-title {
  font-size: 13.5px;
  font-weight: 700;
  color: var(--fg);
}

.tab-switch {
  display: inline-flex;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 2px;
  gap: 2px;
}

.tab-btn {
  border: none;
  background: transparent;
  color: var(--fg-subtle);
  font-size: 11.5px;
  font-weight: 500;
  padding: 3px 10px;
  border-radius: 4px;
  cursor: pointer;
  transition: all 0.15s ease;
}

.tab-btn:hover {
  color: var(--fg);
}

.tab-btn.active {
  background: var(--card-subtle);
  color: var(--fg);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

.drawer-body {
  flex: 1;
  min-height: 0;
  padding: 16px;
  overflow: hidden;
}
</style>
