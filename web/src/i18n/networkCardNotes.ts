// The two network cards' explanatory paragraphs, kept out of the startup locale.
//
// Both are prose in nine languages that renders in one card on the dashboard.
// That is the shape of string that costs every visitor bytes before the app has
// drawn anything, to be read once and understood; the short labels beside them
// stay in the locale files, where they are cheap.
//
// Both notes earn their length. Without them each card is three numbers and a
// picture, and a reader has no way to tell which parts are measured and which
// are scenery -- which is the one thing these cards must not leave ambiguous.

/** What the NKN globe's figures actually count. */
export function globeScopeNote(locale: string): string {
  return globeNotes[locale] ?? globeNotes['en-US']!
}

/** What is lit on the DHT mesh, and what is scenery. */
export function meshNote(locale: string): string {
  return meshNotes[locale] ?? meshNotes['en-US']!
}

const globeNotes: Record<string, string> = {
  "de-DE": "Jede Zahl ist die On-Chain-Abonnentenzahl eines Topics. Die Abonnementdauer ist das Fenster, daher gelten sie netzweit und jeder kann sie über jeden Seed prüfen.",
  "en-US": "Each count is one topic's on-chain subscriber total. The subscription duration is the window, so these are network-wide and anyone can verify them through any seed.",
  "es-ES": "Cada cifra es el total de suscriptores en cadena de un tema. La duración de la suscripción es la ventana, así que son de toda la red y cualquiera puede verificarlas desde cualquier semilla.",
  "fr-FR": "Chaque chiffre est le total des abonnés on-chain d'un sujet. La durée de l'abonnement est la fenêtre : ces chiffres valent pour tout le réseau et chacun peut les vérifier via n'importe quel seed.",
  "ja-JP": "各数値はトピックごとのオンチェーン購読者数です。購読期間がそのまま集計期間なので、ネットワーク全体の値であり、誰でも任意のシードノードから検証できます。",
  "pt-BR": "Cada número é o total de assinantes on-chain de um tópico. A duração da assinatura é a janela, então valem para toda a rede e qualquer um pode verificar por qualquer seed.",
  "vi-VN": "Mỗi con số là tổng người đăng ký on-chain của một chủ đề. Thời hạn đăng ký chính là cửa sổ thống kê, nên đây là số liệu toàn mạng và ai cũng kiểm chứng được qua bất kỳ seed nào.",
  "zh-CN": "每个数字都是某个 topic 的链上订阅者总数。订阅时长就是统计窗口，所以是全网口径，任何人用任何种子节点都能验证。",
  "zh-TW": "每個數字都是某個 topic 的鏈上訂閱者總數。訂閱時長就是統計視窗，所以是全網口徑，任何人用任何種子節點都能驗證。"
}

const meshNotes: Record<string, string> = {
  'en-US':
    'Lit nodes are the peers this gateway has actually verified; the dim ones ' +
    'stand for the rest of the DHT, which it has not mapped. A DHT has no ' +
    'directory, so nobody can count the whole network.',
  'zh-CN':
    '亮起的点是本机真正验证过的节点；暗的点代表 DHT 网络的其余部分，本机并不知道它们是谁。' +
    'DHT 没有名录，谁都数不出全网有多少台。',
  'zh-TW':
    '亮起的點是本機真正驗證過的節點；暗的點代表 DHT 網路的其餘部分，本機並不知道它們是誰。' +
    'DHT 沒有名錄，誰都數不出全網有多少台。',
  'ja-JP':
    '光っている点はこのゲートウェイが実際に検証したピアです。暗い点は把握していない DHT の' +
    '残りを表します。DHT に名簿はなく、全体の台数は誰にも数えられません。',
  'vi-VN':
    'Các điểm sáng là những nút mà thiết bị này đã thực sự xác minh; các điểm mờ ' +
    'đại diện cho phần còn lại của DHT mà nó chưa biết. DHT không có danh bạ, ' +
    'nên không ai đếm được toàn mạng.',
  'es-ES':
    'Los nodos iluminados son los pares que esta pasarela ha verificado de verdad; ' +
    'los apagados representan el resto de la DHT, que no conoce. Una DHT no tiene ' +
    'directorio, así que nadie puede contarla entera.',
  'de-DE':
    'Leuchtende Knoten sind Peers, die dieses Gateway tatsächlich geprüft hat; die ' +
    'matten stehen für den Rest der DHT, den es nicht kennt. Eine DHT hat kein ' +
    'Verzeichnis, niemand kann sie zählen.',
  'fr-FR':
    'Les nœuds allumés sont les pairs que cette passerelle a réellement vérifiés ; ' +
    "les ternes représentent le reste de la DHT, qu'elle ne connaît pas. Une DHT " +
    "n'a pas d'annuaire : personne ne peut la dénombrer.",
  'pt-BR':
    'Os nós acesos são os pares que este gateway realmente verificou; os apagados ' +
    'representam o resto da DHT, que ele não conhece. Uma DHT não tem diretório, ' +
    'então ninguém consegue contá-la inteira.'
}
