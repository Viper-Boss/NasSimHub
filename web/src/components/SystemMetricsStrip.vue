<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DeviceCellular, DeviceMetrics, DeviceMetricsSource } from '../api/nodeFleet'
import { deviceGroupLabels } from '../i18n/deviceGroupLabels'

// The four metric tiles of ONE device.
//
// `source` is that device's own metrics object and the only thing read here:
// the host module's snapshot, or one Node's resources. The strip used to pick
// its own source from a device ID and poll for it; it is handed the object
// now, so it cannot reach another device's figures, and the route is polled
// once for every group (state/nodeFleet.ts) rather than once per strip.
//
// A reading the device did not send is drawn as "unknown", never as 0 and
// never as a neighbour's value. A reading of 0 is drawn as 0.
const props = defineProps<{ source: DeviceMetricsSource }>()
const { t, locale } = useI18n()
const labels = computed(() => deviceGroupLabels(String(locale.value)))

const current = computed<DeviceMetrics | null>(() => props.source.metrics)
const history = computed(() => props.source.history)
/** Why there are no tiles to fill, in the reader's words; '' when there are. */
const gapText = computed(() => {
  if (current.value || !props.source.gap) return ''
  return props.source.error || labels.value.gaps[props.source.gap]
})
/**
 * What an empty tile says first. "Not provided by this device" is kept for the
 * cases where that is the fact; a read that is still under way, or failed, is
 * not the device withholding anything and is worded as what it is.
 */
const absentHeading = computed(() => {
  if (props.source.gap === 'loading') return labels.value.gaps.loading
  if (props.source.gap === 'unavailable' || props.source.gap === '') return t('dashboard.metricsUnavailable')
  return labels.value.notProvided
})
/** The four tiles' titles, for the layout a device without metrics keeps. */
const absentTiles = computed(() => [
  t('dashboard.metricsCpu'),
  t('dashboard.metricsMemory'),
  t('dashboard.metricsFlash'),
  t('dashboard.metricsCellular')
])

/** The tone of a percentage. An unknown one has none: green is a claim. */
function levelOf(percent: number | null): string {
  if (percent === null) return ''
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
  channelDown.value || channelStale.value || !props.source.available
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

// Every formatter answers the "unknown" word for a reading that is absent and
// formats a zero like any other number. That distinction used to be made the
// other way round here (0 bytes drew a dash), which is right only for a
// snapshot whose absent fields were coerced to 0 -- hostDeviceMetrics() now
// undoes that coercion once, so these can be honest for every device.
function formatTemp(value: number | null | undefined): string {
  return value === null || value === undefined ? labels.value.unknown : `${value.toFixed(1)}°C`
}

function formatPercent(value: number | null | undefined): string {
  return value === null || value === undefined ? labels.value.unknown : `${Math.round(value)}%`
}

function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return labels.value.unknown
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${unit === 0 || value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

function formatRate(bytesPerSecond: number | null | undefined): string {
  if (bytesPerSecond === null || bytesPerSecond === undefined) return labels.value.unknown
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
const rxRateValue = computed(() => latestOf('rxRate'))
const txRateValue = computed(() => latestOf('txRate'))
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

// The axis is labelled only when there is a rate to scale it by. Without one
// the scale is a placeholder, and "1 B/s" at the top of an empty chart reads
// as a measurement of an idle link.
const hasRates = computed(() =>
  history.value.some(sample => typeof sample.rxRate === 'number' || typeof sample.txRate === 'number')
)
const rateMaxYLabel = computed(() => (hasRates.value ? formatRate(rateMax.value) : ''))
const rateMidYLabel = computed(() => (hasRates.value ? formatRate(rateMax.value / 2) : ''))

const cpuMeta = computed(() => {
  const snapshot = current.value
  if (!snapshot) return ''
  const parts: string[] = []
  if (snapshot.cpu_model_name) parts.push(snapshot.cpu_model_name.replace(/\s+/g, ' '))
  if (snapshot.cores !== null) parts.push(`${snapshot.cores} ${t('dashboard.metricsCoresUnit')}`)
  if (snapshot.process_count !== null) parts.push(`${snapshot.process_count} ${t('dashboard.metricsProcesses')}`)
  if (snapshot.thread_count !== null) parts.push(`${snapshot.thread_count} ${t('dashboard.metricsThreadsUnit')}`)
  return parts.join(' | ')
})

const memMeta = computed(() => {
  const total = current.value?.memory_total_bytes
  return total === null || total === undefined ? '' : formatBytes(total)
})

const flashMeta = computed(() => {
  const snapshot = current.value
  if (!snapshot) return ''
  const fs = snapshot.flash_fs_type || ''
  const device = snapshot.flash_device_model || snapshot.flash_device_path || ''
  return [device, fs].filter(Boolean).join(' · ')
})

const cellular = computed(() => current.value?.cellular ?? null)

function bandLabel(cell: DeviceCellular | null): string {
  if (!cell || !cell.band) return labels.value.unknown
  // A Node names the band itself ("B3"); the host module sends the number.
  if (/^[A-Za-z]/.test(cell.band)) return cell.band
  const rat = (cell.rat || '').toUpperCase()
  if (rat.startsWith('NR')) return `n${cell.band}`
  return `B${cell.band}`
}

const cellularMeta = computed(() => {
  const cell = cellular.value
  if (!cell) return labels.value.unknown
  const parts: string[] = []
  if (cell.plmn) parts.push(cell.plmn)
  const rat = (cell.rat || '').toUpperCase()
  if (rat) parts.push(rat)
  if (cell.band) parts.push(bandLabel(cell))
  if (cell.rsrp !== null) parts.push(`${cell.rsrp} dBm`)
  return parts.join(' · ') || labels.value.unknown
})

const sinrValue = computed(() => {
  const value = cellular.value?.sinr
  return typeof value === 'number' ? `${value.toFixed(1)} dB` : labels.value.unknown
})

/** Used memory, or null when either half of the pair is unknown. */
const usedBytes = computed(() => {
  const snapshot = current.value
  if (!snapshot || snapshot.memory_total_bytes === null || snapshot.memory_available_bytes === null) {
    return null
  }
  return Math.max(0, snapshot.memory_total_bytes - snapshot.memory_available_bytes)
})

const memSegments = computed(() => {
  const snapshot = current.value
  const used = usedBytes.value
  if (!snapshot || snapshot.memory_total_bytes === null || used === null) return null
  const total = snapshot.memory_total_bytes
  // Cached and buffers are separate readings and either may be missing. A
  // missing one takes no width in the bar and reads "unknown" in the legend;
  // it is not drawn as an empty segment, which would say "none".
  const cached = snapshot.memory_cached_bytes === null ? null : Math.min(snapshot.memory_cached_bytes, used)
  const buffers =
    snapshot.memory_buffers_bytes === null ? null : Math.min(snapshot.memory_buffers_bytes, used - (cached ?? 0))
  const free = Math.max(0, total - used)
  return {
    usedPct: (used / total) * 100,
    cachedPct: ((cached ?? 0) / total) * 100,
    buffersPct: ((buffers ?? 0) / total) * 100,
    freePct: (free / total) * 100,
    used, cached, buffers, free
  }
})

const flashUsedBytes = computed(() => {
  const snapshot = current.value
  if (!snapshot || snapshot.flash_total_bytes === null || snapshot.flash_free_bytes === null) return null
  return Math.max(0, snapshot.flash_total_bytes - snapshot.flash_free_bytes)
})
</script>

<template>
  <!-- Keep the four tiles mounted whenever we hold any snapshot: a wedged
       module channel must not collapse the dashboard layout. Values freeze at
       the last good sample and the banner explains why they stopped moving. -->
  <p
    v-if="current && channelBannerVisible"
    class="tm__channel tm__channel--stale"
    role="status"
  >{{ channelDown
    ? t('dashboard.metricsChannelDown')
    : channelStale
      ? t('dashboard.metricsChannelStale')
      : (source.error || labels.metricsFrozen) }}</p>
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
          <b class="tm-stat__value" :class="levelOf(cpuValue)">{{ formatPercent(cpuValue) }}</b>
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
          <b class="tm-stat__value" :class="levelOf(memValue)">{{ formatPercent(memValue) }}</b>
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
        <span class="tm-ring__label" :class="levelOf(flashValue)">{{ formatPercent(flashValue) }}</span>
      </div>
      <div class="tm-stats">
        <div class="tm-stat tm-stat--blue">
          <span class="tm-stat__label">{{ t('dashboard.metricsUtilized') }}</span>
          <b class="tm-stat__value" :class="levelOf(flashValue)">{{ formatPercent(flashValue) }}</b>
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
        <div class="tm-chart__y"><span>{{ rateMaxYLabel }}</span><span>{{ rateMidYLabel }}</span><span>{{ hasRates ? '0' : '' }}</span></div>
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
  <!-- No metrics for this device: the four tiles stay, each saying so and why.
       A group without them would be a different template from the one beside
       it, and an empty space reads as "nothing to report" rather than "not
       reported". -->
  <div v-else class="tm tm--absent">
    <article v-for="title in absentTiles" :key="title" class="tm-box tm-box--absent">
      <header class="tm-box__head">
        <span class="tm-box__title">{{ title }}</span>
      </header>
      <p class="tm-box__absent">
        <strong>{{ absentHeading }}</strong>
        <span v-if="gapText && gapText !== absentHeading">{{ gapText }}</span>
      </p>
    </article>
  </div>
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

.tm-box__absent {
  display: grid;
  flex: 1 1 auto;
  align-content: center;
  gap: 6px;
  min-height: 96px;
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.tm-box__absent strong {
  color: var(--muted-strong);
  font-size: 13px;
  font-weight: 650;
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
