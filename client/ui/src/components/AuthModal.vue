<script setup lang="ts">
import { ref } from 'vue'
import { state, showToast, askConfirm, resetRoom } from '../store'
import { login, register, fetchMe, logout } from '../api'
import { handleInvite } from '../lib/join'

const username = ref('')
const password = ref('')
const submitting = ref(false)

// 收到邀请时还没登录：先记住房间号，登录成功后接着把它加进去，
// 否则用户登录完还得回头再找一次那条链接。
function resumePendingInvite() {
  const roomId = state.pendingJoinRoomId
  if (!roomId) return
  state.pendingJoinRoomId = null
  handleInvite(roomId)
}

async function handleLogin() {
  if (submitting.value || state.roomBusy) return
  const name = username.value.trim()
  if (!name || !password.value) {
    showToast('请输入用户名与密码', true)
    return
  }
  submitting.value = true
  try {
    await login(name, password.value)
    state.me = await fetchMe()
    showToast('登录成功')
    state.authModalOpen = false
    username.value = ''
    password.value = ''
    resumePendingInvite()
  } catch (e: any) {
    showToast(`登录失败: ${e.message}`, true)
  } finally {
    submitting.value = false
  }
}

async function handleRegister() {
  if (submitting.value || state.roomBusy) return
  const name = username.value.trim()
  if (!name || !password.value) {
    showToast('请输入用户名与密码', true)
    return
  }
  submitting.value = true
  try {
    await register(name, password.value)
    state.me = await fetchMe()
    showToast('注册成功并已自动登录')
    state.authModalOpen = false
    username.value = ''
    password.value = ''
    resumePendingInvite()
  } catch (e: any) {
    showToast(`注册失败: ${e.message}`, true)
  } finally {
    submitting.value = false
  }
}

async function handleLogout() {
  if (submitting.value || state.roomBusy) return
  const ok = await askConfirm({
    title: `退出 ${state.me?.name || '当前账号'}？`,
    detail: '退出后会先离开当前房间，下次打开需要重新登录。',
    confirmText: '退出登录',
    danger: true,
  })
  if (!ok) return
  submitting.value = true
  state.roomBusy = true
  state.roomRevision++
  try {
    await logout()
    resetRoom()
    state.me = null
    state.config = state.config ? { ...state.config, logged_in: false } : null
    state.rooms = []
    state.roomsError = ''
    state.pendingJoinRoomId = null
    state.passModalOpen = false
    state.createDrawerOpen = false
    state.authModalOpen = false
    showToast('已退出登录')
  } catch (e: any) {
    showToast(`退出登录失败：${e.message}`, true)
  } finally {
    submitting.value = false
    state.roomBusy = false
    state.roomPollDue = 0
  }
}

function handleClose() {
  if (submitting.value) return
  state.authModalOpen = false
}
</script>

<template>
  <div v-if="state.authModalOpen" class="modal-backdrop" @click.self="handleClose">
    <div class="modal-dialog">
      <div class="modal-header">
        <h3 class="modal-title">{{ state.me ? '账号信息' : '登录 / 注册' }}</h3>
        <button class="btn btn-ghost btn-icon" @click="handleClose">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>

      <div v-if="state.me" class="modal-body">
        <div class="logged-info">
          <div class="info-row">
            <span class="info-label">用户名称:</span>
            <span class="info-val font-bold">{{ state.me.name }}</span>
          </div>
          <div class="info-row">
            <span class="info-label">用户 ID:</span>
            <span class="info-val mono">#{{ state.me.id }}</span>
          </div>
          <div v-if="state.nodeId" class="info-row">
            <span class="info-label">节点 ID:</span>
            <span class="info-val mono">{{ state.nodeId }}</span>
          </div>
        </div>

        <div class="modal-actions">
          <button type="button" class="btn btn-danger" :disabled="submitting || state.roomBusy" @click="handleLogout">
            退出登录
          </button>
        </div>
      </div>

      <form v-else class="modal-body" @submit.prevent="handleLogin">
        <div class="form-group">
          <label class="form-label">用户名</label>
          <input
            v-model="username"
            type="text"
            class="input"
            placeholder="输入用户名"
            autofocus
          >
        </div>

        <div class="form-group">
          <label class="form-label">密码</label>
          <input
            v-model="password"
            type="password"
            class="input"
            placeholder="输入密码"
          >
        </div>

        <div class="modal-actions auth-actions">
          <button
            type="button"
            class="btn"
            :disabled="submitting || state.roomBusy"
            @click="handleRegister"
          >
            注册新账号
          </button>
          <button
            type="submit"
            class="btn btn-primary"
            :disabled="submitting || state.roomBusy"
          >
            {{ submitting ? '请稍候…' : '登录' }}
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
  gap: 14px;
}

.logged-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 6px 0;
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
}

.info-label {
  color: var(--fg-subtle);
}

.info-val {
  color: var(--fg);
}

.font-bold {
  font-weight: 600;
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

.auth-actions {
  justify-content: space-between;
}
</style>
