// Copy for the administrator's security centre: device trust, the evidence the
// trust engine does and does not use, the security journal, and the list of
// link boundaries.
//
// Kept out of src/i18n/locales/* and loaded with the lazily loaded panel, so
// none of it is in the startup bundle.
//
// TWO LANGUAGES, THE REST FALL BACK TO ENGLISH -- the rule of
// moduleSecurityCardLabels.ts beside it, for the same reason: these sentences
// say what is enforced, what is only recorded and what nothing can attest. A
// machine-guessed rendering of "monitor mode: recorded only, nothing is
// blocked" is worse than the English it replaces. Traditional Chinese reads the
// simplified text.

export type SecurityCentreLabels = {
 issuerAdminNote: string
  title: string
  subtitle: string
  refresh: string
  loading: string
  unknown: string
  none: string
  // Whole-panel states
  adminOnly: string
  offline: string
  unavailable: string
  sessionExpired: string
  failed: string
  invalidResponse: string
  // Enforcement
  enforcement: string
  enforcementModes: Record<string, string>
  monitorMode: string
  monitorNote: string
  enforcementUnknownNote: string
  // Devices
  devicesTitle: string
  devicesEmpty: string
  states: Record<string, string>
  stateUnknown: string
  recordedState: (state: string) => string
  reason: string
  noReason: string
  remaining: (time: string) => string
  day: string
  hour: string
  minute: string
  underMinute: string
  allowedTitle: string
  actions: Record<string, string>
  allowed: string
  notAllowed: string
  notAllowedMonitor: string
  allowedUnknown: string
  recordDamaged: string
  otpLimited: string
  manualHold: string
  counters: string
  simChanges: string
  identityChanges: string
  integrityFailures: string
  riskScore: string
  riskScoreNote: string
  updatedAt: string
  // Administrator actions
  adminActions: Record<string, string>
  adminActionTitles: Record<string, (device: string) => string>
  adminActionExplain: Record<string, string[]>
  adminActionMonitorNote: string
  adminActionDone: Record<string, string>
  reasonLabel: string
  reasonPlaceholder: string
  reasonRequired: string
  reasonTooLong: string
  reasonCount: (used: number, limit: number) => string
  confirm: string
  cancel: string
  working: string
  // Evidence
  evidenceTitle: string
  evidenceNote: string
  evidenceInputs: Record<string, string>
  evidenceStates: Record<string, string>
  evidenceReasons: Record<string, string>
  // Hardware evidence nothing provides
  hardwareTitle: string
  hardwareNote: string
  hardwareUnsupported: string
  hardwareItems: Record<string, string>
  hardwareReasons: Record<string, string>
  // Journal
  journalTitle: string
  journalStates: Record<string, string>
  journalUnknownNote: string
  journalDamagedAt: (seq: number) => string
  journalProblem: string
  journalEvents: string
  journalHead: string
  journalResealedFrom: (seq: number) => string
  journalVerifiedAt: string
  journalVerify: string
  journalVerifying: string
  journalVerifyDone: string
  journalReseal: string
  journalResealTitle: string
  journalResealExplain: string[]
  journalResealConfirm: string
  journalResealDone: string
  // Journal viewer
  viewerTitle: string
  viewerOpen: string
  viewerEmpty: string
  viewerFilter: string
  viewerFilterAll: string
  viewerOlder: string
  viewerNewest: string
  viewerMayBeIncomplete: string
  viewerNoMore: string
  viewerUnverified: string
  eventKinds: Record<string, string>
  eventCause: string
  eventSource: string
  eventResult: string
  eventDevice: string
  eventGateway: string
  eventDetails: string
  eventSeq: string
  eventHash: string
  eventPrevHash: string
  // Link boundaries
  boundariesTitle: string
  boundariesNote: string
  boundariesEmpty: string
  boundariesPending: string
  boundaryNames: Record<string, string>
  boundaryEvidence: Record<string, string>
  boundaryEvidenceNotes: Record<string, string>
  evidence: string
  protection: string
  postQuantum: string
  tristate: Record<string, string>
  requested: string
  negotiated: string
  notStated: string
  shortfall: string
  boundaryDevice: string
  proxyHopNote: string
}

const english: SecurityCentreLabels = {
 issuerAdminNote: 'Approval needs the issuer-signed device trust administrator machine grant. Give a reason; the action is recorded in the security journal.',
  title: 'Security centre',
  subtitle: 'Device trust, the security journal and what protects each link',
  refresh: 'Read again',
  loading: 'Reading…',
  unknown: 'Unknown',
  none: 'None',
  adminOnly: 'Administrator required.',
  offline: 'The gateway could not be reached. Nothing below is current.',
  unavailable: 'Trust records are not available on this gateway.',
  sessionExpired: 'Your session expired. Reload the page and sign in again.',
  failed: 'The operation failed',
  invalidResponse: 'Invalid response from the NAS',
  enforcement: 'Enforcement',
  enforcementModes: { enforce: 'Enforced', monitor: 'Monitor only', unknown: 'Not reported' },
  monitorMode: 'Monitor mode: recorded only, nothing is blocked.',
  monitorNote:
    'The states below are evaluated and written to the journal, but no call, SMS or code is refused because of them. This is not protection.',
  enforcementUnknownNote:
    'The gateway did not say whether these states are enforced, so nothing below should be read as blocking anything.',
  devicesTitle: 'Device trust',
  devicesEmpty: 'No device has a trust record.',
  states: {
    UNBOUND: 'No binding',
    OBSERVATION: 'Observation period',
    TRUSTED: 'Trusted',
    RESTRICTED: 'Restricted',
    QUARANTINE: 'Quarantined'
  },
  stateUnknown: 'Unknown state',
  recordedState: state => `Recorded as: ${state}`,
  reason: 'Why',
  noReason: 'No reason recorded.',
  remaining: time => `${time} of observation left`,
  day: 'd ',
  hour: 'h ',
  minute: 'min',
  underMinute: 'under 1 min',
  allowedTitle: 'What this state allows',
  actions: {
    receive_call: 'Receive calls',
    receive_sms: 'Receive SMS',
    dial: 'Place calls',
    send_sms: 'Send SMS',
    forward_otp: 'Forward codes',
    update: 'Agent update',
    diagnose: 'Diagnostics'
  },
  allowed: 'allowed',
  notAllowed: 'not allowed',
  notAllowedMonitor: 'would be refused (not blocked)',
  allowedUnknown: 'unknown',
  recordDamaged:
    'This trust record failed verification, so the device is held as restricted. Restoring it rebuilds the record.',
  otpLimited: 'Code forwarding is limited: an unusual number of codes arrived in 24 hours.',
  manualHold: 'Set by an administrator.',
  counters: 'Recorded changes',
  simChanges: 'SIM changes',
  identityChanges: 'Identity changes',
  integrityFailures: 'Integrity failures',
  riskScore: 'Risk score',
  riskScoreNote: 'A counter the observation window is derived from, not a risk level.',
  updatedAt: 'Record updated',
  adminActions: { restrict: 'Restrict…', quarantine: 'Quarantine…', restore: 'Restore…' },
  adminActionTitles: {
    restrict: device => `Restrict ${device}?`,
    quarantine: device => `Quarantine ${device}?`,
    restore: device => `Restore ${device}?`
  },
  adminActionExplain: {
    restrict: [
      'Outgoing calls and SMS through this device stop. It keeps receiving calls and SMS.',
      'It stays restricted until an administrator restores it.'
    ],
    quarantine: [
      'The device is revoked for use: no calls, no SMS, no code forwarding in either direction.',
      'Only agent updates and diagnostics remain. It stays quarantined until an administrator restores it.'
    ],
    restore: [
      'The restriction is lifted and the device enters a new OBSERVATION period. It does not return straight to trusted.',
      'During observation it receives calls and SMS; outgoing use starts again only when the period completes without a change.'
    ]
  },
  adminActionMonitorNote:
    'This gateway is in monitor mode: the new state is recorded, and nothing is blocked because of it.',
  adminActionDone: {
    restrict: 'The device was restricted.',
    quarantine: 'The device was quarantined.',
    restore: 'The device was restored into an observation period.'
  },
  reasonLabel: 'Reason (required, written to the journal)',
  reasonPlaceholder: 'Why are you doing this?',
  reasonRequired: 'A reason is required.',
  reasonTooLong: 'The reason is too long (200 bytes at most, about 66 Chinese characters).',
  reasonCount: (used, limit) => `${used} / ${limit} bytes`,
  confirm: 'Confirm',
  cancel: 'Cancel',
  working: 'Working…',
  evidenceTitle: 'What trust decisions are based on',
  evidenceNote: 'An input that is not available is listed as such; it is never counted as "fine".',
  evidenceInputs: {
    device_identity: 'Device identity key',
    sim_change: 'SIM change',
    modem_change: 'Modem change',
    otp_volume: 'Verification-code volume',
    location: 'Location',
    node_local: 'The device\'s own record'
  },
  evidenceStates: {
    observed: 'Observed',
    unsupported: 'Not supported',
    not_used: 'Not used',
    unknown: 'Unknown'
  },
  evidenceReasons: {
    device_identity: 'The Ed25519 key pinned at pairing, checked on every poll.',
    sim_change: 'A keyed hash of the SIM identity, compared on every poll. The identifier itself is never stored.',
    modem_change: 'The Node protocol carries no modem serial, so a swapped modem cannot be detected.',
    otp_volume: 'Verification codes received in 24 hours, counted from stored messages.',
    location: 'Never used for a trust decision.',
    node_local: 'The device keeps no trust record of its own yet; everything here is the gateway\'s view.',
    unknown: 'The gateway did not report this input.'
  },
  hardwareTitle: 'Hardware and boot evidence',
  hardwareNote: 'Nothing in the product attests these. They stay unsupported until something does.',
  hardwareUnsupported: 'Not supported',
  hardwareItems: {
    secure_boot: 'Secure boot',
    tpm: 'TPM / TEE attestation',
    system_signature: 'Kernel and rootfs signature',
    geo_risk: 'Location-based risk'
  },
  hardwareReasons: {
    secure_boot: 'The UFI003 gives the gateway no evidence of its boot chain.',
    tpm: 'The UFI003 has no TPM or TEE evidence the gateway can check.',
    system_signature: 'Nothing verifies the kernel or root file system on the device.',
    geo_risk: 'No location is collected, and none is used for trust.'
  },
  journalTitle: 'Security journal',
  journalStates: { ok: 'Verified', damaged: 'Damaged', unknown: 'Not verified yet' },
  journalUnknownNote: 'No verification result was reported, so nothing is claimed about the journal.',
  journalDamagedAt: seq => `First problem at entry #${seq}`,
  journalProblem: 'Problem',
  journalEvents: 'Entries verified',
  journalHead: 'Last entry verified',
  journalResealedFrom: seq =>
    `Resealed from entry #${seq}: earlier entries are kept but no longer vouched for.`,
  journalVerifiedAt: 'Checked',
  journalVerify: 'Verify now',
  journalVerifying: 'Verifying…',
  journalVerifyDone: 'The journal was re-hashed; the result is shown below.',
  journalReseal: 'Reseal the journal…',
  journalResealTitle: 'Reseal the damaged journal?',
  journalResealExplain: [
    'Nothing is deleted or rewritten.',
    'Earlier entries stay readable but are no longer vouched for: verification restarts at the reseal entry.',
    'Every device returns to an observation period, because the history that justified anything else can no longer be checked.'
  ],
  journalResealConfirm: 'Reseal',
  journalResealDone: 'The journal was resealed. Every device is back in observation.',
  viewerTitle: 'Journal entries',
  viewerOpen: 'Show entries',
  viewerEmpty: 'No entries.',
  viewerFilter: 'Device',
  viewerFilterAll: 'All devices',
  viewerOlder: 'Load older entries',
  viewerNewest: 'Back to the newest',
  viewerMayBeIncomplete:
    'This gateway did not answer newest-first, so these are the entries it chose to send. The list may be incomplete and newer entries may exist.',
  viewerNoMore: 'This is the beginning of the journal.',
  viewerUnverified: 'not vouched for (before the reseal)',
  eventKinds: {
    PAIRING_BOUND: 'Device adopted',
    TRUST_GRANDFATHERED: 'Trusted from before trust records',
    SIM_CHANGED: 'SIM changed',
    PAIRING_CHANGED: 'Identity key changed',
    INTEGRITY_FAILURE: 'Integrity failure',
    OTP_LIMIT_TRIGGERED: 'Code limit triggered',
    OTP_LIMIT_CLEARED: 'Code limit cleared',
    TRUST_DOWNGRADE: 'Trust lowered',
    TRUST_UPGRADE: 'Trust raised',
    MANUAL_RESTRICT: 'Restricted by an administrator',
    MANUAL_QUARANTINE: 'Quarantined by an administrator',
    MANUAL_RESTORE: 'Restored by an administrator',
    ACTION_REFUSED: 'Action refused',
    AUDIT_CHAIN_RESEALED: 'Journal resealed',
    TRUST_RECORD_DAMAGED: 'Trust record damaged',
    IDENTITY_VERIFICATION: 'Identity verification',
    AGENT_UPDATE: 'Agent update'
  },
  eventCause: 'Cause',
  eventSource: 'Source',
  eventResult: 'Result',
  eventDevice: 'Device',
  eventGateway: 'Gateway',
  eventDetails: 'Chain details',
  eventSeq: 'Entry',
  eventHash: 'Hash',
  eventPrevHash: 'Previous hash',
  boundariesTitle: 'Link boundaries',
  boundariesNote:
    'A request crosses several links, each protected by something different. Each is listed with how the gateway knows what it reports.',
  boundariesEmpty: 'This gateway did not report its link boundaries.',
  boundariesPending: 'The gateway did not answer; nothing is shown.',
  boundaryNames: {
    browser_gateway: 'Browser → gateway',
    tailscale: 'Tailscale tunnel',
    tailscale_gateway: 'Tailscale → gateway (local hop)',
    call_media: 'Browser call audio',
    gateway_node: 'Gateway → device',
    node_identity: 'Device identity',
    kcp: 'KCP transport',
    gateway_update: 'Gateway updates',
    node_update: 'Device agent updates'
  },
  boundaryEvidence: {
    observed: 'Observed',
    reported: 'Reported',
    protocol: 'Protocol property',
    not_used: 'Not used',
    unknown: 'Unknown'
  },
  boundaryEvidenceNotes: {
    observed: 'The gateway saw it itself.',
    reported: 'The other end says so; the gateway did not see it.',
    protocol: 'A fixed property of the protocol, not a measurement.',
    not_used: 'This link is not part of this deployment or request.',
    unknown: 'The gateway has no way to tell.'
  },
  evidence: 'Evidence',
  protection: 'Protection',
  postQuantum: 'Post-quantum',
  tristate: { yes: 'Yes', no: 'No', unknown: 'Unknown' },
  requested: 'Requested',
  negotiated: 'Negotiated',
  notStated: 'Not stated',
  shortfall: 'What was negotiated does not meet what was requested.',
  boundaryDevice: 'Device',
  proxyHopNote:
    'This request arrived through Tailscale Serve. What the gateway observed is the hop from Tailscale to the gateway, not the browser\'s own connection.'
}

const chinese: SecurityCentreLabels = {
 issuerAdminNote: '解除观察期、受限或隔离需要签发控制台的“设备限制管理员解除”机器授权；操作需填写原因并记入安全日志。',
  title: '安全中心',
  subtitle: '设备信任、安全日志，以及每一段链路靠什么保护',
  refresh: '重新读取',
  loading: '读取中…',
  unknown: '未知',
  none: '无',
  adminOnly: '需要管理员',
  offline: '连接不上网关。下面的内容都不是当前状态。',
  unavailable: '这台网关没有信任记录功能。',
  sessionExpired: '登录已过期，请刷新页面重新登录。',
  failed: '操作失败',
  invalidResponse: 'NAS 返回的数据格式无效',
  enforcement: '执行方式',
  enforcementModes: { enforce: '强制执行', monitor: '仅监控', unknown: '网关未上报' },
  monitorMode: '监控模式：仅记录，不拦截',
  monitorNote: '下面的状态会被评估并写入日志，但不会因此拒绝任何通话、短信或验证码。这不是保护。',
  enforcementUnknownNote: '网关没有说明这些状态是否在强制执行，所以不要把下面任何一项理解为“已拦截”。',
  devicesTitle: '设备信任',
  devicesEmpty: '没有任何设备有信任记录。',
  states: {
    UNBOUND: '未绑定',
    OBSERVATION: '观察期',
    TRUSTED: '已信任',
    RESTRICTED: '受限',
    QUARANTINE: '已隔离'
  },
  stateUnknown: '未知状态',
  recordedState: state => `记录中的状态：${state}`,
  reason: '原因',
  noReason: '没有记录原因。',
  remaining: time => `观察期剩余 ${time}`,
  day: ' 天 ',
  hour: ' 小时 ',
  minute: ' 分钟',
  underMinute: '不到 1 分钟',
  allowedTitle: '当前状态允许什么',
  actions: {
    receive_call: '接听来电',
    receive_sms: '接收短信',
    dial: '拨出电话',
    send_sms: '发送短信',
    forward_otp: '转发验证码',
    update: '设备软件更新',
    diagnose: '诊断'
  },
  allowed: '允许',
  notAllowed: '不允许',
  notAllowedMonitor: '本应拒绝（未拦截）',
  allowedUnknown: '未知',
  recordDamaged: '这条信任记录没有通过校验，所以设备按“受限”处理。恢复操作会重建这条记录。',
  otpLimited: '验证码转发已受限：24 小时内收到的验证码数量异常。',
  manualHold: '由管理员设置。',
  counters: '记录到的变化',
  simChanges: '换卡次数',
  identityChanges: '身份变化次数',
  integrityFailures: '完整性失败次数',
  riskScore: '风险计数',
  riskScoreNote: '用来推算观察期长度的计数，不是风险等级。',
  updatedAt: '记录更新时间',
  adminActions: { restrict: '限制…', quarantine: '隔离…', restore: '恢复…' },
  adminActionTitles: {
    restrict: device => `限制 ${device}？`,
    quarantine: device => `隔离 ${device}？`,
    restore: device => `恢复 ${device}？`
  },
  adminActionExplain: {
    restrict: [
      '不再通过这台设备拨出电话或发送短信；它仍然可以接听来电和接收短信。',
      '在管理员恢复之前，它会一直保持受限。'
    ],
    quarantine: [
      '这台设备被停用：不接也不打电话，不收发短信，不转发验证码。',
      '只保留设备软件更新和诊断。在管理员恢复之前，它会一直保持隔离。'
    ],
    restore: [
      '解除限制后，设备进入新的观察期（OBSERVATION），不会直接回到“已信任”。',
      '观察期内它可以接听来电和接收短信；只有观察期内没有再发生变化，外呼和发短信才会恢复。'
    ]
  },
  adminActionMonitorNote: '这台网关处于监控模式：新状态会被记录，但不会因此拦截任何操作。',
  adminActionDone: {
    restrict: '已限制这台设备。',
    quarantine: '已隔离这台设备。',
    restore: '已恢复这台设备，它进入了观察期。'
  },
  reasonLabel: '原因（必填，会写入日志）',
  reasonPlaceholder: '为什么要这样做？',
  reasonRequired: '必须填写原因。',
  reasonTooLong: '原因太长（最多 200 字节，约 66 个汉字）。',
  reasonCount: (used, limit) => `${used} / ${limit} 字节`,
  confirm: '确认',
  cancel: '取消',
  working: '处理中…',
  evidenceTitle: '信任判断依据什么',
  evidenceNote: '拿不到的依据会标明拿不到，绝不会被当成“正常”。',
  evidenceInputs: {
    device_identity: '设备身份密钥',
    sim_change: '换卡',
    modem_change: '更换模组',
    otp_volume: '验证码数量',
    location: '地理位置',
    node_local: '设备自己的记录'
  },
  evidenceStates: {
    observed: '已观测',
    unsupported: '不支持',
    not_used: '不使用',
    unknown: '未知'
  },
  evidenceReasons: {
    device_identity: '配对时固定的 Ed25519 密钥，每次轮询都会核对。',
    sim_change: 'SIM 身份的带密钥哈希，每次轮询都会比对；标识本身从不保存。',
    modem_change: 'Node 协议不携带模组序列号，所以发现不了模组被更换。',
    otp_volume: '24 小时内收到的验证码数量，从已保存的短信中统计。',
    location: '从不用于信任判断。',
    node_local: '设备目前不保存自己的信任记录；这里看到的都是网关一侧的判断。',
    unknown: '网关没有上报这一项。'
  },
  hardwareTitle: '硬件与启动证据',
  hardwareNote: '产品里没有任何东西能证明这几项。在有证据之前，它们一直是“不支持”。',
  hardwareUnsupported: '不支持',
  hardwareItems: {
    secure_boot: '安全启动',
    tpm: 'TPM / TEE 证明',
    system_signature: '内核与根文件系统签名',
    geo_risk: '基于地理位置的风险'
  },
  hardwareReasons: {
    secure_boot: 'UFI003 无法向网关提供启动链证据。',
    tpm: 'UFI003 无 TPM/TEE 证据。',
    system_signature: '设备上没有任何东西在校验内核或根文件系统。',
    geo_risk: '不采集地理位置，也不用它做信任判断。'
  },
  journalTitle: '安全日志',
  journalStates: { ok: '校验通过', damaged: '已损坏', unknown: '尚未校验' },
  journalUnknownNote: '网关没有上报校验结果，所以对日志不下任何结论。',
  journalDamagedAt: seq => `第一处问题在第 #${seq} 条`,
  journalProblem: '问题',
  journalEvents: '已校验条数',
  journalHead: '校验到的最后一条',
  journalResealedFrom: seq => `已从第 #${seq} 条重新封存：更早的记录仍然保留，但不再为它们担保。`,
  journalVerifiedAt: '校验时间',
  journalVerify: '立即校验',
  journalVerifying: '校验中…',
  journalVerifyDone: '已重新计算整条日志的哈希，结果见下。',
  journalReseal: '重新封存日志…',
  journalResealTitle: '重新封存已损坏的日志？',
  journalResealExplain: [
    '不会删除或改写任何记录。',
    '更早的记录仍然可以查看，但不再为它们担保：校验从这条封存记录重新开始。',
    '所有设备都会回到观察期，因为能证明它们可以更宽松的历史已经无法核对。'
  ],
  journalResealConfirm: '重新封存',
  journalResealDone: '日志已重新封存。所有设备都回到了观察期。',
  viewerTitle: '日志记录',
  viewerOpen: '查看记录',
  viewerEmpty: '没有记录。',
  viewerFilter: '设备',
  viewerFilterAll: '全部设备',
  viewerOlder: '加载更早的记录',
  viewerNewest: '回到最新',
  viewerMayBeIncomplete: '此网关没有按“最新在前”作答，下面是它返回的记录。列表可能不完整，可能还有更新的记录。',
  viewerNoMore: '已经到日志的开头。',
  viewerUnverified: '不担保（在重新封存之前）',
  eventKinds: {
    PAIRING_BOUND: '设备已纳管',
    TRUST_GRANDFATHERED: '信任记录启用前已配对',
    SIM_CHANGED: '换卡',
    PAIRING_CHANGED: '身份密钥变化',
    INTEGRITY_FAILURE: '完整性失败',
    OTP_LIMIT_TRIGGERED: '触发验证码限制',
    OTP_LIMIT_CLEARED: '验证码限制解除',
    TRUST_DOWNGRADE: '信任降级',
    TRUST_UPGRADE: '信任升级',
    MANUAL_RESTRICT: '管理员限制',
    MANUAL_QUARANTINE: '管理员隔离',
    MANUAL_RESTORE: '管理员恢复',
    ACTION_REFUSED: '操作被拒绝',
    AUDIT_CHAIN_RESEALED: '日志重新封存',
    TRUST_RECORD_DAMAGED: '信任记录损坏',
    IDENTITY_VERIFICATION: '身份校验',
    AGENT_UPDATE: '设备软件更新'
  },
  eventCause: '起因',
  eventSource: '来源',
  eventResult: '结果',
  eventDevice: '设备',
  eventGateway: '网关',
  eventDetails: '链式校验信息',
  eventSeq: '序号',
  eventHash: '哈希',
  eventPrevHash: '上一条哈希',
  boundariesTitle: '链路边界',
  boundariesNote: '一次请求要经过好几段链路，每一段的保护方式都不一样。这里逐段列出，并说明网关是怎么知道的。',
  boundariesEmpty: '这台网关没有上报链路边界。',
  boundariesPending: '网关没有应答，这里不显示任何内容。',
  boundaryNames: {
    browser_gateway: '浏览器 → 网关',
    tailscale: 'Tailscale 隧道',
    tailscale_gateway: 'Tailscale → 网关（本机一跳）',
    call_media: '浏览器通话音频',
    gateway_node: '网关 → 设备',
    node_identity: '设备身份',
    kcp: 'KCP 传输',
    gateway_update: '网关更新',
    node_update: '设备软件更新'
  },
  boundaryEvidence: {
    observed: '已观测',
    reported: '对端上报',
    protocol: '协议固有',
    not_used: '未使用',
    unknown: '未知'
  },
  boundaryEvidenceNotes: {
    observed: '网关自己看到的。',
    reported: '对端这样说，网关自己没有看到。',
    protocol: '协议的固定属性，不是测量结果。',
    not_used: '这段链路不在当前部署或这次请求里。',
    unknown: '网关无从得知。'
  },
  evidence: '依据',
  protection: '保护方式',
  postQuantum: '后量子',
  tristate: { yes: '是', no: '否', unknown: '未知' },
  requested: '要求',
  negotiated: '实际协商',
  notStated: '未说明',
  shortfall: '实际协商的结果没有达到要求。',
  boundaryDevice: '设备',
  proxyHopNote: '这次请求经由 Tailscale Serve 到达。网关观测到的是 Tailscale 到网关的这一跳，不是浏览器自己的连接。'
}

export function securityCentreLabels(locale: string): SecurityCentreLabels {
  return locale.startsWith('zh') ? chinese : english
}

english.adminActions.approve = 'Administrator approval'
english.adminActionTitles.approve = device => `Approve ${device} for outgoing use?`
english.adminActionExplain.approve = ['The issuer-signed machine grant authorizes this administrator action.', 'This device becomes trusted immediately. Give a reason; the action is recorded in the security journal. Later security failures can restrict it again.']
english.adminActionDone.approve = 'The device was approved and its policy will be sent to it.'
chinese.adminActions.approve = '管理员解除限制'
chinese.adminActionTitles.approve = device => `解除 ${device} 的设备使用限制？`
chinese.adminActionExplain.approve = ['此操作需要签发控制台签名的机器授权。', '设备会立即变为已信任。请填写解除原因，操作会记入安全日志；后续安全异常仍可再次触发限制。']
chinese.adminActionDone.approve = '已解除设备使用限制，正在向棒子同步策略。'
