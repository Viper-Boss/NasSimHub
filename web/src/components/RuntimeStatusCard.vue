<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { RefreshCw, RotateCcw } from '@lucide/vue'
import {
  acquireSystemMetrics,
  releaseSystemMetrics,
  systemMetricsState
} from '../state/systemMetrics'
import { gateway } from '../api/client'
import type { UpdateStatus } from '../api/types'
import { bootstrapResource, lineKey, presentModuleLines } from '../state/workspace'
import { devicesResource } from '../state/workspace'
import { restartModem } from '../state/deviceConfiguration'
import { requestConfirmation } from '../state/confirmation'
import { isAdministrator } from '../state/session'

// Runtime status popover shown when the rail avatar is clicked: kernel
// uptime plus live CPU / memory / storage / module-temperature gauges,
// mirroring the task-manager data stream, and the two actions the operator
// asked for on this card.
//
// The two actions are deliberately narrow, because the wide versions of them
// would be promises this card cannot keep:
//
//   UPDATE only CHECKS. Applying an update is a multi-container operation with
//   a progress list and, for the hardware component, its own confirmation --
//   that flow lives in the About settings panel and belongs there. So this
//   button asks the gateway whether a release is newer and reports the answer;
//   when there is one it hands the reader over to that panel rather than
//   starting anything from a popover. It is shown to administrators only,
//   since the panel it leads to is theirs.
//
//   RESTART restarts THE MODULE, and says so in its label. There is no
//   endpoint for restarting the NAS or the gateway process, so a button
//   labelled just "restart" here would be read as offering one. With no module
//   present it is disabled rather than hidden, so the absence is visible.

const { t } = useI18n()
const router = useRouter()

onMounted(() => { acquireSystemMetrics() })
onBeforeUnmount(() => { releaseSystemMetrics() })

const current = computed(() => systemMetricsState.metrics)

const buildId = import.meta.env?.VITE_MODEMDECK_BUILD_ID
const version = typeof buildId === 'string' && buildId.trim()
  ? `v${buildId.trim().replace(/^v/, '')}`
  : ''

function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)
  if (days > 0) return `${days} ${t('runtimeStatus.dayUnit')} ${hours} ${t('runtimeStatus.hourUnit')} ${minutes} ${t('runtimeStatus.minuteUnit')}`
  if (hours > 0) return `${hours} ${t('runtimeStatus.hourUnit')} ${minutes} ${t('runtimeStatus.minuteUnit')} ${secs} ${t('runtimeStatus.secondUnit')}`
  return `${minutes} ${t('runtimeStatus.minuteUnit')} ${secs} ${t('runtimeStatus.secondUnit')}`
}

const uptimeText = computed(() => formatUptime(current.value?.host_uptime_seconds ?? 0))

const cpuPercent = computed(() => {
  const value = current.value?.cpu_usage_percent ?? 0
  return Math.min(100, Math.max(0, value))
})

function formatCompact(bytes: number): string {
  // Module RAM is tens of MB — auto-scale so it never reads "0.0 GB".
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${Math.round(bytes / 1024 ** 2)} MB`
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`
  return `${Math.round(bytes)} B`
}

const memory = computed(() => {
  const snapshot = current.value
  if (!snapshot || !snapshot.memory_total_bytes) return null
  const used = Math.max(0, snapshot.memory_total_bytes - snapshot.memory_available_bytes)
  return {
    percent: Math.min(100, Math.max(0, (used / snapshot.memory_total_bytes) * 100)),
    label: formatCompact(used),
    detail: `${formatCompact(used)} / ${formatCompact(snapshot.memory_total_bytes)}`
  }
})

const flashPercent = computed(() => {
  const value = current.value?.flash_used_percent ?? 0
  return Math.min(100, Math.max(0, value))
})

// Temperature ring maps 35-85 °C onto 0-100% of the arc.
const temperature = computed(() => {
  const value = current.value?.cpu_temp_celsius
  if (value === null || value === undefined) return null
  return {
    celsius: value,
    percent: Math.min(100, Math.max(0, (value - 35) / 50 * 100))
  }
})

const deviceLabel = computed(() => {
  const model = current.value?.cpu_model_name?.replace(/\s+/g, ' ').trim()
  return model || 'NasSimHub Gateway'
})

// Everything on this card comes from the module-side agent snapshot, not the
// NAS host — spell that out so small numbers (42 MB RAM) read correctly.
const sourceLabel = computed(() => {
  const snap = current.value
  if (!snap) return ''
  const parts: string[] = []
  if (snap.cores > 0) parts.push(`${snap.cores} cores`)
  if (snap.cpu_current_mhz > 0) parts.push(`${Math.round(snap.cpu_current_mhz)} MHz`)
  // `dashboard.processes` already carries the count ("{count} procs"), so the
  // number must not be prefixed again -- it was, and the card read "145 145
  // procs" in every language.
  if (snap.process_count > 0) {
    parts.push(t('dashboard.processes', { count: snap.process_count }))
  }
  return parts.join(' · ')
})

interface Gauge {
  key: string
  label: string
  value: string
  detail: string
  percent: number
  tone: 'ok' | 'warn' | 'danger'
}

const gauges = computed<Gauge[]>(() => {
  const list: Gauge[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: `${Math.round(cpuPercent.value)}%`,
      detail: t('runtimeStatus.cpuUsage'),
      percent: cpuPercent.value,
      tone: cpuPercent.value >= 90 ? 'danger' : cpuPercent.value >= 70 ? 'warn' : 'ok'
    }
  ]
  if (memory.value) {
    list.push({
      key: 'memory',
      label: t('runtimeStatus.memory'),
      value: memory.value.label,
      detail: memory.value.detail,
      percent: memory.value.percent,
      tone: memory.value.percent >= 90 ? 'danger' : memory.value.percent >= 80 ? 'warn' : 'ok'
    })
  }
  list.push({
    key: 'flash',
    label: t('runtimeStatus.storage'),
    value: `${Math.round(flashPercent.value)}%`,
    detail: current.value?.flash_fs_type ? `${current.value.flash_fs_type} · ${current.value.flash_path}` : t('runtimeStatus.storage'),
    percent: flashPercent.value,
    tone: flashPercent.value >= 90 ? 'danger' : flashPercent.value >= 80 ? 'warn' : 'ok'
  })
  if (temperature.value) {
    list.push({
      key: 'temp',
      label: t('runtimeStatus.temperature'),
      value: `${Math.round(temperature.value.celsius)}°C`,
      detail: t('runtimeStatus.temperature'),
      percent: temperature.value.percent,
      tone: temperature.value.celsius >= 75 ? 'danger' : temperature.value.celsius >= 60 ? 'warn' : 'ok'
    })
  }
  return list.slice(0, 4)
})

const RADIUS = 20
const CIRCUMFERENCE = 2 * Math.PI * RADIUS

function arcDash(percent: number): string {
  const clamped = Math.min(100, Math.max(0, percent))
  return `${(clamped / 100) * CIRCUMFERENCE} ${CIRCUMFERENCE}`
}

// ---------------------------------------------------------------------------
// Check for updates
// ---------------------------------------------------------------------------

const canCheckUpdate = isAdministrator
const updateChecking = ref(false)
const updateStatus = ref<UpdateStatus | ''>('')

/** The gateway's own verdict, in its own words -- the same four the About panel shows. */
const updateMessage = computed(() =>
  updateStatus.value ? t(`about.updateStatus.${updateStatus.value}.title`) : ''
)

const updateAvailable = computed(() => updateStatus.value === 'update_available')

async function onCheckUpdate(): Promise<void> {
  if (updateChecking.value) return
  updateChecking.value = true
  try {
    // `true` forces a fresh read rather than returning a cached verdict: a
    // button that can answer from a cache is a button that can say "up to
    // date" without having asked anything.
    updateStatus.value = (await gateway.checkForUpdates(true)).status
  } catch {
    // The gateway could not reach the release index. That is its own status,
    // and it has its own sentence -- it is not "up to date".
    updateStatus.value = 'unavailable'
  } finally {
    updateChecking.value = false
  }
}

function openUpdatePanel(): void {
  void router.push({ name: 'settings', params: { section: 'about' } })
}

// ---------------------------------------------------------------------------
// Restart the module
// ---------------------------------------------------------------------------

/**
 * Which module the restart button acts on: the default line when it is
 * actually present, otherwise the only present module.
 *
 * "Otherwise the only" is the important half. With two modules and no default
 * set there is no answer, and picking the first of the list would restart
 * whichever one the gateway happened to enumerate first -- a coin toss on a
 * forty-five-second outage. The button disables itself instead.
 */
const restartTarget = computed(() => {
  const lines = bootstrapResource.data?.lines ?? []
  const present = presentModuleLines(lines, devicesResource.data ?? [])
  if (!present.length) return ''
  const defaultID = bootstrapResource.data?.line_settings.default_line_id || ''
  const chosen = present.find(line => lineKey(line) === defaultID || line.id === defaultID)
  if (chosen) return chosen.id
  return present.length === 1 ? present[0]!.id : ''
})

const restarting = ref(false)

async function onRestartModule(): Promise<void> {
  const target = restartTarget.value
  if (!target || restarting.value) return
  const confirmed = await requestConfirmation({
    title: t('dashboard.restartModem'),
    message: t('dashboard.restartModemConfirm'),
    tone: 'danger'
  })
  if (!confirmed) return
  restarting.value = true
  try {
    await restartModem(target)
  } finally {
    restarting.value = false
  }
}
</script>

<template>
  <div class="runtime-card" role="dialog" :aria-label="t('runtimeStatus.title')">
    <div class="runtime-card__info">
      <strong class="runtime-card__title">{{ t('runtimeStatus.title') }}</strong>
      <span class="runtime-card__device" :title="deviceLabel">{{ deviceLabel }}</span>
      <span v-if="version" class="runtime-card__version">NasSimHub {{ version }}</span>
    </div>

    <div class="runtime-card__uptime">
      <span class="runtime-card__uptime-label">{{ t('runtimeStatus.uptime') }}</span>
      <strong class="runtime-card__uptime-value">{{ uptimeText }}</strong>
    </div>

    <div class="runtime-card__gauges">
      <div v-for="gauge in gauges" :key="gauge.key" class="runtime-gauge">
        <svg viewBox="0 0 48 48" class="runtime-gauge__ring">
          <circle class="runtime-gauge__track" cx="24" cy="24" :r="RADIUS" />
          <circle
            class="runtime-gauge__value"
            :class="`runtime-gauge__value--${gauge.tone}`"
            cx="24" cy="24" :r="RADIUS"
            :stroke-dasharray="arcDash(gauge.percent)"
          />
        </svg>
        <span class="runtime-gauge__value-text" :title="gauge.detail">{{ gauge.value }}</span>
        <span class="runtime-gauge__label">{{ gauge.label }}</span>
      </div>
    </div>

    <span v-if="sourceLabel" class="runtime-card__source">{{ sourceLabel }}</span>

    <div class="runtime-card__actions">
      <button
        v-if="canCheckUpdate"
        class="secondary-button"
        type="button"
        :disabled="updateChecking"
        @click="onCheckUpdate"
      >
        <RefreshCw :size="13" :class="{ 'is-spinning': updateChecking }" />
        {{ updateChecking ? t('about.checkingTitle') : t('about.updateTitle') }}
      </button>
      <button
        class="secondary-button"
        type="button"
        :disabled="!restartTarget || restarting"
        :title="restartTarget ? t('dashboard.restartModem') : t('dashboard.noLines')"
        @click="onRestartModule"
      >
        <RotateCcw :size="13" />
        {{ restarting ? t('dashboard.restarting') : t('dashboard.restartModem') }}
      </button>
    </div>

    <!-- The gateway's verdict, quoted rather than summarised, and a way through
         to the panel that can actually apply it. -->
    <p v-if="updateMessage" class="runtime-card__update" role="status">
      <span>{{ updateMessage }}</span>
      <button v-if="updateAvailable" class="text-button" type="button" @click="openUpdatePanel">
        {{ t('about.updateAction') }}
      </button>
    </p>
  </div>
</template>

<style scoped>
/* Stacked, not two columns.
 *
 * It used to be a text column beside a 2x2 block of gauges, which gave the text
 * whatever was left: measured at the shipped 340px, the info column came out
 * around 140px, and "NasSimHub v…", the CPU model and a three-part uptime all
 * had to wrap or truncate inside it. That is the "contents display wrong" the
 * operator reported, and no popover width fixes it while the gauges take a
 * fixed 142px out of the middle.
 *
 * Stacking gives every line the full width and puts the four gauges in one
 * row -- 4 x 64px plus gaps fits the 300px interior, measured -- which is also
 * what leaves room for the two action buttons underneath. */
.runtime-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 340px;
  max-width: calc(100vw - 24px);
  padding: 18px 20px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  box-shadow: 0 18px 40px rgb(0 0 0 / 35%);
}

.runtime-card__info {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.runtime-card__title {
  font-size: 15px;
  font-weight: 750;
  color: var(--text);
}

.runtime-card__device {
  overflow: hidden;
  font-size: 11.5px;
  color: var(--muted-strong);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-card__version {
  font-size: 10.5px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}

.runtime-card__uptime {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.runtime-card__uptime-label {
  font-size: 10.5px;
  color: var(--muted);
}

.runtime-card__uptime-value {
  font-size: 14px;
  font-weight: 700;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.runtime-card__source {
  font-size: 10px;
  color: var(--faint, var(--muted));
  font-variant-numeric: tabular-nums;
}

/* One row of four. `1fr` columns rather than fixed widths, so a card with
   three gauges (no temperature reading) spreads across the same width instead
   of leaving a gap on the right. */
.runtime-card__gauges {
  display: grid;
  gap: 10px;
  grid-auto-flow: column;
  grid-auto-columns: minmax(0, 1fr);
}

.runtime-gauge {
  display: grid;
  min-width: 0;
  place-items: center;
  grid-template-rows: auto auto;
  row-gap: 3px;
}

.runtime-gauge__ring {
  grid-area: 1 / 1;
  width: 54px;
  height: 54px;
  transform: rotate(-90deg);
}

.runtime-gauge__track,
.runtime-gauge__value {
  fill: none;
  stroke-width: 4;
}

.runtime-gauge__track {
  stroke: var(--border);
}

.runtime-gauge__value {
  stroke-linecap: round;
  transition: stroke-dasharray 0.5s ease;
}

.runtime-gauge__value--ok { stroke: var(--accent, #4f8cff); }
.runtime-gauge__value--warn { stroke: var(--warning, #fbbf24); }
.runtime-gauge__value--danger { stroke: var(--danger, #f87171); }

.runtime-gauge__value-text {
  grid-area: 1 / 1;
  max-width: 44px;
  overflow: hidden;
  font-size: 10px;
  font-weight: 700;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  pointer-events: none;
}

.runtime-gauge__label {
  grid-area: 2 / 1;
  max-width: 100%;
  overflow: hidden;
  font-size: 9.5px;
  color: var(--muted-strong);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-card__actions {
  display: flex;
  gap: 8px;
}

.runtime-card__actions .secondary-button {
  display: inline-flex;
  min-height: 32px;
  flex: 1 1 0;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 10px;
  font-size: 12px;
  white-space: nowrap;
}

.runtime-card__update {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin: 0;
  color: var(--muted-strong);
  font-size: 11.5px;
}

.runtime-card__update .text-button {
  padding: 0;
  font-size: 11.5px;
}

.is-spinning { animation: runtime-card-spin 1s linear infinite; }

@keyframes runtime-card-spin {
  to { transform: rotate(360deg); }
}
</style>
