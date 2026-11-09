export interface Member {
  user_id: number
  name: string
  zt_node_id: string
  assigned_ip: string
  is_owner?: boolean
}

export interface Room {
  id: number
  name: string
  game: string
  network_id: string
  user_count: number
  password_required: boolean
  owner_id?: number
}

export interface ProbeResult {
  state: string
  state_text: string
  reachable: boolean
  latency: number
  tunnel?: number
  icmp?: number
  tcp?: number
  via?: string
  steps?: string[]
}

export interface NetState {
  network_id: string
  status: string
  device: string
  ips: string[]
  ready: boolean
  reason?: string
  steps?: string[]
}

export interface Issue {
  code: string
  text: string
  steps: string[]
  user_text?: string
  user_steps?: string[]
}

export interface HealthReport {
  node_ready: boolean
  node_id: string
  networks: NetState[]
  planet_ok: boolean
  planet_latency: number
  issues: Issue[]
  recovering: boolean
  recover_note?: string
}

export interface ChatMessage {
  id: string
  kind: string
  text: string
  ts: number
  name: string
}

export interface ClientConfig {
  server: string
  listen: string
  logged_in: boolean
}

export interface MeInfo {
  id: number
  name: string
  zt_node_id: string
  room_id?: number
  room_name?: string
}

export interface JoinStatus {
  joined: boolean
  room_id: number
  name?: string
  network_id: string
  authorized: boolean
  ip?: string
}

export interface ZtStatus {
  node_id: string
  networks: Array<{
    network_id: string
    status: string
    device: string
    ips: string[]
  }>
}
