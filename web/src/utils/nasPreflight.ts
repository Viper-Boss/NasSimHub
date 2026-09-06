export class NasPreflightError extends Error {
  constructor(code: 'structure' | 'field' | 'size' | 'json' | 'version' | 'date' | 'host' | 'count' | 'usb' | 'drivers' | 'check' | 'functional' | 'missing') {
    super(`nasPreflight.errors.${code}`)
    this.name = 'NasPreflightError'
  }
}

export const MAX_PREFLIGHT_BYTES = 128 * 1024
export type PreflightStatus = 'pass' | 'warn' | 'fail' | 'not_tested'
export type PreflightCheck = { id: string; title: string; status: PreflightStatus; detail: string }
export type PreflightDevice = { label: string; usb_id: string; family: string; drivers: string[] }
export type PreflightReport = {
  schema: 'nas-comm-preflight/v1'
  source: 'host-read-only'
  generated_at: string
  host: { system: 'Linux' | 'non-Linux'; architecture: 'amd64' | 'arm64' | 'unsupported' }
  devices: PreflightDevice[]
  checks: PreflightCheck[]
}

const functionalIDs = new Set(['sms_receive', 'sms_send', 'call_in', 'call_out', 'two_way_audio', 'recording'])
const requiredIDs = new Set(['linux', 'architecture', 'usb', 'serial', 'qmi', 'audio', 'docker', 'compose', 'data', 'recordings', ...functionalIDs])
const statuses = new Set(['pass', 'warn', 'fail', 'not_tested'])

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new NasPreflightError('structure')
  return value as Record<string, unknown>
}

function text(value: unknown, max = 400): string {
  if (typeof value !== 'string' || !value.trim() || value.length > max) throw new NasPreflightError('field')
  return value
}

/** Reconstruct an allowlisted object; do not retain arbitrary imported fields. */
export function parseNasPreflight(raw: string): PreflightReport {
  if (new TextEncoder().encode(raw).byteLength > MAX_PREFLIGHT_BYTES) throw new NasPreflightError('size')
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new NasPreflightError('json') }
  const input = object(parsed)
  if (input.schema !== 'nas-comm-preflight/v1' || input.source !== 'host-read-only') throw new NasPreflightError('version')
  const generatedAt = text(input.generated_at, 80)
  if (!Number.isFinite(Date.parse(generatedAt))) throw new NasPreflightError('date')
  const host = object(input.host)
  if (!['Linux', 'non-Linux'].includes(String(host.system)) || !['amd64', 'arm64', 'unsupported'].includes(String(host.architecture))) throw new NasPreflightError('host')
  if (!Array.isArray(input.devices) || input.devices.length > 16 || !Array.isArray(input.checks) || input.checks.length > 32) throw new NasPreflightError('count')
  const devices = input.devices.map(value => {
    const device = object(value)
    const usbID = text(device.usb_id, 9)
    if (!/^[a-f0-9]{4}:[a-f0-9]{4}$/.test(usbID)) throw new NasPreflightError('usb')
    if (!Array.isArray(device.drivers) || device.drivers.length > 16) throw new NasPreflightError('drivers')
    return { label: text(device.label, 80), usb_id: usbID, family: text(device.family, 120), drivers: device.drivers.map(driver => text(driver, 40)) }
  })
  const seen = new Set<string>()
  const checks = input.checks.map(value => {
    const item = object(value)
    const id = text(item.id, 40)
    if ((!requiredIDs.has(id) && id !== 'storage_separation') || seen.has(id) || !statuses.has(String(item.status))) throw new NasPreflightError('check')
    seen.add(id)
    // Host probes can never prove successful carrier calls, SMS or recordings.
    if (functionalIDs.has(id) && item.status !== 'not_tested') throw new NasPreflightError('functional')
    return { id, title: text(item.title, 80), status: item.status as PreflightStatus, detail: text(item.detail) }
  })
  if ([...requiredIDs].some(id => !seen.has(id))) throw new NasPreflightError('missing')
  return {
    schema: 'nas-comm-preflight/v1', source: 'host-read-only', generated_at: generatedAt,
    host: { system: host.system as PreflightReport['host']['system'], architecture: host.architecture as PreflightReport['host']['architecture'] },
    devices, checks
  }
}

export function preflightSummary(report: PreflightReport): string {
  if (report.checks.some(check => check.status === 'fail')) return 'nasPreflight.summary.blocked'
  if (report.checks.some(check => check.status === 'warn')) return 'nasPreflight.summary.review'
  return 'nasPreflight.summary.imported'
}
