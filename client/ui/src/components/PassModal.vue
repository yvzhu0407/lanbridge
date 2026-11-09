<script setup lang="ts">
import { ref } from 'vue'
import { state, showToast } from '../store'
import { joinRoom, joinZtNetwork } from '../api'

const password = ref('')
const submitting = ref(false)

async function handleConfirm() {
  const roomId = state.pendingJoinRoomId
  if (!roomId) return
  if (!state.nodeId) {
    showToast('ZeroTier 本地节点尚未就绪', true)
    return
  }

  submitting.value = true
  try {
    const res = await joinRoom(roomId, password.value, state.nodeId)
    await joinZtNetwork(res.network_id)
    state.passModalOpen = false
    password.value = ''
    state.pendingJoinRoomId = null
    state.currentView = 'room'
    showToast('已加入房间，正在建立网络连接')
  } catch (e: any) {
    showToast(e.message, true)
  } finally {
    submitting.value = false
  }
}

function handleClose() {
  state.passModalOpen = false
  password.value = ''
  state.pendingJoinRoomId = null
}
</script>

<template>
  <div v-if="state.passModalOpen" class="modal-backdrop" @click.self="handleClose">
    <div class="modal-dialog">
      <div class="modal-header">
        <h3 class="modal-title">输入房间密码</h3>
        <button class="btn btn-ghost btn-icon" @click="handleClose">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>

      <form class="modal-body" @submit.prevent="handleConfirm">
        <div class="form-group">
          <label class="form-label">该房间设有加入密码</label>
          <input
            v-model="password"
            type="password"
            class="input"
            placeholder="请输入房间密码"
            autofocus
          >
        </div>

        <div class="modal-actions">
          <button type="button" class="btn" @click="handleClose">取消</button>
          <button type="submit" class="btn btn-primary" :disabled="submitting">
            {{ submitting ? '验证中…' : '加入' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(2px);
  z-index: 110;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}

.modal-dialog {
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 8px;
  width: 100%;
  max-width: 360px;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.15);
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}

.modal-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--fg);
}

.modal-body {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 12px;
  color: var(--fg-muted);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
</style>
