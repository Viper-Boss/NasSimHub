// What a hidden command says when it fires.
//
// Every one of these confirmations has the same job: name what just appeared
// AND where to find it. A reveal that says only "unlocked" leaves the operator
// hunting, which is how the WireGuard traffic card came to be reported as
// broken when it had in fact been revealed -- it sits at the foot of the home
// page, below two tall cards, and nothing said so.
//
// Two languages, matching the rest of this app's operator-facing copy, which is
// English and Chinese and falls back to English elsewhere. Loaded on demand
// when a command actually fires, so none of it sits in the startup bundle.

export type HiddenCommandMessages = Record<string, string>

const messages: Record<string, HiddenCommandMessages> = {
  'en-US': {
    wireguardAll:
      'WireGuard revealed: the traffic card at the foot of the home page, and Settings → WireGuard.',
    wireguardCard: 'WireGuard traffic card revealed, at the foot of the home page.',
    dht: 'DHT revealed: Settings → DHT, and the state badge in the header.',
    nkn: 'NKN revealed: Settings → NKN node and Settings → NKN network.'
  },
  'zh-CN': {
    wireguardAll: '已显示 WireGuard：首页底部的流量卡，以及「设置 → WireGuard」面板。',
    wireguardCard: '已显示 WireGuard 流量卡，在首页底部。',
    dht: '已显示 DHT：「设置 → DHT」面板，以及顶栏的状态指示。',
    nkn: '已显示 NKN：「设置 → NKN 节点」和「设置 → NKN 网络」两个面板。'
  }
}

export function hiddenCommandMessages(locale: string): HiddenCommandMessages {
  if (locale.startsWith('zh')) return messages['zh-CN']!
  return messages['en-US']!
}
