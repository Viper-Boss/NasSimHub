// Copy for the outbound-message guard, kept out of the startup locale bundle.
//
// It renders in one place, inside a view that loads as its own chunk, and only
// on the rare occasion the guard actually trips. Shipping it in the payload
// every user downloads before the app has drawn anything would be paying for it
// on every page load to show it almost never.
//
// English and Chinese only, matching how the rest of this feature's copy is
// carried: the other locales fall back to English there too.

export interface MessageGuardMessages {
  smsTitle: string
  smsBody: string
  smsDetail: string
  unlockHint: string
}

const english: MessageGuardMessages = {
  smsTitle: 'Why was this message blocked?',
  smsBody:
    'Same reason as calling. This software is for receiving verification codes and ' +
    'keeping in touch from abroad, not for sending messages to strangers in bulk.',
  smsDetail:
    'Outgoing messages are limited to 3 new recipients per day. Texting someone you ' +
    'have already messaged today is never restricted, so an ordinary conversation ' +
    'never runs into this.',
  unlockHint:
    'If this is your own line and you know what you are doing, type "unlock" on the ' +
    'dial pad to lift both limits.'
}

const chinese: MessageGuardMessages = {
  smsTitle: '这条短信为什么被拦下？',
  smsBody: '和打电话一个道理。这个软件是用来收验证码、和家里人保持联系的，不是用来群发短信给陌生人的。',
  smsDetail: '每天最多给 3 个新号码发短信；给今天已经发过的号码继续发不受任何限制，所以正常聊天永远碰不到这个上限。',
  unlockHint: '如果这是你自己的卡、你清楚自己在做什么，在拨号盘输入 "unlock" 可以同时解除这两个限制。'
}

export function messageGuardMessages(locale: string): MessageGuardMessages {
  return locale.startsWith('zh') ? chinese : english
}
