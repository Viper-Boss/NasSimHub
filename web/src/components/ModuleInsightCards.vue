<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Activity, AlertCircle, CheckCircle2, Gauge, LoaderCircle, Network, Radio, RotateCcw, Signal, Usb, Wrench } from '@lucide/vue'
import { dashboardNetworkNodes, acquireDashboardNetworkNodes, releaseDashboardNetworkNodes } from '../state/dashboardNetworkNodes'
import type { DeviceMetricsSample, DeviceMetricsSource } from '../api/nodeFleet'
import { deviceGroupLabels } from '../i18n/deviceGroupLabels'
import { networkCardLabels } from '../i18n/networkCardNotes'
import './insightCard.css'
import { repairModule, restartModem } from '../state/deviceConfiguration'
import { requestConfirmation } from '../state/confirmation'
import type { LineSummary } from '../api/types'

// Every device keeps the original four insight cards in their original order.
// Resource figures come only from that device's source. Nodes and clients
// describe NasSimHub itself, independently of the selected modem. Every group
// displays the same gateway facts in the original ninth-card position, using
// one shared slower poll regardless of the number or kind of modules.

const props = withDefaults(
  defineProps<{
    scope?: 'device' | 'network'
    /** The device's line. Required for scope="device". */
    line?: LineSummary
    /** The device's own metrics. Required for scope="device". */
    source?: DeviceMetricsSource
    /** Whether the viewer may see what locates the device (cell ID, TAC). */
    admin?: boolean
  }>(),
  { scope: 'device', line: undefined, source: undefined, admin: false }
)

const deviceScope = computed(() => props.scope !== 'network')
const isNode = computed(() => props.line?.device_imei.startsWith('NSH-') === true)
const i18n = useI18n()
const { t } = i18n
const router = useRouter()
const labels = computed(() => deviceGroupLabels(String(i18n.locale.value)))

const { nknNodes, nknAddress, nknEnabled, dhtStatus } = dashboardNetworkNodes
let nodesAcquired = false

onMounted(() => {
  i18nBridge()
  acquireDashboardNetworkNodes()
  nodesAcquired = true
})
onBeforeUnmount(() => { if (nodesAcquired) releaseDashboardNetworkNodes() })

// The card's labels live in the lazily merged module with the other dashboard
// prose, not in the locale files: the startup budget had 0.1 KiB of headroom.
function i18nBridge(): void {
  const locale = i18n.locale.value
  i18n.mergeLocaleMessage(locale, { dashboard: networkCardLabels(locale) })
}

/** The seed nodes the probe actually reached, quickest first. */
const seedNodes = computed(() => {
  const nodes = nknNodes.value?.nodes ?? []
  return nodes
    .filter(node => node.rtt_ms >= 0)
    .sort((a, b) => a.rtt_ms - b.rtt_ms)
    .slice(0, 4)
})

/** The slowest reached node, so the latency bars share one scale. */
const seedScale = computed(() =>
  Math.max(1, ...seedNodes.value.map(node => node.rtt_ms))
)

const dhtPeerLine = computed(() => {
  const status = dhtStatus.value
  if (!status || status.state !== 'ready') return ''
  return `${t('dashboard.meshConnected')} ${status.connected_peers} · ${t('dashboard.meshLan')} ${status.lan_peers}`
})

/** Both paths off, or both unreachable: the card has nothing true to show. */
const nodesEmpty = computed(() =>
  !nknEnabled.value && dhtStatus.value?.state !== 'ready'
)

/**
 * A cryptographic identity is long and its two ENDS are what identify it, so
 * the middle is what gets dropped.
 */
function shortId(value: string): string {
  if (value.length <= 22) return value
  return `${value.slice(0, 10)}…${value.slice(-8)}`
}

/**
 * A seed node's address is the opposite case, and middle-truncating it was
 * wrong in a way the first render made obvious: `mainnet-node-0042.nkn.org:30003`
 * came out as `mainnet-no…rg:30003`, which threw away the number and left four
 * rows that read as four copies of the same node. The identifying part of a
 * host name is its FIRST label; the domain and port are the same for every seed.
 */
function shortHost(value: string): string {
  const host = value.split(':')[0] ?? value
  const first = host.split('.')[0] ?? host
  if (first.length >= 6) return first
  // A bare address with no dots (an IP, or an NKN identifier) has no label to
  // take, so it falls back to the identity rule.
  return shortId(host)
}

const snapshot = computed(() => props.source?.metrics ?? null)
const cellular = computed(() => snapshot.value?.cellular ?? null)
const deviceHistory = computed<DeviceMetricsSample[]>(() => props.source?.history ?? [])

/**
 * Why this device has no metrics at all, for the cards that then have nothing
 * to draw; '' when it has some.
 */
const gapText = computed(() => {
  const source = props.source
  if (!source || source.metrics) return ''
  return source.error || (source.gap ? labels.value.gaps[source.gap] : t('dashboard.metricsUnavailable'))
})
/** "Not provided by this device", unless the read is merely still under way. */
const gapHeading = computed(() =>
  props.source?.gap === 'loading' ? '' : labels.value.notProvided
)

const latestSample = computed<DeviceMetricsSample | null>(() => {
  const history = deviceHistory.value
  return history.length ? history[history.length - 1]! : null
})

/** A byte count. Unknown when absent; 0 is "0 B". */
function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || bytes < 0) return labels.value.unknown
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${unit === 0 || value >= 100 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}

/** A byte rate. Unknown when absent; 0 is "0 B/s" -- nothing moved, measured. */
function formatRate(bytesPerSecond: number | null | undefined): string {
  if (bytesPerSecond === null || bytesPerSecond === undefined || bytesPerSecond < 0) {
    return labels.value.unknown
  }
  return `${formatBytes(bytesPerSecond)}/s`
}

function formatUptime(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || seconds <= 0) return labels.value.unknown
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

/** The interface this device's data card describes; chosen where the metrics are built. */
const netInterface = computed(() => snapshot.value?.data_interface ?? null)

/** Whose interface the data card is about, for its heading. */
const netOwner = computed(() =>
  isNode.value ? props.line?.model || labels.value.unknown : labels.value.netHostInterface
)

const memoryView = computed(() => {
  const snap = snapshot.value
  if (!snap || snap.memory_total_bytes === null || snap.memory_available_bytes === null) return null
  const used = Math.max(0, snap.memory_total_bytes - snap.memory_available_bytes)
  return {
    text: formatMemoryPair(used, snap.memory_total_bytes),
    percent: Math.min(100, Math.max(0, (used / snap.memory_total_bytes) * 100))
  }
})

const flashView = computed(() => {
  const snap = snapshot.value
  if (!snap || snap.flash_total_bytes === null || snap.flash_free_bytes === null) return null
  const used = Math.max(0, snap.flash_total_bytes - snap.flash_free_bytes)
  return {
    text: `${formatBytes(used)} / ${formatBytes(snap.flash_total_bytes)}`,
    percent: Math.min(100, Math.max(0, snap.flash_used_percent ?? (used / snap.flash_total_bytes) * 100))
  }
})

const uptimeText = computed(() => formatUptime(snapshot.value?.host_uptime_seconds))

/** A count for the process line: the number, or the unknown word. */
function countText(value: number | null | undefined): string {
  return value === null || value === undefined ? labels.value.unknown : String(value)
}

const restartBusy = ref(false)
async function onRestartModem(): Promise<void> {
  if (restartBusy.value) return
  const confirmed = await requestConfirmation({
    title: t('dashboard.restartModem'),
    message: t('dashboard.restartModemConfirm'),
    tone: 'danger'
  })
  if (!confirmed) return
  restartBusy.value = true
  try {
    if (props.line) await restartModem(props.line.id)
  } finally {
    restartBusy.value = false
  }
}

// One-click rescue: release_hijacked_usb -> reset_usb -> forced QADBKEY re-auth
// -> re-enable radio -> reprobe voice. Covers the wedged-module cases we hit in
// the field (voice route down, radio disabled after re-enumeration, internal
// adbd wedged, host option driver hijacking ADB/audio interfaces).
const repairBusy = ref(false)
const repairStep = ref('')
const repairResult = ref<'none' | 'ok' | 'failed'>('none')
const repairError = ref('')

const repairStepLabel = computed(() => {
  switch (repairStep.value) {
    case 'interfaces': return t('dashboard.repairStepInterfaces')
    case 'usb': return t('dashboard.repairStepUSB')
    case 'auth': return t('dashboard.repairStepAuth')
    case 'radio': return t('dashboard.repairStepRadio')
    case 'voice': return t('dashboard.repairStepVoice')
    default: return ''
  }
})

async function onRepair(): Promise<void> {
  if (repairBusy.value) return
  const confirmed = await requestConfirmation({
    title: t('dashboard.repairButton'),
    message: t('dashboard.repairConfirm'),
    tone: 'danger'
  })
  if (!confirmed) return
  repairBusy.value = true
  repairResult.value = 'none'
  repairError.value = ''
  try {
    if (!props.line) return
    const outcome = await repairModule(props.line.id, step => { repairStep.value = step })
    repairResult.value = outcome === 'ok' ? 'ok' : 'failed'
    if (outcome !== 'ok') {
      const stepLabels: Record<string, string> = {
        interfaces: t('dashboard.repairStepInterfaces'),
        usb: t('dashboard.repairStepUSB'),
        auth: t('dashboard.repairStepAuth'),
        radio: t('dashboard.repairStepRadio'),
        voice: t('dashboard.repairStepVoice')
      }
      repairError.value = t('dashboard.repairFailedStep', { step: stepLabels[outcome] ?? outcome })
    }
  } catch (reason) {
    repairResult.value = 'failed'
    repairError.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    repairStep.value = ''
    repairBusy.value = false
  }
}

function openDiagnostics(): void {
  void router.push({ name: 'settings', params: { section: 'diagnostics' } })
}

function barTone(percent: number): string {
  return percent >= 90 ? 'is-danger' : percent >= 80 ? 'is-warn' : ''
}

interface RadioFact { label: string; value: string }
// Raw dBm means nothing to most people. These are the standard LTE bands for
// judging a serving cell, so the card can say "good" instead of making the
// reader remember where -94 dBm falls.
type SignalGrade = 'excellent' | 'good' | 'fair' | 'poor'

interface SignalMeter {
  label: string
  value: number
  unit: string
  grade: SignalGrade
  /** 0-1 position on the meter track. */
  fill: number
  hint: string
}

function grade(value: number, excellent: number, good: number, fair: number): SignalGrade {
  if (value >= excellent) return 'excellent'
  if (value >= good) return 'good'
  if (value >= fair) return 'fair'
  return 'poor'
}

function fillRatio(value: number, floor: number, ceiling: number): number {
  if (ceiling <= floor) return 0
  return Math.max(0, Math.min(1, (value - floor) / (ceiling - floor)))
}

const signalMeters = computed<SignalMeter[]>(() => {
  const cell = cellular.value
  if (!cell) return []
  const candidates = [
    {
      label: 'RSRP',
      value: cell.rsrp,
      unit: 'dBm',
      floors: [-80, -90, -100] as const,
      range: [-120, -70] as const,
      hint: t('dashboard.signalRsrpHint')
    },
    {
      label: 'RSRQ',
      value: cell.rsrq,
      unit: 'dB',
      floors: [-10, -15, -20] as const,
      range: [-25, -5] as const,
      hint: t('dashboard.signalRsrqHint')
    },
    {
      label: 'SINR',
      value: cell.sinr,
      unit: 'dB',
      floors: [20, 13, 0] as const,
      range: [-10, 30] as const,
      hint: t('dashboard.signalSinrHint')
    }
  ]
  const meters: SignalMeter[] = []
  for (const entry of candidates) {
    // A meter is a verdict ("good", "poor"). One is drawn only for a reading
    // the device actually took; a missing one is listed as unknown below.
    const value = entry.value
    if (typeof value !== 'number' || !Number.isFinite(value)) continue
    meters.push({
      label: entry.label,
      value,
      unit: entry.unit,
      grade: grade(value, entry.floors[0], entry.floors[1], entry.floors[2]),
      fill: fillRatio(value, entry.range[0], entry.range[1]),
      hint: entry.hint
    })
  }
  return meters
})

/** The three strength readings the device did not send, named. */
const missingMeters = computed(() => {
  const cell = cellular.value
  if (!cell) return []
  return (['rsrp', 'rsrq', 'sinr'] as const)
    .filter(key => typeof cell[key] !== 'number')
    .map(key => key.toUpperCase())
})

function gradeLabel(value: SignalGrade): string {
  return t(`dashboard.signalGrade_${value}`)
}

// Throughput history for the data-interface card. The samples are already
// collected for this device, so drawing them costs no extra requests.
const RATE_CHART_WIDTH = 268
const RATE_CHART_HEIGHT = 42

function ratePath(pick: (sample: DeviceMetricsSample) => number | null | undefined): string {
  // Only the samples that carry a rate: a gap is left out, not drawn at zero.
  const rates = deviceHistory.value
    .slice(-40)
    .map(pick)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (rates.length < 2) return ''
  const peak = Math.max(1, ...rates)
  const step = RATE_CHART_WIDTH / (rates.length - 1)
  return rates
    .map((rate, index) => {
      const x = index * step
      const y = RATE_CHART_HEIGHT - (rate / peak) * (RATE_CHART_HEIGHT - 3) - 1.5
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const rxPath = computed(() => ratePath(sample => sample.rxRate))
const txPath = computed(() => ratePath(sample => sample.txRate))
const hasRateHistory = computed(() => Boolean(rxPath.value || txPath.value))

// Serving-cell identifiers. The signal strength itself is shown as meters
// above; repeating the same numbers as a flat list is what made this card and
// the module card look like duplicates of each other.
//
// CELL ID AND TAC LOCATE THE DEVICE. They are listed for administrators only,
// once, as plain rows: not in a tooltip, not in a summary line, not anywhere a
// screenshot or a copy of some other row would carry them along. For anyone
// else the rows are absent and the card says why. (The object handed to this
// card has them removed already for a non-administrator; the check here is the
// second lock, not the only one.)
const radioFacts = computed<RadioFact[]>(() => {
  const cell = cellular.value
  if (!cell) return []
  const unknown = labels.value.unknown
  const bandwidth =
    cell.downlink_bandwidth_mhz === null && cell.uplink_bandwidth_mhz === null
      ? unknown
      : `↓${cell.downlink_bandwidth_mhz ?? unknown} · ↑${cell.uplink_bandwidth_mhz ?? unknown} MHz`
  const facts: RadioFact[] = [
    { label: t('dashboard.radioBand'), value: cell.band ? `${cell.band}${cell.frequency_mhz !== null ? ` · ${cell.frequency_mhz} MHz` : ''}` : unknown },
    { label: t('dashboard.signalBandwidth'), value: bandwidth },
    { label: 'RSSI', value: cell.rssi === null ? unknown : `${cell.rssi} dBm` },
    { label: 'PCI', value: cell.pci === null ? unknown : String(cell.pci) },
    { label: 'EARFCN', value: cell.earfcn === null ? unknown : String(cell.earfcn) }
  ]
  for (const name of missingMeters.value) facts.push({ label: name, value: unknown })
  if (props.admin) {
    facts.push({ label: 'Cell ID', value: cell.cell_id_hex || unknown })
    facts.push({ label: 'TAC', value: cell.tac_hex || unknown })
  }
  return facts
})
</script>

<template>
  <div class="insight-cards">
    <template v-if="deviceScope && line">
    <!-- Data interface -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Usb :size="15" />
        <h4>{{ t('dashboard.netCard') }} · {{ netOwner }}</h4>
      </header>
      <div v-if="netInterface" class="insight-card__body">
        <div class="insight-net__iface">
          <span class="insight-net__name">{{ netInterface.name }}</span>
          <span v-if="cellular && (cellular.rat || cellular.state)" class="insight-net__rat">{{ [cellular.rat, cellular.state].filter(Boolean).join(' · ') }}</span>
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
        <div v-if="hasRateHistory" class="insight-net__chart" aria-hidden="true">
          <svg :viewBox="`0 0 ${RATE_CHART_WIDTH} ${RATE_CHART_HEIGHT}`" preserveAspectRatio="none">
            <path :d="rxPath" fill="none" stroke="var(--accent)" stroke-width="1.6" stroke-linejoin="round" />
            <path :d="txPath" fill="none" stroke="var(--success)" stroke-width="1.6" stroke-linejoin="round" />
          </svg>
          <span class="insight-net__chart-legend">
            <em class="is-rx">↓ {{ t('dashboard.downlinkRate') }}</em>
            <em class="is-tx">↑ {{ t('dashboard.uplinkRate') }}</em>
          </span>
        </div>

        <dl class="insight-net__totals">
          <div><dt>{{ t('dashboard.rxTotal') }}</dt><dd>{{ formatBytes(netInterface.rx_bytes) }}</dd></div>
          <div><dt>{{ t('dashboard.txTotal') }}</dt><dd>{{ formatBytes(netInterface.tx_bytes) }}</dd></div>
        </dl>
      </div>
      <p v-else-if="!snapshot" class="insight-card__empty insight-card__absent">
        <strong v-if="gapHeading">{{ gapHeading }}</strong>
        <span>{{ gapText }}</span>
      </p>
      <p v-else-if="isNode" class="insight-card__empty insight-card__absent">
        <strong>{{ labels.notProvided }}</strong>
        <span>{{ labels.noDataInterface }}</span>
      </p>
      <p v-else class="insight-card__empty">{{ t('dashboard.noInterface') }}</p>
    </article>

    <!-- System runtime -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Activity :size="15" />
        <h4>{{ t('dashboard.sysRunCard') }} · {{ line.model || labels.unknown }}</h4>
      </header>
      <div class="insight-card__body">
        <p v-if="!snapshot" class="insight-card__absent">
          <strong v-if="gapHeading">{{ gapHeading }}</strong>
          <span>{{ gapText }}</span>
        </p>
        <template v-else>
          <div class="insight-run__uptime">
            <span>{{ t('runtimeStatus.uptime') }}</span>
            <strong>{{ uptimeText }}</strong>
          </div>
          <div class="insight-run__meter">
            <div class="insight-run__meter-head">
              <span>{{ t('runtimeStatus.memory') }}</span>
              <strong>{{ memoryView ? memoryView.text : labels.unknown }}</strong>
            </div>
            <div v-if="memoryView" class="insight-run__bar"><i :class="barTone(memoryView.percent)" :style="{ width: `${memoryView.percent}%` }" /></div>
          </div>
          <div class="insight-run__meter">
            <div class="insight-run__meter-head">
              <span>{{ t('runtimeStatus.storage') }}</span>
              <strong>{{ flashView ? flashView.text : labels.unknown }}</strong>
            </div>
            <div v-if="flashView" class="insight-run__bar"><i :class="barTone(flashView.percent)" :style="{ width: `${flashView.percent}%` }" /></div>
          </div>
          <p class="insight-run__procs">
            {{ t('dashboard.processes', { count: countText(snapshot.process_count) }) }}
            ·
            {{ t('dashboard.threads', { count: countText(snapshot.thread_count) }) }}
            <template v-if="snapshot.cpu_current_mhz !== null">
              · {{ Math.round(snapshot.cpu_current_mhz) }} MHz
            </template>
          </p>
        </template>
        <div v-if="!isNode" class="insight-run__actions">
          <button
            class="secondary-button insight-run__repair"
            type="button"
            :disabled="repairBusy"
            @click="onRepair"
          >
            <Wrench :size="14" />{{ repairBusy ? t('dashboard.repairRunning') : t('dashboard.repairButton') }}
          </button>
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
        <!-- The repair and restart actions drive a module on the NAS's own
             USB. A Node keeps the row, saying so, instead of losing it. -->
        <p v-else class="insight-card__pending-note insight-run__host-only">{{ labels.hostOnlyActions }}</p>
        <p v-if="repairBusy || repairStepLabel" class="insight-run__repair-status" role="status">
          <LoaderCircle :size="12" class="is-spinning" />
          {{ repairStepLabel }}
        </p>
        <p v-else-if="repairResult === 'ok'" class="insight-run__repair-status is-ok" role="status">
          <CheckCircle2 :size="12" />
          {{ t('dashboard.repairDone') }}
        </p>
        <p v-else-if="repairError" class="insight-run__repair-status is-failed" role="alert">
          <AlertCircle :size="12" />
          {{ repairError }}
        </p>
      </div>
    </article>

    <!-- Serving cell detail -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Signal :size="15" />
        <h4>{{ t('dashboard.cellCard') }} · {{ line.model || labels.unknown }}</h4>
      </header>
      <div v-if="cellular" class="insight-card__body">
        <div v-if="signalMeters.length" class="insight-signal">
          <div
            v-for="meter in signalMeters"
            :key="meter.label"
            class="insight-signal__row"
            :class="`is-${meter.grade}`"
            :title="meter.hint"
          >
            <span class="insight-signal__label">{{ meter.label }}</span>
            <span class="insight-signal__track">
              <span class="insight-signal__fill" :style="{ width: `${Math.round(meter.fill * 100)}%` }" />
            </span>
            <span class="insight-signal__value">{{ meter.value }}<em>{{ meter.unit }}</em></span>
            <span class="insight-signal__grade">{{ gradeLabel(meter.grade) }}</span>
          </div>
        </div>

        <dl class="insight-radio">
          <div v-for="fact in radioFacts" :key="fact.label">
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>

        <!-- Operator code and access technology only. The cell ID and TAC
             used to be repeated here; they locate the device and now appear
             once, in the rows above, for administrators. -->
        <p class="insight-radio__note">
          <Radio :size="12" />
          {{ [cellular.plmn, cellular.rat].filter(Boolean).join(' · ') || labels.unknown }}
        </p>
        <p v-if="!admin" class="insight-card__pending-note">{{ labels.cellAdminOnly }}</p>
      </div>
      <p v-else-if="!snapshot" class="insight-card__empty insight-card__absent">
        <strong v-if="gapHeading">{{ gapHeading }}</strong>
        <span>{{ gapText }}</span>
      </p>
      <p v-else-if="isNode" class="insight-card__empty insight-card__absent">
        <strong>{{ labels.notProvided }}</strong>
        <span>{{ labels.noCell }}</span>
      </p>
      <p v-else class="insight-card__empty">{{ t('dashboard.noCellular') }}</p>
    </article>
    </template>

    <!-- Nodes and clients on the two decentralised paths.
         Every figure here is measured: the seed list is the probe's own result
         with its own round-trip times, and the two identities are what this
         gateway reports as its own. A path that is off says so. -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Network :size="15" />
        <h4>{{ t('dashboard.nodesTitle') }}</h4>
      </header>
      <div v-if="!nodesEmpty" class="insight-card__body insight-nodes">
        <div class="insight-nodes__row">
          <span class="insight-nodes__label">{{ t('dashboard.nodesSeeds') }}</span>
          <span v-if="nknNodes" class="insight-nodes__value">
            {{ t('dashboard.nodesReachable', { n: `${nknNodes.reachable}/${nknNodes.probed}` }) }}
          </span>
          <span v-else-if="nknEnabled" class="insight-nodes__value is-muted">
            {{ t('dashboard.nodesProbing') }}
          </span>
          <span v-else class="insight-nodes__value is-muted">
            {{ t('dashboard.globeDhtOff') }}
          </span>
        </div>

        <ul v-if="seedNodes.length" class="insight-nodes__seeds">
          <li v-for="node in seedNodes" :key="node.address">
            <span class="insight-nodes__addr" :title="node.address">{{ shortHost(node.address) }}</span>
            <span class="insight-nodes__bar" aria-hidden="true">
              <i :style="{ width: `${Math.round((node.rtt_ms / seedScale) * 100)}%` }" />
            </span>
            <span class="insight-nodes__rtt">{{ node.rtt_ms }} ms</span>
          </li>
        </ul>

        <div class="insight-nodes__row">
          <span class="insight-nodes__label">{{ t('dashboard.nodesPeers') }}</span>
          <span v-if="dhtPeerLine" class="insight-nodes__value">{{ dhtPeerLine }}</span>
          <span v-else class="insight-nodes__value is-muted">{{ t('dashboard.globeDhtOff') }}</span>
        </div>

        <dl class="insight-nodes__ids">
          <div v-if="nknAddress">
            <dt>{{ t('dashboard.nodesSelfNkn') }}</dt>
            <dd :title="nknAddress">{{ shortId(nknAddress) }}</dd>
          </div>
          <div v-if="dhtStatus?.peer_id">
            <dt>{{ t('dashboard.nodesSelfDht') }}</dt>
            <dd :title="dhtStatus.peer_id">{{ shortId(dhtStatus.peer_id) }}</dd>
          </div>
        </dl>
      </div>
      <p v-else class="insight-card__empty">{{ t('dashboard.nodesEmpty') }}</p>
    </article>
  </div>
</template>

<style scoped>
/* The base card chrome -- .insight-cards, .insight-card, its head, body and
   empty state -- now lives in insightCard.css, imported above, so the cards in
   ModuleExtraCards.vue are the same object as these. Only what is particular to
   these four cards stays below. */

.insight-run__host-only { margin-top: auto; }

/* Net card */
.insight-net__iface {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.insight-net__name {
  min-width: 0;
  overflow-wrap: anywhere;
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
  transition: width var(--motion-slow) var(--ease-emphasized);
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
  flex-wrap: wrap;
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

.insight-run__repair:not(:disabled) {
  border-color: color-mix(in srgb, var(--accent) 45%, var(--border));
  color: var(--accent-strong, var(--accent));
}

.insight-run__repair-status {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 11.5px;
  color: var(--muted-strong);
}

.insight-run__repair-status.is-ok { color: var(--success, #30a46c); }
.insight-run__repair-status.is-failed { color: var(--danger, #f87171); }

.is-spinning { animation: insight-spin 1s linear infinite; }

@keyframes insight-spin {
  to { transform: rotate(360deg); }
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

/* ---- data interface throughput ---- */
.insight-net__chart {
  display: grid;
  flex: 1 1 auto;
  min-height: 56px;
  align-content: end;
  gap: 6px;
  padding: 8px 10px 6px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.insight-net__chart svg {
  display: block;
  width: 100%;
  height: 42px;
}

.insight-net__chart-legend {
  display: flex;
  gap: 12px;
  color: var(--faint);
  font-size: 10.5px;
}

.insight-net__chart-legend em { font-style: normal; }
.insight-net__chart-legend .is-rx { color: var(--accent); }
.insight-net__chart-legend .is-tx { color: var(--success); }

/* ---- serving-cell quality ---- */
.insight-signal {
  display: grid;
  gap: 9px;
}

.insight-signal__row {
  display: grid;
  align-items: center;
  gap: 8px;
  grid-template-columns: 42px minmax(0, 1fr) auto auto;
}

.insight-signal__label {
  color: var(--muted);
  font-size: 11px;
  letter-spacing: 0.04em;
}

.insight-signal__track {
  height: 6px;
  background: var(--border);
  border-radius: var(--radius-pill);
  overflow: hidden;
}

.insight-signal__fill {
  display: block;
  height: 100%;
  border-radius: var(--radius-pill);
  background: currentcolor;
  transition: width var(--motion-slow) var(--ease-emphasized);
}

.insight-signal__value {
  color: var(--text);
  font-size: 12px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.insight-signal__value em {
  margin-left: 2px;
  color: var(--faint);
  font-size: 9.5px;
  font-style: normal;
  font-weight: 500;
}

.insight-signal__grade {
  min-width: 34px;
  font-size: 10.5px;
  text-align: right;
}

/* The row carries the colour; the bar and the verdict both inherit it, so a
   weak signal reads the same way in every element of the row. */
.insight-signal__row.is-excellent { color: var(--success); }
.insight-signal__row.is-good { color: var(--accent); }
.insight-signal__row.is-fair { color: var(--warning); }
.insight-signal__row.is-poor { color: var(--danger); }
/* Nodes and clients. Rows of label/value, a seed list with a shared latency
   scale, and the two identities. The addresses are monospace and truncated in
   the middle: their two ends are what identifies them, and the middle is
   noise that would push the card wider than its neighbours. */
.insight-nodes { gap: 10px; }

.insight-nodes__row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
}

.insight-nodes__label { color: var(--muted); }

.insight-nodes__value {
  color: var(--text);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.insight-nodes__value.is-muted { color: var(--faint); font-weight: 500; }

/* THE GRID IS THE LIST, NOT THE ROW, and that is the whole point of this
   block. Each row used to be its own grid, which meant the three columns were
   measured separately for every row: a two-digit reading made the last column
   narrower than a three-digit one, the `1fr` address column absorbed the
   difference, and the bar beside "72 ms" sat seven pixels to the right of the
   bar beside "314 ms" -- track and all. Bars that are meant to be compared
   with one another cannot start at different places.

   Putting the columns on the <ul> and letting the rows dissolve into it with
   `display: contents` gives every row the same three tracks, and the reading
   column sizes itself to the widest reading in the list. That also survives a
   four-digit latency, which a hard-coded column width would not. */
.insight-nodes__seeds {
  display: grid;
  align-items: center;
  margin: 0;
  padding: 8px 10px;
  gap: 6px 8px;
  grid-template-columns: minmax(0, 1fr) 46px auto;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
  list-style: none;
}

.insight-nodes__seeds li { display: contents; }

.insight-nodes__addr {
  overflow: hidden;
  color: var(--muted);
  font-size: 10.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* One scale across the list, set by the slowest node reached, so the bars
   compare with each other rather than each filling its own track. */
.insight-nodes__bar {
  overflow: hidden;
  height: 3px;
  background: var(--control-muted, var(--border));
  border-radius: 999px;
}

.insight-nodes__bar > i {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: inherit;
}

/* Right-aligned so the digits line up under one another rather than the
   units: with the column now shared, "72 ms" and "314 ms" end at the same
   edge, which is what makes the four readings scan as one column of numbers. */
.insight-nodes__rtt {
  color: var(--text);
  font-size: 10.5px;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.insight-nodes__ids {
  display: grid;
  margin: 0;
  gap: 6px;
}

.insight-nodes__ids > div {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
}

.insight-nodes__ids dt { color: var(--muted); font-size: 11px; }

.insight-nodes__ids dd {
  margin: 0;
  overflow: hidden;
  color: var(--text);
  font-size: 10.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
