import assert from 'node:assert/strict'
import { test, beforeEach, afterEach, mock } from 'node:test'
import { state, resetRoom, syncRoomNetwork, settleConfirm, confirmState } from '../src/store.ts'
import { api, ApiError } from '../src/api.ts'
import { joinRoomFlow, handleInvite } from '../src/lib/join.ts'

const originalFetch = globalThis.fetch
const member = { user_id: 1, name: 'player', zt_node_id: 'node', assigned_ip: '10.147.1.1' }
const room = (id = 1) => ({ room_id: id, network_id: `net-${id}`, joined: true, authorized: true, name: `room ${id}` })
const response = (data, status = 200) => new Response(JSON.stringify({ ok: status === 200, data, error: status === 200 ? undefined : '请求失败' }), { status })

beforeEach(() => {
  mock.timers.enable({ apis: ['setTimeout'] })
  resetRoom()
  state.roomBusy = false
  state.me = { id: 1, name: 'player', zt_node_id: 'node' }
  state.nodeId = 'node'
  state.health = null
  state.healthError = ''
  state.authModalOpen = false
  state.passModalOpen = false
  state.pendingJoinRoomId = null
  state.rooms = [1, 2].map(id => ({ id, name: `room ${id}`, network_id: `net-${id}`, game: 'civ6', user_count: 1, password_required: false }))
  state.toasts = []
})
afterEach(() => {
  settleConfirm(false)
  globalThis.fetch = originalFetch
  mock.timers.reset()
})

test('rejoining current room does not call the server or clear its data', async () => {
  resetRoom(room())
  state.members = [member]
  globalThis.fetch = () => { throw new Error('must not request') }
  assert.equal(await joinRoomFlow(1), true)
  assert.equal(state.currentRoom.network_id, 'net-1')
  assert.equal(state.members.length, 1)
})

test('join requires login and retains the intended room', async () => {
  state.me = null
  globalThis.fetch = () => { throw new Error('must not request') }
  assert.equal(await joinRoomFlow(2), false)
  assert.equal(state.authModalOpen, true)
  assert.equal(state.pendingJoinRoomId, 2)
})

test('cancelled switch preserves the current room without requests', async () => {
  resetRoom(room())
  globalThis.fetch = () => { throw new Error('must not request') }
  const joining = joinRoomFlow(2)
  assert.equal(confirmState.open, true)
  settleConfirm(false)
  assert.equal(await joining, false)
  assert.equal(state.currentRoom.room_id, 1)
  assert.equal(state.roomBusy, false)
})

test('remote switch failure keeps current room and existing member data', async () => {
  resetRoom(room())
  state.members = [member]
  globalThis.fetch = async () => response(null, 503)
  const joining = joinRoomFlow(2)
  settleConfirm(true)
  assert.equal(await joining, false)
  assert.equal(state.currentRoom.room_id, 1)
  assert.equal(state.members.length, 1)
  assert.equal(state.roomBusy, false)
  assert.equal(state.roomPollDue, 0)
})

test('local join failure retains the accepted target and clears stale room data', async () => {
  resetRoom(room())
  state.assignedIp = member.assigned_ip
  state.joined = true
  state.members = [member]
  state.chatUnread = 3
  state.memberLatency = { node: { reachable: true, latency: 1 } }
  const calls = []
  globalThis.fetch = async path => {
    calls.push(path)
    return path === '/api/room/join' ? response(room(2)) : response(null, 503)
  }
  const joining = joinRoomFlow(2)
  settleConfirm(true)
  assert.equal(await joining, true)
  assert.deepEqual(calls, ['/api/room/join', '/api/zt/join'])
  assert.equal(state.currentRoom.room_id, 2)
  assert.equal(state.joined, false)
  assert.equal(state.assignedIp, '')
  assert.equal(state.members.length, 0)
  assert.equal(state.chatUnread, 0)
  assert.deepEqual(Object.keys(state.memberLatency), [])
  assert.match(state.roomError, /本机网络连接未完成/)
})

test('concurrent join clicks issue only one membership request', async () => {
  let complete
  let requests = 0
  globalThis.fetch = path => {
    if (path === '/api/room/join') {
      requests++
      return new Promise(resolve => { complete = resolve })
    }
    return Promise.resolve(response({ joined: true }))
  }
  const joining = joinRoomFlow(1)
  await Promise.resolve()
  assert.equal(await joinRoomFlow(2), false)
  complete(response(room()))
  assert.equal(await joining, true)
  assert.equal(requests, 1)
})

test('network readiness tracks the current room and clears a stale IP after disconnect', () => {
  resetRoom(room())
  state.health = { node_ready: true, node_id: 'node', networks: [
    { network_id: 'unrelated', ready: true, ips: ['10.147.9.1/24'] },
    { network_id: 'net-1', ready: true, ips: ['10.147.1.1/24'] },
  ] }
  syncRoomNetwork()
  assert.equal(state.assignedIp, '10.147.1.1')
  assert.equal(state.joined, true)
  state.health.networks[1].ready = false
  syncRoomNetwork()
  assert.equal(state.joined, false)
  assert.equal(state.assignedIp, '')
  state.health.networks[1].ready = true
  state.currentRoom.authorized = false
  syncRoomNetwork()
  assert.equal(state.joined, false)
  state.health = null
  syncRoomNetwork()
  assert.equal(state.assignedIp, '')
})

test('room lookup failure is reported as unavailable rather than nonexistent', async () => {
  globalThis.fetch = async () => response(null, 503)
  await handleInvite(99)
  assert.match(state.toasts.at(-1).text, /无法获取房间信息/)
  assert.equal(state.passModalOpen, false)
})

test('HTTP authentication errors preserve their status', async () => {
  globalThis.fetch = async () => response(null, 401)
  await assert.rejects(api('/api/me'), error => error instanceof ApiError && error.status === 401)
})

test('stalled requests abort and release callers with an explicit timeout', async () => {
  globalThis.fetch = (_path, { signal }) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
  })
  const result = api('/api/rooms', undefined, 100)
  const rejected = assert.rejects(result, error => error instanceof ApiError && error.status === 408 && /超时/.test(error.message))
  mock.timers.tick(100)
  await rejected
})
