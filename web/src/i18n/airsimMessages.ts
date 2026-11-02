// Copy for the AirSIM iPhone app settings panel. Loaded with the panel, like
// callServicesMessages.ts: one administrator page most installs never open.

export type AirSIMMessages = Record<string, string>

const english: AirSIMMessages = {
  title: 'AirSIM iPhone app',
  subtitle:
    'Pair the AirSIM app (an iPhone app written for an Android phone gateway) with a line on ' +
    'this gateway. The iPhone then makes and answers calls and sends and receives SMS on that ' +
    'line, with the call audio on the phone.',
  unavailable: 'AirSIM compatibility is not running on this gateway (MODEMDECK_AIRSIM is off).',
  loadFailed: 'Could not load the AirSIM settings: {error}',
  enable: 'Allow AirSIM iPhones',
  enableHint: 'Off: paired iPhones are kept but every request they send is refused.',
  line: 'Line used by the iPhone',
  noLines: 'No line is available yet.',
  save: 'Save',
  saved: 'Saved.',
  listening: 'Listening on ports {control} (control), {pcm} (audio) and {pairing} (pairing).',
  notListening: 'The AirSIM ports are not open: {error}',
  portsNote:
    'These ports must be published on the NAS: start the stack with docker-compose.airsim.yml ' +
    '(see docs/AIRSIM-COMPAT.md). Only phones on the same Wi-Fi, hotspot or Tailscale network ' +
    'are accepted. Do not forward these ports from the Internet.',

  pairingTitle: 'Pair an iPhone',
  step1: 'In AirSIM, open pairing and choose the manual (IP address) option.',
  step2: 'Address: {host}   Port: {port}',
  step3: 'Enter this code within the time shown:',
  start: 'Start pairing',
  restart: 'New code',
  cancel: 'Cancel',
  expiresIn: 'Valid for {seconds} s',
  expired: 'The code has expired. Start pairing again.',
  pairedNow: 'Paired. The iPhone is now verifying the connection.',
  needEnabled: 'Turn on AirSIM and choose a line first.',

  devicesTitle: 'Paired iPhones',
  noDevices: 'No iPhone is paired.',
  device: 'iPhone',
  deviceHint: 'iPhone …{hint}',
  pairedAt: 'Paired {time}',
  lastSeen: 'Last seen {time} from {peer}',
  revoke: 'Unpair',
  revokeConfirm: 'Unpair this iPhone? It stops working at once and has to pair again.',

  limitsTitle: 'What works',
  limits:
    'Calls, call audio, DTMF, mute, incoming and outgoing SMS. Incoming calls reach the iPhone ' +
    'while the AirSIM app is running: push wake-up needs AirSIM’s own cloud relay, which this ' +
    'gateway does not use.'
}

const chinese: AirSIMMessages = {
  title: 'AirSIM 手机',
  subtitle:
    '让 AirSIM（原本配合安卓手机网关使用的 iPhone App）与本网关的一条线路配对。配对后 iPhone 可以用这条线路' +
    '拨打和接听电话、收发短信，通话声音在 iPhone 上。',
  unavailable: '本网关没有启用 AirSIM 兼容（MODEMDECK_AIRSIM 为 off）。',
  loadFailed: '无法读取 AirSIM 设置：{error}',
  enable: '允许 AirSIM iPhone 接入',
  enableHint: '关闭后已配对的 iPhone 会保留，但它们的所有请求都会被拒绝。',
  line: 'iPhone 使用的线路',
  noLines: '还没有可用的线路。',
  save: '保存',
  saved: '已保存。',
  listening: '正在监听端口 {control}（控制）、{pcm}（音频）和 {pairing}（配对）。',
  notListening: 'AirSIM 端口未能打开：{error}',
  portsNote:
    'NAS 上需要发布这几个端口：启动时加上 docker-compose.airsim.yml（见 docs/AIRSIM-COMPAT.md）。' +
    '只接受同一 Wi-Fi、热点或 Tailscale 网络里的手机。不要把这些端口映射到公网。',

  pairingTitle: '配对 iPhone',
  step1: '在 AirSIM 里打开配对，选择手动输入 IP 地址。',
  step2: '地址：{host}　端口：{port}',
  step3: '在倒计时结束前输入下面的验证码：',
  start: '开始配对',
  restart: '换一个验证码',
  cancel: '取消',
  expiresIn: '{seconds} 秒内有效',
  expired: '验证码已过期，请重新开始配对。',
  pairedNow: '配对成功，iPhone 正在验证连接。',
  needEnabled: '请先打开 AirSIM 并选择线路。',

  devicesTitle: '已配对的 iPhone',
  noDevices: '还没有配对的 iPhone。',
  device: 'iPhone',
  deviceHint: 'iPhone …{hint}',
  pairedAt: '配对于 {time}',
  lastSeen: '最近 {time}，来自 {peer}',
  revoke: '解除配对',
  revokeConfirm: '解除这台 iPhone 的配对？它会立即停止工作，需要重新配对。',

  limitsTitle: '支持的功能',
  limits:
    '通话、通话音频、拨号键、静音、短信收发。来电只有在 AirSIM App 运行时才能送到 iPhone：' +
    '推送唤醒需要 AirSIM 自己的云端中继，本网关不使用它。'
}

const traditional: AirSIMMessages = {
  ...chinese,
  title: 'AirSIM 手機',
  subtitle:
    '讓 AirSIM（原本搭配安卓手機閘道使用的 iPhone App）與本閘道的一條線路配對。配對後 iPhone 可以用這條線路' +
    '撥打和接聽電話、收發簡訊，通話聲音在 iPhone 上。',
  unavailable: '本閘道沒有啟用 AirSIM 相容（MODEMDECK_AIRSIM 為 off）。',
  loadFailed: '無法讀取 AirSIM 設定：{error}',
  enable: '允許 AirSIM iPhone 接入',
  enableHint: '關閉後已配對的 iPhone 會保留，但它們的所有請求都會被拒絕。',
  line: 'iPhone 使用的線路',
  noLines: '還沒有可用的線路。',
  save: '儲存',
  saved: '已儲存。',
  listening: '正在監聽連接埠 {control}（控制）、{pcm}（音訊）和 {pairing}（配對）。',
  notListening: 'AirSIM 連接埠未能開啟：{error}',
  portsNote:
    'NAS 上需要發布這幾個連接埠：啟動時加上 docker-compose.airsim.yml（見 docs/AIRSIM-COMPAT.md）。' +
    '只接受同一 Wi-Fi、熱點或 Tailscale 網路裡的手機。不要把這些連接埠映射到公網。',
  pairingTitle: '配對 iPhone',
  step1: '在 AirSIM 裡開啟配對，選擇手動輸入 IP 位址。',
  step2: '位址：{host}　連接埠：{port}',
  step3: '在倒數結束前輸入下面的驗證碼：',
  start: '開始配對',
  restart: '換一個驗證碼',
  expiresIn: '{seconds} 秒內有效',
  expired: '驗證碼已過期，請重新開始配對。',
  pairedNow: '配對成功，iPhone 正在驗證連線。',
  needEnabled: '請先開啟 AirSIM 並選擇線路。',
  devicesTitle: '已配對的 iPhone',
  noDevices: '還沒有配對的 iPhone。',
  pairedAt: '配對於 {time}',
  lastSeen: '最近 {time}，來自 {peer}',
  revoke: '解除配對',
  revokeConfirm: '解除這台 iPhone 的配對？它會立即停止運作，需要重新配對。',
  limitsTitle: '支援的功能',
  limits:
    '通話、通話音訊、撥號鍵、靜音、簡訊收發。來電只有在 AirSIM App 執行時才能送到 iPhone：' +
    '推播喚醒需要 AirSIM 自己的雲端中繼，本閘道不使用它。'
}

const translations: Record<string, AirSIMMessages> = {
  'en-US': english,
  'zh-CN': chinese,
  'zh-TW': traditional
}

/** Panel copy for a locale, falling back to English for anything unlisted. */
export function airsimMessages(locale: string): AirSIMMessages {
  if (translations[locale]) return translations[locale]!
  if (locale.startsWith('zh')) return chinese
  return english
}
