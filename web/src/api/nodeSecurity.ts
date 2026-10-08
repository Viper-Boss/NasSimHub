// Node identity, agent updates, device trust and the security journal.
//
// Kept out of client.ts on purpose: code added there lands in the startup
// bundle every visitor downloads, and everything here is reachable only from
// the SIM-nodes view and the administrator's security centre.
//
// THE READERS ARE THE ENFORCEMENT POINT for the rule the rest of the UI lives
// by: a field the gateway did not send, or sent in a shape this build does not
// know, is "unknown" -- never a pass, never a zero, never a default "allowed".
// The Go structs these mirror are named beside each reader; their `json` tags
// are the contract.
//
// NOTHING HERE CACHES. Device update status, trust state and journal health
// are computed by the gateway at the time of each request and served
// `Cache-Control: no-store`; every function below is one request and returns
// what that request said. There is deliberately no module-level store to hand
// a stale answer back from.

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

/**
 * A failed request. `message` is the gateway's own wording (it is what the
 * panels show), `code` its machine code, and `body` the parsed answer for the
 * one error that carries a state worth showing (an apply that did not confirm).
 * `status` 0 means the request never reached the gateway.
 */
export class NodeApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly body: unknown = null
  ) {
    super(message)
  }
}

function csrfToken(): string {
  if (typeof document === 'undefined') return ''
  for (const part of document.cookie.split(';')) {
    const [name, ...rest] = part.trim().split('=')
    if (name === 'modemdeck_csrf') return decodeURIComponent(rest.join('='))
  }
  return ''
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

/**
 * The gateway answers errors as `{code, message}` (httpapi/response.go). The
 * unconfirmed-apply answer nests them under `error` beside the device state,
 * so both shapes are read.
 */
function failureOf(status: number, parsed: unknown): NodeApiError {
  const body = record(parsed)
  const nested = record(body?.error)
  const code = text(body?.code) || text(nested?.code) || (status === 403 ? 'forbidden' : '')
  const message = text(body?.message) || text(nested?.message)
  return new NodeApiError(status, code, message, parsed)
}

type Payload =
  | { kind: 'json'; value: unknown }
  // Sent exactly as given. A signed manifest's signature covers its bytes, so
  // it must never pass through JSON.parse and JSON.stringify on the way.
  | { kind: 'raw'; value: ArrayBuffer | Blob; contentType: string }

export async function nodeRequest(
  method: 'GET' | 'POST',
  path: string,
  payload?: Payload
): Promise<unknown> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  let body: BodyInit | undefined
  if (method === 'POST') {
    headers['X-ModemDeck-CSRF'] = csrfToken()
    if (payload?.kind === 'raw') {
      headers['Content-Type'] = payload.contentType
      body = payload.value
    } else {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(payload?.value ?? {})
    }
  }
  let response: Response
  try {
    response = await fetch(path, {
      method,
      credentials: 'same-origin',
      cache: 'no-store',
      headers,
      body
    })
  } catch {
    throw new NodeApiError(0, 'network', '')
  }
  const parsed: unknown = await response.json().catch(() => null)
  if (!response.ok) throw failureOf(response.status, parsed)
  if (!record(parsed)) throw new NodeApiError(response.status, 'invalid_response', '')
  return parsed
}

// ---------------------------------------------------------------------------
// Field readers
// ---------------------------------------------------------------------------

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** A boolean, or null when the gateway did not say. Never coerced. */
function flag(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null
}

function count(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? Math.floor(value)
    : undefined
}

/**
 * An RFC 3339 instant, or '' when absent, unparseable or Go's zero time
 * (year 1), which a struct without `omitzero` serialises for "never".
 */
function instant(value: unknown): string {
  const raw = text(value)
  if (!raw) return ''
  const parsed = Date.parse(raw)
  if (Number.isNaN(parsed) || new Date(parsed).getUTCFullYear() <= 1) return ''
  return raw
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : undefined
}

export type Tristate = 'yes' | 'no' | 'unknown'

function tristate(value: unknown): Tristate {
  return oneOf(value, ['yes', 'no'] as const) ?? 'unknown'
}

// ---------------------------------------------------------------------------
// Per-node security (GET /api/v1/nodes -> nodes[])
// ---------------------------------------------------------------------------

export type IdentityOutcome = 'unknown' | 'dual' | 'classical_only' | 'refused'
export type PinState = 'none' | 'pinned' | 'changed' | 'withdrawn' | 'unknown'

/** Mirrors nassimhubnode.IdentityView (pqidentity.go). */
export interface NodeIdentity {
  outcome: IdentityOutcome
  policy: string
  enforced: boolean | null
  algorithm: string
  fingerprint: string
  pin_state: PinState
  pinned_at: string
  attestation_supported: boolean | null
  core_key_offered: boolean | null
  core_key_pinned: boolean | null
  /** Whether THIS GATEWAY BUILD can verify ML-DSA at all; null when not reported. */
  available: boolean | null
  backend: string
  reason: string
  checked_at: string
}

export function readNodeIdentity(value: unknown): NodeIdentity {
  const source = record(value) ?? {}
  return {
    outcome: oneOf(source.outcome, ['dual', 'classical_only', 'refused'] as const) ?? 'unknown',
    policy: text(source.policy),
    enforced: flag(source.enforced),
    algorithm: text(source.algorithm),
    fingerprint: text(source.fingerprint),
    // An unrecognised pin state is unknown, not "none": "none" is a claim that
    // nothing is pinned.
    pin_state:
      oneOf(source.pin_state, ['none', 'pinned', 'changed', 'withdrawn'] as const) ?? 'unknown',
    pinned_at: instant(source.pinned_at),
    attestation_supported: flag(source.attestation_supported),
    core_key_offered: flag(source.core_key_offered),
    core_key_pinned: flag(source.core_key_pinned),
    available: flag(source.available),
    backend: text(source.backend),
    reason: text(source.reason),
    checked_at: instant(source.checked_at)
  }
}

/** Mirrors nassimhubnode.LinkSecurityView (pqidentity.go). */
export interface NodeLinkSecurity {
  tls_version: string
  group: string
  post_quantum: Tristate
  node_group: string
  requested_level: string
  required_group: string
  /**
   * Whether the observed session satisfies the requested level. The gateway
   * sends `false` while nothing has been observed, which is not a shortfall,
   * so this is null unless the session is `known`.
   */
  meets_level: boolean | null
  node_profile: string
  node_policy: string
  known: boolean
}

export function readNodeLinkSecurity(value: unknown): NodeLinkSecurity {
  const source = record(value) ?? {}
  const known = source.known === true
  return {
    tls_version: text(source.tls_version),
    group: text(source.group),
    post_quantum: known ? tristate(source.post_quantum) : 'unknown',
    node_group: text(source.node_group),
    requested_level: text(source.requested_level),
    required_group: text(source.required_group),
    meets_level: known ? flag(source.meets_level) : null,
    node_profile: text(source.node_profile),
    node_policy: text(source.node_policy),
    known
  }
}

export const TRUST_STATES = ['UNBOUND', 'OBSERVATION', 'TRUSTED', 'RESTRICTED', 'QUARANTINE'] as const
export type TrustState = (typeof TRUST_STATES)[number]

/** Device observation, independent of the NAS decision. Missing flags stay unknown. */
export interface DevicePolicyStatus {
  known: boolean
  supported: boolean | null
  installed: boolean | null
  enforcing: boolean | null
  stale: boolean | null
  damaged: boolean | null
  matches_gateway: boolean | null
  freshness: 'fresh' | 'stale_restart' | 'stale_expired' | 'unknown'
  generation: number | undefined
  gateway_generation: number | undefined
  checked_at: string
  reason: string
  push_error: string
}

export function readDevicePolicyStatus(value: unknown): DevicePolicyStatus {
  const source = record(value) ?? {}
  const known = source.known === true
  const generation = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined
  return {
    known,
    supported: known ? flag(source.supported) : null,
    installed: known ? flag(source.installed) : null,
    enforcing: known ? flag(source.enforcing) : null,
    stale: known ? flag(source.stale) : null,
    damaged: known ? flag(source.damaged) : null,
    matches_gateway: known ? flag(source.matches_gateway) : null,
    freshness: known ? oneOf(source.freshness, ['fresh', 'stale_restart', 'stale_expired'] as const) ?? 'unknown' : 'unknown',
    generation: known ? generation(source.generation) : undefined,
    gateway_generation: generation(source.gateway_generation),
    checked_at: instant(source.checked_at),
    reason: text(source.reason),
    push_error: text(source.push_error)
  }
}

export type DevicePolicyOutcome = 'unknown' | 'unsupported' | 'missing' | 'monitor' | 'restart' | 'expired' | 'stale' | 'damaged' | 'mismatch' | 'enforced'
export function devicePolicyOutcome(device: DevicePolicyStatus): DevicePolicyOutcome {
  if (!device.known || device.supported === null) return 'unknown'
  if (device.supported === false) return 'unsupported'
  if (device.damaged === true) return 'damaged'
  if (device.installed === false) return 'missing'
  if (device.installed !== true || device.enforcing === null) return 'unknown'
  if (device.enforcing === false) return 'monitor'
  if (device.stale === true) {
    if (device.freshness === 'stale_restart') return 'restart'
    if (device.freshness === 'stale_expired') return 'expired'
    return 'stale'
  }
  if (device.stale !== false || device.damaged !== false) return 'unknown'
  if (device.matches_gateway === false) return 'mismatch'
  return device.matches_gateway === true ? 'enforced' : 'unknown'
}

/** Mirrors nassimhubnode.TrustView (trust.go). */
export interface NodeTrust {
  device: DevicePolicyStatus
  known: boolean
  state: TrustState | ''
  effective_state: TrustState | ''
  reason: string
  remaining_seconds: number | undefined
  /** null when the device has no trust record: nothing is claimed either way. */
  outgoing_allowed: boolean | null
  /** false is monitor mode: evaluated and recorded, nothing refused. */
  enforced: boolean | null
}

export function readNodeTrust(value: unknown): NodeTrust {
  const source = record(value) ?? {}
  const known = source.known === true
  return {
    device: readDevicePolicyStatus(source.device),
    known,
    state: known ? (oneOf(source.state, TRUST_STATES) ?? '') : '',
    effective_state: known ? (oneOf(source.effective_state, TRUST_STATES) ?? '') : '',
    reason: text(source.reason),
    remaining_seconds: known ? count(source.remaining_seconds) : undefined,
    outgoing_allowed: known ? flag(source.outgoing_allowed) : null,
    enforced: flag(source.enforced)
  }
}

export interface NodeSecurity {
  identity: NodeIdentity
  link_security: NodeLinkSecurity
  identity_refused: boolean
  trust: NodeTrust
}

/** One entry of `nodes[]`, reduced to its security facts. */
export function readNodeSecurity(node: unknown): NodeSecurity {
  const source = record(node) ?? {}
  return {
    identity: readNodeIdentity(source.identity),
    link_security: readNodeLinkSecurity(source.link_security),
    identity_refused: source.identity_refused === true,
    trust: readNodeTrust(source.trust)
  }
}

/**
 * POST /api/v1/nodes/pq-pin/reset
 *
 * Forgets the ML-DSA key recorded for one device. Takes the device ID only:
 * the gateway records what the device then proves, not what a request says.
 * Answers the refreshed node, untouched, for the caller to put in its list.
 */
export async function resetNodePQPin(deviceID: string): Promise<unknown> {
  const body = record(
    await nodeRequest('POST', '/api/v1/nodes/pq-pin/reset', {
      kind: 'json',
      value: { device_id: deviceID }
    })
  )
  return body?.node ?? null
}

// ---------------------------------------------------------------------------
// Node agent updates
// ---------------------------------------------------------------------------

/** Mirrors nassimhubnode.NodeRelease (updates.go). */
export interface NodeRelease {
  release_id: string
  version: string
  channel: string
  platform: string
  arch: string
  size: number | undefined
  sha256: string
  released_at: string
  min_core_version: string
  notes: string
  key_id: string
  pq_key_id: string
  pq_algorithm: string
  /** What THIS gateway established. Anything but an explicit "verified" is unverified. */
  gateway_verification: 'verified' | 'unverified'
  /** Only meaningful when the gateway verified; '' otherwise. */
  signatures: 'classical_only' | 'dual_signed' | ''
  artifact_ready: boolean
  imported_at: string
}

export function readNodeRelease(value: unknown): NodeRelease | null {
  const source = record(value)
  const releaseID = text(source?.release_id)
  if (!source || !releaseID) return null
  const verified = source.gateway_verification === 'verified'
  return {
    release_id: releaseID,
    version: text(source.version),
    channel: text(source.channel),
    platform: text(source.platform),
    arch: text(source.arch),
    size: count(source.size),
    sha256: text(source.sha256),
    released_at: instant(source.released_at),
    min_core_version: text(source.min_core_version),
    notes: text(source.notes),
    key_id: text(source.key_id),
    pq_key_id: text(source.pq_key_id),
    pq_algorithm: text(source.pq_algorithm),
    gateway_verification: verified ? 'verified' : 'unverified',
    // A signature outcome is the gateway's finding. Without its check there is
    // no finding, whatever the manifest claims about itself.
    signatures: verified
      ? (oneOf(source.signatures, ['classical_only', 'dual_signed'] as const) ?? '')
      : '',
    artifact_ready: source.artifact_ready === true,
    imported_at: instant(source.imported_at)
  }
}

/** Mirrors nassimhubnode.NodeReleaseCatalog. */
export interface NodeReleaseCatalog {
  releases: NodeRelease[]
  gateway_verifies: boolean
  available: boolean
}

export function readNodeReleaseCatalog(value: unknown): NodeReleaseCatalog {
  const source = record(value) ?? {}
  const releases = Array.isArray(source.releases) ? source.releases : []
  return {
    releases: releases
      .map(readNodeRelease)
      .filter((entry): entry is NodeRelease => entry !== null),
    gateway_verifies: source.gateway_verifies === true,
    available: source.available === true
  }
}

/** Mirrors proto.OTADeviceStatus as the gateway relays it. */
export interface NodeUpdateStatus {
  state: string
  current_version: string
  target_version: string
  previous_version: string
  release_id: string
  progress_percent: number | undefined
  error: string
  attempts: number | undefined
  restart_reason: string
  confirm_deadline: string
  supported: boolean | null
  unsupported_reason: string
  signatures: { policy: string; outcome: string; pq_available: boolean | null }
  keys: { classical: number | undefined; post_quantum: number | undefined }
  received_bytes: number | undefined
  expected_bytes: number | undefined
  active_release: string
  factory_version: string
  restart_pending: boolean | null
}

function readNodeUpdateStatus(value: unknown): NodeUpdateStatus | null {
  const source = record(value)
  if (!source) return null
  const signatures = record(source.signatures) ?? {}
  const keys = record(source.keys) ?? {}
  return {
    state: text(source.state),
    current_version: text(source.current_version),
    target_version: text(source.target_version),
    previous_version: text(source.previous_version),
    release_id: text(source.release_id),
    progress_percent: count(source.progress_percent),
    error: text(source.error),
    attempts: count(source.attempts),
    restart_reason: text(source.restart_reason),
    confirm_deadline: instant(source.confirm_deadline),
    supported: flag(source.supported),
    unsupported_reason: text(source.unsupported_reason),
    signatures: {
      policy: text(signatures.policy),
      outcome: text(signatures.outcome),
      pq_available: flag(signatures.pq_available)
    },
    keys: { classical: count(keys.classical), post_quantum: count(keys.post_quantum) },
    received_bytes: count(source.received_bytes),
    expected_bytes: count(source.expected_bytes),
    active_release: text(source.active_release),
    factory_version: text(source.factory_version),
    restart_pending: flag(source.restart_pending)
  }
}

/** Mirrors nassimhubnode.NodeUpdateView: one device's state, read just now. */
export interface NodeUpdateDevice {
  node_id: string
  reachable: boolean
  supported: boolean
  reason: string
  status: NodeUpdateStatus | null
  read_at: string
}

export function readNodeUpdateDevice(value: unknown): NodeUpdateDevice | null {
  const source = record(value)
  if (!source) return null
  const reachable = source.reachable === true
  return {
    node_id: text(source.node_id),
    reachable,
    supported: reachable && source.supported === true,
    reason: text(source.reason),
    // A device that did not answer has no status. Should a gateway ever send
    // one alongside `reachable: false` it is dropped here, so no panel can
    // draw a last-known state as the current one.
    status: reachable ? readNodeUpdateStatus(source.status) : null,
    read_at: reachable ? instant(source.read_at) : ''
  }
}

export interface NodeUpdatesAnswer {
  catalog: NodeReleaseCatalog
  device: NodeUpdateDevice | null
}

/** GET /api/v1/nodes/updates[?device_id=...]. One live read; not cached. */
export async function getNodeUpdates(deviceID?: string): Promise<NodeUpdatesAnswer> {
  const query = deviceID ? `?device_id=${encodeURIComponent(deviceID)}` : ''
  const body = await nodeRequest('GET', `/api/v1/nodes/updates${query}`)
  return {
    catalog: readNodeReleaseCatalog(body),
    device: deviceID ? readNodeUpdateDevice(record(body)?.device) : null
  }
}

/** The protocol's bounds, mirrored from nassimhub_updates_api.go for the pre-flight check. */
export const NODE_MANIFEST_LIMIT_BYTES = 32 << 10
export const NODE_ARTIFACT_LIMIT_BYTES = 32 << 20

function releaseOf(body: unknown): NodeRelease {
  const release = readNodeRelease(record(body)?.release)
  if (!release) throw new NodeApiError(200, 'invalid_response', '')
  return release
}

/**
 * POST /api/v1/nodes/updates/manifest
 *
 * The file's bytes go out exactly as read. The signature covers them, so they
 * are never decoded and re-encoded here.
 */
export async function importNodeManifest(file: Blob): Promise<NodeRelease> {
  const bytes = await file.arrayBuffer()
  return releaseOf(
    await nodeRequest('POST', '/api/v1/nodes/updates/manifest', {
      kind: 'raw',
      value: bytes,
      contentType: 'application/json'
    })
  )
}

/**
 * POST /api/v1/nodes/updates/artifact?release_id=...
 *
 * XMLHttpRequest rather than fetch for one reason: an artifact is up to 32 MiB
 * going to a NAS over whatever link the browser has, and fetch reports no
 * upload progress. `onProgress` gets a 0..1 share while it is computable.
 */
export function importNodeArtifact(
  releaseID: string,
  file: Blob,
  onProgress?: (share: number) => void
): Promise<NodeRelease> {
  const path = `/api/v1/nodes/updates/artifact?release_id=${encodeURIComponent(releaseID)}`
  if (typeof XMLHttpRequest === 'undefined') {
    return nodeRequest('POST', path, {
      kind: 'raw',
      value: file,
      contentType: 'application/octet-stream'
    }).then(releaseOf)
  }
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest()
    request.open('POST', path)
    request.withCredentials = true
    request.setRequestHeader('Accept', 'application/json')
    request.setRequestHeader('Content-Type', 'application/octet-stream')
    request.setRequestHeader('X-ModemDeck-CSRF', csrfToken())
    request.upload.onprogress = event => {
      if (event.lengthComputable && event.total > 0) onProgress?.(event.loaded / event.total)
    }
    request.onerror = () => reject(new NodeApiError(0, 'network', ''))
    request.onabort = () => reject(new NodeApiError(0, 'network', ''))
    request.onload = () => {
      let parsed: unknown
      try {
        parsed = JSON.parse(request.responseText)
      } catch {
        parsed = null
      }
      if (request.status < 200 || request.status >= 300) {
        reject(failureOf(request.status, parsed))
        return
      }
      try {
        resolve(releaseOf(parsed))
      } catch (cause) {
        reject(cause)
      }
    }
    request.send(file)
  })
}

/** POST /api/v1/nodes/updates/remove. Answers the catalog as it now stands. */
export async function removeNodeRelease(releaseID: string): Promise<NodeReleaseCatalog> {
  return readNodeReleaseCatalog(
    await nodeRequest('POST', '/api/v1/nodes/updates/remove', {
      kind: 'json',
      value: { release_id: releaseID }
    })
  )
}

export type NodeUpdateAction = 'push' | 'apply' | 'confirm' | 'rollback'

export interface NodeUpdateActionResult {
  device: NodeUpdateDevice | null
  /**
   * True when an apply was sent but the device did not come back on the new
   * version in time (409 update_not_confirmed). That is an OUTCOME, with the
   * device's last answer attached: it rolls back by itself.
   */
  notConfirmed: boolean
  message: string
}

/**
 * POST /api/v1/nodes/updates/{push|apply|confirm|rollback}
 *
 * No client-side timeout and no retry: an apply legitimately takes minutes,
 * and repeating one that may have half-happened is the gateway's decision,
 * never this function's.
 */
export async function runNodeUpdateAction(
  action: NodeUpdateAction,
  deviceID: string,
  releaseID: string
): Promise<NodeUpdateActionResult> {
  try {
    const body = await nodeRequest('POST', `/api/v1/nodes/updates/${action}`, {
      kind: 'json',
      value: { device_id: deviceID, release_id: releaseID }
    })
    return { device: readNodeUpdateDevice(record(body)?.device), notConfirmed: false, message: '' }
  } catch (cause) {
    if (cause instanceof NodeApiError && cause.code === 'update_not_confirmed') {
      return {
        device: readNodeUpdateDevice(record(cause.body)?.device),
        notConfirmed: true,
        message: cause.message
      }
    }
    throw cause
  }
}

// ---------------------------------------------------------------------------
// Trust
// ---------------------------------------------------------------------------

export const TRUST_ACTIONS = [
  'receive_call',
  'receive_sms',
  'dial',
  'send_sms',
  'forward_otp',
  'update',
  'diagnose'
] as const
export type TrustAction = (typeof TRUST_ACTIONS)[number]

/** Mirrors trustengine.SubjectView. */
export interface TrustSubject {
  device: DevicePolicyStatus
  evidence: Record<EvidenceKey, EvidenceState>
  node_id: string
  state: TrustState | ''
  effective_state: TrustState | ''
  effective_reason: string
  record_verified: boolean | null
  stable_seconds: number | undefined
  required_seconds: number | undefined
  remaining_seconds: number | undefined
  sim_changes: number | undefined
  identity_changes: number | undefined
  integrity_failures: number | undefined
  risk_score: number | undefined
  otp_limited: boolean | null
  manual: boolean | null
  reason: string
  generation: number | undefined
  updated_at: string
  /** Per action: true, false, or null when the gateway did not list it. */
  allowed: Record<TrustAction, boolean | null>
}

function readTrustSubject(value: unknown): TrustSubject | null {
  const source = record(value)
  const nodeID = text(source?.node_id)
  if (!source || !nodeID) return null
  const allowed = record(source.allowed) ?? {}
  return {
    device: readDevicePolicyStatus(source.device),
    evidence: readTrustEvidence(source.evidence),
    node_id: nodeID,
    state: oneOf(source.state, TRUST_STATES) ?? '',
    effective_state: oneOf(source.effective_state, TRUST_STATES) ?? '',
    effective_reason: text(source.effective_reason),
    record_verified: flag(source.record_verified),
    stable_seconds: count(source.stable_seconds),
    required_seconds: count(source.required_seconds),
    remaining_seconds: count(source.remaining_seconds),
    sim_changes: count(source.sim_changes),
    identity_changes: count(source.identity_changes),
    integrity_failures: count(source.integrity_failures),
    risk_score: count(source.risk_score),
    otp_limited: flag(source.otp_limited),
    manual: flag(source.manual),
    reason: text(source.reason),
    generation: count(source.generation),
    updated_at: instant(source.updated_at),
    allowed: Object.fromEntries(
      TRUST_ACTIONS.map(action => [action, flag(allowed[action])])
    ) as Record<TrustAction, boolean | null>
  }
}

/** Mirrors trustengine.ChainReport. `ok` is null when it was not reported. */
export interface JournalHealth {
  ok: boolean | null
  events: number | undefined
  head_seq: number | undefined
  sealed_from: number | undefined
  problem: string
  first_bad_seq: number | undefined
  verified_at: string
}

export function readJournalHealth(value: unknown): JournalHealth {
  const source = record(value) ?? {}
  return {
    ok: flag(source.ok),
    events: count(source.events),
    head_seq: count(source.head_seq),
    // Zero is "never resealed" and is omitted by the gateway; keep it absent.
    sealed_from: count(source.sealed_from) || undefined,
    problem: text(source.problem),
    first_bad_seq: count(source.first_bad_seq) || undefined,
    verified_at: instant(source.verified_at)
  }
}

export const EVIDENCE_KEYS = [
  'device_identity',
  'sim_change',
  'modem_change',
  'otp_volume',
  'location',
  'node_local'
] as const
export type EvidenceKey = (typeof EVIDENCE_KEYS)[number]
export type EvidenceState = 'observed' | 'unsupported' | 'not_used' | 'unknown'

function readTrustEvidence(value: unknown): Record<EvidenceKey, EvidenceState> {
  const source = record(value) ?? {}
  return Object.fromEntries(EVIDENCE_KEYS.map(key => [key,
    oneOf(source[key], ['observed', 'unsupported', 'not_used'] as const) ?? 'unknown'
  ])) as Record<EvidenceKey, EvidenceState>
}

export interface TrustDocument {
  /** "monitor" evaluates and records and refuses nothing. Unreported is unknown. */
  enforcement: 'enforce' | 'monitor' | 'unknown'
  subjects: TrustSubject[]
  journal: JournalHealth
  evidence: Record<EvidenceKey, EvidenceState>
}

export function readTrustDocument(value: unknown): TrustDocument {
  const source = record(value) ?? {}
  const evidence = record(source.evidence) ?? {}
  const subjects = Array.isArray(source.subjects) ? source.subjects : []
  return {
    enforcement: oneOf(source.enforcement, ['enforce', 'monitor'] as const) ?? 'unknown',
    subjects: subjects
      .map(readTrustSubject)
      .filter((entry): entry is TrustSubject => entry !== null),
    journal: readJournalHealth(source.journal),
    evidence: Object.fromEntries(
      EVIDENCE_KEYS.map(key => [
        key,
        oneOf(evidence[key], ['observed', 'unsupported', 'not_used'] as const) ?? 'unknown'
      ])
    ) as Record<EvidenceKey, EvidenceState>
  }
}

/**
 * What the journal's last check established.
 *
 * Three answers, because the gateway's `ok: false` has two meanings: a check
 * that FAILED, and no check having run yet (trustengine.Engine.Chain returns a
 * zero report until the first verification, which has no `verified_at`). Only a
 * report that carries the instant it was made is a finding; anything else is
 * unknown, drawn as neither healthy nor damaged.
 */
export type JournalStatus = 'ok' | 'damaged' | 'unknown'

export function journalStatus(health: JournalHealth): JournalStatus {
  if (health.ok === null || !health.verified_at) return 'unknown'
  return health.ok ? 'ok' : 'damaged'
}

/**
 * The state decisions are made on, with the recorded one as the fallback for a
 * gateway that sent only that. '' when neither is a state this build knows.
 */
export function subjectState(subject: TrustSubject): TrustState | '' {
  return subject.effective_state || subject.state
}

/** How many devices are in each state; a state nobody is in is absent. */
export function trustCounts(document: TrustDocument): Partial<Record<TrustState, number>> {
  const counts: Partial<Record<TrustState, number>> = {}
  for (const subject of document.subjects) {
    const state = subjectState(subject)
    if (state) counts[state] = (counts[state] ?? 0) + 1
  }
  return counts
}

/**
 * Which administrator actions are worth offering for a device.
 *
 * Mirrors trustengine: Restrict never loosens (it takes the more restrictive
 * of the two states), so it is offered only where it would change something;
 * Restore is refused unless the device is restricted, quarantined, manually
 * held or has a damaged record. A state this build does not recognise offers
 * nothing rather than everything.
 */
export function trustActionsFor(subject: TrustSubject): TrustAdminAction[] {
  if (!subject.state) return []
  const actions: TrustAdminAction[] = []
  if (subject.state !== 'RESTRICTED' && subject.state !== 'QUARANTINE') actions.push('restrict')
  if (subject.state !== 'QUARANTINE') actions.push('quarantine')
  if (
    subject.state === 'RESTRICTED' ||
    subject.state === 'QUARANTINE' ||
    subject.manual === true ||
    subject.record_verified === false
  ) {
    actions.push('restore')
  }
  return actions
}

/** GET /api/v1/security/trust (administrators). */
export async function getTrustDocument(): Promise<TrustDocument> {
  return readTrustDocument(await nodeRequest('GET', '/api/v1/security/trust'))
}

/**
 * The reason an administrator must give.
 *
 * The gateway trims it and refuses an empty one or one over 200 BYTES
 * (security_trust_api.go measures `len(reason)`, which in Go is bytes), so the
 * length is counted in UTF-8 here too: about 66 Chinese characters.
 */
export const REASON_LIMIT_BYTES = 200

export function reasonBytes(reason: string): number {
  return new TextEncoder().encode(reason.trim()).length
}

export function reasonProblem(reason: string): '' | 'required' | 'too_long' {
  const size = reasonBytes(reason)
  if (size === 0) return 'required'
  return size > REASON_LIMIT_BYTES ? 'too_long' : ''
}

export type TrustAdminAction = 'restrict' | 'quarantine' | 'restore'

/**
 * POST /api/v1/security/trust/{restrict|quarantine|restore}
 *
 * Refuses locally without a usable reason, so the request the journal would
 * reject is never sent. Answers the trust document as it stands afterwards.
 */
export async function runTrustAction(
  action: TrustAdminAction,
  deviceID: string,
  reason: string
): Promise<TrustDocument> {
  const problem = reasonProblem(reason)
  if (problem) throw new NodeApiError(0, `reason_${problem}`, '')
  return readTrustDocument(
    await nodeRequest('POST', `/api/v1/security/trust/${action}`, {
      kind: 'json',
      value: { device_id: deviceID, reason: reason.trim() }
    })
  )
}

// ---------------------------------------------------------------------------
// Journal
// ---------------------------------------------------------------------------

/** Mirrors trustengine.Event. */
export interface JournalEvent {
  seq: number
  at: string
  node_id: string
  kind: string
  cause: string
  source: string
  result: string
  prev_hash: string
  hash: string
}

function readJournalEvent(value: unknown): JournalEvent | null {
  const source = record(value)
  const seq = count(source?.seq)
  if (!source || seq === undefined) return null
  return {
    seq,
    at: instant(source.at),
    node_id: text(source.node_id),
    kind: text(source.kind),
    cause: text(source.cause),
    source: text(source.source),
    result: text(source.result),
    prev_hash: text(source.prev_hash),
    hash: text(source.hash)
  }
}

export const JOURNAL_PAGE_SIZE = 50

export interface JournalPage {
  /** Newest first, whatever order the gateway answered in. */
  events: JournalEvent[]
  journal: JournalHealth
  /**
   * False when the answer shows the gateway ignored the newest-first query (a
   * build older than `order=desc` / `before=`). The entries are then whatever
   * that gateway chose to send -- its OLDEST page -- so the viewer must say the
   * list may be incomplete and must not page on from it.
   */
  newestFirst: boolean
  /** Whether an older page can exist. Never true for an ignored query. */
  more: boolean
}

/**
 * The query for one page, newest first.
 *
 * `order=desc` alone is the newest page; `before=<seq>` is the page older than
 * an entry already shown (exclusive). Both are always sent together so a
 * gateway that knows either reads the request the same way. `limit` is clamped
 * to what the route accepts (1..500), and a device filter rides on the same
 * call rather than being applied to a page after the fact.
 */
export function journalQuery(before: number | undefined, limit = JOURNAL_PAGE_SIZE, nodeID = ''): string {
  const query = new URLSearchParams({
    order: 'desc',
    limit: String(Math.max(1, Math.min(500, Math.floor(limit) || 1)))
  })
  if (before !== undefined && before > 0) query.set('before', String(Math.floor(before)))
  if (nodeID) query.set('node_id', nodeID)
  return query.toString()
}

/**
 * Whether an answer is the one that was asked for.
 *
 * A gateway that predates the newest-first query ignores `order` and `before`
 * and answers its first page oldest-first. That shows in the answer itself:
 * two entries in ascending order, or an entry at or beyond the `before` bound.
 * A single entry (or none) cannot be told apart and does not need to be -- it
 * is the same list either way.
 */
export function journalAnswerHonoured(events: JournalEvent[], before: number | undefined): boolean {
  if (before !== undefined && before > 0 && events.some(event => event.seq >= before)) return false
  for (let index = 1; index < events.length; index += 1) {
    if (events[index]!.seq > events[index - 1]!.seq) return false
  }
  return true
}

/**
 * GET /api/v1/security/journal?order=desc&limit=&before=&node_id= (administrators).
 *
 * One request, one page. Opening the viewer is `before` undefined; each older
 * page passes the lowest sequence number already shown.
 */
export async function getJournalPage(
  before: number | undefined,
  limit = JOURNAL_PAGE_SIZE,
  nodeID = ''
): Promise<JournalPage> {
  const body = record(
    await nodeRequest('GET', `/api/v1/security/journal?${journalQuery(before, limit, nodeID)}`)
  )
  const raw = Array.isArray(body?.events) ? body.events : []
  const events = raw
    .map(readJournalEvent)
    .filter((entry): entry is JournalEvent => entry !== null)
  const newestFirst = journalAnswerHonoured(events, before)
  const size = Math.max(1, Math.min(500, Math.floor(limit) || 1))
  const sorted = [...events].sort((left, right) => right.seq - left.seq)
  const oldest = sorted.length ? sorted[sorted.length - 1]!.seq : 0
  return {
    events: sorted,
    journal: readJournalHealth(body?.journal),
    newestFirst,
    // A full page may have more behind it; a short one is the end. Counted on
    // what the gateway sent, before entries without a sequence were dropped.
    more: newestFirst && raw.length >= size && oldest > 1
  }
}

/** What the viewer holds: the entries on screen and what may follow them. */
export interface JournalListing {
  events: JournalEvent[]
  more: boolean
  newestFirst: boolean
}

export const EMPTY_JOURNAL_LISTING: JournalListing = { events: [], more: false, newestFirst: true }

/**
 * A listing with one more page under it.
 *
 * `before` is what the page was asked with. Whatever came back, nothing at or
 * beyond it is added (so an ignored `before` cannot repeat rows already on
 * screen), and once any answer showed the query being ignored the listing
 * stays marked and stops paging.
 */
export function appendJournalPage(
  listing: JournalListing,
  page: JournalPage,
  before: number | undefined
): JournalListing {
  const fresh = before === undefined ? page.events : page.events.filter(event => event.seq < before)
  const newestFirst = listing.newestFirst && page.newestFirst
  return { events: [...listing.events, ...fresh], more: newestFirst && page.more, newestFirst }
}

/** The `before` for the next older page: the lowest sequence number shown. */
export function journalOlderBound(listing: JournalListing): number | undefined {
  return listing.events.length ? listing.events[listing.events.length - 1]!.seq : undefined
}

/** POST /api/v1/security/journal/verify: re-hash the whole journal now. */
export async function verifyJournal(): Promise<JournalHealth> {
  const body = record(
    await nodeRequest('POST', '/api/v1/security/journal/verify', { kind: 'json', value: {} })
  )
  return readJournalHealth(body?.journal)
}

/** POST /api/v1/security/journal/reseal {reason}: authorised recovery of a damaged journal. */
export async function resealJournal(reason: string): Promise<JournalHealth> {
  const problem = reasonProblem(reason)
  if (problem) throw new NodeApiError(0, `reason_${problem}`, '')
  const body = record(
    await nodeRequest('POST', '/api/v1/security/journal/reseal', {
      kind: 'json',
      value: { reason: reason.trim() }
    })
  )
  return readJournalHealth(body?.journal)
}
