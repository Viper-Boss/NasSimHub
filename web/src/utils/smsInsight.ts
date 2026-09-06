import type { Message, MessageThread } from '../api/types'

export type SmsCategory = 'code' | 'bank' | 'courier' | 'carrier' | 'promo' | 'normal'
export type SmsInsight = { category: SmsCategory; source: string; code: string }

// Adapted from the user's HomeSIM smsMeta.js. Heuristics describe message text,
// not verified sender identity. Requiring digits avoids treating "code below"
// or "OTP expires" as credentials. Lookarounds prevent truncating longer IDs.
const token = '([A-Z0-9]{4,8})(?![A-Z0-9])'
const codePatterns = [
  new RegExp('(?:验证码|校验码|动态密码|动态码)[：:\\s，,是为]{0,8}' + token, 'i'),
  new RegExp('\\b(?:verification\\s+code|security\\s+code|one[- ]time\\s+(?:code|password)|OTP|code)(?:\\s+is)?[：:\\s-]{1,8}' + token, 'i'),
  /(?<![A-Z0-9])([A-Z0-9]{4,8})(?![A-Z0-9])\s*(?:是您的|为您的|is your)\s*(?:验证码|校验码|verification code|security code|OTP)/i
]

export function analyzeSms(value: string): SmsInsight {
  const text = String(value || '').slice(0, 12000)
  const source = /【([^】\r\n]{1,24})】/.exec(text)?.[1] || ''
  for (const pattern of codePatterns) {
    const code = pattern.exec(text)?.[1] || ''
    if (/\d/.test(code)) return { category: 'code', source, code }
  }
  let category: SmsCategory = 'normal'
  // An opt-out/advertising message is promotional even if it mentions a bank.
  if (/退订|回复\s*TD|优惠券|限时|unsubscribe/i.test(text)) category = 'promo'
  else if (/取件码|快递|快件|驿站|包裹|配送|揽收|顺丰|中通|圆通|菜鸟/.test(text)) category = 'courier'
  else if (/银行|储蓄卡|借记卡|信用卡|账户余额|银行卡/.test(text)) category = 'bank'
  else if (/中国移动|中国联通|中国电信|话费|套餐|流量/.test(text)) category = 'carrier'
  return { category, source, code: '' }
}

export type CodeSuggestion = {
  key: string; threadKey: string; code: string; source: string; timestamp: string; previewOnly: boolean; content: string; peer: string
}

export function collectCodeSuggestions(
  threads: readonly MessageThread[],
  loaded: Record<string, { data: Message[] }>,
  lineFilter = '',
  maximum = 50,
  includePreviews = true
): CodeSuggestion[] {
  const result: CodeSuggestion[] = []
  const seen = new Set<string>()
  for (const thread of threads) {
    if (lineFilter && lineFilter !== 'all' && thread.line_id !== lineFilter) continue
    const messages = loaded[thread.key]?.data || []
    const add = (id: string, content: string, timestamp: string, previewOnly: boolean) => {
      const insight = analyzeSms(content)
      if (!insight.code || !Number.isFinite(Date.parse(timestamp))) return
      const key = JSON.stringify([thread.key, id])
      if (seen.has(key)) return
      seen.add(key)
      result.push({ key, threadKey: thread.key, code: insight.code, source: insight.source || thread.contact_name || thread.peer, timestamp, previewOnly, content, peer: thread.peer })
    }
    for (const message of messages) {
      if (message.direction === 'incoming') add(message.id, message.content, message.timestamp, false)
    }
    // The summary contract has no direction. Keep such suggestions explicitly
    // labelled as previews; once loaded, never expose a known outgoing message.
    if (includePreviews && !messages.some(message => message.id === thread.last_message_id)) {
      add(thread.last_message_id, thread.last_content || '', thread.last_timestamp, true)
    }
  }
  return result.sort((a, b) => Date.parse(b.timestamp) - Date.parse(a.timestamp)).slice(0, maximum)
}

const signatureAliases: Record<string, string> = {
  '中国建设银行': '建设银行', '建行': '建设银行', '建设银行': '建设银行',
  '中信银行信用卡': '中信银行', '中信银行': '中信银行',
  '中国工商银行': '工商银行', '工商银行': '工商银行', '工行': '工商银行',
  '中国农业银行': '农业银行', '农业银行': '农业银行', '农行': '农业银行',
  '招商银行信用卡': '招商银行', '招商银行': '招商银行'
}

export function smsSignature(content: string): string {
  const source = analyzeSms(content).source.trim()
  return signatureAliases[source] || source
}

// A reversible view filter, not a contact merge or authenticated identity.
export function signatureGroups(threads: readonly MessageThread[]) {
  const groups = new Map<string, { source: string; keys: string[]; unread: number }>()
  for (const thread of threads) {
    const source = smsSignature(thread.last_content || '')
    if (!source) continue
    const group = groups.get(source) || { source, keys: [], unread: 0 }
    group.keys.push(thread.key)
    group.unread += thread.unread_count || (thread.marked_unread ? 1 : 0)
    groups.set(source, group)
  }
  return [...groups.values()]
}
