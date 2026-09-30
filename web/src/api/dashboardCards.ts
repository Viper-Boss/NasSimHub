// The dashboard cards that were drawn before their backend existed.
//
// The operator asked for these cards to be drawn first, against a design the
// backend was then written to. That came with one rule that the readers here
// enforce, and it still holds now the endpoints exist, because a gateway older
// than them answers nothing: a card with no backend behind it says so, field
// by field, and never shows a value it does not have.
//
// In particular it never shows ZERO. "0 SMS today" and "no SMS figures yet" are
// different statements, and the first one is a claim about the gateway's day
// that nothing has measured. Every field is therefore optional, and the cards
// render an absent field as "not wired yet" rather than as a number.
//
// Every reader answers `null` for the whole payload when the endpoint is not
// there -- which is the state on a gateway older than it: any failure -- 404,
// 501, a parse error, a gateway that has never heard of the path -- means the
// same thing to the card, so they are not distinguished.

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
 * Every member is optional; a member the backend omits renders as "not wired
 * yet". What the gateway serves (internal/httpapi/security_posture.go) is a
 * description of the VIEWER'S OWN CONNECTION as the gateway's nginx
 * negotiated it, plus two facts about the gateway itself -- for a browser on
 * the HTTPS port, something like:
 *
 * ```json
 * {
 *   "transport":    { "state": "ok",  "value": "TLS 1.3" },
 *   "post_quantum": { "state": "ok",  "value": "ML-KEM", "detail": "X25519MLKEM768" },
 *   "packet_auth":  { "state": "ok",  "value": "AES-128-GCM", "detail": "TLS_AES_128_GCM_SHA256" },
 *   "replay":       { "state": "ok" },
 *   "identity":     { "state": "ok",  "value": "ECDSA P-256", "detail": "<certificate fingerprint>" },
 *   "pinning":      { "state": "off" },
 *   "ota":          { "state": "ok",  "value": "Ed25519", "detail": "b813a48a" },
 *   "events": {
 *     "window_hours": 24,
 *     "anomalous_logins": 2,
 *     "key_state": "ok",
 *     "downgrade_guard": true
 *   }
 * }
 * ```
 *
 * A state with no value (replay "ok", pinning "off") is worded by the card
 * from the state. Through Cloudflare the rows about the handshake are
 * "unknown", because the browser's TLS ends at Cloudflare. `anomalous_logins`
 * is failed password logins, over `window_hours` -- less than 24 for a day
 * after a restart, because the count lives in memory. `replay_blocked` is
 * never sent: nothing counts rejected replays. The gateway-wide events go to
 * administrators only.
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
 * Served by internal/httpapi/communication_stats.go, from the gateway's whole
 * message and call record, scoped to the lines the caller may open. Its zeros
 * are real counts.
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

// ---------------------------------------------------------------------------
// Module security
// ---------------------------------------------------------------------------

/**
 * GET /api/v1/security/module
 *
 * THE CARD THIS FEEDS EXISTS FOR A LEGAL REASON, which changes what it may
 * say. A gateway that can place calls and send messages can be used to defraud
 * people, and whoever ships it carries some responsibility for that. The card
 * is where an operator sees what is actually restraining the box and how close
 * it is to those limits.
 *
 * That purpose is exactly why nothing here may be softened into a reassurance.
 * A "risk: low" badge with nothing computing it, or a green "integrity: ok"
 * beside a check that does not exist, is worse on this card than on any other:
 * it is a compliance claim, it will be screenshotted, and the person reading it
 * has no way to tell it apart from the real rows beside it. So every field is
 * optional and an absent field renders as "not wired yet" -- never as zero, and
 * never as a pass.
 *
 * WHAT IS ALREADY REAL TODAY, and is therefore NOT in this contract because the
 * card reads it from the endpoints that already serve it:
 *
 *   the public egress IP and ISP   GET /api/v1/vowifi-diagnostics -> egress
 *   the inbound call filter policy GET /api/v1/settings/homesim/call-filter
 *
 * WHAT THE GATEWAY SERVES HERE TODAY is `guard` alone -- the anti-fraud
 * guard's budgets, today's use of them, whether it is lifted, and the day
 * (internal/httpapi/call_guard.go, moduleSecurity). `risk`, `integrity` and
 * `trust` are left out of the response because nothing computes them, and the
 * card reads their absence as "not wired". The example below is the full
 * shape, not today's payload.
 *
 * ```json
 * {
 *   "risk":  { "state": "ok", "value": "低", "detail": "无异常行为" },
 *   "guard": {
 *     "call_limit": 5, "call_used": 2,
 *     "message_limit": 3, "message_used": 1,
 *     "unlocked": false, "date": "2026-09-21",
 *     "resets_at": "2026-09-22T00:00:00+08:00"
 *   },
 *   "integrity": {
 *     "module":    { "state": "ok",      "value": "正常" },
 *     "system":    { "state": "ok",      "value": "正常" },
 *     "secure_boot": { "state": "off",   "value": "未启用" },
 *     "firmware":  { "state": "unknown", "value": "未验证" }
 *   },
 *   "trust": { "state": "warn", "value": "观察期中", "remaining_hours": 109 }
 * }
 * ```
 */
export interface FraudGuardState {
  /**
   * The daily allowance, in DISTINCT DESTINATION NUMBERS -- not attempts.
   * Re-dialling a number already used today costs nothing, which is the whole
   * shape of the guard: it restrains reaching many strangers, not making many
   * calls. A card that called this "calls today" would misdescribe it.
   */
  call_limit?: number
  call_used?: number
  message_limit?: number
  message_used?: number
  /**
   * True once an administrator has lifted the guard for this installation.
   * Reported rather than hidden: an unlocked guard is the single most
   * important thing on this card, because it is the state in which nothing is
   * restraining the box.
   */
  unlocked?: boolean
  /** The local date the counters belong to; they reset when it changes. */
  date?: string
  /**
   * When the counters next go back to zero, as an RFC 3339 instant. Reported
   * by the gateway because the rollover is ITS local midnight, which on a
   * container with no time zone set is UTC -- not the reader's midnight.
   */
  resets_at?: string
}

export interface ModuleTrust {
  state?: SecurityState
  value?: string
  remaining_hours?: number
}

export interface ModuleSecurity {
  risk?: SecurityRow
  guard?: FraudGuardState
  integrity?: {
    module?: SecurityRow
    system?: SecurityRow
    secure_boot?: SecurityRow
    firmware?: SecurityRow
  }
  trust?: ModuleTrust
}

export async function getModuleSecurity(): Promise<ModuleSecurity | null> {
  let payload: unknown
  try {
    payload = await get(`${API_ROOT}/security/module`)
  } catch {
    // Not built yet, or unreachable. Same thing to the card.
    return null
  }
  if (!payload || typeof payload !== 'object') return null
  const source = payload as Record<string, unknown>
  const guard = source.guard as Record<string, unknown> | undefined
  const integrity = source.integrity as Record<string, unknown> | undefined
  const trust = source.trust as Record<string, unknown> | undefined
  const trustState = trust?.state
  return {
    risk: readRow(source.risk),
    guard: guard
      ? {
          call_limit: readCount(guard.call_limit),
          call_used: readCount(guard.call_used),
          message_limit: readCount(guard.message_limit),
          message_used: readCount(guard.message_used),
          unlocked: typeof guard.unlocked === 'boolean' ? guard.unlocked : undefined,
          date: typeof guard.date === 'string' && guard.date ? guard.date : undefined,
          resets_at:
            typeof guard.resets_at === 'string' && !Number.isNaN(Date.parse(guard.resets_at))
              ? guard.resets_at
              : undefined
        }
      : undefined,
    integrity: integrity
      ? {
          module: readRow(integrity.module),
          system: readRow(integrity.system),
          secure_boot: readRow(integrity.secure_boot),
          firmware: readRow(integrity.firmware)
        }
      : undefined,
    trust: trust
      ? {
          state:
            trustState === 'ok' ||
            trustState === 'warn' ||
            trustState === 'off' ||
            trustState === 'unknown'
              ? trustState
              : undefined,
          value: typeof trust.value === 'string' && trust.value ? trust.value : undefined,
          remaining_hours: readCount(trust.remaining_hours)
        }
      : undefined
  }
}
