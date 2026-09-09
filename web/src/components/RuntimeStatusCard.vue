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

const memory = computed(() => {
  const snapshot = current.value
  if (!snapshot || !snapshot.memory_total_bytes) return null
  const used = Math.max(0, snapshot.memory_total_bytes - snapshot.memory_available_bytes)
  return {
    percent: Math.min(100, Math.max(0, (used / snapshot.memory_total_bytes) * 100)),
    label: `${(used / 1024 ** 3).toFixed(1)} GB`
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

interface Gauge {
  key: string
  label: string
  value: string
  percent: number
  tone: 'ok' | 'warn' | 'danger'
}

const gauges = computed<Gauge[]>(() => {
  const list: Gauge[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: `${Math.round(cpuPercent.value)}%`,
      percent: cpuPercent.value,
      tone: cpuPercent.value >= 90 ? 'danger' : cpuPercent.value >= 70 ? 'warn' : 'ok'
    }
  ]
  if (memory.value) {
    list.push({
      key: 'memory',
      label: t('runtimeStatus.memory'),
      value: memory.value.label,
      percent: memory.value.percent,
      tone: memory.value.percent >= 90 ? 'danger' : memory.value.percent >= 80 ? 'warn' : 'ok'
    })
  }
  list.push({
    key: 'flash',
    label: t('runtimeStatus.storage'),
    value: `${Math.round(flashPercent.value)}%`,
    percent: flashPercent.value,
    tone: flashPercent.value >= 90 ? 'danger' : flashPercent.value >= 80 ? 'warn' : 'ok'
  })
  if (temperature.value) {
    list.push({
      key: 'temp',
      label: t('runtimeStatus.temperature'),
      value: `${Math.round(temperature.value.celsius)}°C`,
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
        <span class="runtime-gauge__label">{{ gauge.label }}</span>
        <span class="runtime-gauge__value-text">{{ gauge.value }}</span>
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

.runtime-card__gauges {
  display: grid;
  flex: 0 0 auto;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 12px;
}

.runtime-gauge {
  position: relative;
  display: grid;
  width: 64px;
  place-items: center;
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

.runtime-gauge__label,
.runtime-gauge__value-text {
  grid-area: 1 / 1;
  pointer-events: none;
}

.runtime-gauge__label {
  margin-top: -34px;
  font-size: 9px;
  color: var(--muted-strong);
}

.runtime-gauge__value-text {
  margin-top: 12px;
  font-size: 11px;
  font-weight: 700;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}
</style>
