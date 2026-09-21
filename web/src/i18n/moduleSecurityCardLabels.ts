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
    guardWhatShort: '算的是不同号码个数，不是次数。超限返回 403。',
    guardCalls: '外呼',
    guardMessages: '发短信',
    guardLimit: '每日上限',
    guardNumbers: '{n} 个号码',
    guardUsed: '今日已用',
    guardUsedPending: '待接入',
    guardUsedNote: '网关里有这个计数（call_guard.json / message_guard.json），但还没有接口把它读出来，所以这里先空着。',
    guardDefaultNote: '上限是出厂默认值，不是从这台网关读回来的。',
    guardUnlocked: '限额已解除',
    guardUnlockedNote: '管理员已经解除了这台设备的反诈限额，现在没有任何数量限制。这是这张卡上最需要留意的一行。',
    guardLocked: '限额生效中',

    // Egress. Real today.
    egressTitle: '网络出口',
    egressIP: '出口 IP',
    egressISP: '运营商',
    egressPending: '读取中',
    egressUnavailable: '取不到',
    egressNote: '这是这台网关在公网上的出口地址，由 NAS 侧探测得到。不是地理位置——系统里没有任何一处在做网关定位。',
    egressNoteShort: '公网出口地址，不是地理位置。',
    egressRefresh: '重新探测',

    // Call filter. Real today.
    filterTitle: '来电过滤',
    filterMode: '模式',
    filterOff: '未启用',
    filterBlacklist: '黑名单',
    filterWhitelist: '白名单',
    filterEntries: '名单条目',
    filterEntriesValue: '{n} 条',
    filterMinReports: '标记阈值',
    filterMinReportsValue: '{n} 次举报',
    filterNotReady: '规则已存，执行未接',
    filterNotReadyNote: '过滤规则已经保存在网关里，但真正执行它需要一个还没接上的来电标记源，所以现在只是策略不是拦截。',
    filterUnavailable: '读不到过滤策略',

    // Integrity. None of it exists.
    integrityTitle: '设备完整性与信任',
    integrityModule: '模组完整性',
    integritySystem: '系统完整性',
    integritySecureBoot: '安全启动',
    integrityFirmware: '固件签名',
    integrityPending: '待接入',
    integrityNote: '这四项都还没有后端。系统里现在没有 TPM、没有安全启动、也没有任何一处在验固件签名——所以这里一个绿勾都不会亮，等做好了才会。',

    integrityNoteShort: '四项都还没有后端，所以不会有绿勾。',
    trustTitle: '设备信任',
    trustPending: '未评估',
    trustRemaining: '剩余 {d} 天 {h} 小时',

    // Footer.
    details: '诊断详情',
    recheck: '立即复检',
    rechecking: '复检中',
    recheckNote: '复检只会重新跑上面那些真的在跑的检查：出口探测和过滤策略。',
    adminOnly: '只有管理员能看到这张卡。',
    pending: '待接入',
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
    guardWhatShort: 'Counts distinct numbers, not attempts. Over the cap the API returns 403.',
    guardCalls: 'Outbound calls',
    guardMessages: 'Outbound SMS',
    guardLimit: 'Daily cap',
    guardNumbers: '{n} numbers',
    guardUsed: 'Used today',
    guardUsedPending: 'Not wired yet',
    guardUsedNote:
      'The gateway keeps this count (call_guard.json / message_guard.json) but no endpoint reads it out yet, so it is left blank rather than guessed.',
    guardDefaultNote: 'The cap is the shipped default, not a value read back from this gateway.',
    guardUnlocked: 'Limit lifted',
    guardUnlockedNote:
      'An administrator has lifted the anti-fraud limit on this device, so nothing caps the number of destinations. This is the line on this card worth noticing.',
    guardLocked: 'Limit in force',

    egressTitle: 'Network egress',
    egressIP: 'Public IP',
    egressISP: 'ISP',
    egressPending: 'Reading',
    egressUnavailable: 'Unavailable',
    egressNote:
      'The address this gateway presents on the public internet, probed from the NAS side. Not a location — nothing in the system geolocates the gateway.',
    egressNoteShort: 'The public egress address, not a location.',
    egressRefresh: 'Probe again',

    filterTitle: 'Inbound call filter',
    filterMode: 'Mode',
    filterOff: 'Off',
    filterBlacklist: 'Blocklist',
    filterWhitelist: 'Allowlist',
    filterEntries: 'Entries',
    filterEntriesValue: '{n}',
    filterMinReports: 'Report threshold',
    filterMinReportsValue: '{n} reports',
    filterNotReady: 'Policy stored, enforcement not connected',
    filterNotReadyNote:
      'The rules are saved on the gateway, but enforcing them needs a call-tagging source that is not connected yet, so this is policy rather than blocking.',
    filterUnavailable: 'Filter policy unavailable',

    integrityTitle: 'Device integrity and trust',
    integrityModule: 'Module integrity',
    integritySystem: 'System integrity',
    integritySecureBoot: 'Secure boot',
    integrityFirmware: 'Firmware signature',
    integrityPending: 'Not wired yet',
    integrityNote:
      'None of these four has a backend. There is no TPM, no secure boot, and nothing anywhere verifies a firmware signature — so no tick lights up here until one does.',

    integrityNoteShort: 'None of the four has a backend, so no tick lights up.',
    trustTitle: 'Device trust',
    trustPending: 'Not assessed',
    trustRemaining: '{d}d {h}h remaining',

    details: 'Diagnostics',
    recheck: 'Re-check now',
    rechecking: 'Checking',
    recheckNote:
      'A re-check re-runs only the checks that actually run: the egress probe and the filter policy.',
    adminOnly: 'Only an administrator can see this card.',
    pending: 'Not wired yet',
    notAvailable: '—'
  }
}

export function moduleSecurityCardLabels(locale: string): ModuleSecurityCardLabels {
  // Traditional Chinese reads the simplified text rather than English: same
  // language, and far better served by zh-CN than by falling back.
  if (locale.startsWith('zh')) return labels['zh-CN']!
  return labels[locale] ?? labels['en-US']!
}
