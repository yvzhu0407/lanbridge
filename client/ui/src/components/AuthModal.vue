<script setup lang="ts">
import { ref } from 'vue'
import { state, showToast } from '../store'
import { login, register, fetchMe, leaveRoom } from '../api'

const username = ref('')
const password = ref('')
const submitting = ref(false)

async function handleLogin() {
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
  } catch (e: any) {
    showToast(`登录失败: ${e.message}`, true)
  } finally {
    submitting.value = false
  }
}

async function handleRegister() {
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
  } catch (e: any) {
    showToast(`注册失败: ${e.message}`, true)
  } finally {
    submitting.value = false
  }
}

async function handleLogout() {
  if (confirm(`当前登录为 ${state.me?.name}，是否退出登录？`)) {
    try {
      await leaveRoom()
    } catch {}
    location.reload()
  }
}

function handleClose() {
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
          <button type="button" class="btn btn-danger" @click="handleLogout">
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
            :disabled="submitting"
            @click="handleRegister"
          >
            注册新账号
          </button>
          <button
            type="submit"
            class="btn btn-primary"
            :disabled="submitting"
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
