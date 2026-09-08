// A writable VoLTE setting is not evidence of IMS registration.
export function voiceStatus(input: { registered: boolean; network: string; activeBearer?: string }) {
  if (!input.registered || !['4G', '5G'].includes(input.network)) return { state: 'hidden', hint: '' }
  if (input.activeBearer === 'volte') return { state: 'on', hint: '当前通话已确认使用 VoLTE' }
  return { state: 'unknown', hint: '设备未上报 IMS 注册状态；这不代表 VoLTE 已关闭，是否允许修改 VoLTE 设置与通话状态无关' }
}

export function chineseLineState(state = ''): string {
  return ({ registered: '已注册', connected: '已连接', connecting: '连接中', searching: '搜网中', enabled: '已启用', disabled: '已关闭', locked: 'SIM 已锁定', failed: '设备异常', initializing: '初始化中', enabling: '启用中', disabling: '关闭中', disconnecting: '断开中', unknown: '状态待确认' } as Record<string, string>)[state] || '状态待确认'
}
