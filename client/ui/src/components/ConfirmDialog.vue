<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { confirmState, settleConfirm } from '../store'

function onKey(e: KeyboardEvent) {
  if (!confirmState.open) return
  if (e.key === 'Escape') {
    e.preventDefault()
    settleConfirm(false)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    settleConfirm(true)
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="confirmState.open" class="confirm-backdrop" @click.self="settleConfirm(false)">
    <div class="confirm-dialog" role="dialog" aria-modal="true">
      <div class="confirm-title">{{ confirmState.title }}</div>
      <div v-if="confirmState.detail" class="confirm-detail">{{ confirmState.detail }}</div>
      <div class="confirm-actions">
        <button class="btn btn-sm" @click="settleConfirm(false)">取消</button>
        <button
          class="btn btn-sm"
          :class="confirmState.danger ? 'btn-danger' : 'btn-primary'"
          @click="settleConfirm(true)"
        >
          {{ confirmState.confirmText }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.confirm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(2px);
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}

.confirm-dialog {
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 8px;
  width: 100%;
  max-width: 320px;
  padding: 16px 18px 14px;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.22);
}

.confirm-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--fg);
  line-height: 1.45;
}

.confirm-detail {
  margin-top: 6px;
  font-size: 12px;
  color: var(--fg-subtle);
  line-height: 1.5;
}

.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.btn-danger {
  background: var(--err);
  border-color: var(--err);
  color: #fff;
}

.btn-danger:hover:not(:disabled) {
  filter: brightness(1.08);
}
</style>
