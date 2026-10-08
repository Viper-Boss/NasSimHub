import type { DevicePolicyOutcome, EvidenceState } from '../api/nodeSecurity'

type Copy = {
  title: string; checked: string; delivery: string; hardware: string; local: string
  outcomes: Record<DevicePolicyOutcome, string>; evidence: Record<EvidenceState, string>
}
// Keep all nine supported languages explicit; these describe device evidence,
// never a promise that a NAS decision has already been applied.
const rows: Record<string, readonly string[]> = {
  'zh-CN': ['设备实际执行', '最近读取', '策略下发失败', '换模组检测', '设备本地闸门', '未知', '此版本不支持', '尚未收到策略', '仅记录，不拦截', '重启后等待 NAS 确认', '许可已过期，等待 NAS 确认', '策略有效性待确认', '策略校验失败', '设备与 NAS 策略尚未同步', '已执行 NAS 当前策略', '有设备依据', '不参与判断'],
  'zh-TW': ['裝置實際執行', '最近讀取', '策略下發失敗', '更換模組偵測', '裝置本機閘門', '未知', '此版本不支援', '尚未收到策略', '僅記錄，不攔截', '重新啟動後等待 NAS 確認', '許可已過期，等待 NAS 確認', '策略有效性待確認', '策略驗證失敗', '裝置與 NAS 策略尚未同步', '已執行 NAS 目前策略', '有裝置依據', '不參與判斷'],
  'en-US': ['Device enforcement', 'Last read', 'Policy delivery failed', 'Module replacement detection', 'Device-local gate', 'Unknown', 'Unsupported in this version', 'Policy not received', 'Recorded only, not blocked', 'Restarted; waiting for NAS confirmation', 'Permission expired; waiting for NAS confirmation', 'Policy validity not confirmed', 'Policy verification failed', 'Device policy differs from the NAS', 'Current NAS policy enforced', 'Device evidence available', 'Not used'],
  'de-DE': ['Durchsetzung am Gerät', 'Zuletzt gelesen', 'Richtlinie nicht zugestellt', 'Erkennung eines Modulwechsels', 'Lokale Gerätesperre', 'Unbekannt', 'In dieser Version nicht unterstützt', 'Richtlinie noch nicht empfangen', 'Nur protokolliert, nicht gesperrt', 'Neustart; Bestätigung durch NAS ausstehend', 'Erlaubnis abgelaufen; NAS-Bestätigung ausstehend', 'Gültigkeit der Richtlinie nicht bestätigt', 'Richtlinienprüfung fehlgeschlagen', 'Gerät und NAS haben unterschiedliche Richtlinien', 'Aktuelle NAS-Richtlinie wird durchgesetzt', 'Gerätenachweis vorhanden', 'Nicht verwendet'],
  'es-ES': ['Aplicación en el dispositivo', 'Última lectura', 'Falló el envío de la política', 'Detección de cambio de módulo', 'Control local del dispositivo', 'Desconocido', 'No compatible con esta versión', 'Política aún no recibida', 'Solo se registra; no se bloquea', 'Reiniciado; pendiente de confirmación del NAS', 'Permiso caducado; pendiente del NAS', 'Validez de la política sin confirmar', 'Falló la verificación de la política', 'La política del dispositivo difiere del NAS', 'Política actual del NAS aplicada', 'Hay evidencia del dispositivo', 'No se utiliza'],
  'fr-FR': ['Application sur l’appareil', 'Dernière lecture', 'Échec de transmission de la politique', 'Détection de remplacement du module', 'Contrôle local de l’appareil', 'Inconnu', 'Non pris en charge dans cette version', 'Politique non reçue', 'Journalisation seule, aucun blocage', 'Redémarré ; confirmation du NAS attendue', 'Autorisation expirée ; confirmation du NAS attendue', 'Validité de la politique non confirmée', 'Échec de vérification de la politique', 'La politique de l’appareil diffère de celle du NAS', 'Politique actuelle du NAS appliquée', 'Preuve de l’appareil disponible', 'Non utilisé'],
  'ja-JP': ['端末での実際の適用', '最終取得', 'ポリシー送信失敗', 'モジュール交換の検出', '端末のローカル制御', '不明', 'このバージョンでは非対応', 'ポリシー未受信', '記録のみ・制限なし', '再起動後、NAS の確認待ち', '許可期限切れ・NAS の確認待ち', 'ポリシーの有効性は未確認', 'ポリシー検証失敗', '端末と NAS のポリシーが未同期', '現在の NAS ポリシーを適用済み', '端末からの根拠あり', '判定に使用しない'],
  'pt-BR': ['Aplicação no dispositivo', 'Última leitura', 'Falha ao entregar a política', 'Detecção de troca do módulo', 'Controle local do dispositivo', 'Desconhecido', 'Não suportado nesta versão', 'Política ainda não recebida', 'Apenas registro, sem bloqueio', 'Reiniciado; aguardando confirmação do NAS', 'Permissão expirada; aguardando o NAS', 'Validade da política não confirmada', 'Falha na verificação da política', 'Política do dispositivo diferente da do NAS', 'Política atual do NAS aplicada', 'Evidência do dispositivo disponível', 'Não utilizado'],
  'vi-VN': ['Thực thi trên thiết bị', 'Lần đọc gần nhất', 'Gửi chính sách thất bại', 'Phát hiện thay mô-đun', 'Kiểm soát cục bộ thiết bị', 'Chưa rõ', 'Phiên bản này không hỗ trợ', 'Chưa nhận chính sách', 'Chỉ ghi nhận, không chặn', 'Đã khởi động lại; chờ NAS xác nhận', 'Quyền đã hết hạn; chờ NAS xác nhận', 'Chưa xác nhận hiệu lực chính sách', 'Xác minh chính sách thất bại', 'Chính sách thiết bị chưa đồng bộ với NAS', 'Đã thực thi chính sách NAS hiện tại', 'Có bằng chứng từ thiết bị', 'Không sử dụng']
}
export function devicePolicyCopy(locale: string): Copy {
  const key = Object.keys(rows).find(key => key.toLowerCase() === locale.toLowerCase())
    ?? (locale.startsWith('zh') ? 'zh-CN' : Object.keys(rows).find(key => key.split('-')[0] === locale.split('-')[0]))
    ?? 'en-US'
  const row = rows[key]!
  return {
    title: row[0]!, checked: row[1]!, delivery: row[2]!, hardware: row[3]!, local: row[4]!,
    outcomes: { unknown: row[5]!, unsupported: row[6]!, missing: row[7]!, monitor: row[8]!, restart: row[9]!, expired: row[10]!, stale: row[11]!, damaged: row[12]!, mismatch: row[13]!, enforced: row[14]! },
    evidence: { unknown: row[5]!, unsupported: row[6]!, observed: row[15]!, not_used: row[16]! }
  }
}
