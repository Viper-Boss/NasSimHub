export type SimNodeInventoryCopy = {
  title: string
  subtitle: string
  refresh: string
  loading: string
  notice: string
  empty: string
  readFailed: string
  invalidResponse: string
  deviceId: string
  platform: string
  pairing: string
  link: string
  usb: string
  wifi: string
  lastSeen: string
  unknown: string
  disconnected: string
  missing: string
  noRecord: string
  discover: string
  discovering: string
  candidateTitle: string
  noCandidates: string
  unverified: string
  identityVerifiedUnpaired: string
  discoverFailed: string
  // Pairing
  fingerprint: string
  model: string
  version: string
  pair: string
  pairing_: string
  pairTitle: string
  pairCompare: string
  pairName: string
  pairNamePlaceholder: string
  pairConfirm: string
  pairCancel: string
  pairDone: string
  alreadyThisNAS: string
  ownedByOther: string
  keyMismatch: string
  incompatible: string
  expiresIn: (seconds: number) => string
  expired: string
  // Node card
  online: string
  offline: string
  revoked: string
  operator: string
  simState: string
  registration: string
  signal: string
  sms: string
  smsReady: string
  smsNotReady: string
  volte: string
  volteRegistered: string
  volteUnknown: string
  callAudio: string
  callAudioReady: string
  callAudioUnavailable: string
  onDevice: string
  lastError: string
  refreshNode: string
  unpair: string
  unpairConfirm: string
  unpairKeep: string
  unpairDone: string
  unpairLocalOnly: string
  pairStates: Record<string, string>
  simStates: Record<string, string>
  registrations: Record<string, string>
  transports: Record<string, string>
  errors: Record<string, string>
  genericError: string
}

const english: SimNodeInventoryCopy = {
  title: 'SIM Nodes',
  subtitle: 'External cellular devices (such as the UFI003 stick) paired with this NAS.',
  refresh: 'Refresh',
  loading: 'Loading…',
  notice: 'Plug the stick into the NAS over USB, then search for it. Pair only if the device ID shown here matches the one on the stick (its label or the Windows client). After pairing, its SMS appear in Messages and can be sent from there; the NAS keeps using Wi-Fi if USB is unplugged.',
  empty: 'No SIM Node is paired with this NAS yet.',
  readFailed: 'Unable to load the node list',
  invalidResponse: 'Invalid response from the NAS',
  deviceId: 'Device ID',
  platform: 'Platform',
  pairing: 'Pairing',
  link: 'Link',
  usb: 'USB',
  wifi: 'Wi-Fi',
  lastSeen: 'Last seen',
  unknown: 'Unknown',
  disconnected: 'Not connected',
  missing: 'Not recorded',
  noRecord: 'No record',
  discover: 'Search over USB',
  discovering: 'Searching…',
  candidateTitle: 'Devices found',
  noCandidates: 'No device answered on the USB link. Check the cable and wait for the stick to finish starting.',
  unverified: 'Identity not verified',
  identityVerifiedUnpaired: 'Identity verified; not paired',
  discoverFailed: 'Search failed',
  fingerprint: 'Key fingerprint',
  model: 'Model',
  version: 'Software',
  pair: 'Pair…',
  pairing_: 'Pairing…',
  pairTitle: 'Confirm this is your device',
  pairCompare: 'Compare the device ID with the one on the stick. If it differs, cancel: another device is answering on this link.',
  pairName: 'Name (optional)',
  pairNamePlaceholder: 'e.g. Living room stick',
  pairConfirm: 'The IDs match — pair',
  pairCancel: 'Cancel',
  pairDone: 'Paired. Its line will appear in a few seconds.',
  alreadyThisNAS: 'Already paired with this NAS',
  ownedByOther: 'Paired with another NAS — release it there first',
  keyMismatch: 'A device with this ID was paired before with a different key. Remove the old record first.',
  incompatible: 'Software not compatible with this NAS',
  expiresIn: (seconds) => `Confirmation valid for ${Math.max(0, Math.floor(seconds / 60))}:${String(Math.max(0, seconds % 60)).padStart(2, '0')}`,
  expired: 'Confirmation expired — search again',
  online: 'Online',
  offline: 'Offline',
  revoked: 'Released by the device',
  operator: 'Operator',
  simState: 'SIM',
  registration: 'Network',
  signal: 'Signal',
  sms: 'SMS',
  smsReady: 'Ready',
  smsNotReady: 'Not ready',
  volte: 'IMS / VoLTE',
  volteRegistered: 'Registered (signaling only)',
  volteUnknown: 'Not verified',
  callAudio: 'Call audio',
  callAudioReady: 'Available',
  callAudioUnavailable: 'Unavailable; calling from NasSimHub is disabled',
  onDevice: 'Messages on device',
  lastError: 'Last problem',
  refreshNode: 'Check now',
  unpair: 'Unpair',
  unpairConfirm: 'Unpair now',
  unpairKeep: 'Keep',
  unpairDone: 'Unpaired. Received messages stay in Messages.',
  unpairLocalOnly: 'Removed from this NAS. The stick was not reachable, so it still considers itself paired; reset it before pairing it elsewhere.',
  pairStates: { paired: 'Paired', pairing: 'Pairing', unpaired: 'Not paired' },
  simStates: { ready: 'Ready', missing: 'No SIM', pin_locked: 'PIN locked', puk_locked: 'PUK locked', error: 'SIM error', unknown: 'Unknown' },
  registrations: { registered: 'Registered', roaming: 'Roaming', searching: 'Searching', denied: 'Registration denied', unregistered: 'Not registered', unknown: 'Unknown' },
  transports: { usb: 'USB', wifi: 'Wi-Fi', none: 'Not connected', '': 'Not connected' },
  errors: {
    pairing_expired: 'The confirmation expired or was already used. Search again.',
    fingerprint_mismatch: 'The device key does not match what was shown. Do not pair; search again.',
    identity_changed: 'A different device is answering now. Do not pair.',
    already_paired: 'This device is already paired with this NAS.',
    key_changed: 'A device with this ID was paired before with a different key. Remove the old record first.',
    owned_elsewhere: 'The device is paired with another NAS. Release it there first.',
    incompatible: 'The device software is not compatible with this NAS version.',
    not_confirmed: 'The device did not confirm the pairing. Try again.',
    node_unreachable: 'The device is not reachable.',
    node_not_found: 'This device is not paired with this NAS.',
    nodes_unavailable: 'SIM Node support is turned off on this NAS.',
    csrf_failed: 'Your session expired. Reload the page and sign in again.',
    forbidden: 'Only an administrator can manage SIM Nodes.'
  },
  genericError: 'The operation failed'
}

const chinese: SimNodeInventoryCopy = {
  title: 'SIM 节点',
  subtitle: '与这台 NAS 配对的外置蜂窝设备（例如 UFI003 棒子）。',
  refresh: '刷新',
  loading: '读取中…',
  notice: '把棒子用 USB 插到 NAS 上，然后点“通过 USB 查找”。只有当这里显示的设备 ID 与棒子上的一致（贴纸或 Windows 客户端里能看到）时才配对。配对后，它收到的短信会出现在“短信”里，也可以从那里发短信；拔掉 USB 后会自动改走 Wi-Fi。',
  empty: '这台 NAS 还没有配对 SIM 节点。',
  readFailed: '设备清单读取失败',
  invalidResponse: 'NAS 返回的数据格式无效',
  deviceId: '设备 ID',
  platform: '平台',
  pairing: '配对',
  link: '链路',
  usb: 'USB',
  wifi: 'Wi-Fi',
  lastSeen: '上次在线',
  unknown: '未知',
  disconnected: '未连接',
  missing: '未记录',
  noRecord: '尚无记录',
  discover: '通过 USB 查找',
  discovering: '正在查找…',
  candidateTitle: '找到的设备',
  noCandidates: 'USB 链路上没有设备响应。请检查连接，并等棒子启动完成（约 1 分钟）。',
  unverified: '身份未验证',
  identityVerifiedUnpaired: '身份已校验，尚未配对',
  discoverFailed: '查找失败',
  fingerprint: '密钥指纹',
  model: '型号',
  version: '软件版本',
  pair: '配对…',
  pairing_: '正在配对…',
  pairTitle: '确认这是你的设备',
  pairCompare: '请核对设备 ID 与棒子上的是否一致。如果不一致，请取消：说明这条链路上应答的是另一台设备。',
  pairName: '名称（可选）',
  pairNamePlaceholder: '例如：客厅棒子',
  pairConfirm: '一致，确认配对',
  pairCancel: '取消',
  pairDone: '已配对，几秒后会出现它的线路。',
  alreadyThisNAS: '已与这台 NAS 配对',
  ownedByOther: '已与另一台 NAS 配对，请先在那边解除',
  keyMismatch: '这个设备 ID 以前用另一把密钥配对过，请先删除旧记录。',
  incompatible: '设备软件与这台 NAS 版本不兼容',
  expiresIn: (seconds) => `确认在 ${Math.max(0, Math.floor(seconds / 60))}:${String(Math.max(0, seconds % 60)).padStart(2, '0')} 内有效`,
  expired: '确认已过期，请重新查找',
  online: '在线',
  offline: '离线',
  revoked: '设备已解除配对',
  operator: '运营商',
  simState: 'SIM 卡',
  registration: '网络',
  signal: '信号',
  sms: '短信',
  smsReady: '可用',
  smsNotReady: '暂不可用',
  volte: 'IMS / VoLTE',
  volteRegistered: '已注册（仅信令）',
  volteUnknown: '未验证',
  callAudio: '通话音频',
  callAudioReady: '可用',
  callAudioUnavailable: '尚不可用；NasSimHub 暂不开放拨号',
  onDevice: '设备上的短信',
  lastError: '最近的问题',
  refreshNode: '立即检查',
  unpair: '解除配对',
  unpairConfirm: '确认解除',
  unpairKeep: '保留',
  unpairDone: '已解除配对，已收到的短信仍保留在“短信”里。',
  unpairLocalOnly: '已从这台 NAS 移除。当时联系不上棒子，它仍认为自己已配对；要配对到别处前请先重置它。',
  pairStates: { paired: '已配对', pairing: '配对中', unpaired: '未配对' },
  simStates: { ready: '就绪', missing: '未插卡', pin_locked: 'PIN 已锁', puk_locked: 'PUK 已锁', error: 'SIM 卡错误', unknown: '未知' },
  registrations: { registered: '已注册', roaming: '漫游', searching: '搜索网络中', denied: '注册被拒绝', unregistered: '未注册', unknown: '未知' },
  transports: { usb: 'USB', wifi: 'Wi-Fi', none: '未连接', '': '未连接' },
  errors: {
    pairing_expired: '确认已过期或已使用，请重新查找。',
    fingerprint_mismatch: '设备密钥与刚才显示的不一致。请不要配对，重新查找。',
    identity_changed: '现在应答的是另一台设备。请不要配对。',
    already_paired: '这台设备已经与这台 NAS 配对。',
    key_changed: '这个设备 ID 以前用另一把密钥配对过，请先删除旧记录。',
    owned_elsewhere: '设备已与另一台 NAS 配对，请先在那边解除。',
    incompatible: '设备软件与这台 NAS 版本不兼容。',
    not_confirmed: '设备没有确认配对，请重试。',
    node_unreachable: '联系不上设备。',
    node_not_found: '这台设备没有与这台 NAS 配对。',
    nodes_unavailable: '这台 NAS 已关闭 SIM 节点功能。',
    csrf_failed: '登录已过期，请刷新页面重新登录。',
    forbidden: '只有管理员可以管理 SIM 节点。'
  },
  genericError: '操作失败'
}

export function simNodeInventoryCopy(locale: string): SimNodeInventoryCopy {
  return locale.startsWith('zh') ? chinese : english
}
