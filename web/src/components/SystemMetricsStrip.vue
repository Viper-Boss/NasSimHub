<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SystemMetricsSnapshot } from '../api/client'
import {
  acquireSystemMetrics,
  releaseSystemMetrics,
  systemMetricsState
} from '../state/systemMetrics'

const { t } = useI18n()

onMounted(() => { acquireSystemMetrics() })
onBeforeUnmount(() => { releaseSystemMetrics() })

const current = computed<SystemMetricsSnapshot | null>(() => systemMetricsState.metrics)
const history = computed(() => systemMetricsState.history)

function levelOf(percent: number): string {
  if (percent >= 90) return 'is-danger'
  if (percent >= 70) return 'is-warn'
  return 'is-ok'
}

function tempLevelOf(value: number | null | undefined): string {
  if (value === null || value === undefined) return ''
  if (value >= 85) return 'is-danger'
  if (value >= 70) return 'is-warn'
  return ''
}

const channelState = computed(() => current.value?.module_channel ?? '')
const channelStale = computed(() => channelState.value === 'stale')
const channelDown = computed(() => (
  !!current.value && channelState.value === 'unavailable'
))

const CHART_W = 200
const CHART_H = 56

function chartPoints(values: (number | null)[]): string {
  const points = values.filter((value): value is number => value !== null && Number.isFinite(value))
  if (points.length < 2) return ''
  const max = Math.max(...points)
  const min = Math.min(...points)
  const span = max - min || Math.max(Math.abs(max) * 0.1, 1)
  return points
    .map((value, index) => {
      const x = (index / (points.length - 1)) * CHART_W
      const y = CHART_H - 4 - ((value - min) / span) * (CHART_H - 8)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const modemTempSpark = computed(() => chartPoints(history.value.map(sample => sample.modemTemp)))
const cpuTempSpark = computed(() => chartPoints(history.value.map(sample => sample.cpuTemp)))
const cpuSpark = computed(() => chartPoints(history.value.map(sample => sample.cpu)))
const memSpark = computed(() => chartPoints(history.value.map(sample => sample.mem)))
const flashSpark = computed(() => chartPoints(history.value.map(sample => sample.flash)))

function formatTemp(value: number | null): string {
  return value === null || value === undefined ? '—' : `${value.toFixed(1)}°C`
}

function formatPercent(value: number | null): string {
  return value === null || value === undefined ? '—' : `${Math.round(value)}%`
}

function formatBytes(bytes: number): string {
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

function formatRate(bytesPerSecond: number): string {
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / 1024 / 1024).toFixed(1)} MB/s`
  if (bytesPerSecond >= 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`
  return `${Math.round(bytesPerSecond)} B/s`
}

function formatMHz(value: number): string {
  if (!value) return '—'
  return value >= 1000 ? `${(value / 1000).toFixed(2)} GHz` : `${Math.round(value)} MHz`
}

const cpuValue = computed(() => current.value ? current.value.load_percent_1 : null)
const memValue = computed(() => current.value ? current.value.memory_used_percent : null)
const flashValue = computed(() => current.value ? current.value.flash_used_percent : null)

function latestSeries(key: 'modemTemp' | 'cpuTemp' | 'rxRate'): number | null {
  const last = history.value.at(-1)
  if (!last) return null
  const value = last[key]
  return value === null || value === undefined ? null : value
}

const modemTempValue = computed(() => latestSeries('modemTemp'))
const cpuTempValue = computed(() => latestSeries('cpuTemp'))
const rxRateValue = computed(() => latestSeries('rxRate') ?? 0)
const modemTempLevel = computed(() => tempLevelOf(modemTempValue.value))
const cpuTempLevel = computed(() => tempLevelOf(cpuTempValue.value))

function formatCache(kb: number): string {
  if (!kb) return ''
  return kb >= 1024 ? `${(kb / 1024).toFixed(1)}MB` : `${kb}KB`
}

const cpuCacheLabel = computed(() => {
  const snapshot = current.value
  if (!snapshot) return '—'
  const parts: string[] = []
  const l1 = snapshot.cpu_l1_data_kb + snapshot.cpu_l1_inst_kb
  if (l1) parts.push(`L1 ${formatCache(l1)}`)
  if (snapshot.cpu_l2_kb) parts.push(`L2 ${formatCache(snapshot.cpu_l2_kb)}`)
  if (snapshot.cpu_l3_kb) parts.push(`L3 ${formatCache(snapshot.cpu_l3_kb)}`)
  return parts.join(' · ') || '—'
})

const loadLabel = computed(() => {
  if (!current.value) return '—'
  return `${Math.round(current.value.load_percent_1)}% · ${Math.round(current.value.load_percent_5)}% · ${Math.round(current.value.load_percent_15)}%`
})
</script>

<template>
  <p v-if="channelStale" class="tm__channel tm__channel--stale">{{ t('dashboard.metricsChannelStale') }}</p>
  <div v-if="systemMetricsState.available && current && !channelDown" class="tm">

    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsThermal') }}</span>
        <span class="tm-box__big" :class="modemTempLevel">{{ formatTemp(modemTempValue) }}</span>
      </header>
      <svg class="tm-box__chart" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
        <polyline v-if="modemTempSpark" :points="modemTempSpark" fill="none" class="tm-chart-line tm-chart-line--temp" stroke-width="1.6" />
        <polyline v-if="cpuTempSpark" :points="cpuTempSpark" fill="none" class="tm-chart-line tm-chart-line--cpu" stroke-width="1.2" stroke-dasharray="3 2" />
      </svg>
      <div class="tm-box__rows">
        <div class="tm-row"><span>{{ t('dashboard.metricsTemp') }}</span><b :class="modemTempLevel">{{ formatTemp(modemTempValue) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCpuTemp') }}</span><b :class="cpuTempLevel">{{ formatTemp(cpuTempValue) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsData') }}</span><b>{{ formatRate(rxRateValue) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsLoad') }}</span><b>{{ loadLabel }}</b></div>
      </div>
    </article>

    
    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsCpu') }}</span>
        <span class="tm-box__big" :class="levelOf(cpuValue ?? 0)">{{ formatPercent(cpuValue) }}</span>
      </header>
      <svg class="tm-box__chart" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
        <polyline v-if="cpuSpark" :points="cpuSpark" fill="none" class="tm-chart-line" stroke-width="1.6" />
      </svg>
      <div class="tm-box__rows">
        <div class="tm-row"><span>{{ t('dashboard.metricsModel') }}</span><b class="tm-row__wrap">{{ current.cpu_model_name || '—' }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCores') }}</span><b>{{ current.cores || '—' }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsFreq') }}</span><b>{{ formatMHz(current.cpu_current_mhz) }} / {{ formatMHz(current.cpu_max_mhz) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCache') }}</span><b>{{ cpuCacheLabel }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCpuTemp') }}</span><b :class="cpuTempLevel">{{ formatTemp(current.cpu_temp_celsius) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsProcesses') }}</span><b>{{ current.process_count || '—' }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsThreads') }}</span><b>{{ current.thread_count || '—' }}</b></div>
      </div>
    </article>

    
    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsMemory') }}</span>
        <span class="tm-box__big" :class="levelOf(memValue ?? 0)">{{ formatPercent(memValue) }}</span>
      </header>
      <svg class="tm-box__chart" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
        <polyline v-if="memSpark" :points="memSpark" fill="none" class="tm-chart-line" stroke-width="1.6" />
      </svg>
      <div class="tm-box__rows">
        <div class="tm-row"><span>{{ t('dashboard.metricsCapacity') }}</span><b>{{ formatBytes(current.memory_total_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsUsed') }}</span><b>{{ formatBytes(current.memory_total_bytes - current.memory_available_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsAvailable') }}</span><b>{{ formatBytes(current.memory_available_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCached') }}</span><b>{{ formatBytes(current.memory_cached_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsBuffers') }}</span><b>{{ formatBytes(current.memory_buffers_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsMemSpec') }}</span><b>—</b></div>
      </div>
    </article>

    
    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsFlash') }}</span>
        <span class="tm-box__big" :class="levelOf(flashValue ?? 0)">{{ formatPercent(flashValue) }}</span>
      </header>
      <svg class="tm-box__chart" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
        <polyline v-if="flashSpark" :points="flashSpark" fill="none" class="tm-chart-line" stroke-width="1.6" />
      </svg>
      <div class="tm-box__rows">
        <div class="tm-row"><span>{{ t('dashboard.metricsDevice') }}</span><b class="tm-row__wrap">{{ current.flash_device_model || current.flash_device_path || '—' }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsFs') }}</span><b>{{ current.flash_fs_type || '—' }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsCapacity') }}</span><b>{{ formatBytes(current.flash_total_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsFree') }}</span><b>{{ formatBytes(current.flash_free_bytes) }}</b></div>
        <div class="tm-row"><span>{{ t('dashboard.metricsMount') }}</span><b class="tm-row__wrap">{{ current.flash_path || '—' }}</b></div>
      </div>
    </article>
  </div>
  <p v-else-if="channelDown" class="tm__loading">{{ t('dashboard.metricsChannelDown') }}</p>
  <p v-else-if="systemMetricsState.available" class="tm__loading">{{ t('dashboard.metricsUnavailable') }}</p>
  <p v-else class="tm__loading">{{ systemMetricsState.error || t('dashboard.metricsUnavailable') }}</p>
</template>

<style scoped>
.tm {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 12px;
}

.tm__loading {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--muted);
}

.tm__channel {
  margin: 0 0 8px;
  font-size: 11.5px;
  color: var(--muted);
}

.tm__channel--stale {
  color: var(--warning, #fbbf24);
}

.tm-row b.is-warn { color: var(--warning, #fbbf24); }
.tm-row b.is-danger { color: var(--danger, #f87171); }

.tm-box {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 10px;
}

.tm-box__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.tm-box__title {
  font-size: 12px;
  font-weight: 650;
  color: var(--text);
}

.tm-box__big {
  font-size: 20px;
  font-weight: 750;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

.tm-box__big.is-ok { color: var(--success, #34d399); }
.tm-box__big.is-warn { color: var(--warning, #fbbf24); }
.tm-box__big.is-danger { color: var(--danger, #f87171); }

.tm-box__chart {
  display: block;
  width: 100%;
  height: 56px;
}

.tm-chart-line {
  stroke: var(--accent, #4f8cff);
  stroke-linejoin: round;
  stroke-linecap: round;
}

.tm-chart-line--temp { stroke: #f59e0b; }
.tm-chart-line--cpu { stroke: var(--muted); }

.tm-box__rows {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding-top: 6px;
  border-top: 1px solid var(--border);
}

.tm-row {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.tm-row span {
  flex: 0 0 auto;
  color: var(--muted);
  font-size: 11px;
}

.tm-row b {
  overflow: hidden;
  font-size: 11.5px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text);
}

.tm-row b.tm-row__wrap {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 1100px) {
  .tm {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .tm {
    grid-template-columns: 1fr;
  }
}
</style>
