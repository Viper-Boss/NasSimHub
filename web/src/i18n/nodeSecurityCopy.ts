// Copy for the per-node security block and the node agent-update panel.
//
// Lives beside simNodeInventory.ts and follows its rule: the SIM-nodes view is
// an administrator's page with long, exact sentences about what was verified
// and what was not, so it ships Chinese and English and every other locale
// reads the English. A machine-guessed rendering of "the gateway did not
// verify this release; the device will" is worse than the English it replaces.
//
// Loaded with the lazily loaded view, never from src/i18n/locales/*, so none
// of it is in the startup bundle.

export type NodeSecurityCopy = {
  title: string
  unknown: string
  notReported: string
  // Identity
  classicalIdentity: string
  classicalPinned: string
  pqIdentity: string
  outcomes: Record<string, string>
  pqUnavailable: (backend: string) => string
  pqNotReported: string
  noAttestation: string
  pqPin: string
  pinStates: Record<string, string>
  pinnedAt: string
  pqFingerprint: string
  policy: string
  policyEnforced: string
  policyReportOnly: string
  identityRefused: string
  identityRefusedNoReason: string
  identityNote: string
  // Link
  requestedLevel: string
  negotiatedGroup: string
  linkPostQuantum: string
  tristate: Record<string, string>
  linkNotObserved: string
  linkShortfall: (required: string) => string
  linkShortfallNoGroup: string
  nodeGroupDiffers: (group: string) => string
  // Trust
  trust: string
  trustStates: Record<string, string>
  trustUnknown: string
  trustRemaining: (time: string) => string
  trustReason: string
  outgoingBlocked: string
  outgoingWouldBlock: string
  outgoingBlockedUnknownMode: string
  monitorMode: string
  day: string
  hour: string
  minute: string
  underMinute: string
  // PQ pin reset
  resetOffer: string
  resetTitle: string
  resetExplain: string[]
  resetConfirm: string
  resetCancel: string
  resetting: string
  resetDone: string
  // Updates
  updates: {
    open: string
    title: string
    close: string
    refresh: string
    loading: string
    offline: string
    forbidden: string
    sessionExpired: string
    failed: string
    invalidResponse: string
    deviceSection: string
    notAnswering: string
    notAnsweringNote: string
    unsupported: string
    noDeviceStatus: string
    noReason: string
    currentVersion: string
    factoryVersion: string
    activeRelease: string
    factoryAgent: string
    state: string
    states: Record<string, string>
    targetVersion: string
    previousVersion: string
    transfer: string
    lastError: string
    restartReason: string
    restartReasons: Record<string, string>
    confirmDeadline: string
    signaturePolicy: string
    lastSignatures: string
    signatureOutcomes: Record<string, string>
    readAt: string
    pendingTitle: string
    pendingNote: string
    confirm: string
    rollback: string
    releases: string
    storeUnavailable: string
    gatewayNoKeys: string
    noReleases: string
    gatewayVerified: string
    gatewayUnverified: string
    dualSigned: string
    classicalOnly: string
    signaturesUnknown: string
    artifactReady: string
    artifactMissing: string
    version: string
    channel: string
    platform: string
    size: string
    releasedAt: string
    minCore: string
    notes: string
    details: string
    releaseId: string
    digest: string
    keyIds: string
    push: string
    pushing: string
    apply: string
    applying: string
    applyNeedsPush: string
    remove: string
    removeConfirm: string
    keep: string
    applyTitle: string
    applyExplain: string[]
    applyConfirm: string
    cancel: string
    outcomePushed: string
    outcomeApplied: string
    outcomeNotConfirmed: string
    outcomeConfirmed: string
    outcomeRolledBack: string
    outcomeRemoved: string
    importTitle: string
    importNote: string
    manifestFile: string
    artifactFile: string
    importManifest: string
    importArtifact: string
    importing: string
    uploading: (percent: number) => string
    manifestTooLarge: string
    artifactTooLarge: string
    artifactSizeMismatch: (expected: string) => string
    artifactFor: string
    chooseRelease: string
    manifestImported: string
    artifactImported: string
  }
}

const english: NodeSecurityCopy = {
  title: 'Security',
  unknown: 'Unknown',
  notReported: 'Not reported by this gateway',
  classicalIdentity: 'Device identity',
  classicalPinned: 'Ed25519, pinned at pairing',
  pqIdentity: 'Post-quantum identity',
  outcomes: {
    unknown: 'Not checked yet',
    dual: 'Dual identity verified',
    classical_only: 'Classical identity only',
    refused: 'Refused'
  },
  pqUnavailable: (backend) => `This gateway build cannot verify ML-DSA${backend ? ` (${backend})` : ''}; nothing is concluded`,
  pqNotReported: 'Not reported by this gateway; nothing is concluded',
  noAttestation: 'The device software does not support the identity proof exchange.',
  pqPin: 'Post-quantum key pin',
  pinStates: {
    none: 'No key recorded',
    pinned: 'Recorded and matching',
    changed: 'The device presents a different key',
    withdrawn: 'The device no longer presents a key',
    unknown: 'Unknown'
  },
  pinnedAt: 'Recorded',
  pqFingerprint: 'Post-quantum key fingerprint',
  policy: 'Identity policy',
  policyEnforced: 'enforced',
  policyReportOnly: 'reported only, not enforced',
  identityRefused: 'Identity check refused',
  identityRefusedNoReason: 'The gateway gave no reason.',
  identityNote: 'Identity note',
  requestedLevel: 'Requested level',
  negotiatedGroup: 'Negotiated key exchange',
  linkPostQuantum: 'Link is post-quantum',
  tristate: { yes: 'Yes', no: 'No', unknown: 'Unknown' },
  linkNotObserved: 'No session observed; nothing is claimed about the link.',
  linkShortfall: (required) => `The negotiated key exchange does not meet the requested level (requires ${required}).`,
  linkShortfallNoGroup: 'The negotiated key exchange does not meet the requested level.',
  nodeGroupDiffers: (group) => `The device reports a different group (${group}) than the gateway observed.`,
  trust: 'Trust',
  trustStates: {
    UNBOUND: 'No binding',
    OBSERVATION: 'Observation period',
    TRUSTED: 'Trusted',
    RESTRICTED: 'Restricted',
    QUARANTINE: 'Quarantined'
  },
  trustUnknown: 'No trust record',
  trustRemaining: (time) => `${time} of observation left`,
  trustReason: 'Why',
  outgoingBlocked: 'Outgoing calls and SMS are not placed through this device.',
  outgoingWouldBlock: 'In this state outgoing calls and SMS would not be placed through this device, but nothing is being blocked.',
  outgoingBlockedUnknownMode: 'This state does not allow outgoing calls or SMS; the gateway did not say whether that is enforced.',
  monitorMode: 'Monitor mode: recorded only, nothing is blocked.',
  day: 'd ',
  hour: 'h ',
  minute: 'min',
  underMinute: 'under 1 min',
  resetOffer: 'Accept the new post-quantum identity…',
  resetTitle: 'Accept the new post-quantum identity?',
  resetExplain: [
    'This makes the gateway forget the ML-DSA key it recorded for this device.',
    'The Ed25519 key pinned at pairing is not touched and keeps identifying the device.',
    'At the next check the gateway records whichever post-quantum key the device then proves it holds. Do this only if you changed the key on purpose (for example by reinstalling or updating the agent).'
  ],
  resetConfirm: 'Forget the recorded key',
  resetCancel: 'Cancel',
  resetting: 'Resetting…',
  resetDone: 'The recorded post-quantum key was forgotten. The next check records the key the device proves.',
  updates: {
    open: 'Agent update…',
    title: 'Agent update',
    close: 'Close',
    refresh: 'Read again',
    loading: 'Asking the device…',
    offline: 'The gateway could not be reached.',
    forbidden: 'Administrator required.',
    sessionExpired: 'Your session expired. Reload the page and sign in again.',
    failed: 'The operation failed',
    invalidResponse: 'Invalid response from the NAS',
    deviceSection: 'This device',
    notAnswering: 'The device did not answer',
    notAnsweringNote: 'No status is shown: the gateway keeps no last-known state, and an old one would not describe the device now.',
    unsupported: 'This device cannot be updated from here',
    noDeviceStatus: 'The gateway did not return this device\'s update state; nothing is known about it.',
    noReason: 'No reason was given.',
    currentVersion: 'Running version',
    factoryVersion: 'Factory version',
    activeRelease: 'Installed release',
    factoryAgent: 'None (factory agent running)',
    state: 'State',
    states: {
      IDLE: 'Idle',
      CHECKING: 'Checking',
      AVAILABLE: 'Offered',
      DOWNLOADING: 'Receiving',
      STAGED: 'Received',
      VALIDATING: 'Validating',
      READY: 'Validated, ready to apply',
      APPLYING: 'Applying',
      PENDING_CONFIRM: 'Applied, waiting for confirmation',
      ROLLING_BACK: 'Rolling back',
      FAILED: 'Failed',
      ROLLED_BACK: 'Rolled back'
    },
    targetVersion: 'Target version',
    previousVersion: 'Previous version',
    transfer: 'Transfer',
    lastError: 'Device error',
    restartReason: 'Last restart',
    restartReasons: {
      expected_restart: 'Restarted for the update',
      unexpected_restart: 'Restarted unexpectedly',
      wrong_version: 'Came back on the wrong version',
      confirm_timeout: 'Not confirmed in time; rolled back',
      health_failed: 'Health check failed; rolled back'
    },
    confirmDeadline: 'Rolls back unless confirmed by',
    signaturePolicy: 'Device signature policy',
    lastSignatures: 'Last verified manifest',
    signatureOutcomes: { classical_only: 'Ed25519 only', dual_signed: 'Dual-signed' },
    readAt: 'Device answered at',
    pendingTitle: 'The device is running the new agent and is waiting for confirmation',
    pendingNote: 'Confirm only after checking the device. Without a confirmation it returns to its previous agent on its own.',
    confirm: 'Confirm the update',
    rollback: 'Roll back now',
    releases: 'Releases stored on this gateway',
    storeUnavailable: 'This gateway has no release store, so nothing can be imported or pushed.',
    gatewayNoKeys: 'This gateway has no release keys and verifies nothing itself. Each device still verifies a release against its own keys before installing it.',
    noReleases: 'No release has been imported.',
    gatewayVerified: 'Verified by gateway',
    gatewayUnverified: 'Not verified by gateway — verified on the device',
    dualSigned: 'Dual-signed',
    classicalOnly: 'Ed25519 only',
    signaturesUnknown: 'Signatures not established here',
    artifactReady: 'Artifact stored',
    artifactMissing: 'Artifact not imported',
    version: 'Version',
    channel: 'Channel',
    platform: 'Platform',
    size: 'Size',
    releasedAt: 'Released',
    minCore: 'Needs gateway',
    notes: 'Notes',
    details: 'Identifiers',
    releaseId: 'Release ID',
    digest: 'SHA-256',
    keyIds: 'Key IDs claimed by the manifest',
    push: 'Push to device',
    pushing: 'Pushing…',
    apply: 'Apply…',
    applying: 'Applying, waiting for the device…',
    applyNeedsPush: 'Push first: the device must receive and validate this release before it can be applied.',
    remove: 'Remove',
    removeConfirm: 'Remove from gateway',
    keep: 'Keep',
    applyTitle: 'Apply this release to the device?',
    applyExplain: [
      'The device restarts its agent to switch to the new version.',
      'Calls in progress on this device will drop.',
      'If the device does not come back on the new version in time, it returns to its previous agent automatically. This can take up to about three minutes.'
    ],
    applyConfirm: 'Restart the agent and apply',
    cancel: 'Cancel',
    outcomePushed: 'The release was transferred. Nothing was restarted.',
    outcomeApplied: 'The device came back on the new version and the update was confirmed.',
    outcomeNotConfirmed: 'The device did not come back on the new version in time. It returns to its previous agent on its own. The state below was read from the device again just now.',
    outcomeConfirmed: 'The update was confirmed.',
    outcomeRolledBack: 'The rollback was requested.',
    outcomeRemoved: 'The release was removed from this gateway. Devices that already hold it are not affected.',
    importTitle: 'Import a release',
    importNote: 'Import the signed manifest first, then its artifact. The manifest is sent byte for byte as it is in the file.',
    manifestFile: 'Signed manifest (.json)',
    artifactFile: 'Artifact',
    importManifest: 'Import manifest',
    importArtifact: 'Import artifact',
    importing: 'Importing…',
    uploading: (percent) => `Uploading… ${percent}%`,
    manifestTooLarge: 'This file is larger than a signed manifest can be (32 KiB).',
    artifactTooLarge: 'This file is larger than an agent artifact can be (32 MiB).',
    artifactSizeMismatch: (expected) => `This file is not the size the manifest states (${expected}).`,
    artifactFor: 'Artifact for release',
    chooseRelease: 'Import a manifest first.',
    manifestImported: 'Manifest imported. Now import its artifact.',
    artifactImported: 'Artifact imported and matched against the manifest.'
  }
}

const chinese: NodeSecurityCopy = {
  title: '安全',
  unknown: '未知',
  notReported: '网关未上报',
  classicalIdentity: '设备身份',
  classicalPinned: 'Ed25519，配对时已固定',
  pqIdentity: '后量子身份',
  outcomes: {
    unknown: '尚未检查',
    dual: '双重身份已验证',
    classical_only: '仅经典身份',
    refused: '已拒绝'
  },
  pqUnavailable: (backend) => `此网关版本无法验证 ML-DSA${backend ? `（${backend}）` : ''}，不下结论`,
  pqNotReported: '网关未上报，不下结论',
  noAttestation: '设备软件不支持身份证明交换。',
  pqPin: '后量子密钥固定',
  pinStates: {
    none: '未记录密钥',
    pinned: '已记录且一致',
    changed: '设备出示了另一把密钥',
    withdrawn: '设备不再出示密钥',
    unknown: '未知'
  },
  pinnedAt: '记录时间',
  pqFingerprint: '后量子密钥指纹',
  policy: '身份策略',
  policyEnforced: '强制执行',
  policyReportOnly: '仅报告，不强制',
  identityRefused: '身份校验被拒绝',
  identityRefusedNoReason: '网关没有给出原因。',
  identityNote: '身份说明',
  requestedLevel: '要求的级别',
  negotiatedGroup: '实际协商的密钥交换',
  linkPostQuantum: '链路是否后量子',
  tristate: { yes: '是', no: '否', unknown: '未知' },
  linkNotObserved: '没有观测到会话，对链路不做任何判断。',
  linkShortfall: (required) => `实际协商的密钥交换没有达到要求的级别（要求 ${required}）。`,
  linkShortfallNoGroup: '实际协商的密钥交换没有达到要求的级别。',
  nodeGroupDiffers: (group) => `设备上报的密钥交换组（${group}）与网关观测到的不一致。`,
  trust: '信任状态',
  trustStates: {
    UNBOUND: '未绑定',
    OBSERVATION: '观察期',
    TRUSTED: '已信任',
    RESTRICTED: '受限',
    QUARANTINE: '已隔离'
  },
  trustUnknown: '没有信任记录',
  trustRemaining: (time) => `观察期剩余 ${time}`,
  trustReason: '原因',
  outgoingBlocked: '不会通过这台设备拨出电话或发送短信。',
  outgoingWouldBlock: '按当前状态本不应通过这台设备外呼或发短信，但现在并没有拦截。',
  outgoingBlockedUnknownMode: '当前状态不允许外呼和发短信；网关没有说明是否在强制执行。',
  monitorMode: '监控模式：仅记录，不拦截',
  day: ' 天 ',
  hour: ' 小时 ',
  minute: ' 分钟',
  underMinute: '不到 1 分钟',
  resetOffer: '接受新的后量子身份…',
  resetTitle: '接受新的后量子身份？',
  resetExplain: [
    '这会让网关忘掉为这台设备记录的 ML-DSA 密钥。',
    '配对时固定的 Ed25519 密钥不受影响，仍然用来识别这台设备。',
    '下一次校验时，网关会记录设备当时能证明持有的后量子密钥。只有在你确实主动换过密钥（例如重装或更新了设备软件）时才这样做。'
  ],
  resetConfirm: '忘掉已记录的密钥',
  resetCancel: '取消',
  resetting: '正在重置…',
  resetDone: '已忘掉记录的后量子密钥。下一次校验会记录设备能证明持有的密钥。',
  updates: {
    open: '设备软件更新…',
    title: '设备软件更新',
    close: '关闭',
    refresh: '重新读取',
    loading: '正在询问设备…',
    offline: '连接不上网关。',
    forbidden: '需要管理员',
    sessionExpired: '登录已过期，请刷新页面重新登录。',
    failed: '操作失败',
    invalidResponse: 'NAS 返回的数据格式无效',
    deviceSection: '这台设备',
    notAnswering: '设备未应答',
    notAnsweringNote: '这里不显示任何状态：网关不保存“上一次的状态”，旧状态也说明不了设备现在的情况。',
    unsupported: '这台设备不能从这里更新',
    noDeviceStatus: '网关没有返回这台设备的更新状态，目前对它一无所知。',
    noReason: '没有给出原因。',
    currentVersion: '正在运行的版本',
    factoryVersion: '出厂版本',
    activeRelease: '已安装的发布',
    factoryAgent: '无（正在运行出厂软件）',
    state: '状态',
    states: {
      IDLE: '空闲',
      CHECKING: '检查中',
      AVAILABLE: '已提供',
      DOWNLOADING: '接收中',
      STAGED: '已接收',
      VALIDATING: '校验中',
      READY: '已校验，可以应用',
      APPLYING: '应用中',
      PENDING_CONFIRM: '已应用，等待确认',
      ROLLING_BACK: '回滚中',
      FAILED: '失败',
      ROLLED_BACK: '已回滚'
    },
    targetVersion: '目标版本',
    previousVersion: '上一个版本',
    transfer: '传输',
    lastError: '设备报告的错误',
    restartReason: '上次重启',
    restartReasons: {
      expected_restart: '为更新而重启',
      unexpected_restart: '意外重启',
      wrong_version: '重启后版本不对',
      confirm_timeout: '超时未确认，已回滚',
      health_failed: '健康检查未通过，已回滚'
    },
    confirmDeadline: '在此之前不确认就会回滚',
    signaturePolicy: '设备的签名策略',
    lastSignatures: '上一次校验的清单',
    signatureOutcomes: { classical_only: '仅 Ed25519', dual_signed: '双签名' },
    readAt: '设备应答时间',
    pendingTitle: '设备已在运行新版本，正在等待确认',
    pendingNote: '请先检查设备再确认。不确认的话，它会自己回到之前的版本。',
    confirm: '确认更新',
    rollback: '立即回滚',
    releases: '这台网关保存的发布',
    storeUnavailable: '这台网关没有发布仓库，无法导入或推送。',
    gatewayNoKeys: '这台网关没有发布密钥，自己不做任何验证。每台设备在安装前仍会用它自己的密钥验证发布。',
    noReleases: '还没有导入任何发布。',
    gatewayVerified: '网关已验证',
    gatewayUnverified: '网关未验证—由设备验证',
    dualSigned: '双签名',
    classicalOnly: '仅 Ed25519',
    signaturesUnknown: '签名情况在这里未确定',
    artifactReady: '安装包已保存',
    artifactMissing: '尚未导入安装包',
    version: '版本',
    channel: '通道',
    platform: '平台',
    size: '大小',
    releasedAt: '发布时间',
    minCore: '需要的网关版本',
    notes: '说明',
    details: '标识',
    releaseId: '发布 ID',
    digest: 'SHA-256',
    keyIds: '清单声明的密钥 ID',
    push: '推送到设备',
    pushing: '正在推送…',
    apply: '应用…',
    applying: '正在应用，等待设备恢复…',
    applyNeedsPush: '请先推送：设备必须先收到并校验这个发布，才能应用。',
    remove: '移除',
    removeConfirm: '从网关移除',
    keep: '保留',
    applyTitle: '把这个发布应用到设备？',
    applyExplain: [
      '设备会重启它的软件以切换到新版本。',
      '这台设备上正在进行的通话会中断。',
      '如果设备没有在规定时间内以新版本恢复，它会自动回到之前的版本。整个过程最长大约三分钟。'
    ],
    applyConfirm: '重启设备软件并应用',
    cancel: '取消',
    outcomePushed: '发布已传到设备，没有重启任何东西。',
    outcomeApplied: '设备已以新版本恢复，更新已确认。',
    outcomeNotConfirmed: '设备没有在规定时间内以新版本恢复。它会自己回到之前的版本。下面的状态是刚刚重新从设备读取的。',
    outcomeConfirmed: '更新已确认。',
    outcomeRolledBack: '已请求回滚。',
    outcomeRemoved: '已从这台网关移除该发布。已经持有它的设备不受影响。',
    importTitle: '导入发布',
    importNote: '先导入签名清单，再导入对应的安装包。清单按文件原样逐字节发送。',
    manifestFile: '签名清单（.json）',
    artifactFile: '安装包',
    importManifest: '导入清单',
    importArtifact: '导入安装包',
    importing: '正在导入…',
    uploading: (percent) => `正在上传… ${percent}%`,
    manifestTooLarge: '这个文件超过了签名清单的大小上限（32 KiB）。',
    artifactTooLarge: '这个文件超过了安装包的大小上限（32 MiB）。',
    artifactSizeMismatch: (expected) => `这个文件的大小与清单声明的不一致（${expected}）。`,
    artifactFor: '安装包对应的发布',
    chooseRelease: '请先导入清单。',
    manifestImported: '清单已导入。接下来导入它的安装包。',
    artifactImported: '安装包已导入，并与清单核对一致。'
  }
}

export function nodeSecurityCopy(locale: string): NodeSecurityCopy {
  return locale.startsWith('zh') ? chinese : english
}
