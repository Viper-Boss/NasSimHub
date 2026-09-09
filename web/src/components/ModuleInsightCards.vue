<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Activity, Gauge, Radio, RotateCcw, Signal, Usb } from '@lucide/vue'
import {
  acquireSystemMetrics,
  modemInterface,
  releaseSystemMetrics,
  systemMetricsState,
  type SystemMetricsSample
} from '../state/systemMetrics'
import { restartModem } from '../state/deviceConfiguration'
import type { LineSummary } from '../api/types'

// Three insight cards rendered next to the module card on the dashboard:
// data interface throughput, system runtime health with a modem restart
// action, and the live serving-cell radio detail. All read from the shared
// 10 s metrics poller — no extra API traffic.

const props = defineProps<{ line: LineSummary }>()

const { t } = useI18n()
const router = useRouter()

onMounted(() => { acquireSystemMetrics() })
onBeforeUnmount(() => { releaseSystemMetrics() })

const snapshot = computed(() => systemMetricsState.metrics)
const cellular = computed(() => snapshot.value?.cellular ?? null)

const latestSample = computed<SystemMetricsSample | null>(() => {
  const history = systemMetricsState.history
  return history.length ? history[history.length - 1]! : null
})

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}

function formatRate(bytesPerSecond: number | undefined): string {
  if (bytesPerSecond === undefined || bytesPerSecond < 0) return '—'
  return `${formatBytes(bytesPerSecond)}/s`
}

function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}${t('runtimeStatus.dayUnit')}${hours}${t('runtimeStatus.hourUnit')}`
  if (hours > 0) return `${hours}${t('runtimeStatus.hourUnit')}${minutes}${t('runtimeStatus.minuteUnit')}`
  return `${minutes}${t('runtimeStatus.minuteUnit')}`
}

function formatMemoryPair(usedBytes: number, totalBytes: number): string {
  const fmt = (value: number): string => (
    value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(1)} GB` : `${Math.round(value / 1024 ** 2)} MB`
  )
  return `${fmt(usedBytes)} / ${fmt(totalBytes)}`
}

const netInterface = computed(() => (
  snapshot.value ? modemInterface(snapshot.value) : null
))

const memoryView = computed(() => {
  const snap = snapshot.value
  if (!snap || !snap.memory_total_bytes) return null
  const used = Math.max(0, snap.memory_total_bytes - snap.memory_available_bytes)
  return {
    text: formatMemoryPair(used, snap.memory_total_bytes),
    percent: Math.min(100, Math.max(0, (used / snap.memory_total_bytes) * 100))
  }
})

const flashView = computed(() => {
  const snap = snapshot.value
  if (!snap || !snap.flash_total_bytes) return null
  return {
    text: `${formatBytes(snap.flash_total_bytes - snap.flash_free_bytes)} / ${formatBytes(snap.flash_total_bytes)}`,
    percent: Math.min(100, Math.max(0, snap.flash_used_percent))
  }
})

const uptimeText = computed(() => formatUptime(snapshot.value?.host_uptime_seconds ?? 0))

const restartBusy = ref(false)
async function onRestartModem(): Promise<void> {
  if (restartBusy.value) return
  if (!window.confirm(t('dashboard.restartModemConfirm'))) return
  restartBusy.value = true
  try {
    await restartModem(props.line.id)
  } finally {
    restartBusy.value = false
  }
}

function openDiagnostics(): void {
  void router.push({ name: 'settings', params: { section: 'diagnostics' } })
}

function barTone(percent: number): string {
  return percent >= 90 ? 'is-danger' : percent >= 80 ? 'is-warn' : ''
}

interface RadioFact { label: string; value: string }
const radioFacts = computed<RadioFact[]>(() => {
  const cell = cellular.value
  if (!cell) return []
  const facts: RadioFact[] = [
    { label: 'RSRP', value: `${cell.rsrp} dBm` },
    { label: 'RSRQ', value: `${cell.rsrq} dB` },
    { label: 'RSSI', value: `${cell.rssi} dBm` },
    { label: 'SINR', value: `${cell.sinr} dB` },
    { label: 'PCI', value: String(cell.pci) },
    { label: 'EARFCN', value: String(cell.earfcn) }
  ]
  if (cell.cell_id_hex) facts.push({ label: 'Cell ID', value: cell.cell_id_hex })
  if (cell.tac_hex) facts.push({ label: 'TAC', value: cell.tac_hex })
  return facts
})
</script>

<template>
  <div class="insight-cards">
    <!-- Data interface -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Usb :size="15" />
        <h4>{{ t('dashboard.netCard') }}</h4>
      </header>
      <div v-if="netInterface" class="insight-card__body">
        <div class="insight-net__iface">
          <span class="insight-net__name">{{ netInterface.name }}</span>
          <span v-if="cellular" class="insight-net__rat">{{ cellular.rat }} · {{ cellular.state }}</span>
        </div>
        <div class="insight-net__rates">
          <div class="insight-net__rate">
            <span class="insight-net__rate-label">↓ {{ t('dashboard.downlinkRate') }}</span>
            <strong>{{ formatRate(latestSample?.rxRate) }}</strong>
          </div>
          <div class="insight-net__rate">
            <span class="insight-net__rate-label">↑ {{ t('dashboard.uplinkRate') }}</span>
            <strong>{{ formatRate(latestSample?.txRate) }}</strong>
          </div>
        </div>
        <dl class="insight-net__totals">
          <div><dt>{{ t('dashboard.rxTotal') }}</dt><dd>{{ formatBytes(netInterface.rx_bytes) }}</dd></div>
          <div><dt>{{ t('dashboard.txTotal') }}</dt><dd>{{ formatBytes(netInterface.tx_bytes) }}</dd></div>
        </dl>
      </div>
      <p v-else class="insight-card__empty">{{ t('dashboard.noInterface') }}</p>
    </article>

    <!-- System runtime -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Activity :size="15" />
        <h4>{{ t('dashboard.sysRunCard') }}</h4>
      </header>
      <div class="insight-card__body">
        <div class="insight-run__uptime">
          <span>{{ t('runtimeStatus.uptime') }}</span>
          <strong>{{ uptimeText }}</strong>
        </div>
        <div v-if="memoryView" class="insight-run__meter">
          <div class="insight-run__meter-head">
            <span>{{ t('runtimeStatus.memory') }}</span>
            <strong>{{ memoryView.text }}</strong>
          </div>
          <div class="insight-run__bar"><i :class="barTone(memoryView.percent)" :style="{ width: `${memoryView.percent}%` }" /></div>
        </div>
        <div v-if="flashView" class="insight-run__meter">
          <div class="insight-run__meter-head">
            <span>{{ t('runtimeStatus.storage') }}</span>
            <strong>{{ flashView.text }}</strong>
          </div>
          <div class="insight-run__bar"><i :class="barTone(flashView.percent)" :style="{ width: `${flashView.percent}%` }" /></div>
        </div>
        <p v-if="snapshot" class="insight-run__procs">
          {{ t('dashboard.processes', { count: snapshot.process_count }) }}
          ·
          {{ t('dashboard.threads', { count: snapshot.thread_count }) }}
          <template v-if="snapshot.cpu_current_mhz > 0">
            · {{ Math.round(snapshot.cpu_current_mhz) }} MHz
          </template>
        </p>
        <div class="insight-run__actions">
          <button
            class="secondary-button"
            type="button"
            :disabled="restartBusy"
            @click="onRestartModem"
          >
            <RotateCcw :size="14" />{{ restartBusy ? t('dashboard.restarting') : t('dashboard.restartModem') }}
          </button>
          <button class="text-button" type="button" @click="openDiagnostics">
            <Gauge :size="14" />{{ t('dashboard.openDiagnostics') }}
          </button>
        </div>
      </div>
    </article>

    <!-- Serving cell detail -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Signal :size="15" />
        <h4>{{ t('dashboard.cellCard') }}</h4>
      </header>
      <div v-if="radioFacts.length" class="insight-card__body">
        <dl class="insight-radio">
          <div v-for="fact in radioFacts" :key="fact.label">
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>
        <p class="insight-radio__note">
          <Radio :size="12" />
          {{ cellular?.plmn }} · {{ cellular?.band }} · {{ cellular?.frequency_mhz }} MHz · {{ cellular?.downlink_bandwidth_mhz }} MHz
        </p>
      </div>
      <p v-else class="insight-card__empty">{{ t('dashboard.noCellular') }}</p>
    </article>
  </div>
</template>

<style scoped>
.insight-cards {
  display: contents;
}

.insight-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  max-width: 420px;
  padding: 14px 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
}

.insight-card__head {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--muted-strong);
}

.insight-card__head h4 {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
}

.insight-card__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.insight-card__empty {
  margin: auto;
  padding: 12px 0;
  font-size: 12.5px;
  color: var(--muted);
}

/* Net card */
.insight-net__iface {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.insight-net__name {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 13px;
  font-weight: 700;
  color: var(--accent-strong, var(--accent));
}

.insight-net__rat {
  font-size: 11px;
  color: var(--muted);
}

.insight-net__rates {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.insight-net__rate {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.insight-net__rate-label {
  font-size: 10.5px;
  color: var(--muted);
}

.insight-net__rate strong {
  font-size: 15px;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.insight-net__totals {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin: 0;
}

.insight-net__totals div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 11.5px;
}

.insight-net__totals dt {
  color: var(--muted);
}

.insight-net__totals dd {
  margin: 0;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

/* Runtime card */
.insight-run__uptime {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  color: var(--muted);
}

.insight-run__uptime strong {
  font-size: 15px;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.insight-run__meter {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.insight-run__meter-head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 11.5px;
  color: var(--muted);
}

.insight-run__meter-head strong {
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.insight-run__bar {
  height: 6px;
  overflow: hidden;
  background: var(--surface-subtle);
  border-radius: var(--radius-pill);
}

.insight-run__bar i {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: var(--radius-pill);
  transition: width 0.5s ease;
}

.insight-run__bar i.is-warn { background: var(--warning, #fbbf24); }
.insight-run__bar i.is-danger { background: var(--danger, #f87171); }

.insight-run__procs {
  margin: 0;
  font-size: 11.5px;
  color: var(--muted);
}

.insight-run__actions {
  display: flex;
  gap: 8px;
  margin-top: auto;
  padding-top: 4px;
}

.insight-run__actions .secondary-button,
.insight-run__actions .text-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 34px;
  padding: 6px 12px;
  font-size: 12px;
}

/* Radio card */
.insight-radio {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 14px;
  margin: 0;
}

.insight-radio div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
}

.insight-radio dt {
  color: var(--muted);
}

.insight-radio dd {
  margin: 0;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.insight-radio__note {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding-top: 8px;
  border-top: 1px solid var(--border);
  font-size: 11px;
  color: var(--muted);
}
</style>
