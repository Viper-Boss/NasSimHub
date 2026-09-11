import { reactive } from 'vue'
import { getWireGuardSettings } from '../api/client'

// Shared singleton poller for WireGuard transfer counters, built on the same
// pattern as the system-metrics poller: the dashboard card and the settings
// card both read from here, so the endpoint is polled once rather than once per
// consumer.
//
// The agent reports cumulative rx/tx per peer, not rates. A rate chart
// therefore has to be built from differences between samples, which has two
// consequences worth stating plainly:
//
//   * The history only covers what this browser has watched. It is mirrored to
//     localStorage so a reload does not wipe it, but it is not gateway history
//     and it does not go back before the first time the chart was opened.
//   * Counters restart whenever the interface is recreated. A restart would
//     otherwise read as one enormous spike, so a sample whose counters went
//     backwards is recorded as a zero rate rather than a negative or a jump.

export interface WireGuardTrafficSample {
  /** Bytes per second received across all peers since the previous sample. */
  rxRate: number
  /** Bytes per second sent across all peers since the previous sample. */
  txRate: number
  /** Epoch millis when the sample was taken; used to expire restored history. */
  at: number
}

export const wireGuardTrafficState = reactive({
  /** Cumulative totals across every peer, as last reported. */
  rxTotal: 0,
  txTotal: 0,
  /** Peers whose handshake is recent enough to be carrying traffic. */
  livePeers: 0,
  peerCount: 0,
  interfaceState: '' as string,
  history: [] as WireGuardTrafficSample[],
  /** False once a poll has failed; the card says so rather than showing zeros. */
  available: true,
  error: '',
  /** True once at least one poll has completed, successfully or not. */
  loaded: false
})

const POLL_MS = 10_000
// 90 samples x 10s is 15 minutes of history, matching the system metrics chart.
const HISTORY = 90
const STORAGE_KEY = 'modemdeck.wireguardTraffic.history'
const HISTORY_TTL_MS = HISTORY * POLL_MS + 5_000

function restoreHistory(): WireGuardTrafficSample[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const cutoff = Date.now() - HISTORY_TTL_MS
    return parsed
      .filter((item): item is WireGuardTrafficSample => (
        !!item &&
        typeof item === 'object' &&
        typeof (item as WireGuardTrafficSample).at === 'number' &&
        typeof (item as WireGuardTrafficSample).rxRate === 'number' &&
        typeof (item as WireGuardTrafficSample).txRate === 'number'
      ))
      .filter(item => item.at >= cutoff)
      .slice(-HISTORY)
  } catch {
    return []
  }
}

function persistHistory(samples: WireGuardTrafficSample[]): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(samples.slice(-HISTORY)))
  } catch {
    // Quota or privacy-mode failures must never break polling.
  }
}

wireGuardTrafficState.history = restoreHistory()

let timer: ReturnType<typeof setInterval> | undefined
let previous: { rx: number; tx: number; at: number } | null = null
let pollInFlight = false
let consumers = 0

/** Bytes per second between two counter readings, or 0 if the counter reset. */
function rateBetween(current: number, earlier: number, seconds: number): number {
  if (seconds <= 0) return 0
  // A counter that went backwards means the interface was recreated. There is
  // no meaningful rate across that boundary, and reporting the raw difference
  // would draw a spike that never happened.
  if (current < earlier) return 0
  return (current - earlier) / seconds
}

async function poll(): Promise<void> {
  if (pollInFlight) return
  pollInFlight = true
  try {
    const response = await getWireGuardSettings()
    const peers = response.status.peers ?? []
    const rx = peers.reduce((total, peer) => total + (peer.rx_bytes || 0), 0)
    const tx = peers.reduce((total, peer) => total + (peer.tx_bytes || 0), 0)
    const at = Date.now()

    const baseline = previous
    let rxRate = 0
    let txRate = 0
    if (baseline) {
      const seconds = (at - baseline.at) / 1000
      rxRate = rateBetween(rx, baseline.rx, seconds)
      txRate = rateBetween(tx, baseline.tx, seconds)
    }
    previous = { rx, tx, at }

    wireGuardTrafficState.rxTotal = rx
    wireGuardTrafficState.txTotal = tx
    wireGuardTrafficState.peerCount = peers.length
    wireGuardTrafficState.livePeers = peers.filter(peer => peer.current).length
    wireGuardTrafficState.interfaceState = response.status.state || ''
    wireGuardTrafficState.available = true
    wireGuardTrafficState.error = ''
    wireGuardTrafficState.loaded = true

    // The first reading of a polling run has nothing to be differenced against,
    // so it carries no rate. Recording it anyway would plant a false zero at
    // the join between the restored history and the new samples.
    if (baseline) {
      wireGuardTrafficState.history = [
        ...wireGuardTrafficState.history,
        { rxRate, txRate, at }
      ].slice(-HISTORY)
      persistHistory(wireGuardTrafficState.history)
    }
  } catch (error) {
    wireGuardTrafficState.available = false
    wireGuardTrafficState.error = error instanceof Error ? error.message : String(error)
    wireGuardTrafficState.loaded = true
  }
  pollInFlight = false
}

function start(): void {
  if (timer) return
  void poll()
  timer = setInterval(() => { void poll() }, POLL_MS)
}

function stop(): void {
  if (timer) {
    clearInterval(timer)
    timer = undefined
  }
  // Drop the baseline: after a pause the next reading would otherwise be
  // differenced against a stale one and reported as a single huge burst.
  previous = null
}

/** Starts polling when the first consumer mounts. */
export function acquireWireGuardTraffic(): void {
  consumers += 1
  start()
}

/** Stops polling when the last consumer unmounts. */
export function releaseWireGuardTraffic(): void {
  consumers = Math.max(0, consumers - 1)
  if (consumers === 0) stop()
}
