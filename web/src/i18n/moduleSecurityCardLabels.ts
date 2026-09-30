// The module-security card's labels, kept out of the startup locale.
//
// Same reasoning as networkCardNotes and softwareUpdateCardLabels beside it:
// one card, one screen, enough prose that carrying it in the startup bundle
// would cost every visitor bytes before the app had drawn anything.
//
// TWO LANGUAGES, THE REST FALL BACK TO ENGLISH. A deliberate choice, not an
// unfinished job: these sentences say which protections are running and which
// are not, on a card that exists to answer a legal question. A machine-guessed
// rendering of "this is the factory default, not a reading from your gateway"
// is worse than the English it replaces.

export type ModuleSecurityCardLabels = Record<string, string>

const labels: Record<string, ModuleSecurityCardLabels> = {
  'zh-CN': {
    title: '模组安全',
    subtitle: 'MODULE SECURITY',
    purpose: '这台网关能打电话、能发短信，也就能被拿去做坏事。这张卡是用来看清楚：现在有什么在拦着它。',

    riskLabel: '风险等级',
    riskPending: '未评估',
    riskPendingNote: '没有任何代码在算这个分数，所以这里不显示等级。后端接上之后自动出现。',

    // The anti-fraud guard. The one real protection in the product.
    guardTitle: '反诈限额',
    guardWhat: '每天能联系的「陌生号码」数量上限。同一个号码当天重复拨打或重复发送不再计数——它拦的是"广撒网"，不是"打得多"。超限时接口直接返回 403。',
    guardWhatShort: '按不同号码计，不按次数',
    guardTodayCalls: '今日外呼',
    guardTodayMessages: '今日短信',
    guardOfLimit: '/ {n} 个号码',
    guardPerDay: '个号码 / 日',
    guardUsedPending: '今日计数未上报',
    guardUsedNote: '这台网关还没有报告今日用了几个号码，所以这里只显示上限，不猜数字。写着「出厂默认上限」时，连上限也是默认值，不是从网关读回来的。网关更新后自动出现。',
    guardDefaultNote: '出厂默认上限',
    guardUnlockedShort: '已解除，不再限制号码数',
    guardReading: '读取中',
    guardUnlocked: '限额已解除',
    guardUnlockedNote: '管理员已解除反诈限额：现在联系多少个陌生号码都不受限制。',
    guardLocked: '限额生效中',
    guardResetTitle: '下次清零',
    guardResetHours: '小时后',
    guardResetMinutes: '分钟后',
    guardResetDue: '正在清零',
    guardResetAt: '{time} 清零',
    guardResetDaily: '每天零点',
    guardResetLocal: '按网关本地时间',

    // Egress and the call filter. Real today.
    egressTitle: '网络出口',
    egressIP: '出口 IP',
    egressISP: '运营商',
    egressPending: '读取中',
    egressUnavailable: '取不到',
    egressNote: '这是这台网关在公网上的出口地址，由 NAS 侧探测得到。不是地理位置——系统里没有任何一处在做网关定位。',
    egressNoteShort: '公网出口，不是地理位置',

    filterTitle: '来电过滤',
    filterOff: '未启用',
    filterBlacklist: '黑名单',
    filterWhitelist: '白名单',
    filterEntriesValue: '{n} 条',
    filterMinReportsValue: '标记阈值：{n} 次举报',
    filterNotReady: '规则已存，执行未接',
    filterNotReadyNote: '过滤规则已经保存在网关里，但真正执行它需要一个还没接上的来电标记源，所以现在只是策略不是拦截。',
    filterUnavailable: '读不到策略',
    filterSubOff: '所有来电都会接进来',
    filterSubBlacklist: '拦截名单里的号码',
    filterSubWhitelist: '只接名单里的号码',

    // Integrity and trust. None of it exists.
    integrityTitle: '完整性与信任',
    integrityModule: '模组完整性',
    integritySystem: '系统完整性',
    integritySecureBoot: '安全启动',
    integrityFirmware: '固件签名',
    integrityPending: '待接入',
    integrityNote: '这几项都还没有后端。系统里现在没有 TPM、没有安全启动、没有风险评分，也没有任何一处在验固件签名——所以这里一个绿勾都不会亮，等做好了才会。',
    integrityNoteShort: '都还没有后端，不会亮绿勾',
    trustTitle: '设备信任',
    trustPending: '未评估',
    trustPendingNote: '没有任何代码在评估设备信任，所以这里不显示观察期。',
    trustRemaining: '剩余 {d} 天 {h} 小时',

    // Footer.
    details: '诊断详情',
    recheck: '立即复检',
    rechecking: '复检中',
    recheckNote: '复检会重新读取反诈限额、出口探测和过滤策略——也就是上面那些真的在跑的东西。',
    notAvailable: '—'
  },

  'en-US': {
    title: 'Module security',
    subtitle: 'MODULE SECURITY',
    purpose:
      'This gateway can place calls and send messages, which means it can be used to defraud people. This card is where you see what is currently restraining it.',

    riskLabel: 'Risk level',
    riskPending: 'Not assessed',
    riskPendingNote:
      'Nothing computes this score, so no level is shown. It appears once the backend does.',

    guardTitle: 'Anti-fraud limit',
    guardWhat:
      'A daily cap on how many distinct numbers can be contacted. Dialling or texting the same number again the same day does not count again — the guard restrains reaching many strangers, not making many calls. Over the limit, the API returns 403.',
    guardWhatShort: 'Distinct numbers, not attempts',
    guardTodayCalls: 'Calls today',
    guardTodayMessages: 'Texts today',
    guardOfLimit: '/ {n} numbers',
    guardPerDay: 'numbers a day',
    guardUsedPending: 'Today’s count not reported',
    guardUsedNote:
      'This gateway has not reported how many numbers today has used, so only the cap is shown rather than a guess. Where it says shipped default, the cap too is the default rather than a value read back from the gateway. It appears once the gateway is updated.',
    guardDefaultNote: 'Shipped default',
    guardUnlockedShort: 'Lifted: no cap on numbers',
    guardReading: 'Reading',
    guardUnlocked: 'Limit lifted',
    guardUnlockedNote: 'An administrator has lifted the anti-fraud limit: nothing caps how many strangers this box can reach.',
    guardLocked: 'Limit in force',
    guardResetTitle: 'Next reset',
    guardResetHours: 'hours',
    guardResetMinutes: 'minutes',
    guardResetDue: 'Resetting now',
    guardResetAt: 'at {time}',
    guardResetDaily: 'Midnight daily',
    guardResetLocal: 'gateway local time',

    egressTitle: 'Network egress',
    egressIP: 'Public IP',
    egressISP: 'ISP',
    egressPending: 'Reading',
    egressUnavailable: 'Unavailable',
    egressNote:
      'The address this gateway presents on the public internet, probed from the NAS side. Not a location — nothing in the system geolocates the gateway.',
    egressNoteShort: 'Public egress, not a location',

    filterTitle: 'Call filter',
    filterOff: 'Off',
    filterBlacklist: 'Blocklist',
    filterWhitelist: 'Allowlist',
    filterEntriesValue: '{n} entries',
    filterMinReportsValue: 'Report threshold: {n}',
    filterNotReady: 'Stored, not enforced',
    filterNotReadyNote:
      'The rules are saved on the gateway, but enforcing them needs a call-tagging source that is not connected yet, so this is policy rather than blocking.',
    filterUnavailable: 'Unavailable',
    filterSubOff: 'Every call comes through',
    filterSubBlacklist: 'Blocks listed numbers',
    filterSubWhitelist: 'Only listed numbers ring',

    integrityTitle: 'Integrity and trust',
    integrityModule: 'Module integrity',
    integritySystem: 'System integrity',
    integritySecureBoot: 'Secure boot',
    integrityFirmware: 'Firmware signature',
    integrityPending: 'Not wired',
    integrityNote:
      'None of these has a backend. There is no TPM, no secure boot, no risk score, and nothing anywhere checks a firmware signature — so no tick lights up here until one does.',
    integrityNoteShort: 'No backend yet, so no ticks',
    trustTitle: 'Device trust',
    trustPending: 'Not assessed',
    trustPendingNote: 'Nothing assesses device trust, so no observation period is shown.',
    trustRemaining: '{d}d {h}h remaining',

    details: 'Diagnostics',
    recheck: 'Re-check now',
    rechecking: 'Checking',
    recheckNote:
      'A re-check re-reads the anti-fraud limit, the egress probe and the filter policy — the things above that actually run.',
    notAvailable: '—'
  }
}

export function moduleSecurityCardLabels(locale: string): ModuleSecurityCardLabels {
  // Traditional Chinese reads the simplified text rather than English: same
  // language, and far better served by zh-CN than by falling back.
  if (locale.startsWith('zh')) return labels['zh-CN']!
  return labels[locale] ?? labels['en-US']!
}
