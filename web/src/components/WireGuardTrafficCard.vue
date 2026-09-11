<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ShieldCheck } from '@lucide/vue'
import {
  acquireWireGuardTraffic,
  releaseWireGuardTraffic,
  wireGuardTrafficState,
  type WireGuardTrafficSample
} from '../state/wireguardTraffic'

// Transfer through the WireGuard tunnel: cumulative totals, and the recent rate
// as a line chart. Drawn with the same geometry and colours as the data
// interface chart on the dashboard so the two read as one family.

const { t } = useI18n()

onMounted(acquireWireGuardTraffic)
onBeforeUnmount(releaseWireGuardTraffic)

const CHART_WIDTH = 268
const CHART_HEIGHT = 42

function ratePath(pick: (sample: WireGuardTrafficSample) => number): string {
  const samples = wireGuardTrafficState.history.slice(-40)
  if (samples.length < 2) return ''
  const peak = Math.max(1, ...samples.map(pick))
  const step = CHART_WIDTH / (samples.length - 1)
  return samples
    .map((sample, index) => {
      const x = index * step
      const y = CHART_HEIGHT - (pick(sample) / peak) * (CHART_HEIGHT - 3) - 1.5
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const rxPath = computed(() => ratePath(sample => sample.rxRate))
const txPath = computed(() => ratePath(sample => sample.txRate))
const hasHistory = computed(() => wireGuardTrafficState.history.length >= 2)
const latest = computed(() => {
  const history = wireGuardTrafficState.history
  return history.length ? history[history.length - 1]! : null
})

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function formatRate(value: number | undefined): string {
  if (value === undefined) return '—'
  return `${formatBytes(Math.round(value))}/s`
}

// An interface that is up with nobody handshaking carries nothing, and saying
// only "up" would read as working.
const peerSummary = computed(() => {
  const { peerCount, livePeers } = wireGuardTrafficState
  if (peerCount === 0) return t('wireguardChart.noPeers')
  return t('wireguardChart.peerSummary', { live: livePeers, total: peerCount })
})

const peersQuiet = computed(
  () => wireGuardTrafficState.peerCount > 0 && wireGuardTrafficState.livePeers === 0
)
</script>

<template>
  <article class="wg-traffic">
    <header class="wg-traffic__head">
      <ShieldCheck :size="15" />
      <h4>{{ t('wireguardChart.title') }}</h4>
      <span class="wg-traffic__peers" :class="{ 'is-quiet': peersQuiet }">{{ peerSummary }}</span>
    </header>

    <p v-if="!wireGuardTrafficState.available" class="wg-traffic__empty">
      {{ wireGuardTrafficState.error || t('wireguardChart.unavailable') }}
    </p>

    <div v-else class="wg-traffic__body">
      <div class="wg-traffic__rates">
        <div class="wg-traffic__rate">
          <span class="wg-traffic__rate-label">↓ {{ t('wireguardChart.downRate') }}</span>
          <strong>{{ formatRate(latest?.rxRate) }}</strong>
        </div>
        <div class="wg-traffic__rate">
          <span class="wg-traffic__rate-label">↑ {{ t('wireguardChart.upRate') }}</span>
          <strong>{{ formatRate(latest?.txRate) }}</strong>
        </div>
      </div>

      <div v-if="hasHistory" class="wg-traffic__chart" aria-hidden="true">
        <svg :viewBox="`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`" preserveAspectRatio="none">
          <path :d="rxPath" fill="none" stroke="var(--accent)" stroke-width="1.6" stroke-linejoin="round" />
          <path :d="txPath" fill="none" stroke="var(--success)" stroke-width="1.6" stroke-linejoin="round" />
        </svg>
        <span class="wg-traffic__legend">
          <em class="is-rx">↓ {{ t('wireguardChart.downRate') }}</em>
          <em class="is-tx">↑ {{ t('wireguardChart.upRate') }}</em>
        </span>
      </div>
      <p v-else class="wg-traffic__collecting">{{ t('wireguardChart.collecting') }}</p>

      <dl class="wg-traffic__totals">
        <div>
          <dt>{{ t('wireguardChart.rxTotal') }}</dt>
          <dd>{{ formatBytes(wireGuardTrafficState.rxTotal) }}</dd>
        </div>
        <div>
          <dt>{{ t('wireguardChart.txTotal') }}</dt>
          <dd>{{ formatBytes(wireGuardTrafficState.txTotal) }}</dd>
        </div>
      </dl>

      <p class="wg-traffic__note">{{ t('wireguardChart.historyNote') }}</p>
    </div>
  </article>
</template>

<style scoped>
.wg-traffic {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  background: var(--surface);
}

.wg-traffic__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
}

.wg-traffic__head h4 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
}

.wg-traffic__peers {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}

.wg-traffic__peers.is-quiet {
  color: var(--warning, #b45309);
}

.wg-traffic__body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.wg-traffic__rates {
  display: flex;
  gap: 18px;
}

.wg-traffic__rate {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.wg-traffic__rate-label {
  color: var(--muted);
  font-size: 11px;
}

.wg-traffic__rate strong {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}

.wg-traffic__chart {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.wg-traffic__chart svg {
  display: block;
  width: 100%;
  height: 42px;
}

.wg-traffic__legend {
  display: flex;
  gap: 12px;
  color: var(--muted);
  font-size: 10.5px;
  font-style: normal;
}

.wg-traffic__legend em {
  font-style: normal;
}

.wg-traffic__legend .is-rx {
  color: var(--accent);
}

.wg-traffic__legend .is-tx {
  color: var(--success);
}

.wg-traffic__collecting,
.wg-traffic__empty,
.wg-traffic__note {
  margin: 0;
  color: var(--muted);
  font-size: 11px;
}

.wg-traffic__totals {
  display: grid;
  margin: 0;
  gap: 6px 18px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.wg-traffic__totals div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.wg-traffic__totals dt {
  color: var(--muted);
  font-size: 11px;
}

.wg-traffic__totals dd {
  margin: 0;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
</style>
