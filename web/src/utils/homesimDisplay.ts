// Presentation only: never change the destination used for dialing or replying.
export function groupedPhone(value: string): string {
  const compact = value.replace(/[\s()-]/g, '')
  const match = /^(\+86|0086)?(1[3-9]\d{9})$/.exec(compact)
  if (!match) return value
  const number = match[2]!
  return `${match[1] ? '+86 ' : ''}${number.slice(0, 3)} ${number.slice(3, 7)} ${number.slice(7)}`
}

export function messageTime(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  return date.toLocaleString(undefined, {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false
  })
}
