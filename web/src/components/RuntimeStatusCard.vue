<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  acquireSystemMetrics,
  releaseSystemMetrics,
  systemMetricsState
} from '../state/systemMetrics'

// Runtime status popover shown when the rail avatar is clicked: kernel
// uptime plus live CPU / memory / storage / module-temperature gauges,
// mirroring the task-manager data stream.

const { t } = useI18n()

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
  if (snap.process_count > 0) {
    parts.push(`${snap.process_count} ${t('dashboard.processes', { count: snap.process_count })}`)
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
</script>

<template>
  <div class="runtime-card" role="dialog" :aria-label="t('runtimeStatus.title')">
    <div class="runtime-card__info">
      <strong class="runtime-card__title">{{ t('runtimeStatus.title') }}</strong>
      <span class="runtime-card__device">{{ deviceLabel }}</span>
      <span v-if="version" class="runtime-card__version">NasSimHub {{ version }}</span>
      <div class="runtime-card__uptime">
        <span class="runtime-card__uptime-label">{{ t('runtimeStatus.uptime') }}</span>
        <strong class="runtime-card__uptime-value">{{ uptimeText }}</strong>
      </div>
      <span v-if="sourceLabel" class="runtime-card__source">{{ sourceLabel }}</span>
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
  </div>
</template>

<style scoped>
.runtime-card {
  display: flex;
  gap: 18px;
  width: 340px;
  padding: 18px 20px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  box-shadow: 0 18px 40px rgb(0 0 0 / 35%);
}

.runtime-card__info {
  display: flex;
  flex: 1 1 auto;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
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
  flex-direction: column;
  gap: 2px;
  margin-top: auto;
  padding-top: 10px;
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

.runtime-card__gauges {
  display: grid;
  flex: 0 0 auto;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 14px;
}

.runtime-gauge {
  display: grid;
  width: 64px;
  place-items: center;
  grid-template-rows: auto auto;
  row-gap: 3px;
}

.runtime-gauge__ring {
  grid-area: 1 / 1;
  width: 58px;
  height: 58px;
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
  font-size: 9.5px;
  color: var(--muted-strong);
}
</style>
