// Copy for the dashboard's per-device card groups and the update centre's
// Node list.
//
// Kept out of src/i18n/locales/* and loaded with the lazily loaded dashboard,
// so none of it is in the startup bundle.
//
// TWO LANGUAGES, THE REST FALL BACK TO ENGLISH -- the rule of
// securityCentreLabels.ts and softwareUpdateCardLabels.ts beside it, for the
// same reason: these sentences say what a device did not report and why a
// figure is withheld. A machine-guessed rendering of "the gateway serves one
// set of module metrics, so with two modules attached it cannot say whose they
// are" is worse than the English it replaces. Traditional Chinese reads the
// simplified text.

import type { DeviceMetricsGap } from '../api/nodeFleet'

export type DeviceGroupLabels = {
  /** A reading the device did not send. Never drawn as a number. */
  unknown: string
  /** A card, or a row, this kind of device has no source for. */
  notProvided: string
  gaps: Record<Exclude<DeviceMetricsGap, ''>, string>
  // Group headings
  hostDji: string
  hostModem: string
  nodeTitle: (model: string) => string
  globalTitle: string
  globalTag: string
  deviceId: string
  // Card-level reasons
  metricsFrozen: string
  netHostInterface: string
  noDataInterface: string
  noCell: string
  cellAdminOnly: string
  hostOnlyActions: string
  imeiNotProvided: string
  portsNotProvided: string
  noSim: string
  // Device trust row of the secure-transport card
  trustTitle: string
  trustStates: Record<string, string>
  trustNoRecord: string
  trustMonitor: string
  trustAdminOnly: string
  trustHostNotApplicable: string
  trustNotApplicable: string
  // Update centre: Node agents
  agentsTitle: string
  agentsSubtitle: string
  agentsLoading: string
  agentsEmpty: string
  agentsAdminOnly: string
  agentsUnavailable: string
  agentsRetry: string
  agentVersion: string
  agentVersionUnknown: string
  agentVersionLastSeen: string
  agentOnline: string
  agentOffline: string
  agentLastSeen: string
  agentNeverSeen: string
  agentOpen: (name: string) => string
  agentOpenShort: string
}

const english: DeviceGroupLabels = {
  unknown: 'Unknown',
  notProvided: 'Not provided by this device',
  gaps: {
    loading: 'Reading this device…',
    not_reported:
      'The device reported no resource data (an older agent, or it has not sampled yet).',
    admin_only: 'Only an administrator may read this device’s resource data.',
    unavailable: 'The gateway’s device list is unavailable right now.',
    ambiguous_host:
      'The gateway serves one set of module metrics, so with more than one module attached it cannot say whose they are. Nothing is shown rather than a guess.'
  },
  hostDji: 'DJI 4G module',
  hostModem: '4G module',
  nodeTitle: model => `NasSimHub Node · ${model}`,
  globalTitle: 'NAS services',
  globalTag: 'NasSimHub',
  deviceId: 'Device ID',
  metricsFrozen: 'This device cannot be read right now; these are the last figures received.',
  netHostInterface: 'NAS USB',
  noDataInterface: 'The device reported no cellular data interface.',
  noCell: 'The device reported no serving cell.',
  cellAdminOnly: 'Cell ID and TAC locate the device and are shown to administrators only.',
  hostOnlyActions:
    'Repair and modem restart act on a module attached to the NAS; this device does not offer them here.',
  imeiNotProvided: 'The gateway does not read a Node’s modem IMEI.',
  portsNotProvided: 'A Node exposes no serial ports to the NAS.',
  noSim: 'No line: the device has no ready SIM.',
  trustTitle: 'Device trust',
  trustStates: {
    UNBOUND: 'No binding',
    OBSERVATION: 'Observation period',
    TRUSTED: 'Trusted',
    RESTRICTED: 'Restricted',
    QUARANTINE: 'Quarantined'
  },
  trustNoRecord: 'No trust record',
  trustMonitor: 'Monitor mode: recorded only, nothing is blocked.',
  trustAdminOnly: 'Only an administrator may read a device’s trust record.',
  trustHostNotApplicable:
    'The trust engine assesses NasSimHub Nodes. A module attached to the NAS has no trust record.',
  trustNotApplicable: 'Not applicable',
  agentsTitle: 'Node agents',
  agentsSubtitle: 'Paired NasSimHub Nodes, read from the gateway just now. Nothing here is remembered.',
  agentsLoading: 'Reading the device list…',
  agentsEmpty: 'No Node is paired with this NAS.',
  agentsAdminOnly: 'Only an administrator may read the device list.',
  agentsUnavailable: 'The device list is unavailable right now.',
  agentsRetry: 'Try again',
  agentVersion: 'Agent version',
  agentVersionUnknown: 'Not reported',
  agentVersionLastSeen: 'at last connection',
  agentOnline: 'Online',
  agentOffline: 'Offline',
  agentLastSeen: 'Last seen',
  agentNeverSeen: 'no record',
  agentOpen: name => `Open agent updates for ${name}`,
  agentOpenShort: 'Agent updates…'
}

const chinese: DeviceGroupLabels = {
  unknown: '未知',
  notProvided: '此设备未提供',
  gaps: {
    loading: '正在读取本设备数据…',
    not_reported: '设备没有上报资源数据（代理版本较旧，或尚未完成采样）。',
    admin_only: '只有管理员可以读取本设备的资源数据。',
    unavailable: '网关的设备列表暂时不可用。',
    ambiguous_host:
      '网关只提供一份模组指标，接入多个直连模组时无法确定它属于哪一个，因此不显示，也不猜测。'
  },
  hostDji: '大疆 4G 模组',
  hostModem: '4G 模组',
  nodeTitle: model => `NasSimHub Node · ${model}`,
  globalTitle: 'NAS 全局服务',
  globalTag: 'NasSimHub',
  deviceId: '设备 ID',
  metricsFrozen: '暂时读不到本设备的数据，下面是最后一次收到的数据。',
  netHostInterface: 'NAS USB',
  noDataInterface: '设备没有上报蜂窝数据网卡。',
  noCell: '设备没有上报服务小区。',
  cellAdminOnly: '小区 ID 与 TAC 可以定位设备，仅管理员可见。',
  hostOnlyActions: '一键修复与重启调制解调器只作用于 NAS 直连模组，本设备在这里不提供。',
  imeiNotProvided: '网关不读取 Node 调制解调器的 IMEI。',
  portsNotProvided: 'Node 不向 NAS 暴露串口。',
  noSim: '没有线路：设备里没有就绪的 SIM 卡。',
  trustTitle: '设备信任',
  trustStates: {
    UNBOUND: '未绑定',
    OBSERVATION: '观察期',
    TRUSTED: '已信任',
    RESTRICTED: '受限',
    QUARANTINE: '已隔离'
  },
  trustNoRecord: '没有信任记录',
  trustMonitor: '监控模式：仅记录，不拦截',
  trustAdminOnly: '只有管理员可以读取设备的信任记录。',
  trustHostNotApplicable: '信任引擎只评估 NasSimHub Node；NAS 直连模组没有信任记录。',
  trustNotApplicable: '不适用',
  agentsTitle: 'Node 代理',
  agentsSubtitle: '已配对的 NasSimHub Node，刚从网关读取。这里不保存任何历史结果。',
  agentsLoading: '正在读取设备列表…',
  agentsEmpty: '此 NAS 尚未配对任何 Node。',
  agentsAdminOnly: '只有管理员可以读取设备列表。',
  agentsUnavailable: '设备列表暂时不可用。',
  agentsRetry: '重试',
  agentVersion: '代理版本',
  agentVersionUnknown: '未上报',
  agentVersionLastSeen: '上次连接时',
  agentOnline: '在线',
  agentOffline: '离线',
  agentLastSeen: '上次在线',
  agentNeverSeen: '无记录',
  agentOpen: name => `打开 ${name} 的代理更新`,
  agentOpenShort: '代理更新…'
}

export function deviceGroupLabels(locale: string): DeviceGroupLabels {
  return locale.startsWith('zh') ? chinese : english
}
