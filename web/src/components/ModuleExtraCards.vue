<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
// Every glyph here is one the app already bundles. Lucide icons are collected
// into a single shared `icons` chunk that sits in the STARTUP SHELL, so a new
// one imported by a lazily loaded card still costs every visitor bytes before
// the app has drawn anything: five new glyphs measured 0.9 KiB gzip and put the
// startup budget 0.9 KiB over its 246.0 KiB limit. Picking from the 131 already
// in the chunk costs nothing at all.
import {
  BadgeCheck,
  ChartNoAxesCombined,
  CloudUpload,
  Cpu,
  FileCheck2,
  History,
  KeyRound,
  Lock,
  MessageSquareText,
  PhoneCall,
  RefreshCw,
  ShieldCheck,
  Zap
} from '@lucide/vue'
import type { Component } from 'vue'
import type { Device, LineSummary, ModemPort } from '../api/types'
import { networkCardLabels } from '../i18n/networkCardNotes'
import './insightCard.css'
import {
  getCommunicationStats,
  getSecurityPosture,
  type CommunicationStats,
  type SecurityPosture,
  type SecurityRow
} from '../api/dashboardCards'
import { refreshDeviceWorkspace } from '../state/workspace'
import SensitiveValue from './SensitiveValue.vue'

// The three cards that fill out the dashboard's insight row: secure transport,
// module information, and the communication summary.
//
// They are one component because they are one row: the three share their
// chrome with the four cards beside them through insightCard.css, and keeping
// them together is what makes "same object, seven times" enforceable rather
// than merely intended.
//
// Two of the three have NO BACKEND YET. The operator asked for the interface
// first, against the design their backend will be written to, and that is the
// one thing these cards have to be careful about: a card with nothing behind it
// says so, per field, and never shows a figure it does not have. It never shows
// zero either -- "0 SMS today" is a claim about the gateway's day that nothing
// has measured, and it is a different statement from "no SMS figures yet". So
// every absent field renders as the pending label, no pill is ever tinted for a
// value that did not come from the endpoint, and each card carries a footnote
// saying the endpoint is not in place while that is true.
//
// The module card is the exception: every line on it is real, read from the
// line and device records the app already holds, and its refresh button
// re-reads exactly those two.

const props = defineProps<{ line: LineSummary; device?: Device }>()

const i18n = useI18n()
const { t } = i18n

// The labels live in the lazily merged dashboard module rather than the locale
// files, for the same reason the nodes card's do: the startup bundle had 0.1
// KiB of headroom and these are thirty keys deep in nine languages.
//
// Merged SYNCHRONOUSLY DURING SETUP, before the first render, and not from
// `onMounted`. Merging a message is a mutation of the i18n store that `t()`
// does not track as a reactive dependency, so a component that merges after it
// has rendered only recovers if something else happens to re-render it. These
// three cards have almost no changing state -- two of them sit on endpoints
// that answer nothing -- so nothing did, and every label on them rendered as
// its raw key name. Measured on the review server before this changed.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, { dashboard: networkCardLabels(locale) })
  },
  { immediate: true, flush: 'sync' }
)

// ---------------------------------------------------------------------------
// Secure transport
// ---------------------------------------------------------------------------

const posture = ref<SecurityPosture | null>(null)
const stats = ref<CommunicationStats | null>(null)
let timer: ReturnType<typeof setInterval> | undefined

async function loadCards(): Promise<void> {
  const [security, communication] = await Promise.all([
    getSecurityPosture(),
    getCommunicationStats()
  ])
  posture.value = security
  stats.value = communication
}

onMounted(() => {
  void loadCards()
  // Five minutes. Neither of these is a live reading: a posture changes when
  // something is reconfigured, and a daily count moves by one. Polling them at
  // the metrics cadence would be ten times the traffic for the same screen.
  timer = setInterval(() => { void loadCards() }, 300_000)
})
onBeforeUnmount(() => { if (timer) clearInterval(timer) })

interface PostureLine {
  key: string
  icon: Component
  label: string
  row?: SecurityRow
}

/**
 * The seven rows, in the operator's own order.
 *
 * `row` is undefined until the endpoint answers with that member, and an
 * undefined row is what renders the pending pill. The row is never synthesised
 * from anything this component can see: the browser cannot observe the
 * gateway's TLS floor, its pinning set or its OTA signing policy, so guessing
 * any of them here would be the card inventing its own subject.
 */
const postureLines = computed<PostureLine[]>(() => {
  const data = posture.value
  return [
    { key: 'transport', icon: Lock, label: t('dashboard.secTransport'), row: data?.transport },
    { key: 'pq', icon: Zap, label: t('dashboard.secPostQuantum'), row: data?.post_quantum },
    { key: 'pinning', icon: BadgeCheck, label: t('dashboard.secPinning'), row: data?.pinning },
    { key: 'identity', icon: KeyRound, label: t('dashboard.secIdentity'), row: data?.identity },
    { key: 'replay', icon: History, label: t('dashboard.secReplay'), row: data?.replay },
    { key: 'packet', icon: FileCheck2, label: t('dashboard.secPacketAuth'), row: data?.packet_auth },
    { key: 'ota', icon: CloudUpload, label: t('dashboard.secOta'), row: data?.ota }
  ]
})

/**
 * The pill's tone. Only a row that arrived from the endpoint gets one: a green
 * pill is a claim that something was checked, so an absent row stays neutral
 * rather than reading as "fine".
 */
function postureTone(row: SecurityRow | undefined): string {
  if (!row) return ''
  if (row.state === 'ok') return 'is-ok'
  if (row.state === 'warn') return 'is-warn'
  return ''
}

const events = computed(() => posture.value?.events ?? null)

const eventsHeading = computed(() => {
  const hours = events.value?.window_hours
  return hours ? t('dashboard.secEventsTitle', { h: hours }) : t('dashboard.secEventsPending')
})

const keyStateLabel = computed(() => {
  switch (events.value?.key_state) {
    case 'ok': return t('dashboard.secKeyOk')
    case 'warn': return t('dashboard.secKeyWarn')
    case 'expired': return t('dashboard.secKeyExpired')
    default: return ''
  }
})

const downgradeLabel = computed(() => {
  const guard = events.value?.downgrade_guard
  if (guard === undefined) return ''
  return guard ? t('dashboard.secOn') : t('dashboard.secOff')
})

/** A figure, or the empty string when the field was not sent. Never zero by default. */
function figure(value: number | undefined): string {
  return value === undefined ? '' : value.toLocaleString(i18n.locale.value)
}

/**
 * The four readings in the events panel, as label-over-value tiles.
 *
 * Tiles rather than the label/value lines this card started with, and measured
 * rather than preferred: on a 350px track the English labels and the pending
 * text both wrapped to two lines and collided in the middle of the panel. A
 * tile gives each its own line, and it is also the shape the traffic card uses
 * for its figures, so the two cards read as one family.
 *
 * An absent reading is a DASH here, not the pending pill the rows above use.
 * The distinction is deliberate and consistent: a pill carries a state, a
 * figure that was never measured is simply not there.
 */
const eventEntries = computed(() => [
  { key: 'logins', label: t('dashboard.secAnomalous'), value: figure(events.value?.anomalous_logins) },
  { key: 'replay', label: t('dashboard.secReplayBlocked'), value: figure(events.value?.replay_blocked) },
  { key: 'keys', label: t('dashboard.secKeyState'), value: keyStateLabel.value },
  { key: 'downgrade', label: t('dashboard.secDowngradeGuard'), value: downgradeLabel.value }
])

// ---------------------------------------------------------------------------
// Module information
// ---------------------------------------------------------------------------

interface ModuleFact { label: string; value: string; sensitive?: string }

/**
 * Everything here is read from the line and device records the app already
 * holds -- no request of its own. A field the module did not report is shown as
 * "not reported" rather than dropped, because on this card the absence is
 * itself worth seeing: a module that reports no firmware string is a module
 * that answered the query differently from the one beside it.
 */
const moduleFacts = computed<ModuleFact[]>(() => {
  const line = props.line
  const device = props.device
  const unknown = t('device.notReported')
  const facts: ModuleFact[] = [
    { label: t('lines.model'), value: line.model || device?.model || unknown },
    { label: t('lines.firmware'), value: line.firmware || device?.firmware || unknown },
    { label: t('device.hardwareVersion'), value: line.hardware_revision || unknown },
    { label: t('diagnostics.modemIMEI'), value: '', sensitive: line.device_imei || device?.imei || '' },
    { label: t('device.primaryPort'), value: line.primary_port || device?.port || unknown },
    { label: 'ICCID', value: '', sensitive: line.iccid },
    { label: 'IMSI', value: '', sensitive: line.imsi }
  ]
  const operator = line.serving_operator_name || line.home_operator_name || line.operator
  if (operator) facts.push({ label: t('network.operator'), value: operator })
  return facts
})

function portTypeLabel(port: ModemPort): string {
  switch (port.type) {
    case 'net': return t('device.portNetwork')
    case 'at': return t('device.portAT')
    case 'qcdm': return t('device.portQCDM')
    case 'gps': return 'GNSS'
    case 'qmi': return t('device.portQMI')
    case 'mbim': return t('device.portMBIM')
    case 'audio': return t('device.portAudio')
    case 'ignored': return t('device.portIgnored')
    default: return port.type || t('common.unknown')
  }
}

const ports = computed(() => props.line.ports ?? [])

const moduleRefreshing = ref(false)
async function onRefreshModule(): Promise<void> {
  if (moduleRefreshing.value) return
  moduleRefreshing.value = true
  try {
    await refreshDeviceWorkspace()
  } finally {
    moduleRefreshing.value = false
  }
}

// ---------------------------------------------------------------------------
// Communication summary
// ---------------------------------------------------------------------------

interface StatTile { key: string; label: string; value: string }

const statsWindowLabel = computed(() => {
  const hours = stats.value?.window_hours
  return hours ? t('dashboard.statsWindow', { h: hours }) : ''
})

const smsTiles = computed<StatTile[]>(() => {
  const sms = stats.value?.sms
  return [
    { key: 'in', label: t('dashboard.statsSmsIn'), value: figure(sms?.received) },
    { key: 'out', label: t('dashboard.statsSmsOut'), value: figure(sms?.sent) },
    { key: 'failed', label: t('dashboard.statsSmsFailed'), value: figure(sms?.failed) },
    { key: 'code', label: t('dashboard.statsSmsCode'), value: figure(sms?.verification) }
  ]
})

function formatDuration(seconds: number | undefined): string {
  if (seconds === undefined) return ''
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}${t('runtimeStatus.hourUnit')}${minutes}${t('runtimeStatus.minuteUnit')}`
  return `${minutes}${t('runtimeStatus.minuteUnit')}`
}

const callTiles = computed<StatTile[]>(() => {
  const calls = stats.value?.calls
  return [
    { key: 'in', label: t('dashboard.incoming'), value: figure(calls?.incoming) },
    { key: 'out', label: t('dashboard.outgoing'), value: figure(calls?.outgoing) },
    { key: 'missed', label: t('calls.missed'), value: figure(calls?.missed) },
    { key: 'duration', label: t('dashboard.duration'), value: formatDuration(calls?.duration_seconds) }
  ]
})

// The trend, drawn only when the buckets are there. A chart with no data is an
// empty frame that reads as "nothing happened", which is a stronger claim than
// "we have not been told".
const TREND_WIDTH = 268
const TREND_HEIGHT = 40

const trendBuckets = computed(() => stats.value?.hourly ?? [])

const trendPeak = computed(() =>
  Math.max(1, ...trendBuckets.value.map(b => (b.messages ?? 0) + (b.calls ?? 0)))
)

/** One bar per bucket, so a quiet hour is a gap rather than an interpolation. */
const trendBars = computed(() => {
  const buckets = trendBuckets.value
  if (!buckets.length) return []
  const slot = TREND_WIDTH / buckets.length
  const barWidth = Math.max(1.5, slot - 2)
  return buckets.map((bucket, index) => {
    const total = (bucket.messages ?? 0) + (bucket.calls ?? 0)
    const height = total > 0 ? Math.max(1.5, (total / trendPeak.value) * (TREND_HEIGHT - 2)) : 0
    return {
      key: `${bucket.hour}-${index}`,
      x: index * slot + (slot - barWidth) / 2,
      y: TREND_HEIGHT - height,
      width: barWidth,
      height,
      // The bar's own figures, so the chart is readable without axis labels it
      // has no room for. Built from the two group labels already on the card
      // rather than new copy, so the chart and the tiles above it name the
      // same two things the same way.
      title: `${t('diagnostics.messages')} ${bucket.messages ?? 0} · ${t('device.calls')} ${bucket.calls ?? 0}`
    }
  })
})
</script>

<template>
  <div class="insight-cards">
    <!-- Secure transport. Interface first: the endpoint behind it is not built
         yet, so every row reads "not wired yet" until it answers, no pill is
         tinted, and the footnote says why. -->
    <article class="insight-card">
      <header class="insight-card__head">
        <ShieldCheck :size="15" />
        <h4>{{ t('dashboard.secTitle') }}</h4>
      </header>
      <div class="insight-card__body">
        <ul class="insight-sec__rows">
          <li v-for="entry in postureLines" :key="entry.key" class="insight-sec__row">
            <component :is="entry.icon" :size="13" class="insight-sec__icon" aria-hidden="true" />
            <span class="insight-sec__label">{{ entry.label }}</span>
            <span
              v-if="entry.row"
              class="insight-tag"
              :class="postureTone(entry.row)"
              :title="entry.row.detail"
            >{{ entry.row.value || t('dashboard.secOn') }}</span>
            <span v-else class="insight-tag insight-sec__pending">{{ t('dashboard.cardPending') }}</span>
          </li>
        </ul>

        <section class="insight-sec__events">
          <h5>{{ eventsHeading }}</h5>
          <div class="insight-sec__events-grid">
            <div v-for="entry in eventEntries" :key="entry.key" class="insight-sec__event">
              <span class="insight-sec__event-label">{{ entry.label }}</span>
              <strong :class="{ 'is-pending': !entry.value }">{{ entry.value || '—' }}</strong>
            </div>
          </div>
        </section>

        <p v-if="!posture" class="insight-card__pending-note">{{ t('dashboard.cardPendingNote') }}</p>
      </div>
    </article>

    <!-- Module information. Every line real, read from records the app already
         holds; the head action re-reads those two records and nothing else. -->
    <article class="insight-card">
      <header class="insight-card__head">
        <Cpu :size="15" />
        <h4>{{ t('dashboard.modTitle') }}</h4>
        <button
          class="insight-card__action"
          type="button"
          :disabled="moduleRefreshing"
          :aria-label="t('common.refresh')"
          :title="t('common.refresh')"
          @click="onRefreshModule"
        >
          <RefreshCw :size="14" :class="{ 'is-spinning': moduleRefreshing }" />
        </button>
      </header>
      <div class="insight-card__body">
        <dl class="insight-mod__facts">
          <div v-for="fact in moduleFacts" :key="fact.label" class="insight-line">
            <dt class="insight-line__label">{{ fact.label }}</dt>
            <dd v-if="fact.sensitive !== undefined" class="insight-mod__sensitive">
              <SensitiveValue :value="fact.sensitive" :label="fact.label" />
            </dd>
            <dd v-else class="insight-line__value">{{ fact.value }}</dd>
          </div>
        </dl>

        <div v-if="ports.length" class="insight-mod__ports">
          <span class="insight-line__label">{{ t('dashboard.modPortsLabel') }}</span>
          <ul>
            <li v-for="port in ports" :key="port.name">
              <span class="insight-tag" :title="port.name">{{ portTypeLabel(port) }}</span>
              <span class="insight-mono">{{ port.name }}</span>
            </li>
          </ul>
        </div>
      </div>
    </article>

    <!-- Communication summary. Interface first, like the security card: the
         counts cannot be computed in the browser (the call list is paginated
         and messages load per conversation), so a tile with no figure shows a
         dash, never a zero. -->
    <article class="insight-card">
      <header class="insight-card__head">
        <ChartNoAxesCombined :size="15" />
        <h4>{{ t('dashboard.statsTitle') }}</h4>
        <span v-if="statsWindowLabel" class="insight-card__action insight-stats__window">
          {{ statsWindowLabel }}
        </span>
      </header>
      <div class="insight-card__body">
        <section class="insight-stats__group">
          <h5><MessageSquareText :size="12" />{{ t('diagnostics.messages') }}</h5>
          <div class="insight-stats__tiles">
            <div v-for="tile in smsTiles" :key="tile.key" class="insight-stats__tile">
              <span class="insight-stats__tile-label">{{ tile.label }}</span>
              <strong :class="{ 'is-pending': !tile.value }">{{ tile.value || '—' }}</strong>
            </div>
          </div>
        </section>

        <section class="insight-stats__group">
          <h5><PhoneCall :size="12" />{{ t('device.calls') }}</h5>
          <div class="insight-stats__tiles">
            <div v-for="tile in callTiles" :key="tile.key" class="insight-stats__tile">
              <span class="insight-stats__tile-label">{{ tile.label }}</span>
              <strong :class="{ 'is-pending': !tile.value }">{{ tile.value || '—' }}</strong>
            </div>
          </div>
        </section>

        <section v-if="trendBars.length" class="insight-stats__trend">
          <h5>{{ t('dashboard.statsTrend') }}</h5>
          <svg
            :viewBox="`0 0 ${TREND_WIDTH} ${TREND_HEIGHT}`"
            preserveAspectRatio="none"
            role="img"
            :aria-label="t('dashboard.statsTrend')"
          >
            <!-- The bars sit ON this line, so a quiet hour reads as a gap in a
                 row of hours rather than as a missing bar. -->
            <line
              :x1="0"
              :y1="TREND_HEIGHT - 0.5"
              :x2="TREND_WIDTH"
              :y2="TREND_HEIGHT - 0.5"
              stroke="var(--border)"
              stroke-width="1"
            />
            <rect
              v-for="bar in trendBars"
              :key="bar.key"
              :x="bar.x"
              :y="bar.y"
              :width="bar.width"
              :height="bar.height"
              fill="var(--accent)"
              rx="0.8"
            ><title>{{ bar.title }}</title></rect>
          </svg>
        </section>

        <p v-if="!stats" class="insight-card__pending-note">{{ t('dashboard.cardPendingNote') }}</p>
      </div>
    </article>
  </div>
</template>

<style scoped>
/* The frame, head, body, empty state, label/value line, pill and monospace
   identifier all come from insightCard.css, which the four cards beside these
   import too. Only what is particular to these three is below. */

/* ---- secure transport ---- */
.insight-sec__rows {
  display: grid;
  margin: 0;
  padding: 0;
  gap: 7px;
  list-style: none;
}

.insight-sec__row {
  display: grid;
  align-items: center;
  gap: 8px;
  grid-template-columns: 15px minmax(0, 1fr) auto;
}

.insight-sec__icon { color: var(--muted); }

.insight-sec__label {
  overflow: hidden;
  color: var(--muted-strong);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The pending pill is the quietest thing on the card: it marks a row that has
   no state yet, and any emphasis on it would read as a state. */
.insight-sec__pending {
  color: var(--faint);
  background: none;
  border-style: dashed;
}

.insight-sec__events {
  padding: 9px 11px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.insight-sec__events h5 {
  margin: 0 0 7px;
  color: var(--faint);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.insight-sec__events-grid {
  display: grid;
  gap: 7px 10px;
  grid-template-columns: 1fr 1fr;
}

.insight-sec__event {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}

.insight-sec__event-label {
  color: var(--muted);
  font-size: 10.5px;
  line-height: 1.35;
}

.insight-sec__event strong {
  color: var(--text);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.insight-sec__event strong.is-pending {
  color: var(--faint);
  font-weight: 500;
}

/* ---- module information ---- */
/* The facts take whatever height the row's tallest card forces on this one and
   spread over it, rather than sitting in a block with a void underneath. The
   nodes card beside it does the same thing, which is what makes the two read as
   one row rather than as a full card next to a half-empty one. */
.insight-mod__facts {
  display: grid;
  flex: 1 1 auto;
  align-content: space-between;
  margin: 0;
  gap: 7px;
}

.insight-mod__facts dd { margin: 0; min-width: 0; }

.insight-mod__sensitive {
  color: var(--text);
  font-size: 11px;
  text-align: right;
}

.insight-mod__ports {
  display: grid;
  gap: 6px;
  padding-top: 9px;
  border-top: 1px solid var(--border);
  font-size: 12px;
}

.insight-mod__ports ul {
  display: flex;
  flex-wrap: wrap;
  margin: 0;
  padding: 0;
  gap: 5px 8px;
  list-style: none;
}

/* A port is its role and its device node, and neither is much use without the
   other -- so the pair stays together on one line and the list wraps between
   pairs rather than inside one. */
.insight-mod__ports li {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
}

.insight-mod__ports .insight-mono { color: var(--muted); }

/* ---- communication summary ---- */
.insight-stats__window {
  color: var(--faint);
  cursor: default;
  font-size: 10.5px;
  font-variant-numeric: tabular-nums;
}

.insight-stats__group {
  display: grid;
  gap: 7px;
}

.insight-stats__group h5 {
  display: flex;
  align-items: center;
  gap: 5px;
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}

/* Two by two, which is what makes the card fit a narrow column: four figures
   across would set a width this track has not got, and the operator asked for
   the group to take two rows for exactly that reason. */
.insight-stats__tiles {
  display: grid;
  gap: 6px;
  grid-template-columns: 1fr 1fr;
}

.insight-stats__tile {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 6px 9px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.insight-stats__tile-label {
  color: var(--muted);
  font-size: 10.5px;
}

.insight-stats__tile strong {
  color: var(--text);
  font-size: 15px;
  font-variant-numeric: tabular-nums;
}

/* A dash, not a zero, and quiet enough that it reads as "no figure" rather
   than as a figure. */
.insight-stats__tile strong.is-pending {
  color: var(--faint);
  font-size: 14px;
  font-weight: 500;
}

.insight-stats__trend {
  display: grid;
  gap: 5px;
}

.insight-stats__trend h5 {
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}

.insight-stats__trend svg {
  display: block;
  width: 100%;
  height: 40px;
}

.is-spinning { animation: insight-extra-spin 1s linear infinite; }

@keyframes insight-extra-spin {
  to { transform: rotate(360deg); }
}
</style>
