export function formatCallDuration(seconds: number | undefined, units: { hour: string; minute: string; second: string }): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return ''
  const total = Math.floor(seconds)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const remainder = total % 60
  if (hours > 0) return `${hours}${units.hour}${minutes}${units.minute}${remainder}${units.second}`
  if (minutes > 0) return `${minutes}${units.minute}${remainder}${units.second}`
  return `${remainder}${units.second}`
}

export function cumulativeDurationLabel(locale: string): string {
  const labels: Record<string, string> = {
    'zh-CN': '累计时长', 'zh-TW': '累計時長', 'en-US': 'Total duration',
    'ja-JP': '合計通話時間', 'vi-VN': 'Tổng thời lượng', 'es-ES': 'Duración total',
    'de-DE': 'Gesamtdauer', 'fr-FR': 'Durée totale', 'pt-BR': 'Duração total'
  }
  return labels[locale] ?? labels['en-US']!
}
