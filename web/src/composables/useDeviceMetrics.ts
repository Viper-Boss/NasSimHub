import { computed, onBeforeUnmount, onMounted, reactive } from 'vue'
import type { SystemMetricsSnapshot } from '../api/client'
import { systemMetricsState, type SystemMetricsSample } from '../state/systemMetrics'

// Each component instance is keyed by device identity. Never borrow host metrics
// for a node whose optional metrics have not been reported.
export function useDeviceMetrics(deviceId: () => string | undefined) {
  const local = reactive({ metrics: null as SystemMetricsSnapshot | null, history: [] as SystemMetricsSample[], available: true, error: '' })
  const isNode = computed(() => !!deviceId()?.startsWith('NSH-'))
  let timer: ReturnType<typeof setInterval> | undefined
  let request: AbortController | undefined
  let stopped = false
  let lastObserved = ''
  let previousInterface: { name: string; rx_bytes: number; tx_bytes: number; at: number } | undefined
  async function poll() {
    request?.abort()
    const controller = new AbortController(); request = controller
    const id = deviceId()
    try {
      const response = await fetch('/api/v1/nodes', { credentials: 'same-origin', signal: controller.signal })
      if (!response.ok) throw new Error('unavailable')
      const body = await response.json()
      const node = body.nodes?.find((item: { node_id: string; online: boolean }) => item.node_id === id && item.online)
      if (stopped || controller.signal.aborted || id !== deviceId()) return
      const r = node?.resources
      if (!r) { local.metrics = null; local.history = []; return }
      const mem = r.memory_total_bytes > 0 ? 100 * (r.memory_total_bytes - r.memory_available_bytes) / r.memory_total_bytes : null
      // Unknown optional fields remain null; zero would imply a measurement.
      const interfaces = (r.metrics?.interfaces ?? []).filter((iface: { name: string }) => iface.name.startsWith('wwan'))
      const iface = interfaces[0]
      const at = Date.parse(r.observed_at)
      const seconds = previousInterface ? (at - previousInterface.at) / 1000 : 0
      const comparable = iface && previousInterface && iface.name === previousInterface.name && seconds > 0
      const rxRate = comparable && iface.rx_bytes >= previousInterface!.rx_bytes ? (iface.rx_bytes - previousInterface!.rx_bytes) / seconds : null
      const txRate = comparable && iface.tx_bytes >= previousInterface!.tx_bytes ? (iface.tx_bytes - previousInterface!.tx_bytes) / seconds : null
      const m = Object.fromEntries(['load_raw_1','load_raw_5','load_raw_15','load_percent_1','load_percent_5','load_percent_15','cores','memory_cached_bytes','memory_buffers_bytes','flash_total_bytes','flash_free_bytes','flash_used_percent','modem_temp_celsius','cpu_current_mhz','cpu_max_mhz','cpu_l1_data_kb','cpu_l1_inst_kb','cpu_l2_kb','cpu_l3_kb','cpu_temp_celsius','thread_count'].map(key => [key, null]))
      local.metrics = { ...m, ...r.metrics, observed_at: r.observed_at, cpu_model_name: r.metrics?.cpu_model_name ?? node.model ?? 'UFI003', cpu_usage_percent: r.cpu_percent, memory_total_bytes: r.memory_total_bytes, memory_available_bytes: r.memory_available_bytes, memory_used_percent: mem, process_count: r.process_count, host_uptime_seconds: r.uptime_seconds, interfaces, cellular: r.metrics?.cellular ?? null, available: true, module_channel: 'ready', flash_path: r.metrics?.flash_path ?? '', flash_fs_type: r.metrics?.flash_fs_type ?? '', flash_device_model: r.metrics?.flash_device_model ?? '', flash_device_path: r.metrics?.flash_device_path ?? '' } as unknown as SystemMetricsSnapshot
      if (r.observed_at !== lastObserved) {
        local.history = [...local.history, { cpuUsage: r.cpu_percent, mem, flash: r.metrics?.flash_used_percent ?? null, cpuTemp: r.metrics?.cpu_temp_celsius ?? null, modemTemp: r.metrics?.modem_temp_celsius ?? null, rxRate, txRate, at: Date.parse(r.observed_at) } as unknown as SystemMetricsSample].slice(-90)
        lastObserved = r.observed_at
        previousInterface = iface ? { ...iface, at } : undefined
      }
    } catch { if (!stopped && !controller.signal.aborted) { local.metrics = null; local.history = [] } }
  }
  onMounted(() => { if (isNode.value) { void poll(); timer = setInterval(() => { void poll() }, 10000) } })
  onBeforeUnmount(() => { stopped = true; clearInterval(timer); request?.abort() })
  return computed(() => isNode.value ? local : systemMetricsState)
}
