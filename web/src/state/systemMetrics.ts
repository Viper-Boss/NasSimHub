import { reactive } from 'vue'
import {
  getSystemMetrics,
  type SystemMetricsInterfaceCounters,
  type SystemMetricsSnapshot
} from '../api/client'

// Shared singleton poller for the dashboard system metrics. The metrics strip
// (task-manager boxes) and the module cards (serving-cell radio facts) both
// read from here so the API is polled once, not once per consumer.

export interface SystemMetricsSample {
  modemTemp: number | null
  cpuTemp: number | null
  /** Real utilization percent from /proc/stat deltas. */
  cpuUsage: number
  mem: number
  flash: number
  rxRate: number
  txRate?: number
  /** Epoch millis when the sample was taken; used to expire restored history. */
  at?: number
}

export const systemMetricsState = reactive({
  metrics: null as SystemMetricsSnapshot | null,
  history: [] as SystemMetricsSample[],
  available: true,
  error: ''
})

const POLL_MS = 10_000
// 90 samples x 10s covers the 15-minute utilization average window.
const HISTORY = 90
const STORAGE_KEY = 'modemdeck.systemMetrics.history'
const HISTORY_TTL_MS = HISTORY * POLL_MS + 5_000

// The sparklines should survive a page reload: samples are timestamped and
// mirrored to localStorage, then restored (minus expired entries) on startup.
function restoreHistory(): SystemMetricsSample[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const cutoff = Date.now() - HISTORY_TTL_MS
    return parsed
      .filter((item): item is SystemMetricsSample => (
        !!item && typeof item === 'object' && typeof (item as SystemMetricsSample).at === 'number'
      ))
      .filter(item => (item.at as number) >= cutoff)
      .slice(-HISTORY)
  } catch {
    return []
  }
}

function persistHistory(samples: SystemMetricsSample[]): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(samples.slice(-HISTORY)))
  } catch {
    // Quota or privacy-mode failures must never break polling.
  }
}

systemMetricsState.history = restoreHistory()

let timer: ReturnType<typeof setInterval> | undefined
let previous: SystemMetricsSnapshot | null = null
let pollInFlight = false
let consumers = 0

export function modemInterface(
  snapshot: SystemMetricsSnapshot
): SystemMetricsInterfaceCounters | null {
  const list = snapshot.interfaces || []
  const usb = list.find(item => item.name.startsWith('enx') || item.name.startsWith('usb'))
  if (usb) return usb
  return list.reduce<SystemMetricsInterfaceCounters | null>((best, item) => (
    !best || item.rx_bytes > best.rx_bytes ? item : best
  ), null)
}

async function poll(): Promise<void> {
  if (pollInFlight) return
  pollInFlight = true
  try {
    const snapshot = await getSystemMetrics()
    if (!snapshot.available) {
      systemMetricsState.available = false
      systemMetricsState.error = snapshot.error || ''
      pollInFlight = false
      return
    }
    const iface = modemInterface(snapshot)
    let rxRate = 0
    let txRate = 0
    if (previous && iface) {
      const previousIface = modemInterface(previous)
      const seconds = (Date.parse(snapshot.observed_at) - Date.parse(previous.observed_at)) / 1000
      if (
        previousIface &&
        previousIface.name === iface.name &&
        seconds > 0 &&
        iface.rx_bytes >= previousIface.rx_bytes
      ) {
        rxRate = (iface.rx_bytes - previousIface.rx_bytes) / seconds
        txRate = iface.tx_bytes >= previousIface.tx_bytes
          ? (iface.tx_bytes - previousIface.tx_bytes) / seconds
          : 0
      }
    }
    previous = snapshot
    systemMetricsState.metrics = snapshot
    systemMetricsState.available = true
    systemMetricsState.error = ''
    systemMetricsState.history = [
      ...systemMetricsState.history,
      {
        modemTemp: snapshot.modem_temp_celsius,
        cpuTemp: snapshot.cpu_temp_celsius,
        cpuUsage: snapshot.cpu_usage_percent,
        mem: snapshot.memory_used_percent,
        flash: snapshot.flash_used_percent,
        rxRate,
        txRate,
        at: Date.now()
      }
    ].slice(-HISTORY)
    persistHistory(systemMetricsState.history)
  } catch (error) {
    systemMetricsState.available = false
    systemMetricsState.error = error instanceof Error ? error.message : String(error)
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
}

/** Increments the consumer count and starts polling when the first consumer mounts. */
export function acquireSystemMetrics(): void {
  consumers += 1
  start()
}

/** Decrements the consumer count and stops polling when the last consumer unmounts. */
export function releaseSystemMetrics(): void {
  consumers = Math.max(0, consumers - 1)
  if (consumers === 0) stop()
}
