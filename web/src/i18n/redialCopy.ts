const labels: Record<string, string> = {
  'zh-CN': '重拨', 'zh-TW': '重撥', 'en-US': 'Redial', 'de-DE': 'Wahlwiederholung',
  'fr-FR': 'Rappeler', 'es-ES': 'Volver a llamar', 'pt-BR': 'Rediscar',
  'ja-JP': 'リダイヤル', 'vi-VN': 'Gọi lại'
}
export function redialLabel(locale: string): string { return labels[locale] || labels['en-US']! }
