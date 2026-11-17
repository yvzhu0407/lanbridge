<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { state, showToast, isSelfMember } from '../store'
import type { ChatMessage, Member } from '../types'
import { userCopy } from '../lib/userCopy'

const inputMessage = ref('')
const sending = ref(false)
const msgListEl = ref<HTMLElement | null>(null)

const messages = ref<ChatMessage[]>([])
const renderedIds = new Set<string>()
const delivery = ref<Record<string, string>>({})

// 聊天开关与未读数放在 store：结论栏上的「聊天」按钮要读写它们
const expanded = computed({
  get: () => state.chatOpen,
  set: v => { state.chatOpen = v },
})
const unreadCount = computed({
  get: () => state.chatUnread,
  set: v => { state.chatUnread = v },
})

// Peer health: ip -> { ok, failedAt, failCount }
const peerHealth = new Map<string, { ok: boolean; failedAt: number; failCount: number }>()
const CHAT_TIMEOUT = 2000
const PEER_BACKOFF = 30000

let pollTimer: any = null
let isPolling = false

// 聊天走 8890 局域网直连，服务端只放行 Status=OK 的网络。
// 所以这里必须把「网络就绪」也算进可用性，否则会一直 403 而界面毫无解释。
const netState = computed(() => {
  const id = state.currentRoom?.network_id
  if (!id) return null
  return (state.health?.networks || []).find(n => n.network_id === id) || null
})

const isReady = computed(() => {
  return !!(state.joined && state.currentRoom && state.assignedIp && netState.value?.ready)
})

// 不可用时给用户一句人话，而不是空列表。
// 必须保证任何 !isReady 的分支都有文案，否则会出现「暂时不能聊天：」后面空一片。
const blockReason = computed(() => {
  if (!state.currentRoom) return '还没进入房间'
  const net = netState.value
  if (net && !net.ready) return net.user_reason || net.reason || '虚拟网络状态异常'
  if (!state.assignedIp) return '虚拟网络还没分到联机地址'
  if (!net) return '虚拟网络还没建好'
  if (!state.joined) return '正在等服务器确认房间权限'
  return '虚拟网络还在就绪中'
})

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
  // 网络没就绪时服务端必然 403，轮询只是刷错误，直接跳过
  if (isPolling || !state.currentRoom || !isReady.value) return
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
  <!-- 聊天不常驻：默认隐藏，由结论栏的「聊天」按钮开关 -->
  <div v-if="expanded" class="chat-overlay">
    <div class="chat-panel">
      <div class="chat-header">
        <span class="chat-title">房间聊天</span>
        <span class="online-peers mono">{{ chatPeers().length }} 人在线</span>
        <button class="btn btn-sm btn-ghost close-btn" title="收起聊天" @click="toggleExpand">✕</button>
      </div>

      <!-- 网络未就绪：聊天服务会拒绝所有请求，必须先说明原因 -->
      <div v-if="!isReady" class="chat-blocked">
        <span class="blocked-icon">!</span>
        <span class="blocked-text">暂时不能聊天：{{ blockReason }}</span>
        <span class="blocked-hint">网络连通后会自动可用</span>
      </div>

      <div ref="msgListEl" class="chat-msg-list">
        <div
          v-for="m in messages"
          :key="m.id"
          class="chat-msg-row"
          :class="{ 'from-self': state.me && m.name === state.me.name }"
        >
          <div class="msg-sender">{{ m.name }}</div>
          <div class="msg-bubble">{{ m.text }}</div>
          <div v-if="delivery[m.id]" class="msg-delivery">{{ delivery[m.id] }}</div>
        </div>
        <div v-if="messages.length === 0" class="empty-chat">
          {{ isReady ? '还没有消息' : '网络通了才能收发消息' }}
        </div>
      </div>

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
/* 右侧浮层：盖住右半边，不挤压成员表 */
.chat-overlay {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 300px;
  z-index: 30;
  display: flex;
  padding: 10px 12px 10px 0;
  animation: chat-in 0.16s ease-out;
}

@keyframes chat-in {
  from { transform: translateX(12px); opacity: 0; }
  to { transform: translateX(0); opacity: 1; }
}

.chat-panel {
  flex: 1;
  min-height: 0;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: -6px 0 20px rgba(15, 23, 42, 0.10);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.chat-header {
  height: 32px;
  padding: 0 8px 0 10px;
  display: flex;
  align-items: center;
  gap: 6px;
  border-bottom: 1px solid var(--border);
  background: var(--card-subtle);
  flex: none;
}

.chat-title {
  font-weight: 600;
  font-size: 12px;
  color: var(--fg);
}

.online-peers {
  font-size: 10.5px;
  color: var(--fg-faint);
}

.chat-blocked {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  background: var(--warn-subtle);
  border-bottom: 1px solid var(--border);
  font-size: 11px;
  color: var(--warn);
  line-height: 1.4;
}

.blocked-icon {
  flex: none;
  width: 13px;
  height: 13px;
  border-radius: 50%;
  background: var(--warn);
  color: #fff;
  font-size: 9px;
  font-weight: 700;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.blocked-text {
  flex: 1;
  min-width: 0;
  font-weight: 500;
}

.blocked-hint {
  flex: none;
  color: var(--fg-faint);
  font-size: 10.5px;
}

.close-btn {
  margin-left: auto;
  padding: 1px 5px;
  font-size: 11px;
  line-height: 1.3;
}

.chat-msg-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.chat-msg-row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 88%;
}

.chat-msg-row.from-self {
  align-self: flex-end;
  align-items: flex-end;
}

.msg-sender {
  font-size: 10.5px;
  color: var(--fg-faint);
  margin-bottom: 1px;
  padding: 0 3px;
}

.msg-bubble {
  background: var(--card-subtle);
  color: var(--fg);
  padding: 5px 9px;
  border-radius: 7px;
  font-size: 12px;
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
  font-size: 10px;
  color: var(--fg-subtle);
  margin-top: 1px;
  padding: 0 3px;
}

.empty-chat {
  text-align: center;
  color: var(--fg-faint);
  font-size: 11px;
  margin: auto;
}

.chat-footer {
  padding: 6px 8px;
  border-top: 1px solid var(--border);
  display: flex;
  gap: 5px;
  background: var(--card);
  flex: none;
}

.chat-input {
  font-size: 12px;
  padding: 4px 8px;
}
</style>
