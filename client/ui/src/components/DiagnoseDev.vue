<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { fetchLogs } from '../api'
import { copyToClipboard } from '../lib/format'
import { showToast } from '../store'

const logs = ref('')
const logPath = ref('')
const loading = ref(false)
const preEl = ref<HTMLElement | null>(null)

async function loadDevLogs(manual = false) {
  if (loading.value) return
  loading.value = true
  try {
    const res = await fetchLogs('dev')
    logs.value = res.logs || '暂无开发者日志'
    logPath.value = res.path || ''
    if (manual) showToast('日志已刷新')
    setTimeout(() => {
      if (preEl.value) {
        preEl.value.scrollTop = preEl.value.scrollHeight
      }
    }, 50)
  } catch (e: any) {
    logs.value = `获取日志失败: ${e.message}`
    if (manual) showToast(logs.value, true)
  } finally {
    loading.value = false
  }
}

async function copyAll() {
  if (!logs.value) return
  const ok = await copyToClipboard(logs.value)
  showToast(ok ? '已复制全部开发日志' : '复制失败', !ok)
}

onMounted(() => {
  loadDevLogs()
})
</script>

<template>
  <div class="diagnose-dev">
    <div class="dev-toolbar">
      <div class="path-box" :title="logPath ? `日志文件: ${logPath}` : ''">
        <span class="path-label">路径:</span>
        <span class="path-text mono">{{ logPath || '内存缓冲' }}</span>
      </div>

      <div class="toolbar-btns">
        <button
          class="btn btn-sm"
          :class="{ spinning: loading }"
          :disabled="loading"
          @click="loadDevLogs(true)"
        >
          刷新
        </button>
        <button
          class="btn btn-sm btn-primary"
          :disabled="!logs"
          @click="copyAll"
        >
          复制全部
        </button>
      </div>
    </div>

    <div class="log-pre-wrap">
      <pre ref="preEl" class="log-pre mono">{{ logs }}</pre>
    </div>
  </div>
</template>

<style scoped>
.diagnose-dev {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
}

.dev-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  background: var(--card-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 12px;
  flex: none;
}

.path-box {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.path-label {
  color: var(--fg-faint);
  flex: none;
}

.path-text {
  color: var(--fg-subtle);
  overflow: hidden;
  text-overflow: ellipsis;
}

.toolbar-btns {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}

.log-pre-wrap {
  flex: 1;
  min-height: 0;
  background: #090d16;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
  display: flex;
}

.log-pre {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px;
  margin: 0;
  color: #e2e8f0;
  font-size: 11.5px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
