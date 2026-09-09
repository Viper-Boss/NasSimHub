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
  cpu: number
  mem: number
  flash: number
  rxRate: number
}

export const systemMetricsState = reactive({
  metrics: null as SystemMetricsSnapshot | null,
  history: [] as SystemMetricsSample[],
  available: true,
  error: ''
})

const POLL_MS = 10_000
const HISTORY = 60

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
        cpu: snapshot.load_percent_1,
        mem: snapshot.memory_used_percent,
        flash: snapshot.flash_used_percent,
        rxRate
      }
    ].slice(-HISTORY)
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
