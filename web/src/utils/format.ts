import { resolvedLocale, translate } from '../i18n'

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return '#'
  if (parts.length === 1) return Array.from(parts[0] || '#').slice(0, 2).join('').toUpperCase()
  return `${Array.from(parts[0] || '#')[0] || ''}${Array.from(parts.at(-1) || '#')[0] || ''}`.toUpperCase()
}

import {
  parsePhoneNumberFromString,
  type CountryCode
} from 'libphonenumber-js/min'

export function formatRelativeDate(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return value
  const now = new Date()
  const sameDay = date.toDateString() === now.toDateString()
  const locale = resolvedLocale()
  if (sameDay) {
    return new Intl.DateTimeFormat(locale, {
      hour: '2-digit',
      minute: '2-digit'
    }).format(date)
  }
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  if (date.toDateString() === yesterday.toDateString()) return translate('common.yesterday')
  if (date.getFullYear() === now.getFullYear()) {
    return new Intl.DateTimeFormat(locale, { month: 'numeric', day: 'numeric' }).format(date)
  }
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'numeric',
    day: 'numeric'
  }).format(date)
}

export function formatDateTime(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return value
  return new Intl.DateTimeFormat(resolvedLocale(), {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date)
}

/**
 * A length in SECONDS as the lists show it: "32 sec" below a minute, then
 * m:ss, then h:mm:ss. The hour is carried so a long recording does not read
 * as "1439:59", and seconds are never dropped or relabelled as minutes.
 */
export function formatDuration(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(safe / 3600)
  const minutes = Math.floor((safe % 3600) / 60)
  const remainder = safe % 60
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
  }
  return minutes > 0
    ? `${minutes}:${String(remainder).padStart(2, '0')}`
    : translate('common.durationSeconds', { count: remainder })
}

export function primaryPhone(phones: Array<{ number: string; primary: boolean }>): string {
  return phones.find(phone => phone.primary)?.number || phones[0]?.number || ''
}

export function phoneDestination(phone: {
  number: string
  normalized_number?: string
}): string {
  return phone.normalized_number?.trim() || phone.number.trim()
}

export function primaryPhoneDestination(
  phones: Array<{ number: string; normalized_number?: string; primary: boolean }>
): string {
  const phone = phones.find(item => item.primary) || phones[0]
  return phone ? phoneDestination(phone) : ''
}

export function formatPhoneNumber(value: string, homeRegion = ''): string {
  const original = value.trim()
  if (!original) return ''
  const normalizedRegion = homeRegion.trim().toUpperCase()
  const region: CountryCode | undefined = /^[A-Z]{2}$/.test(normalizedRegion)
    ? (normalizedRegion as CountryCode)
    : undefined
  const parsed = parsePhoneNumberFromString(original, region)
  if (!parsed?.isPossible()) return original
  return region && parsed.country === region
    ? parsed.formatNational()
    : parsed.formatInternational()
}
