<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSystemMetrics, type SystemMetricsSnapshot, type SystemMetricsInterfaceCounters } from '../api/client'

const { t } = useI18n()

const POLL_MS = 10_000
const HISTORY = 40

interface Sample {
  temp: number | null
  cpu: number
  mem: number
  flash: number
  rxRate: number
}

const history = ref<Sample[]>([])
const current = ref<SystemMetricsSnapshot | null>(null)
const available = ref(true)
const errorText = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let previous: SystemMetricsSnapshot | null = null
let pollInFlight = false

function modemInterface(snapshot: SystemMetricsSnapshot): SystemMetricsInterfaceCounters | null {
  const list = snapshot.interfaces || []
  const usb = list.find(item => item.name.startsWith('enx') || item.name.startsWith('usb'))
  if (usb) return usb
  return list.reduce<SystemMetricsInterfaceCounters | null>((best, item) => (
    !best || item.rx_bytes > best.rx_bytes ? item : best
  ), null)
}

function levelOf(percent: number): string {
  if (percent >= 90) return 'is-danger'
  if (percent >= 70) return 'is-warn'
  return 'is-ok'
}

function sparkline(values: (number | null)[]): string {
  const points = values.filter((value): value is number => value !== null)
  if (points.length < 2) return ''
  const width = 96
  const height = 26
  const max = Math.max(...points, 0.0001)
  const min = Math.min(...points, 0)
  const span = max - min || 1
  return points
    .map((value, index) => {
      const x = (index / (points.length - 1)) * width
      const y = height - 2 - ((value - min) / span) * (height - 4)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

function latest(key: keyof Sample): number | null {
  const last = history.value.at(-1)
  if (!last) return null
  return last[key]
}

const tempValue = computed(() => latest('temp'))
const cpuValue = computed(() => latest('cpu'))
const memValue = computed(() => latest('mem'))
const flashValue = computed(() => latest('flash'))
const rxRateValue = computed(() => latest('rxRate'))

const tempSpark = computed(() => sparkline(history.value.map(sample => sample.temp)))
const cpuSpark = computed(() => sparkline(history.value.map(sample => sample.cpu)))
const memSpark = computed(() => sparkline(history.value.map(sample => sample.mem)))
const flashSpark = computed(() => sparkline(history.value.map(sample => sample.flash)))
const rxSpark = computed(() => sparkline(history.value.map(sample => sample.rxRate)))

function formatTemp(value: number | null): string {
  return value === null ? '—' : `${value.toFixed(1)}°C`
}

function formatPercent(value: number | null): string {
  return value === null ? '—' : `${Math.round(value)}%`
}

function formatRate(bytesPerSecond: number | null): string {
  if (bytesPerSecond === null) return '—'
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / 1024 / 1024).toFixed(1)} MB/s`
  if (bytesPerSecond >= 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`
  return `${Math.round(bytesPerSecond)} B/s`
}

const loadLabel = computed(() => {
  if (!current.value) return '—'
  const one = Math.round(current.value.load_percent_1)
  const five = Math.round(current.value.load_percent_5)
  const fifteen = Math.round(current.value.load_percent_15)
  return `${one}% · ${five}% · ${fifteen}%`
})

async function poll(): Promise<void> {
  if (pollInFlight) return
  pollInFlight = true
  try {
    const snapshot = await getSystemMetrics()
    if (!snapshot.available) {
      available.value = false
      errorText.value = snapshot.error || ''
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
    current.value = snapshot
    available.value = true
    errorText.value = ''
    history.value = [
      ...history.value,
      {
        temp: snapshot.modem_temp_celsius,
        cpu: snapshot.load_percent_1,
        mem: snapshot.memory_used_percent,
        flash: snapshot.flash_used_percent,
        rxRate
      }
    ].slice(-HISTORY)
  } catch (error) {
    available.value = false
    errorText.value = error instanceof Error ? error.message : String(error)
  }
  pollInFlight = false
}

onMounted(() => {
  void poll()
  timer = setInterval(() => { void poll() }, POLL_MS)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="system-metrics">
    <template v-if="available">
      <div class="system-metrics__blocks">
        <div class="system-metrics__block" :title="t('dashboard.metricsTemp')">
          <span class="system-metrics__label">{{ t('dashboard.metricsTemp') }}</span>
          <span class="system-metrics__value">{{ formatTemp(tempValue) }}</span>
          <svg v-if="tempSpark" class="system-metrics__spark" viewBox="0 0 96 26" preserveAspectRatio="none">
            <polyline :points="tempSpark" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </div>
        <div class="system-metrics__block" :class="levelOf(cpuValue ?? 0)" :title="t('dashboard.metricsCpu')">
          <span class="system-metrics__label">{{ t('dashboard.metricsCpu') }}</span>
          <span class="system-metrics__value">{{ formatPercent(cpuValue) }}</span>
          <svg v-if="cpuSpark" class="system-metrics__spark" viewBox="0 0 96 26" preserveAspectRatio="none">
            <polyline :points="cpuSpark" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </div>
        <div class="system-metrics__block" :class="levelOf(memValue ?? 0)" :title="t('dashboard.metricsMemory')">
          <span class="system-metrics__label">{{ t('dashboard.metricsMemory') }}</span>
          <span class="system-metrics__value">{{ formatPercent(memValue) }}</span>
          <svg v-if="memSpark" class="system-metrics__spark" viewBox="0 0 96 26" preserveAspectRatio="none">
            <polyline :points="memSpark" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </div>
        <div class="system-metrics__block" :class="levelOf(flashValue ?? 0)" :title="t('dashboard.metricsFlash')">
          <span class="system-metrics__label">{{ t('dashboard.metricsFlash') }}</span>
          <span class="system-metrics__value">{{ formatPercent(flashValue) }}</span>
          <svg v-if="flashSpark" class="system-metrics__spark" viewBox="0 0 96 26" preserveAspectRatio="none">
            <polyline :points="flashSpark" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </div>
        <div class="system-metrics__block" :title="t('dashboard.metricsData')">
          <span class="system-metrics__label">{{ t('dashboard.metricsData') }}</span>
          <span class="system-metrics__value">{{ formatRate(rxRateValue) }}</span>
          <svg v-if="rxSpark" class="system-metrics__spark" viewBox="0 0 96 26" preserveAspectRatio="none">
            <polyline :points="rxSpark" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </div>
      </div>
      <div v-if="current" class="system-metrics__load" :title="t('dashboard.metricsLoadHint')">
        <span>{{ t('dashboard.metricsLoad') }}</span>
        <strong>{{ loadLabel }}</strong>
      </div>
    </template>
    <p v-else class="system-metrics__error">{{ errorText || t('dashboard.metricsUnavailable') }}</p>
  </div>
</template>

<style scoped>
.system-metrics {
  display: flex;
  flex-wrap: wrap;
  align-items: stretch;
  gap: 8px;
  margin-bottom: 12px;
}

.system-metrics__blocks {
  display: flex;
  flex: 1 1 auto;
  flex-wrap: wrap;
  gap: 8px;
}

.system-metrics__block {
  display: flex;
  flex: 1 1 120px;
  flex-direction: column;
  gap: 2px;
  min-width: 110px;
  padding: 8px 10px;
  color: var(--muted);
  background: var(--surface-subtle);
  border-radius: 10px;
}

.system-metrics__block.is-ok { color: var(--success, #34d399); }
.system-metrics__block.is-warn { color: var(--warning, #fbbf24); }
.system-metrics__block.is-danger { color: var(--danger, #f87171); }

.system-metrics__label {
  font-size: 10.5px;
  color: var(--muted);
}

.system-metrics__value {
  font-size: 15px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

.system-metrics__block.is-ok .system-metrics__value,
.system-metrics__block.is-warn .system-metrics__value,
.system-metrics__block.is-danger .system-metrics__value {
  color: currentColor;
}

.system-metrics__spark {
  display: block;
  width: 96px;
  max-width: 100%;
  height: 26px;
}

.system-metrics__load {
  display: flex;
  flex: 0 0 auto;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
  padding: 8px 12px;
  background: var(--surface-subtle);
  border-radius: 10px;
}

.system-metrics__load span {
  font-size: 10.5px;
  color: var(--muted);
}

.system-metrics__load strong {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

.system-metrics__error {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
</style>
