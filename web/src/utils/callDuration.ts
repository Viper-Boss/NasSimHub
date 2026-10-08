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

/**
 * Time left, for a countdown measured in days: an observation period, a
 * confirmation deadline.
 *
 * Two units at most, largest first, and never a unit the value does not reach:
 * 32 seconds is "under a minute", not "32 minutes" and not "0 hours". Floors
 * rather than rounds, so the display never claims more time than there is in
 * the larger unit. Returns '' for a value that is absent or not a duration, so
 * the caller shows "unknown" instead of a zero nothing measured.
 */
export function formatRemaining(
  seconds: number | undefined,
  units: { day: string; hour: string; minute: string; underMinute: string }
): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return ''
  const total = Math.floor(seconds)
  if (total < 60) return units.underMinute
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (days > 0) return hours > 0 ? `${days}${units.day}${hours}${units.hour}` : `${days}${units.day}`
  if (hours > 0) return minutes > 0 ? `${hours}${units.hour}${minutes}${units.minute}` : `${hours}${units.hour}`
  return `${minutes}${units.minute}`
}
