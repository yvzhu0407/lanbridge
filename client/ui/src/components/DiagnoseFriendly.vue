<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { state, showToast, syncRoomNetwork } from '../store'
import { fetchZtHealth, fetchLogs } from '../api'
import { userCopy } from '../lib/userCopy'
import type { Issue } from '../types'

const checking = ref(false)
const userEvents = ref<Array<{ time: string; level: string; code: string; text: string }>>([])

const issues = computed<Issue[]>(() => {
  return state.health?.issues || []
})

const anyReady = computed(() => {
  return (state.health?.networks || []).some(n => n.ready)
})

const planetDot = computed(() => {
  if (!state.health) return 'warn'
  if (state.health?.planet_ok) return 'ok'
  if (anyReady.value) return 'warn'
  return 'err'
})

const planetText = computed(() => {
  if (!state.health) return '尚未获取检测结果'
  if (state.health?.planet_ok) {
    const lat = state.health.planet_latency
    return lat >= 0 ? `已连通 (${lat} ms)` : '已连通'
  }
  if (anyReady.value) return '偶有抖动（已用缓存配置，不影响对局）'
  return '未连通'
})

function getIssueTitle(is: Issue): string {
  if (is.user_text) return is.user_text
  const baseCode = is.code.split(':')[0]
  const fallback = (userCopy.fallbackIssues as any)[baseCode]
  if (fallback?.text) return fallback.text
  return is.text || '网络连接异常'
}

function getIssueSteps(is: Issue): string[] {
  if (is.user_steps && is.user_steps.length > 0) return is.user_steps
  const baseCode = is.code.split(':')[0]
  const fallback = (userCopy.fallbackIssues as any)[baseCode]
  if (fallback?.steps) return fallback.steps
  return is.steps || ['请尝试重启 LANBridge']
}

async function loadUserLogs() {
  try {
    const res = await fetchLogs('user')
    const raw = res.logs || ''
    const lines = raw.split('\n').filter(Boolean)
    const parsed = lines.slice(-40).map(line => {
      // 2026-11-09T10:20:31|warn|net_not_ready|文本
      const parts = line.split('|')
      if (parts.length >= 4) {
        return {
          time: parts[0].slice(11, 19),
          level: parts[1],
          code: parts[2],
          text: parts.slice(3).join('|'),
        }
      }
      return { time: '', level: 'info', code: '', text: line }
    })
    userEvents.value = parsed.reverse() // 最新在最前
  } catch {}
}

async function handleRecheck() {
  if (checking.value) return
  checking.value = true
  try {
    const rep = await fetchZtHealth()
    state.health = rep
    state.healthError = ''
    state.nodeId = rep.node_ready ? rep.node_id : ''
    syncRoomNetwork()
    await loadUserLogs()
    const count = (rep.issues || []).length
    showToast(count > 0 ? `发现 ${count} 项异常` : !rep.node_ready ? '联机引擎未就绪' : !anyReady.value ? '引擎已响应，尚无就绪的房间网络' : '本机虚拟网络已就绪', count > 0 || !rep.node_ready)
  } catch (e: any) {
    state.health = null
    state.healthError = e.message
    state.nodeId = ''
    syncRoomNetwork()
    showToast(`检查失败: ${e.message}`, true)
  } finally {
    setTimeout(() => {
      checking.value = false
    }, 300)
  }
}

onMounted(() => {
  loadUserLogs()
})
</script>

<template>
  <div class="diagnose-friendly">
    <!-- 诊断状态提示卡 -->
    <div class="status-summary-card">
      <div v-if="!state.health" class="alert-box alert-warn">
        <div class="alert-title">{{ state.healthError ? '无法获取网络状态' : '尚未完成网络检测' }}</div>
        <div class="alert-desc">{{ state.healthError || '请点击重新检测，获取本机联机引擎和虚拟网络状态。' }}</div>
      </div>
      <div v-else-if="state.health.recovering" class="alert-box alert-warn">
        <div class="alert-title">🔄 自动恢复中：{{ state.health?.recover_note || '正在重新建立连接' }}</div>
        <div class="alert-desc">已交给后台自动处理，无需手动操作；若长时间未恢复，可尝试重启程序。</div>
      </div>

      <div v-else-if="issues.length > 0" class="issues-list">
        <div v-for="is in issues" :key="is.code" class="alert-box alert-err">
          <div class="alert-title">⚠ {{ getIssueTitle(is) }}</div>
          <ol class="action-steps">
            <li v-for="(step, sIdx) in getIssueSteps(is)" :key="sIdx">
              {{ step }}
            </li>
          </ol>
        </div>
      </div>

      <div v-else-if="!state.health.node_ready" class="alert-box alert-err">
        <div class="alert-title">联机引擎未就绪</div>
        <div class="alert-desc">请稍后重新检测，或查看最近事件。</div>
      </div>
      <div v-else-if="!anyReady" class="alert-box alert-warn">
        <div class="alert-title">{{ state.currentRoom ? '房间网络尚未就绪' : '联机引擎已响应，尚未连接房间' }}</div>
        <div class="alert-desc">{{ state.currentRoom ? '正在等待虚拟网卡和联机地址，稍后会自动更新。' : '加入房间后才能检测成员之间是否互通。' }}</div>
      </div>
      <div v-else class="alert-box alert-ok">
        <div class="alert-title">✓ 虚拟网络状态正常</div>
        <div class="alert-desc">本机虚拟网络已就绪。成员互通情况请查看房间测速，游戏内连接仍需实际验证。</div>
      </div>

      <!-- 根节点链路行 -->
      <div class="planet-row">
        <div class="planet-left">
          <span class="dot" :class="`dot-${planetDot}`"></span>
          <span class="planet-label">网络根节点链路:</span>
          <span class="planet-val">{{ planetText }}</span>
        </div>
        <button
          class="btn btn-sm"
          :class="{ spinning: checking }"
          :disabled="checking"
          @click="handleRecheck"
        >
          重新检测
        </button>
      </div>
    </div>

    <!-- 最近事件 -->
    <div class="recent-events-section">
      <div class="section-head">
        <h5 class="section-title">最近事件</h5>
        <span class="section-sub mono">{{ userEvents.length }} 条记录</span>
      </div>

      <div class="events-scroll">
        <div
          v-for="(ev, idx) in userEvents"
          :key="idx"
          class="event-item"
          :class="`level-${ev.level}`"
        >
          <span v-if="ev.time" class="event-time mono">{{ ev.time }}</span>
          <span class="event-text">{{ ev.text }}</span>
        </div>

        <div v-if="userEvents.length === 0" class="empty-events">
          暂无历史事件记录
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.diagnose-friendly {
  display: flex;
  flex-direction: column;
  gap: 16px;
  height: 100%;
}

.status-summary-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.alert-box {
  padding: 10px 14px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
}

.alert-ok {
  background: var(--ok-subtle);
  border-color: rgba(22, 163, 74, 0.25);
  color: var(--ok);
}

.alert-warn {
  background: var(--warn-subtle);
  border-color: rgba(245, 158, 11, 0.3);
  color: var(--warn);
}

.alert-err {
  background: var(--err-subtle);
  border-color: rgba(220, 38, 38, 0.25);
  color: var(--err);
}

.alert-title {
  font-weight: 600;
  font-size: 13px;
  margin-bottom: 4px;
}

.alert-desc {
  font-size: 11.5px;
  opacity: 0.9;
  line-height: 1.5;
}

.action-steps {
  margin-top: 6px;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.action-steps li {
  font-size: 12px;
  color: var(--fg);
}

.planet-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: var(--card-subtle);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius);
  font-size: 12px;
}

.planet-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.planet-label {
  color: var(--fg-subtle);
}

.planet-val {
  color: var(--fg);
  font-weight: 500;
}

.recent-events-section {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--card-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--card);
}

.section-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--fg);
}

.section-sub {
  font-size: 11px;
  color: var(--fg-faint);
}

.events-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.event-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px;
  border-radius: 4px;
  font-size: 12px;
  background: var(--card);
  border: 1px solid var(--border-subtle);
}

.event-time {
  font-size: 11px;
  color: var(--fg-faint);
  flex: none;
}

.event-text {
  color: var(--fg);
  line-height: 1.4;
}

.event-item.level-warn .event-text {
  color: var(--warn);
}

.event-item.level-error .event-text {
  color: var(--err);
}

.empty-events {
  text-align: center;
  padding: 30px;
  color: var(--fg-faint);
  font-size: 12px;
}
</style>
