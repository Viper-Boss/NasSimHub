import { API_ROOT, fixtureMode, get, writeJSON } from './client'

// Call forwarding and call waiting.
//
// In its own module rather than in client.ts, because client.ts is the startup
// bundle: every visitor downloads it before the app draws anything, and this
// feature is reachable from exactly one settings page that most people never
// open. Measured cost of having it in client.ts: 0.3 KiB gzip of startup
// payload, which took the bundle over budget.
//
// These are the only settings in this app that the gateway does not own: they
// live in the operator's network against the subscriber. So there is no
// revision and no local copy -- every read is a live round trip, every write is
// followed by a read-back, and what the panel shows is always what the network
// just said rather than what was asked for. A handset with the same SIM can
// change the same state, so a cached value would go stale silently.

export type CallForwardReason = 'unconditional' | 'busy' | 'no_reply' | 'unreachable'

export interface CallForwardingRule {
  reason: CallForwardReason
  active: boolean
  number: string
  /** Ring delay before forwarding; 'no_reply' only. */
  seconds?: number
  /** The network refused to report this condition; `active` means nothing. */
  unreadable?: boolean
}

export interface CallServices {
  line_id: string
  forwarding: CallForwardingRule[]
  waiting_active: boolean
  /** False when the modem said nothing about voice call waiting, which is not
   *  the same as off. */
  waiting_known: boolean
  errors?: Record<string, string>
  observed_at: string
}

export interface CallServicesResponse {
  call_services: CallServices
  /** The agent cannot reach these services at all -- a build or capability
   *  problem, not something retrying will fix. */
  unavailable?: boolean
  error?: string
}

function callServicesPath(lineID: string): string {
  return `${API_ROOT}/lines/${encodeURIComponent(lineID)}/call-services`
}

const fixtureCallServices: CallServices = {
  line_id: 'line-a',
  forwarding: [
    { reason: 'unconditional', active: false, number: '' },
    { reason: 'busy', active: true, number: '+8613800138000' },
    { reason: 'no_reply', active: true, number: '+8613800138000', seconds: 20 },
    { reason: 'unreachable', active: false, number: '' }
  ],
  waiting_active: true,
  waiting_known: true,
  observed_at: new Date().toISOString()
}

export async function getCallServices(lineID: string): Promise<CallServicesResponse> {
  if (fixtureMode) {
    return { call_services: { ...fixtureCallServices, line_id: lineID } }
  }
  const result = await get(callServicesPath(lineID)) as CallServicesResponse | null
  if (!result?.call_services) throw new Error('呼叫业务状态响应无效。')
  return result
}

export async function setCallForwarding(
  lineID: string,
  change: { reason: CallForwardReason; enabled: boolean; number: string; seconds?: number }
): Promise<CallServicesResponse> {
  if (fixtureMode) throw new Error('当前为演示模式，呼叫转移未下发到网络。')
  return await writeJSON(
    callServicesPath(lineID), 'PUT', { forwarding: { ...change, seconds: change.seconds ?? 0 } }, 200
  ) as CallServicesResponse
}

export async function setCallWaiting(
  lineID: string,
  enabled: boolean
): Promise<CallServicesResponse> {
  if (fixtureMode) throw new Error('当前为演示模式，呼叫等待未下发到网络。')
  return await writeJSON(
    callServicesPath(lineID), 'PUT', { waiting: { enabled } }, 200
  ) as CallServicesResponse
}

