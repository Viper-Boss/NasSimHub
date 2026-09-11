// Copy for the call forwarding / call waiting panel.
//
// Loaded with the panel rather than the app, like the DHT panel's copy: it is
// a page inside settings that most users open rarely, and paying for its prose
// on every page load to show it seldom is the wrong trade. See
// networkCardNotes.ts for the same reasoning.
//
// The wording carries real weight here, more than in most panels. Forwarding
// is the one setting where a misunderstanding means missing every call, and
// the state is held by the operator rather than by this software -- so the copy
// has to say that plainly, not bury it.

export type CallServicesMessages = Record<string, string>

const english: CallServicesMessages = {
  title: 'Call forwarding and call waiting',
  subtitle:
    'These live in your operator’s network, not on this gateway. Anything with the same SIM ' +
    'in it — a phone, the operator’s app, a *21# code — changes the same settings, so ' +
    'this page reads them fresh every time instead of keeping its own copy.',
  slowNote: 'Each read and each change is a round trip to your operator, so it takes a few seconds.',
  reading: 'Asking the network…',
  applying: 'Sending to the network…',
  refresh: 'Read again',
  lineLabel: 'Line',
  observed: 'Read at {time}',

  forwardingTitle: 'Forward incoming calls',
  unconditional: 'Always',
  unconditionalHint: 'Every call goes to the other number. This gateway never rings.',
  busy: 'When this line is busy',
  busyHint: 'Only when a call is already in progress here.',
  no_reply: 'When nobody answers',
  no_replyHint: 'After ringing here for the delay below.',
  unreachable: 'When this gateway is unreachable',
  unreachableHint:
    'Powered off, no signal, or the broadband is down. This is the one worth setting on a ' +
    'gateway at home — it is what covers the outages you are not there for.',
  destination: 'Forward to',
  destinationPlaceholder: 'e.g. +8613800138000',
  delay: 'Ring here first for',
  seconds: '{count}s',
  save: 'Apply',
  clear: 'Turn off',
  off: 'Off',
  activeTo: 'Forwarding to {number}',
  activeNoNumber: 'Active, but the network did not say where to',
  unreadable: 'The network would not report this one',

  waitingTitle: 'Call waiting',
  waitingHint:
    'Lets a second caller through while you are already on a call, instead of hearing engaged. ' +
    'With it off, a second caller is told the line is busy.',
  waitingOn: 'On',
  waitingOff: 'Off',
  waitingUnknown: 'The modem did not report this',

  unavailable: 'Not available on this build',
  unavailableHint:
    'The agent on this gateway does not offer supplementary services. Nothing is wrong with ' +
    'your SIM or your network.',
  noLine: 'No module is fitted, so there is no line to configure.',
  networkError: 'The network did not answer: {error}',
  numberRequired: 'Enter a number to forward to.',
  numberInvalid: 'A number can only contain digits and + * #.'
}

const chinese: CallServicesMessages = {
  title: '呼叫转移与呼叫等待',
  subtitle:
    '这两项设置在运营商网络里，不在这台网关上。任何插着同一张卡的设备——手机、运营商 App、' +
    '拨 *21# ——改的都是同一份设置，所以这个页面每次都去重新读取，不保存本地副本。',
  slowNote: '每次读取和每次修改都要往运营商那里走一趟，会等几秒。',
  reading: '正在询问网络…',
  applying: '正在下发到网络…',
  refresh: '重新读取',
  lineLabel: '线路',
  observed: '读取于 {time}',

  forwardingTitle: '来电转移',
  unconditional: '无条件转移',
  unconditionalHint: '所有来电都转到另一个号码，这台网关不会响。',
  busy: '遇忙时转移',
  busyHint: '仅当这边已经在通话中时。',
  no_reply: '无应答时转移',
  no_replyHint: '在这边响铃到下面设定的时长之后。',
  unreachable: '无法接通时转移',
  unreachableHint:
    '断电、没信号、或者宽带断了。放在家里的网关，最值得设的就是这一条——它覆盖的正是你不在场的那些故障。',
  destination: '转移到',
  destinationPlaceholder: '例如 +8613800138000',
  delay: '先在本机响铃',
  seconds: '{count} 秒',
  save: '下发',
  clear: '关闭',
  off: '未开启',
  activeTo: '正在转移到 {number}',
  activeNoNumber: '已开启，但网络没有返回转移到哪里',
  unreadable: '网络拒绝报告这一条',

  waitingTitle: '呼叫等待',
  waitingHint:
    '通话中再有人打进来时能插入进来，而不是听到占线。关闭后，第二个来电会被告知线路忙。',
  waitingOn: '已开启',
  waitingOff: '已关闭',
  waitingUnknown: '模组没有报告这一项',

  unavailable: '当前版本不支持',
  unavailableHint: '这台网关上的 agent 没有提供补充业务能力。不是你的卡或网络的问题。',
  noLine: '没有插入模组，没有可配置的线路。',
  networkError: '网络没有应答：{error}',
  numberRequired: '请填写要转移到的号码。',
  numberInvalid: '号码只能包含数字和 + * #。'
}

const translations: Record<string, CallServicesMessages> = {
  'en-US': english,
  'zh-CN': chinese,
  'zh-TW': chinese
}

/** Panel copy for a locale, falling back to English for anything unlisted. */
export function callServicesMessages(locale: string): CallServicesMessages {
  if (translations[locale]) return translations[locale]!
  // Chinese variants share the simplified copy rather than falling all the way
  // back to English, which is a worse answer for a reader of either.
  if (locale.startsWith('zh')) return chinese
  return english
}
