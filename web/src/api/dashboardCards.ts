// The two dashboard cards whose backend is not built yet.
//
// The operator asked for these two cards to be drawn now, against a design the
// backend will be written to. That is a reasonable way round, and it comes with
// one rule that the readers here enforce: a card with no backend behind it says
// so, field by field, and never shows a value it does not have.
//
// In particular it never shows ZERO. "0 SMS today" and "no SMS figures yet" are
// different statements, and the first one is a claim about the gateway's day
// that nothing has measured. Every field is therefore optional, and the cards
// render an absent field as "not wired yet" rather than as a number.
//
// Both readers answer `null` for the whole payload when the endpoint is not
// there, which is the state today: any failure -- 404, 501, a parse error, a
// gateway that has never heard of the path -- means the same thing to the card,
// so they are not distinguished.

import { API_ROOT, get } from './client'

// ---------------------------------------------------------------------------
// Security posture
// ---------------------------------------------------------------------------

/**
 * One row of the security card.
 *
 * `state` is what colours the pill, and it is deliberately not a boolean:
 * "on", "off" and "we have not been told" are three different things, and a
 * boolean would collapse the last two into the alarming one.
 */
export type SecurityState = 'ok' | 'warn' | 'off' | 'unknown'

export interface SecurityRow {
  state: SecurityState
  /** The short value shown in the pill, e.g. "TLS 1.3" or "Ed25519". */
  value?: string
  /** Optional detail for the row's title attribute. */
  detail?: string
}

/**
 * GET /api/v1/security/posture
 *
 * The shape the card is drawn against. Every member is optional; a member the
 * backend omits renders as "not wired yet".
 *
 * ```json
 * {
 *   "transport":   { "state": "ok",      "value": "TLS 1.3" },
 *   "post_quantum":{ "state": "ok",      "value": "PQ Active",  "detail": "X25519MLKEM768" },
 *   "pinning":     { "state": "ok",      "value": "Public-Key Pinning" },
 *   "identity":    { "state": "ok",      "value": "Ed25519 + Pairing" },
 *   "replay":      { "state": "ok",      "value": "Enabled" },
 *   "packet_auth": { "state": "ok",      "value": "HMAC-SHA256" },
 *   "ota":         { "state": "ok",      "value": "Dual Signature" },
 *   "events": {
 *     "window_hours": 24,
 *     "anomalous_logins": 0,
 *     "key_state": "ok",
 *     "replay_blocked": 0,
 *     "downgrade_guard": true
 *   }
 * }
 * ```
 */
export interface SecurityEvents {
  window_hours?: number
  anomalous_logins?: number
  key_state?: 'ok' | 'warn' | 'expired' | 'unknown'
  replay_blocked?: number
  downgrade_guard?: boolean
}

export interface SecurityPosture {
  transport?: SecurityRow
  post_quantum?: SecurityRow
  pinning?: SecurityRow
  identity?: SecurityRow
  replay?: SecurityRow
  packet_auth?: SecurityRow
  ota?: SecurityRow
  events?: SecurityEvents
}

function readRow(value: unknown): SecurityRow | undefined {
  if (!value || typeof value !== 'object') return undefined
  const row = value as Record<string, unknown>
  const state = row.state
  if (state !== 'ok' && state !== 'warn' && state !== 'off' && state !== 'unknown') {
    return undefined
  }
  return {
    state,
    value: typeof row.value === 'string' && row.value ? row.value : undefined,
    detail: typeof row.detail === 'string' && row.detail ? row.detail : undefined
  }
}

function readCount(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? Math.floor(value)
    : undefined
}

export async function getSecurityPosture(): Promise<SecurityPosture | null> {
  let payload: unknown
  try {
    payload = await get(`${API_ROOT}/security/posture`)
  } catch {
    // Not built yet, or unreachable. Same thing to the card.
    return null
  }
  if (!payload || typeof payload !== 'object') return null
  const source = payload as Record<string, unknown>
  const events = source.events as Record<string, unknown> | undefined
  const keyState = events?.key_state
  return {
    transport: readRow(source.transport),
    post_quantum: readRow(source.post_quantum),
    pinning: readRow(source.pinning),
    identity: readRow(source.identity),
    replay: readRow(source.replay),
    packet_auth: readRow(source.packet_auth),
    ota: readRow(source.ota),
    events: events
      ? {
          window_hours: readCount(events.window_hours),
          anomalous_logins: readCount(events.anomalous_logins),
          key_state:
            keyState === 'ok' || keyState === 'warn' || keyState === 'expired'
              ? keyState
              : undefined,
          replay_blocked: readCount(events.replay_blocked),
          downgrade_guard:
            typeof events.downgrade_guard === 'boolean' ? events.downgrade_guard : undefined
        }
      : undefined
  }
}

// ---------------------------------------------------------------------------
// Communication statistics
// ---------------------------------------------------------------------------

/**
 * GET /api/v1/stats/communication
 *
 * This one cannot be computed in the browser, and that is why it is here rather
 * than derived from the workspace stores: the call list is PAGINATED and the
 * messages are loaded per conversation on demand, so counting either in the
 * browser silently counts one page and reports it as the day.
 *
 * ```json
 * {
 *   "window_hours": 24,
 *   "sms":   { "received": 23, "sent": 4, "failed": 2, "verification": 17 },
 *   "calls": { "incoming": 6, "outgoing": 3, "missed": 1, "duration_seconds": 2520 },
 *   "hourly": [ { "hour": 0, "messages": 1, "calls": 0 }, ... 24 entries ]
 * }
 * ```
 *
 * `hourly` drives the 24-hour trend line. Hour 0 is the oldest bucket in the
 * window, not midnight, so the line reads left to right as "a day ago" to
 * "now" without the reader having to know the gateway's timezone.
 */
export interface CommunicationBucket {
  hour: number
  messages?: number
  calls?: number
}

export interface CommunicationStats {
  window_hours?: number
  sms?: { received?: number; sent?: number; failed?: number; verification?: number }
  calls?: { incoming?: number; outgoing?: number; missed?: number; duration_seconds?: number }
  hourly?: CommunicationBucket[]
}

/**
 * One bucket, or `null` for an entry with no usable hour. A bucket without an
 * hour cannot be placed on the trend, and dropping it is right: guessing its
 * position from its index would put a bar somewhere nothing was measured.
 */
function readBucket(entry: unknown): CommunicationBucket | null {
  if (!entry || typeof entry !== 'object') return null
  const bucket = entry as Record<string, unknown>
  const hour = readCount(bucket.hour)
  if (hour === undefined) return null
  return {
    hour,
    messages: readCount(bucket.messages),
    calls: readCount(bucket.calls)
  }
}

function isBucket(entry: CommunicationBucket | null): entry is CommunicationBucket {
  return entry !== null
}

export async function getCommunicationStats(): Promise<CommunicationStats | null> {
  let payload: unknown
  try {
    payload = await get(`${API_ROOT}/stats/communication`)
  } catch {
    return null
  }
  if (!payload || typeof payload !== 'object') return null
  const source = payload as Record<string, unknown>
  const sms = source.sms as Record<string, unknown> | undefined
  const calls = source.calls as Record<string, unknown> | undefined
  const hourly = Array.isArray(source.hourly) ? source.hourly : undefined
  return {
    window_hours: readCount(source.window_hours),
    sms: sms
      ? {
          received: readCount(sms.received),
          sent: readCount(sms.sent),
          failed: readCount(sms.failed),
          verification: readCount(sms.verification)
        }
      : undefined,
    calls: calls
      ? {
          incoming: readCount(calls.incoming),
          outgoing: readCount(calls.outgoing),
          missed: readCount(calls.missed),
          duration_seconds: readCount(calls.duration_seconds)
        }
      : undefined,
    hourly: hourly?.map(readBucket).filter(isBucket)
  }
}
