<script setup lang="ts">
import { ref } from 'vue'
import { state, showToast } from '../store'
import { createRoom } from '../api'
import { connectRoom, confirmRoomSwitch } from '../lib/join'

const roomName = ref('')
const roomPassword = ref('')
const submitting = ref(false)

async function handleSubmit() {
  if (submitting.value || state.roomBusy) return
  if (!state.me) {
    state.authModalOpen = true
    showToast('请先登录，登录后可继续创建房间')
    return
  }
  const name = roomName.value.trim()
  if (!name) {
    showToast('请输入房间名称', true)
    return
  }
  if (!state.nodeId) {
    showToast('ZeroTier 本地节点尚未就绪', true)
    return
  }

  submitting.value = true
  state.roomBusy = true
  state.roomRevision++
  try {
    if (!await confirmRoomSwitch(name)) return
    const res = await createRoom(name, roomPassword.value, state.nodeId)
    await connectRoom(res, name)
    roomName.value = ''
    roomPassword.value = ''
    state.createDrawerOpen = false
  } catch (e: any) {
    showToast(e.message, true)
  } finally {
    submitting.value = false
    state.roomBusy = false
    state.roomPollDue = 0
  }
}

function handleClose() {
  if (submitting.value) return
  state.createDrawerOpen = false
}
</script>

<template>
  <div v-if="state.createDrawerOpen" class="modal-backdrop" @click.self="handleClose">
    <div class="modal-dialog">
      <div class="modal-header">
        <h3 class="modal-title">创建联机房间</h3>
        <button class="btn btn-ghost btn-icon" @click="handleClose">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>

      <form class="modal-body" @submit.prevent="handleSubmit">
        <div class="form-group">
          <label class="form-label">房间名称</label>
          <input
            v-model="roomName"
            type="text"
            class="input"
            placeholder="例如：周末四人局"
            autofocus
          >
        </div>

        <div class="form-group">
          <label class="form-label">加入密码（可选）</label>
          <input
            v-model="roomPassword"
            type="password"
            class="input"
            placeholder="留空则任何人均可加入"
          >
        </div>

        <div class="modal-actions">
          <button type="button" class="btn" @click="handleClose">取消</button>
          <button type="submit" class="btn btn-primary" :disabled="submitting || state.roomBusy">
            {{ submitting ? '创建中…' : '创建并进入' }}
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
  z-index: 100;
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
  max-width: 400px;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.15);
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.modal-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--fg);
}

.modal-body {
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--fg-muted);
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 6px;
}
</style>
