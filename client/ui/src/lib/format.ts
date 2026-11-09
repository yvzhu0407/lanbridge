import type { ProbeResult } from '../types'

export interface BadgeStyle {
  label: string
  colorClass: 'ok' | 'warn' | 'err' | 'faint'
  tooltip?: string
}

export function formatMemberStatus(probe?: ProbeResult | null, isMe?: boolean): BadgeStyle {
  if (isMe) {
    return {
      label: '0 ms',
      colorClass: 'ok',
      tooltip: '本机连接',
    }
  }

  if (!probe) {
    return {
      label: '正在连接…',
      colorClass: 'warn',
      tooltip: '正在探测网络连通性',
    }
  }

  // 映射规则 (对应 5.3 状态徽标映射)
  const st = probe.state_text || ''

  if (probe.reachable && probe.latency >= 0) {
    const via = probe.via ? ` (经 ${probe.via.toUpperCase()})` : ''
    let color: 'ok' | 'warn' | 'faint' = 'ok'
    if (probe.latency > 80) color = 'warn'
    return {
      label: `${probe.latency} ms`,
      colorClass: color,
      tooltip: `真实数据面往返 ${probe.latency} ms${via}`,
    }
  }

  if (st === '隧道通·数据不通') {
    return {
      label: '对方网络异常',
      colorClass: 'faint',
      tooltip: 'ZeroTier 隧道已建立，但虚拟网卡数据不通。故障在对方机器上：让对方以管理员重启 LANBridge',
    }
  }

  if (st === '链路协商中' || probe.state === 'wait_handshake') {
    return {
      label: '正在连接…',
      colorClass: 'warn',
      tooltip: 'ZeroTier 正在协商直连路径，刚进房间时正常，稍等片刻会自动就绪',
    }
  }

  if (st === '本机网络未就绪' || probe.state === 'local_down') {
    return {
      label: '你这台没连上',
      colorClass: 'err',
      tooltip: '本机虚拟网卡尚未就绪或未分配到 IP',
    }
  }

  // 其余链路未建立 / no_tunnel
  return {
    label: '连不上',
    colorClass: 'faint',
    tooltip: probe.steps && probe.steps.length ? probe.steps.join('；') : '未能与对方建立链路，请打开右上角「诊断」查看详情',
  }
}

export function copyToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text))
  }
  return Promise.resolve(fallbackCopy(text))
}

function fallbackCopy(text: string): boolean {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}
