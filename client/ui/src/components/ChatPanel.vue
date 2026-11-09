<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { state, showToast, isSelfMember } from '../store'
import type { ChatMessage, Member } from '../types'
import { userCopy } from '../lib/userCopy'

const expanded = ref(false)
const inputMessage = ref('')
const sending = ref(false)
const msgListEl = ref<HTMLElement | null>(null)

const messages = ref<ChatMessage[]>([])
const renderedIds = new Set<string>()
const delivery = ref<Record<string, string>>({})
const unreadCount = ref(0)

// Peer health: ip -> { ok, failedAt, failCount }
const peerHealth = new Map<string, { ok: boolean; failedAt: number; failCount: number }>()
const CHAT_TIMEOUT = 2000
const PEER_BACKOFF = 30000

let pollTimer: any = null
let isPolling = false

const isReady = computed(() => !!(state.joined && state.currentRoom && state.assignedIp))

function chatPeers(): Member[] {
  return state.members.filter(m => !isSelfMember(m) && m.assigned_ip)
}

function isPeerUsable(ip: string): boolean {
  const h = peerHealth.get(ip)
  if (!h || h.failCount === 0) return true
  return Date.now() - h.failedAt > PEER_BACKOFF
}

function markPeerOk(ip: string) {
  const h = peerHealth.get(ip) || { failCount: 0, failedAt: 0, ok: true }
  h.ok = true
  h.failCount = 0
  peerHealth.set(ip, h)
}

function markPeerFail(ip: string) {
  const h = peerHealth.get(ip) || { failCount: 0, failedAt: 0, ok: false }
  h.ok = false
  h.failCount = (h.failCount || 0) + 1
  h.failedAt = Date.now()
  peerHealth.set(ip, h)
}

async function fetchWithTimeout(url: string, opts: RequestInit = {}): Promise<any> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), CHAT_TIMEOUT)
  try {
    const res = await fetch(url, { ...opts, signal: controller.signal })
    return await res.json()
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

async function pollMessages() {
  if (isPolling || !state.currentRoom) return
  isPolling = true
  try {
    const net = encodeURIComponent(state.currentRoom.network_id || '')
    let since = 0
    if (messages.value.length > 0) {
      since = Math.max(0, messages.value[messages.value.length - 1].ts - 1000)
    }

    const selfIp = state.assignedIp || '127.0.0.1'
    const targets = [selfIp]
    for (const p of chatPeers()) {
      if (isPeerUsable(p.assigned_ip)) {
        targets.push(p.assigned_ip)
      }
    }

    const responses = await Promise.all(
      targets.map(ip =>
        fetchWithTimeout(`http://${ip}:8890/chat/messages?net=${net}&since=${since}`)
      )
    )

    let hasNew = false
    targets.forEach((ip, idx) => {
      const res = responses[idx]
      if (!res || !res.ok) {
        if (ip !== selfIp) markPeerFail(ip)
        return
      }
      if (ip !== selfIp) markPeerOk(ip)

      const list = res.data?.messages || []
      for (const m of list) {
        if (!m || !m.id || renderedIds.has(m.id)) continue
        renderedIds.add(m.id)
        messages.value.push(m)
        hasNew = true
        if (!expanded.value) {
          unreadCount.value++
        }
      }
    })

    if (hasNew) {
      messages.value.sort((a, b) => a.ts - b.ts)
      scrollToBottom()
    }
  } finally {
    isPolling = false
  }
}

function scrollToBottom() {
  nextTick(() => {
    if (msgListEl.value) {
      msgListEl.value.scrollTop = msgListEl.value.scrollHeight
    }
  })
}

async function handleSend() {
  const text = inputMessage.value.trim()
  if (!text || sending.value || !isReady.value || !state.currentRoom) return

  const peers = chatPeers()
  if (peers.length === 0) {
    showToast(userCopy.chat.noOthers, true)
    return
  }

  const myName = state.me?.name || '我'
  const net = encodeURIComponent(state.currentRoom.network_id || '')
  const msg: ChatMessage = {
    id: 'm' + Date.now() + Math.random().toString(36).slice(2, 6),
    kind: 'text',
    text,
    ts: Date.now(),
    name: myName,
  }

  inputMessage.value = ''
  sending.value = true
  renderedIds.add(msg.id)
  messages.value.push(msg)
  delivery.value[msg.id] = '发送中…'
  scrollToBottom()

  const body = JSON.stringify(msg)
  const sendToIp = async (ip: string) => {
    const res = await fetchWithTimeout(`http://${ip}:8890/chat/send?net=${net}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
    const ok = !!(res && res.ok && res.data && res.data.stored === msg.id)
    if (ok) markPeerOk(ip)
    else markPeerFail(ip)
    return ok
  }

  try {
    const reachable = peers.filter(p => isPeerUsable(p.assigned_ip))
    const skipped = peers.filter(p => !isPeerUsable(p.assigned_ip))
    const results = await Promise.all(reachable.map(p => sendToIp(p.assigned_ip)))

    const okCount = results.filter(Boolean).length
    const failedNames = reachable
      .filter((_, i) => !results[i])
      .map(p => p.name || p.assigned_ip)
      .concat(skipped.map(p => `${p.name || p.assigned_ip}(离线)`))

    if (failedNames.length === 0) {
      delivery.value[msg.id] = `已送达 ${okCount}/${peers.length}`
    } else {
      delivery.value[msg.id] = `已送达 ${okCount}/${peers.length}，未送达：${failedNames.join('、')}`
      showToast(delivery.value[msg.id], true)
    }

    // 本机留底
    if (okCount > 0) {
      sendToIp(state.assignedIp || '127.0.0.1')
    }
  } finally {
    sending.value = false
    scrollToBottom()
  }
}

function toggleExpand() {
  expanded.value = !expanded.value
  if (expanded.value) {
    unreadCount.value = 0
    scrollToBottom()
  }
}

watch(expanded, val => {
  if (val) {
    unreadCount.value = 0
    scrollToBottom()
  }
})

onMounted(() => {
  pollTimer = setInterval(pollMessages, 2000)
  pollMessages()
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="chat-container" :class="{ expanded }">
    <!-- 收起态：44px 高度条 -->
    <div v-if="!expanded" class="chat-collapsed-bar" @click="toggleExpand">
      <div class="bar-left">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path>
        </svg>
        <span class="chat-bar-title">房间聊天</span>
        <span v-if="unreadCount > 0" class="badge badge-warn unread-badge">
          {{ unreadCount }} 条未读
        </span>
      </div>
      <span class="expand-hint">点击展开 ▸</span>
    </div>

    <!-- 展开态：满高聊天面板 -->
    <div v-else class="chat-expanded-panel">
      <div class="chat-header">
        <div class="chat-header-title">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path>
          </svg>
          <span>房间聊天</span>
          <span class="online-peers-hint mono">{{ chatPeers().length }} 人在线</span>
        </div>
        <button class="btn btn-sm btn-ghost" @click="toggleExpand" title="收起聊天">
          收起 ▾
        </button>
      </div>

      <!-- 消息列表 (内部滚动) -->
      <div ref="msgListEl" class="chat-msg-list">
        <div
          v-for="m in messages"
          :key="m.id"
          class="chat-msg-row"
          :class="{ 'from-self': state.me && m.name === state.me.name }"
        >
          <div class="msg-sender">{{ m.name }}</div>
          <div class="msg-bubble">{{ m.text }}</div>
          <div v-if="delivery[m.id]" class="msg-delivery">
            {{ delivery[m.id] }}
          </div>
        </div>

        <div v-if="messages.length === 0" class="empty-chat">
          暂无聊天消息
        </div>
      </div>

      <!-- 发送框 -->
      <div class="chat-footer">
        <input
          v-model="inputMessage"
          type="text"
          class="input chat-input"
          :placeholder="isReady ? userCopy.chat.placeholder : userCopy.chat.notReadyHint"
          :disabled="!isReady || sending"
          @keydown.enter="handleSend"
        >
        <button
          class="btn btn-sm btn-primary"
          :disabled="!isReady || !inputMessage.trim() || sending"
          @click="handleSend"
        >
          发送
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat-container {
  width: 300px;
  flex: none;
  display: flex;
  flex-direction: column;
}

.chat-collapsed-bar {
  height: 44px;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  transition: all 0.15s ease;
}

.chat-collapsed-bar:hover {
  background: var(--card-hover);
  border-color: var(--accent);
}

.bar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg);
}

.chat-bar-title {
  font-weight: 600;
  font-size: 12.5px;
}

.unread-badge {
  font-size: 10.5px;
  padding: 1px 5px;
}

.expand-hint {
  font-size: 11.5px;
  color: var(--fg-subtle);
}

.chat-expanded-panel {
  flex: 1;
  min-height: 0;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.chat-header {
  height: 42px;
  padding: 0 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--border);
  background: var(--card-subtle);
  flex: none;
}

.chat-header-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  font-size: 12.5px;
  color: var(--fg);
}

.online-peers-hint {
  font-size: 11px;
  color: var(--fg-subtle);
  margin-left: 4px;
}

.chat-msg-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.chat-msg-row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 85%;
}

.chat-msg-row.from-self {
  align-self: flex-end;
  align-items: flex-end;
}

.msg-sender {
  font-size: 11px;
  color: var(--fg-faint);
  margin-bottom: 2px;
  padding: 0 4px;
}

.msg-bubble {
  background: var(--card-subtle);
  color: var(--fg);
  padding: 6px 10px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.45;
  word-break: break-word;
  border: 1px solid var(--border-subtle);
}

.from-self .msg-bubble {
  background: var(--accent);
  color: #ffffff;
  border-color: var(--accent);
}

.msg-delivery {
  font-size: 10.5px;
  color: var(--fg-subtle);
  margin-top: 2px;
  padding: 0 4px;
}

.empty-chat {
  text-align: center;
  color: var(--fg-faint);
  font-size: 11.5px;
  margin: auto;
}

.chat-footer {
  padding: 8px 10px;
  border-top: 1px solid var(--border);
  display: flex;
  gap: 6px;
  background: var(--card);
  flex: none;
}

.chat-input {
  font-size: 12px;
  padding: 5px 8px;
}
</style>
