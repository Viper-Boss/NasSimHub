// The two network cards' prose and labels, kept out of the startup locale.
//
// Everything in this module renders only on the dashboard, inside the two
// network cards, which load lazily -- and it is all nine languages deep. In the
// locale files it would cost every visitor bytes before the app had drawn
// anything; here it arrives with the cards that use it. The startup budget is
// what decides where a string goes, and it noticed the moment these moved the
// wrong way.
//
// The notes earn their length. Without them each card is three numbers and a
// picture, and a reader has no way to tell which parts are measured and which
// are illustration -- which is the one thing these cards must not leave
// ambiguous.
//
// The Chinese wording is the operator's own, from the reference build of these
// cards. ONE CLAUSE IS CHANGED, and it is worth recording why. The reference
// read "bright dots are active connections, dim dots are neighbouring nodes",
// which is a claim about each dot -- and in the reference's own code (and in
// ours, which keeps its drawing) the bright/dim split is decoration: a handful
// start dim and one node changes every four and a half seconds, on a timer,
// with nothing behind it. Its README says so. So the sentence now says what the
// drawing actually is, and adds the one thing on that card that IS measured:
// the amber beads, which only travel while the DHT is genuinely ready.

/** What the NKN globe's figures actually count. */
export function globeScopeNote(locale: string): string {
  return globeNotes[locale] ?? globeNotes['en-US']!
}

/** What is measured on the DHT net, and what is decoration. */
export function meshNote(locale: string): string {
  return meshNotes[locale] ?? meshNotes['en-US']!
}

/**
 * What a screen reader is told each drawing is.
 *
 * These describe the drawing AS a drawing, which matters: the alternative text
 * for an illustration should say what the picture looks like, not restate
 * figures the reader can already reach in the list underneath it.
 */
export function globeCanvasLabel(locale: string): string {
  return globeLabels[locale] ?? globeLabels['en-US']!
}

export function meshCanvasLabel(locale: string): string {
  return meshLabels[locale] ?? meshLabels['en-US']!
}

const globeNotes: Record<string, string> = {
  'zh-CN': '共享 Topic 中的近期活跃身份数。统计窗口以订阅与续期策略为准，不代表实时在线数。',
  'zh-TW': '共享 Topic 中的近期活躍身分數。統計視窗以訂閱與續期策略為準，不代表即時在線數。',
  'en-US':
    'The count of recently active identities in a shared topic. The window follows the ' +
    'subscription and renewal policy, so it is not a live count of who is online.',
  'ja-JP':
    '共有トピック内で最近アクティブだった識別子の数です。集計期間は購読と更新の方針に' +
    '従うため、リアルタイムのオンライン数ではありません。',
  'vi-VN':
    'Số danh tính hoạt động gần đây trong một topic dùng chung. Cửa sổ thống kê theo ' +
    'chính sách đăng ký và gia hạn, nên đây không phải số đang trực tuyến.',
  'es-ES':
    'Número de identidades activas recientemente en un topic compartido. La ventana ' +
    'sigue la política de suscripción y renovación, así que no es un recuento en vivo.',
  'de-DE':
    'Die Zahl der kürzlich aktiven Identitäten in einem gemeinsamen Topic. Das Fenster ' +
    'folgt der Abo- und Verlängerungspolitik und ist kein Live-Onlinestand.',
  'fr-FR':
    "Le nombre d'identités récemment actives dans un topic partagé. La fenêtre suit la " +
    "politique d'abonnement et de renouvellement : ce n'est pas un décompte en direct.",
  'pt-BR':
    'O número de identidades ativas recentemente em um topic compartilhado. A janela ' +
    'segue a política de assinatura e renovação, então não é uma contagem ao vivo.'
}

const meshNotes: Record<string, string> = {
  'zh-CN':
    '动画为局部拓扑示意，节点的明暗与漂移都是装饰，不代表全网规模或真实地理位置。' +
    '黄色信号只在 DHT 就绪时出现；三个数字来自本机路由表。',
  'zh-TW':
    '動畫為局部拓撲示意，節點的明暗與漂移都是裝飾，不代表全網規模或真實地理位置。' +
    '黃色信號只在 DHT 就緒時出現；三個數字來自本機路由表。',
  'en-US':
    'The animation illustrates a local topology. Node brightness and drift are ' +
    'decorative and stand for neither network size nor real locations. The amber ' +
    'signals appear only while the DHT is ready; the three figures are this ' +
    "gateway's own routing table.",
  'ja-JP':
    'アニメーションは局所的なトポロジーの概念図です。ノードの明暗と揺れは装飾で、' +
    'ネットワーク規模や実際の位置を表しません。黄色の信号は DHT が準備できている間だけ' +
    '現れ、3 つの数値はこのゲートウェイ自身のルーティングテーブルです。',
  'vi-VN':
    'Hoạt ảnh minh hoạ một cấu trúc cục bộ. Độ sáng và dao động của các nút chỉ là ' +
    'trang trí, không thể hiện quy mô mạng hay vị trí thật. Tín hiệu vàng chỉ xuất ' +
    'hiện khi DHT đã sẵn sàng; ba con số là bảng định tuyến của chính thiết bị này.',
  'es-ES':
    'La animación ilustra una topología local. El brillo y la deriva de los nodos son ' +
    'decorativos y no representan el tamaño de la red ni ubicaciones reales. Las ' +
    'señales ámbar solo aparecen cuando la DHT está lista; las tres cifras son la ' +
    'tabla de rutas de esta pasarela.',
  'de-DE':
    'Die Animation zeigt eine lokale Topologie. Helligkeit und Drift der Knoten sind ' +
    'Dekoration und stehen weder für die Netzgröße noch für echte Orte. Die gelben ' +
    'Signale erscheinen nur, wenn die DHT bereit ist; die drei Zahlen sind die ' +
    'Routingtabelle dieses Gateways.',
  'fr-FR':
    "L'animation illustre une topologie locale. La luminosité et la dérive des nœuds " +
    "sont décoratives et ne représentent ni la taille du réseau ni des lieux réels. " +
    "Les signaux ambrés n'apparaissent que lorsque la DHT est prête ; les trois " +
    "chiffres sont la table de routage de cette passerelle.",
  'pt-BR':
    'A animação ilustra uma topologia local. O brilho e a deriva dos nós são ' +
    'decorativos e não representam o tamanho da rede nem locais reais. Os sinais ' +
    'âmbar só aparecem quando a DHT está pronta; os três números são a tabela de ' +
    'rotas deste gateway.'
}

const globeLabels: Record<string, string> = {
  'zh-CN': '缓慢旋转的蓝色点阵地球，表示 NKN 全球连接',
  'zh-TW': '緩慢旋轉的藍色點陣地球，表示 NKN 全球連線',
  'en-US': 'A slowly turning globe of blue dots, standing for NKN worldwide connectivity',
  'ja-JP': 'ゆっくり回転する青い点の地球。NKN の世界規模の接続を表します',
  'vi-VN': 'Quả cầu điểm xanh quay chậm, biểu thị kết nối toàn cầu của NKN',
  'es-ES': 'Un globo de puntos azules que gira despacio, en representación de la conectividad global de NKN',
  'de-DE': 'Ein langsam drehender Globus aus blauen Punkten, für die weltweite NKN-Anbindung',
  'fr-FR': 'Un globe de points bleus tournant lentement, représentant la connectivité mondiale de NKN',
  'pt-BR': 'Um globo de pontos azuis girando devagar, representando a conectividade global da NKN'
}

const meshLabels: Record<string, string> = {
  'zh-CN': '铺满区域的 DHT 鱼网，节点缓慢漂移，黄色信号沿连线传递',
  'zh-TW': '鋪滿區域的 DHT 魚網，節點緩慢漂移，黃色信號沿連線傳遞',
  'en-US': 'A DHT net filling the panel, its nodes drifting slowly while amber signals travel the strands',
  'ja-JP': '画面を埋める DHT の網。ノードがゆっくり漂い、黄色の信号が線上を進みます',
  'vi-VN': 'Lưới DHT phủ kín khung, các nút trôi chậm và tín hiệu vàng chạy trên các sợi',
  'es-ES': 'Una red DHT que cubre el panel, con nodos a la deriva y señales ámbar recorriendo los hilos',
  'de-DE': 'Ein DHT-Netz über die ganze Fläche, dessen Knoten langsam treiben, während gelbe Signale die Fäden entlanglaufen',
  'fr-FR': 'Un filet DHT qui remplit le panneau, ses nœuds dérivant lentement tandis que des signaux ambrés parcourent les brins',
  'pt-BR': 'Uma rede DHT preenchendo o painel, com nós à deriva e sinais âmbar percorrendo os fios'
}



/**
 * The two cards' short labels.
 *
 * These are badges, row names and tile captions -- ordinarily the shape of
 * string that belongs in the locale files, where short strings are cheap. They
 * are here instead because there are twenty of them and NONE of them is
 * rendered anywhere but these two cards, which load with the dashboard: in the
 * locale files they cost every visitor bytes before the app has drawn anything,
 * and the startup budget noticed immediately.
 *
 * Returned as one object so the card merges them in a single call rather than
 * twenty.
 */
export function networkCardLabels(locale: string): Record<string, string> {
  return labels[locale] ?? labels['en-US']!
}

const labels: Record<string, Record<string, string>> = {
  'zh-CN': {
    globeBadgeOn: '已连接',
    globeBadgeOff: '未连接',
    globeVisualNote: '全球连接 · 持续在线',
    globeVisualNoteOff: '全球节点示意',
    globePathName: 'NKN 主通路',
    globePathOk: '连接正常',
    globePathWaiting: '等待连接',
    globeSecondName: 'DHT 第二通路',
    globeSecondReady: '可用 · 自动协同',
    globeActive24h: '24小时活跃',
    globeActive30d: '30天活跃',
    globeActive90d: '90天活跃',
    meshBadgeHealthy: '网络健康',
    meshBadgeOff: '已关闭',
    meshVisualNote: '局部连接示意',
    meshSignalNote: '路由信号',
    meshDiscoveryName: '节点发现',
    meshDiscoveryOn: '持续发现邻居',
    meshDiscoveryOff: '节点发现已暂停',
    meshSecondName: '第二通路',
    meshSecondOn: '已启用',
  },
  'zh-TW': {
    globeBadgeOn: '已連線',
    globeBadgeOff: '未連線',
    globeVisualNote: '全球連線 · 持續在線',
    globeVisualNoteOff: '全球節點示意',
    globePathName: 'NKN 主通路',
    globePathOk: '連線正常',
    globePathWaiting: '等待連線',
    globeSecondName: 'DHT 第二通路',
    globeSecondReady: '可用 · 自動協同',
    globeActive24h: '24小時活躍',
    globeActive30d: '30天活躍',
    globeActive90d: '90天活躍',
    meshBadgeHealthy: '網路健康',
    meshBadgeOff: '已關閉',
    meshVisualNote: '局部連線示意',
    meshSignalNote: '路由信號',
    meshDiscoveryName: '節點發現',
    meshDiscoveryOn: '持續發現鄰居',
    meshDiscoveryOff: '節點發現已暫停',
    meshSecondName: '第二通路',
    meshSecondOn: '已啟用',
  },
  'en-US': {
    globeBadgeOn: 'Connected',
    globeBadgeOff: 'Not connected',
    globeVisualNote: 'Worldwide · always on',
    globeVisualNoteOff: 'Worldwide nodes, illustrative',
    globePathName: 'NKN main path',
    globePathOk: 'Connected',
    globePathWaiting: 'Waiting to connect',
    globeSecondName: 'DHT second path',
    globeSecondReady: 'Available · working alongside',
    globeActive24h: 'Active in 24h',
    globeActive30d: 'Active in 30d',
    globeActive90d: 'Active in 90d',
    meshBadgeHealthy: 'Mesh healthy',
    meshBadgeOff: 'Off',
    meshVisualNote: 'Local topology, illustrative',
    meshSignalNote: 'Routing signal',
    meshDiscoveryName: 'Peer discovery',
    meshDiscoveryOn: 'Finding neighbours',
    meshDiscoveryOff: 'Discovery paused',
    meshSecondName: 'Second path',
    meshSecondOn: 'Enabled',
  },
  'ja-JP': {
    globeBadgeOn: '接続済み',
    globeBadgeOff: '未接続',
    globeVisualNote: '世界中と接続 · 常時オンライン',
    globeVisualNoteOff: '世界のノードの概念図',
    globePathName: 'NKN 主経路',
    globePathOk: '接続は正常',
    globePathWaiting: '接続待ち',
    globeSecondName: 'DHT 第二経路',
    globeSecondReady: '利用可能 · 自動で連携',
    globeActive24h: '24時間の活動',
    globeActive30d: '30日の活動',
    globeActive90d: '90日の活動',
    meshBadgeHealthy: 'メッシュは良好',
    meshBadgeOff: 'オフ',
    meshVisualNote: '局所トポロジーの概念図',
    meshSignalNote: 'ルーティング信号',
    meshDiscoveryName: 'ノード探索',
    meshDiscoveryOn: '隣接ノードを探索中',
    meshDiscoveryOff: '探索は停止中',
    meshSecondName: '第二経路',
    meshSecondOn: '有効',
  },
  'vi-VN': {
    globeBadgeOn: 'Đã kết nối',
    globeBadgeOff: 'Chưa kết nối',
    globeVisualNote: 'Toàn cầu · luôn trực tuyến',
    globeVisualNoteOff: 'Sơ đồ các nút toàn cầu',
    globePathName: 'Đường chính NKN',
    globePathOk: 'Kết nối bình thường',
    globePathWaiting: 'Đang chờ kết nối',
    globeSecondName: 'Đường thứ hai DHT',
    globeSecondReady: 'Khả dụng · tự phối hợp',
    globeActive24h: 'Hoạt động 24h',
    globeActive30d: 'Hoạt động 30 ngày',
    globeActive90d: 'Hoạt động 90 ngày',
    meshBadgeHealthy: 'Mạng lưới tốt',
    meshBadgeOff: 'Đã tắt',
    meshVisualNote: 'Sơ đồ cấu trúc cục bộ',
    meshSignalNote: 'Tín hiệu định tuyến',
    meshDiscoveryName: 'Phát hiện nút',
    meshDiscoveryOn: 'Đang tìm nút lân cận',
    meshDiscoveryOff: 'Đã tạm dừng tìm kiếm',
    meshSecondName: 'Đường thứ hai',
    meshSecondOn: 'Đã bật',
  },
  'es-ES': {
    globeBadgeOn: 'Conectado',
    globeBadgeOff: 'Sin conexión',
    globeVisualNote: 'Todo el mundo · siempre activo',
    globeVisualNoteOff: 'Nodos globales, ilustrativo',
    globePathName: 'Vía principal NKN',
    globePathOk: 'Conexión correcta',
    globePathWaiting: 'Esperando conexión',
    globeSecondName: 'Segunda vía DHT',
    globeSecondReady: 'Disponible · en paralelo',
    globeActive24h: 'Activos en 24 h',
    globeActive30d: 'Activos en 30 d',
    globeActive90d: 'Activos en 90 d',
    meshBadgeHealthy: 'Malla sana',
    meshBadgeOff: 'Apagada',
    meshVisualNote: 'Topología local, ilustrativa',
    meshSignalNote: 'Señal de rutas',
    meshDiscoveryName: 'Descubrimiento',
    meshDiscoveryOn: 'Buscando vecinos',
    meshDiscoveryOff: 'Descubrimiento en pausa',
    meshSecondName: 'Segunda vía',
    meshSecondOn: 'Activada',
  },
  'de-DE': {
    globeBadgeOn: 'Verbunden',
    globeBadgeOff: 'Nicht verbunden',
    globeVisualNote: 'Weltweit · immer online',
    globeVisualNoteOff: 'Weltweite Knoten, schematisch',
    globePathName: 'NKN Hauptweg',
    globePathOk: 'Verbindung normal',
    globePathWaiting: 'Warte auf Verbindung',
    globeSecondName: 'DHT Zweitweg',
    globeSecondReady: 'Verfügbar · arbeitet mit',
    globeActive24h: 'Aktiv in 24 h',
    globeActive30d: 'Aktiv in 30 T',
    globeActive90d: 'Aktiv in 90 T',
    meshBadgeHealthy: 'Netz gesund',
    meshBadgeOff: 'Aus',
    meshVisualNote: 'Lokale Topologie, schematisch',
    meshSignalNote: 'Routing-Signal',
    meshDiscoveryName: 'Knotensuche',
    meshDiscoveryOn: 'Sucht Nachbarn',
    meshDiscoveryOff: 'Suche angehalten',
    meshSecondName: 'Zweitweg',
    meshSecondOn: 'Aktiviert',
  },
  'fr-FR': {
    globeBadgeOn: 'Connecté',
    globeBadgeOff: 'Non connecté',
    globeVisualNote: 'Partout dans le monde · toujours en ligne',
    globeVisualNoteOff: 'Nœuds mondiaux, schématique',
    globePathName: 'Voie principale NKN',
    globePathOk: 'Connexion normale',
    globePathWaiting: 'En attente de connexion',
    globeSecondName: 'Deuxième voie DHT',
    globeSecondReady: 'Disponible · en complément',
    globeActive24h: 'Actifs sur 24 h',
    globeActive30d: 'Actifs sur 30 j',
    globeActive90d: 'Actifs sur 90 j',
    meshBadgeHealthy: 'Maillage sain',
    meshBadgeOff: 'Désactivé',
    meshVisualNote: 'Topologie locale, schématique',
    meshSignalNote: 'Signal de routage',
    meshDiscoveryName: 'Découverte',
    meshDiscoveryOn: 'Recherche de voisins',
    meshDiscoveryOff: 'Découverte en pause',
    meshSecondName: 'Deuxième voie',
    meshSecondOn: 'Activée',
  },
  'pt-BR': {
    globeBadgeOn: 'Conectado',
    globeBadgeOff: 'Sem conexão',
    globeVisualNote: 'No mundo todo · sempre on-line',
    globeVisualNoteOff: 'Nós globais, ilustrativo',
    globePathName: 'Via principal NKN',
    globePathOk: 'Conexão normal',
    globePathWaiting: 'Aguardando conexão',
    globeSecondName: 'Segunda via DHT',
    globeSecondReady: 'Disponível · atuando junto',
    globeActive24h: 'Ativos em 24 h',
    globeActive30d: 'Ativos em 30 d',
    globeActive90d: 'Ativos em 90 d',
    meshBadgeHealthy: 'Malha saudável',
    meshBadgeOff: 'Desligada',
    meshVisualNote: 'Topologia local, ilustrativa',
    meshSignalNote: 'Sinal de rotas',
    meshDiscoveryName: 'Descoberta de nós',
    meshDiscoveryOn: 'Procurando vizinhos',
    meshDiscoveryOff: 'Descoberta pausada',
    meshSecondName: 'Segunda via',
    meshSecondOn: 'Ativada',
  },
}
