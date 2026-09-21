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
// Single source of truth for "the banner is already explaining the state", so
// the no-snapshot fallback below cannot repeat the same sentence underneath it.
const channelBannerVisible = computed(() => (
  channelDown.value || channelStale.value || !systemMetricsState.available
))

const CHART_W = 300
const CHART_H = 96

function cleanSeries(values: (number | null | undefined)[]): number[] {
  return values.filter((value): value is number => (
    value !== null && value !== undefined && Number.isFinite(value)
  ))
}

function percentPoints(values: (number | null | undefined)[]): string {
  const points = cleanSeries(values)
  if (points.length < 2) return ''
  return points
    .map((value, index) => {
      const x = (index / (points.length - 1)) * CHART_W
      const clamped = Math.min(100, Math.max(0, value))
      const y = CHART_H - (clamped / 100) * CHART_H
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

function percentArea(values: (number | null | undefined)[]): string {
  const line = percentPoints(values)
  if (!line) return ''
  return `${line} ${CHART_W},${CHART_H} 0,${CHART_H}`
}

function rateSeries(values: (number | null | undefined)[]): number[] {
  return cleanSeries(values)
}

function ratePoints(values: (number | null | undefined)[], max: number): string {
  const points = rateSeries(values)
  if (points.length < 2 || max <= 0) return ''
  return points
    .map((value, index) => {
      const x = (index / (points.length - 1)) * CHART_W
      const y = CHART_H - Math.min(1, value / max) * (CHART_H - 2) - 1
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const rateMax = computed(() => {
  const peak = Math.max(
    ...history.value.map(sample => Math.max(sample.rxRate ?? 0, sample.txRate ?? 0)),
    1
  )
  return peak * 1.15
})

const cpuArea = computed(() => percentArea(history.value.map(sample => sample.cpuUsage)))
const cpuLine = computed(() => percentPoints(history.value.map(sample => sample.cpuUsage)))
// Temperature history overlays the utilization chart, mapped 0-100°C to 0-100%.
const cpuTempLine = computed(() => percentPoints(history.value.map(sample => sample.cpuTemp)))
const cpuTempArea = computed(() => {
  const line = cpuTempLine.value
  return line ? `${line} ${CHART_W},${CHART_H} 0,${CHART_H}` : ''
})
const memArea = computed(() => percentArea(history.value.map(sample => sample.mem)))
const memLine = computed(() => percentPoints(history.value.map(sample => sample.mem)))
const rxLine = computed(() => ratePoints(history.value.map(sample => sample.rxRate), rateMax.value))
const txLine = computed(() => ratePoints(history.value.map(sample => sample.txRate ?? null), rateMax.value))
const txArea = computed(() => {
  const line = txLine.value
  return line ? `${line} ${CHART_W},${CHART_H} 0,${CHART_H}` : ''
})

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
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / 1024 / 1024).toFixed(2)} MB/s`
  if (bytesPerSecond >= 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`
  return `${Math.round(bytesPerSecond)} B/s`
}

const cpuValue = computed(() => {
  const last = history.value.at(-1)
  const usage = last?.cpuUsage
  if (typeof usage === 'number' && Number.isFinite(usage)) return usage
  return current.value ? current.value.cpu_usage_percent : null
})
const memValue = computed(() => current.value ? current.value.memory_used_percent : null)
const flashValue = computed(() => current.value ? current.value.flash_used_percent : null)

function latestOf(key: 'modemTemp' | 'cpuTemp' | 'rxRate' | 'txRate'): number | null {
  const last = history.value.at(-1)
  if (!last) return null
  const value = last[key]
  return value === null || value === undefined ? null : value
}

const cpuTempValue = computed(() => latestOf('cpuTemp'))
const rxRateValue = computed(() => latestOf('rxRate') ?? 0)
const txRateValue = computed(() => latestOf('txRate') ?? 0)
const cpuTempLevel = computed(() => tempLevelOf(cpuTempValue.value))
// The modem temperature is still collected and kept in the history samples
// (see state/systemMetrics.ts, 'modemTemp'), but this strip has no tile for it
// and no label to name one, so nothing derives from it here.

// Load averages as mean real utilization over 1/5/15 minutes. The kernel's
// loadavg overstates this single-core modem (it counts uninterruptible
// tasks), so the panel rolls /proc/stat utilization samples instead.
function windowAverageUtilization(windowMs: number): number | null {
  const cutoff = Date.now() - windowMs
  let total = 0
  let count = 0
  for (const sample of history.value) {
    if ((sample.at ?? 0) < cutoff) continue
    const usage = sample.cpuUsage
    if (typeof usage !== 'number' || !Number.isFinite(usage)) continue
    total += usage
    count += 1
  }
  return count > 0 ? total / count : null
}

const loadAvg1 = computed(() => windowAverageUtilization(60_000))
const loadAvg5 = computed(() => windowAverageUtilization(300_000))
const loadAvg15 = computed(() => windowAverageUtilization(900_000))

const rateMaxYLabel = computed(() => formatRate(rateMax.value))
const rateMidYLabel = computed(() => formatRate(rateMax.value / 2))

const cpuMeta = computed(() => {
  const snapshot = current.value
  if (!snapshot) return ''
  const parts: string[] = []
  if (snapshot.cpu_model_name) parts.push(snapshot.cpu_model_name.replace(/\s+/g, ' '))
  if (snapshot.cores) parts.push(`${snapshot.cores} ${t('dashboard.metricsCoresUnit')}`)
  if (snapshot.process_count) parts.push(`${snapshot.process_count} ${t('dashboard.metricsProcesses')}`)
  if (snapshot.thread_count) parts.push(`${snapshot.thread_count} ${t('dashboard.metricsThreadsUnit')}`)
  return parts.join(' | ')
})

const memMeta = computed(() => formatBytes(current.value?.memory_total_bytes ?? 0))

const flashMeta = computed(() => {
  const snapshot = current.value
  if (!snapshot) return ''
  const fs = snapshot.flash_fs_type || ''
  const device = snapshot.flash_device_model || snapshot.flash_device_path || ''
  return [device, fs].filter(Boolean).join(' · ')
})

const cellular = computed(() => current.value?.cellular ?? null)

function bandLabel(cell: NonNullable<SystemMetricsSnapshot['cellular']> | null): string {
  if (!cell || !cell.band) return '—'
  const rat = (cell.rat || '').toUpperCase()
  if (rat.startsWith('NR')) return `n${cell.band}`
  return `B${cell.band}`
}

const cellularMeta = computed(() => {
  const cell = cellular.value
  if (!cell) return t('dashboard.metricsUnavailable')
  const parts: string[] = []
  if (cell.plmn) parts.push(cell.plmn)
  const rat = (cell.rat || '').toUpperCase()
  if (rat) parts.push(rat)
  const band = bandLabel(cell)
  if (band !== '—') parts.push(band)
  if (typeof cell.rsrp === 'number' && cell.rsrp !== 0) parts.push(`${cell.rsrp} dBm`)
  return parts.join(' · ') || t('dashboard.metricsUnavailable')
})

const sinrValue = computed(() => {
  const value = cellular.value?.sinr
  return typeof value === 'number' && value !== 0 ? `${value.toFixed(1)} dB` : '—'
})

const usedBytes = computed(() => {
  const snapshot = current.value
  if (!snapshot || !snapshot.memory_total_bytes) return 0
  return Math.max(0, snapshot.memory_total_bytes - snapshot.memory_available_bytes)
})

const memSegments = computed(() => {
  const snapshot = current.value
  if (!snapshot || !snapshot.memory_total_bytes) return null
  const total = snapshot.memory_total_bytes
  const used = usedBytes.value
  const cached = Math.min(snapshot.memory_cached_bytes ?? 0, used)
  const buffers = Math.min(snapshot.memory_buffers_bytes ?? 0, used - cached)
  const free = Math.max(0, total - used)
  return {
    usedPct: (used / total) * 100,
    cachedPct: (cached / total) * 100,
    buffersPct: (buffers / total) * 100,
    freePct: (free / total) * 100,
    used, cached, buffers, free
  }
})

const flashUsedBytes = computed(() => {
  const snapshot = current.value
  if (!snapshot || !snapshot.flash_total_bytes) return 0
  return Math.max(0, snapshot.flash_total_bytes - snapshot.flash_free_bytes)
})
</script>

<template>
  <!-- Keep the four tiles mounted whenever we hold any snapshot: a wedged
       module channel must not collapse the dashboard layout. Values freeze at
       the last good sample and the banner explains why they stopped moving. -->
  <p
    v-if="channelBannerVisible"
    class="tm__channel tm__channel--stale"
  >{{ channelDown
    ? t('dashboard.metricsChannelDown')
    : channelStale
      ? t('dashboard.metricsChannelStale')
      : (systemMetricsState.error || t('dashboard.metricsUnavailable')) }}</p>
  <div v-if="current" class="tm">

    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsCpu') }}</span>
        <span class="tm-box__meta">{{ cpuMeta }}</span>
      </header>
      <div class="tm-chart">
        <div class="tm-chart__y"><span>100</span><span>50</span><span>0</span></div>
        <div class="tm-chart__plot">
          <div class="tm-chart__grid"><i v-for="n in 4" :key="n" /></div>
          <svg class="tm-chart__svg" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
            <defs>
              <linearGradient id="tmGradCpu" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stop-color="#4f8cff" stop-opacity="0.45" />
                <stop offset="1" stop-color="#4f8cff" stop-opacity="0.04" />
              </linearGradient>
              <linearGradient id="tmGradTemp" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stop-color="#f59e0b" stop-opacity="0.4" />
                <stop offset="1" stop-color="#f59e0b" stop-opacity="0.04" />
              </linearGradient>
            </defs>
            <polygon v-if="cpuTempArea" :points="cpuTempArea" fill="url(#tmGradTemp)" />
            <polygon v-if="cpuArea" :points="cpuArea" fill="url(#tmGradCpu)" />
            <polyline v-if="cpuLine" :points="cpuLine" fill="none" pathLength="1" class="tm-chart__line" stroke-width="1.4" />
            <polyline v-if="cpuTempLine" :points="cpuTempLine" fill="none" pathLength="1" class="tm-chart__line tm-chart__line--temp" stroke-width="1.2" />
          </svg>
        </div>
      </div>
      <div class="tm-stats">
        <div class="tm-stat tm-stat--blue">
          <span class="tm-stat__label">{{ t('dashboard.metricsUtilized') }}</span>
          <b class="tm-stat__value" :class="levelOf(cpuValue ?? 0)">{{ formatPercent(cpuValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--orange">
          <span class="tm-stat__label">{{ t('dashboard.metricsCpuTemp') }}</span>
          <b class="tm-stat__value" :class="cpuTempLevel">{{ formatTemp(cpuTempValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--purple tm-stat--wide">
          <span class="tm-stat__label">{{ t('dashboard.metricsLoad') }}</span>
          <b class="tm-stat__value tm-stat__value--small">{{ formatPercent(loadAvg1) }} · {{ formatPercent(loadAvg5) }} · {{ formatPercent(loadAvg15) }}</b>
        </div>
      </div>
    </article>

    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsMemory') }}</span>
        <span class="tm-box__meta">{{ memMeta }}</span>
      </header>
      <div class="tm-chart">
        <div class="tm-chart__y"><span>100</span><span>50</span><span>0</span></div>
        <div class="tm-chart__plot">
          <div class="tm-chart__grid"><i v-for="n in 4" :key="n" /></div>
          <svg class="tm-chart__svg" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
            <defs>
              <linearGradient id="tmGradMem" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stop-color="#4f8cff" stop-opacity="0.45" />
                <stop offset="1" stop-color="#4f8cff" stop-opacity="0.04" />
              </linearGradient>
            </defs>
            <polygon v-if="memArea" :points="memArea" fill="url(#tmGradMem)" />
            <polyline v-if="memLine" :points="memLine" fill="none" pathLength="1" class="tm-chart__line" stroke-width="1.4" />
          </svg>
        </div>
      </div>
      <div v-if="memSegments" class="tm-bar">
        <div class="tm-bar__track">
          <i class="tm-bar__seg tm-bar__seg--used" :style="{ width: `${memSegments.usedPct}%` }" />
          <i class="tm-bar__seg tm-bar__seg--cached" :style="{ width: `${memSegments.cachedPct}%` }" />
          <i class="tm-bar__seg tm-bar__seg--buffers" :style="{ width: `${memSegments.buffersPct}%` }" />
          <i class="tm-bar__seg tm-bar__seg--free" :style="{ width: `${memSegments.freePct}%` }" />
        </div>
        <div class="tm-bar__legend">
          <span><i class="tm-dot tm-dot--used" />{{ t('dashboard.metricsUsed') }} {{ formatBytes(memSegments.used) }}</span>
          <span><i class="tm-dot tm-dot--cached" />{{ t('dashboard.metricsCached') }} {{ formatBytes(memSegments.cached) }}</span>
          <span><i class="tm-dot tm-dot--buffers" />{{ t('dashboard.metricsBuffers') }} {{ formatBytes(memSegments.buffers) }}</span>
          <span><i class="tm-dot tm-dot--free" />{{ t('dashboard.metricsAvailable') }} {{ formatBytes(memSegments.free) }}</span>
        </div>
      </div>
      <div class="tm-stats">
        <div class="tm-stat tm-stat--blue">
          <span class="tm-stat__label">{{ t('dashboard.metricsUtilized') }}</span>
          <b class="tm-stat__value" :class="levelOf(memValue ?? 0)">{{ formatPercent(memValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--purple">
          <span class="tm-stat__label">{{ t('dashboard.metricsCapacity') }}</span>
          <b class="tm-stat__value">{{ formatBytes(current.memory_total_bytes) }}</b>
        </div>
        <div class="tm-stat tm-stat--orange">
          <span class="tm-stat__label">{{ t('dashboard.metricsUsed') }}</span>
          <b class="tm-stat__value">{{ formatBytes(usedBytes) }}</b>
        </div>
        <div class="tm-stat tm-stat--green">
          <span class="tm-stat__label">{{ t('dashboard.metricsAvailable') }}</span>
          <b class="tm-stat__value">{{ formatBytes(current.memory_available_bytes) }}</b>
        </div>
      </div>
    </article>

    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsFlash') }}</span>
        <span class="tm-box__meta">{{ flashMeta }}</span>
      </header>
      <div class="tm-ring-wrap">
        <svg class="tm-ring" viewBox="0 0 96 96">
          <circle class="tm-ring__track" cx="48" cy="48" r="40" />
          <circle
            class="tm-ring__value"
            cx="48" cy="48" r="40"
            :stroke-dasharray="`${(Math.min(100, Math.max(0, flashValue ?? 0)) / 100) * 251.33} 251.33`"
          />
        </svg>
        <span class="tm-ring__label" :class="levelOf(flashValue ?? 0)">{{ formatPercent(flashValue) }}</span>
      </div>
      <div class="tm-stats">
        <div class="tm-stat tm-stat--blue">
          <span class="tm-stat__label">{{ t('dashboard.metricsUtilized') }}</span>
          <b class="tm-stat__value" :class="levelOf(flashValue ?? 0)">{{ formatPercent(flashValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--purple">
          <span class="tm-stat__label">{{ t('dashboard.metricsCapacity') }}</span>
          <b class="tm-stat__value">{{ formatBytes(current.flash_total_bytes) }}</b>
        </div>
        <div class="tm-stat tm-stat--orange">
          <span class="tm-stat__label">{{ t('dashboard.metricsUsed') }}</span>
          <b class="tm-stat__value">{{ formatBytes(flashUsedBytes) }}</b>
        </div>
        <div class="tm-stat tm-stat--green">
          <span class="tm-stat__label">{{ t('dashboard.metricsFree') }}</span>
          <b class="tm-stat__value">{{ formatBytes(current.flash_free_bytes) }}</b>
        </div>
      </div>
    </article>

    <article class="tm-box">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ t('dashboard.metricsCellular') }}</span>
        <span class="tm-box__meta">{{ cellularMeta }}</span>
      </header>
      <div class="tm-chart">
        <div class="tm-chart__y"><span>{{ rateMaxYLabel }}</span><span>{{ rateMidYLabel }}</span><span>0</span></div>
        <div class="tm-chart__plot">
          <div class="tm-chart__grid"><i v-for="n in 4" :key="n" /></div>
          <svg class="tm-chart__svg" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none">
            <defs>
              <linearGradient id="tmGradRx" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stop-color="#34d399" stop-opacity="0.5" />
                <stop offset="1" stop-color="#34d399" stop-opacity="0.04" />
              </linearGradient>
              <linearGradient id="tmGradTx" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stop-color="#22d3ee" stop-opacity="0.45" />
                <stop offset="1" stop-color="#22d3ee" stop-opacity="0.04" />
              </linearGradient>
            </defs>
            <polygon
              v-if="rxLine"
              :points="`${rxLine} ${CHART_W},${CHART_H} 0,${CHART_H}`"
              fill="url(#tmGradRx)"
            />
            <polygon v-if="txArea" :points="txArea" fill="url(#tmGradTx)" />
            <polyline v-if="rxLine" :points="rxLine" fill="none" pathLength="1" class="tm-chart__line tm-chart__line--rx" stroke-width="1.4" />
            <polyline v-if="txLine" :points="txLine" fill="none" pathLength="1" class="tm-chart__line tm-chart__line--tx" stroke-width="1.2" />
          </svg>
        </div>
      </div>
      <div class="tm-stats">
        <div class="tm-stat tm-stat--green">
          <span class="tm-stat__label">{{ t('dashboard.metricsRx') }}</span>
          <b class="tm-stat__value">{{ formatRate(rxRateValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--blue">
          <span class="tm-stat__label">{{ t('dashboard.metricsTx') }}</span>
          <b class="tm-stat__value">{{ formatRate(txRateValue) }}</b>
        </div>
        <div class="tm-stat tm-stat--purple">
          <span class="tm-stat__label">{{ t('dashboard.radioBand') }}</span>
          <b class="tm-stat__value">{{ bandLabel(cellular) }}</b>
        </div>
        <div class="tm-stat tm-stat--orange">
          <span class="tm-stat__label">{{ t('dashboard.metricsSINR') }}</span>
          <b class="tm-stat__value">{{ sinrValue }}</b>
        </div>
      </div>
    </article>
  </div>
  <!-- With no snapshot to show, the banner above is already saying why. Only
       add a line here when there is no banner, otherwise the same sentence is
       printed twice, stacked. -->
  <p v-else-if="!channelBannerVisible" class="tm__loading">{{ t('dashboard.metricsUnavailable') }}</p>
</template>

<style scoped>
/* The same track the cards below use, from the same token. Four fixed columns
   is what this was, and it is why the strip and the card field never lined up:
   the two bands split the same width into different numbers of pieces. */
.tm {
  display: grid;
  grid-template-columns: var(--dashboard-card-track);
  gap: 12px;
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

.tm-box {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 10px;
  padding: 16px 18px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
}

.tm-box__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.tm-box__title {
  flex: 0 0 auto;
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
}

.tm-box__meta {
  overflow: hidden;
  font-size: 11px;
  color: var(--muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tm-chart {
  display: flex;
  gap: 6px;
  /* Stretch to fill the space between header and bottom-pinned stats. */
  flex: 1 1 auto;
  min-height: 96px;
}

.tm-chart__y {
  display: flex;
  flex: 0 0 auto;
  flex-direction: column;
  justify-content: space-between;
  padding: 0 2px 0 0;
  font-size: 9px;
  line-height: 1;
  color: var(--muted);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.tm-chart__y span {
  transform: translateY(4px);
}

.tm-chart__y span:last-child {
  transform: translateY(-2px);
}

.tm-chart__plot {
  position: relative;
  flex: 1 1 auto;
  min-width: 0;
}

.tm-chart__grid {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.tm-chart__grid i {
  height: 1px;
  background: var(--border);
  opacity: 0.55;
}

.tm-chart__svg {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.tm-chart__line {
  stroke: var(--accent, #4f8cff);
  stroke-linejoin: round;
  stroke-linecap: round;
  /* Draw-in: line sweeps left -> right when it first appears (pathLength=1). */
  stroke-dasharray: 1;
  stroke-dashoffset: 1;
  animation: tm-chart-draw 0.9s ease-out forwards;
}

@keyframes tm-chart-draw {
  to { stroke-dashoffset: 0; }
}

.tm-chart__svg polygon {
  animation: tm-chart-fade 0.9s ease-out;
}

@keyframes tm-chart-fade {
  from { opacity: 0; }
  to { opacity: 1; }
}

.tm-chart__line--rx { stroke: var(--success, #34d399); }
.tm-chart__line--tx { stroke: #22d3ee; }
.tm-chart__line--temp { stroke: #f59e0b; }

.tm-ring-wrap {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1 1 auto;
  min-height: 96px;
}

.tm-ring {
  width: 92px;
  height: 92px;
  transform: rotate(-90deg);
}

.tm-ring__track,
.tm-ring__value {
  fill: none;
  stroke-width: 8;
  stroke-linecap: round;
}

.tm-ring__track {
  stroke: var(--border);
}

.tm-ring__value {
  stroke: var(--accent, #4f8cff);
  transition: stroke-dasharray 0.6s ease;
}

.tm-ring__label {
  position: absolute;
  font-size: 16px;
  font-weight: 750;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

.tm-ring__label.is-warn { color: var(--warning, #fbbf24); }
.tm-ring__label.is-danger { color: var(--danger, #f87171); }

.tm-stats {
  /* Pin to the card bottom so stat rows align across the four boxes. */
  margin-top: auto;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}

.tm-stat {
  min-width: 0;
  padding-left: 8px;
  border-left: 3px solid var(--border);
}

.tm-stat--wide { grid-column: 1 / -1; }

.tm-stat--blue { border-left-color: var(--accent, #4f8cff); }
.tm-stat--orange { border-left-color: #f59e0b; }
.tm-stat--purple { border-left-color: #a78bfa; }
.tm-stat--green { border-left-color: var(--success, #34d399); }

.tm-stat__label {
  display: block;
  margin-bottom: 2px;
  overflow: hidden;
  font-size: 10.5px;
  color: var(--muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tm-stat__value {
  overflow: hidden;
  font-size: 14px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tm-stat__value--small {
  font-size: 12px;
}

.tm-stat__value.is-ok { color: var(--success, #34d399); }
.tm-stat__value.is-warn { color: var(--warning, #fbbf24); }
.tm-stat__value.is-danger { color: var(--danger, #f87171); }

.tm-bar {
  display: flex;
  flex-direction: column;
  gap: 5px;
  flex: 0 0 auto;
  padding-top: 8px;
}

.tm-bar__track {
  display: flex;
  overflow: hidden;
  height: 6px;
  border-radius: var(--radius-xs);
  background: var(--border);
}

.tm-bar__seg {
  height: 100%;
}

.tm-bar__seg--used { background: var(--accent, #4f8cff); }
.tm-bar__seg--cached { background: #f59e0b; }
.tm-bar__seg--buffers { background: #22d3ee; }
.tm-bar__seg--free { background: var(--border); }

.tm-bar__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  font-size: 10px;
  color: var(--muted);
}

.tm-bar__legend span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.tm-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.tm-dot--used { background: var(--accent, #4f8cff); }
.tm-dot--cached { background: #f59e0b; }
.tm-dot--buffers { background: #22d3ee; }
.tm-dot--free { background: var(--border); }

/* The viewport breakpoints that used to be here are gone, and their absence is
   the fix rather than a simplification. They asked how wide the WINDOW was,
   while what decides how many tiles fit is how wide this strip's own container
   is -- and on this dashboard those are very different numbers: the activity
   list on the left and the dialer on the right take roughly 800px out of the
   middle, so a 3441px window gave this strip about 1714px to work with. The
   auto-fill track above measures the container, so it needs no breakpoints and
   cannot disagree with the cards below. */
</style>
