<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { bootstrapResource } from '../state/workspace'
import { requestPhoneGeo } from '../state/phoneGeo'
import { accessTechnologyLabel, ownNumberFacts } from '../utils/phoneRegion'

const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const open = ref(false)

const BUILD_ID = import.meta.env.VITE_MODEMDECK_BUILD_ID
const version = typeof BUILD_ID === 'string' && BUILD_ID.trim() ? `v${BUILD_ID.trim().replace(/^v/, '')}` : ''

const line = computed(() => {
  const lines = bootstrapResource.data?.lines || []
  return lines.find(item => item.state === 'registered') || lines[0]
})

const facts = computed(() => ownNumberFacts(line.value?.phone_number || '', line.value?.operator))

const remoteGeo = ref<{ province: string; carrier: string } | undefined>()

watch(
  () => line.value?.phone_number || '',
  number => {
    remoteGeo.value = undefined
    if (number) void requestPhoneGeo(number).then(geo => { remoteGeo.value = geo })
  },
  { immediate: true }
)

const geoLine = computed(() => {
  if (remoteGeo.value) {
    const parts = [remoteGeo.value.province, remoteGeo.value.carrier].filter(Boolean)
    if (parts.length) return parts.join(' · ')
  }
  return facts.value.geoLine
})

const networkLabel = computed(() => accessTechnologyLabel(line.value?.serving_radio?.access_technology))

const registered = computed(() => line.value?.state === 'registered')

const volteState = computed<'on' | 'off' | 'hidden'>(() => {
  if (!line.value || !registered.value) return 'hidden'
  if (networkLabel.value !== '4G') return 'hidden'
  const volteControl = bootstrapResource.data?.capabilities?.volte_control
  if (volteControl === false) return 'off'
  return line.value.capabilities?.voice === false ? 'off' : 'on'
})

const signalQuality = computed(() => {
  if (!line.value || !registered.value) return null
  return line.value.signal_quality ?? null
})

const activeBars = computed(() => {
  if (signalQuality.value === null) return 0
  return Math.ceil(Math.min(Math.max(signalQuality.value, 0), 100) / 25)
})

// Plan quota rings: data green / SMS blue / voice amber; turns red when left <= 20%
const QUOTA_RING = 2 * Math.PI * 5.5
const quotaRings = computed(() => {
  const quota = line.value?.quota
  if (!quota) return []
  const mk = (label: string, left: number, total: number, color: string, unit: string) => {
    const pct = total > 0 ? Math.max(0, Math.min(1, left / total)) : 0
    return {
      label,
      color,
      pct,
      dash: QUOTA_RING * pct,
      low: pct <= 0.2,
      leftText: `${Number(left.toFixed(1))}${unit}`,
      totalText: `${total}${unit}`,
      hint: t('homesim.quota.left', { left: Number(left.toFixed(1)), total, unit })
    }
  }
  return [
    mk(t('homesim.quota.data'), quota.data_total - quota.data_used, quota.data_total, 'var(--success)', t('homesim.quota.unitData')),
    mk(t('homesim.quota.sms'), quota.sms_total - quota.sms_used, quota.sms_total, 'var(--accent)', t('homesim.quota.unitSms')),
    mk(t('homesim.quota.voice'), quota.voice_total - quota.voice_used, quota.voice_total, 'var(--warning)', t('homesim.quota.unitVoice'))
  ]
})

function toggle(): void {
  open.value = !open.value
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (root.value && !root.value.contains(event.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('pointerdown', onDocumentPointerDown))
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocumentPointerDown))
</script>

<template>
  <div v-if="line" ref="root" class="line-status-wrap">
    <div
      class="line-status"
      :class="{ 'is-open': open }"
      role="button"
      tabindex="0"
      :aria-label="t('homesim.quota.title')"
      :aria-expanded="open"
      @click="toggle"
      @keydown.enter.prevent="toggle"
      @keydown.space.prevent="toggle"
    >
      <div class="line-status__grid">
        <div class="line-status__row">
          <span class="line-status__bars" role="img" :aria-label="`${signalQuality ?? 0}%`">
            <span
              v-for="bar in 4"
              :key="bar"
              class="line-status__bar"
              :class="{ 'is-active': registered && signalQuality !== null && bar <= activeBars }"
            />
          </span>
          <span v-if="networkLabel" class="line-status__tag">{{ networkLabel }}</span>
          <span
            v-if="volteState !== 'hidden'"
            class="line-status__tag"
            :class="{ 'is-off': volteState === 'off' }"
          >VoLTE</span>
          <span v-if="line.state !== 'registered'" class="line-status__state">{{ line.state }}</span>
          <span
            v-for="ring in quotaRings"
            :key="ring.label"
            class="line-status__ring"
            :title="ring.hint"
          >
            <svg width="13" height="13" viewBox="0 0 13 13">
              <circle cx="6.5" cy="6.5" r="5.5" fill="none" stroke="var(--border)" stroke-width="2" />
              <circle
                cx="6.5"
                cy="6.5"
                r="5.5"
                fill="none"
                :stroke="ring.low ? 'var(--danger)' : ring.color"
                stroke-width="2"
                stroke-linecap="round"
                :stroke-dasharray="QUOTA_RING"
                :stroke-dashoffset="QUOTA_RING - ring.dash"
                transform="rotate(-90 6.5 6.5)"
              />
            </svg>
            <span class="line-status__ring-label">{{ ring.label }}</span>
          </span>
        </div>
        <div class="line-status__row line-status__row--split">
          <span class="line-status__number">{{ facts.formatted }}</span>
          <span v-if="geoLine" class="line-status__geo" :title="geoLine">{{ geoLine }}</span>
          <span v-else-if="line.operator" class="line-status__geo">{{ line.operator }}</span>
        </div>
      </div>
      <span v-if="version" class="line-status__version">{{ version }}</span>
    </div>

    <!-- Quota popover: horizontal bars, colors match the header rings -->
    <div v-if="open" class="line-status-pop">
      <header>{{ t('homesim.quota.title') }}</header>
      <div
        v-for="ring in quotaRings"
        :key="ring.label"
        class="line-status-pop__row"
      >
        <span class="line-status-pop__label" :style="{ color: ring.low ? 'var(--danger)' : ring.color }">{{ ring.label }}</span>
        <span class="line-status-pop__bar">
          <span
            class="line-status-pop__fill"
            :style="{ width: Math.round(ring.pct * 100) + '%', background: ring.low ? 'var(--danger)' : ring.color }"
          />
        </span>
        <span class="line-status-pop__text">
          <span class="line-status-pop__left-cluster">
            <em class="line-status-pop__left-label">{{ t('homesim.quota.leftLabel') }}</em>
            <span>{{ ring.leftText }}</span>
          </span>
          <span class="line-status-pop__slash">/</span>
          <span class="line-status-pop__num line-status-pop__num--total">{{ ring.totalText }}</span>
        </span>
      </div>
      <p v-if="!quotaRings.length" class="line-status-pop__empty">{{ t('homesim.quotaEmpty') }}</p>
    </div>
  </div>
</template>

<style scoped>
.line-status-wrap {
  position: relative;
  display: inline-flex;
  flex: 0 1 auto;
  min-width: 0;
}

.line-status {
  display: inline-flex;
  flex: 0 1 auto;
  align-items: center;
  gap: 10px;
  min-width: 0;
  padding: 3px 10px;
  overflow: hidden;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.45;
  white-space: nowrap;
  cursor: pointer;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: color-mix(in srgb, var(--surface) 72%, transparent);
  transition: border-color var(--motion-fast) var(--ease-standard);
}

.line-status:hover,
.line-status.is-open {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
}

.line-status:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.line-status__grid {
  display: flex;
  flex: 0 1 auto;
  flex-direction: column;
  justify-content: center;
  gap: 1px;
  min-width: 0;
}

.line-status__row {
  display: flex;
  flex: 1 1 auto;
  min-width: 0;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}

.line-status__row--split {
  justify-content: space-between;
}

.line-status__bars {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: flex-end;
  gap: 2px;
  height: 11px;
}

.line-status__bar {
  width: 3px;
  background: color-mix(in srgb, var(--muted) 28%, transparent);
  border-radius: 1px 1px 0 0;
}

.line-status__bar:nth-child(1) {
  height: 4px;
}

.line-status__bar:nth-child(2) {
  height: 6px;
}

.line-status__bar:nth-child(3) {
  height: 8px;
}

.line-status__bar:nth-child(4) {
  height: 11px;
}

.line-status__bar.is-active {
  background: var(--success, #34d399);
}

.line-status__tag {
  flex: 0 0 auto;
  padding: 0 5px;
  font-size: 9.5px;
  font-weight: 700;
  color: var(--accent-strong);
  letter-spacing: 0.02em;
  border: 1px solid color-mix(in srgb, var(--accent-strong) 36%, transparent);
  border-radius: 4px;
}

.line-status__tag.is-off {
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 40%, transparent);
  text-decoration: line-through;
}

.line-status__state {
  flex: 0 0 auto;
  color: var(--danger);
}

.line-status__ring {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 3px;
}

.line-status__ring svg {
  display: block;
}

.line-status__ring-label {
  font-size: 9.5px;
  color: var(--muted);
}

.line-status__number {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11.5px;
  font-weight: 650;
  color: var(--text);
  text-overflow: ellipsis;
}

.line-status__geo {
  flex: 0 1 auto;
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.line-status__version {
  display: inline-flex;
  flex: 0 0 auto;
  align-self: stretch;
  align-items: center;
  padding-left: 10px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 10px;
  border-left: 1px solid var(--border);
}

.line-status-pop {
  position: absolute;
  z-index: var(--layer-popover);
  top: calc(100% + 8px);
  left: 0;
  width: 340px;
  padding: 12px 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  box-shadow: 0 12px 32px rgb(16 24 40 / 16%);
}

.line-status-pop header {
  margin-bottom: 10px;
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
}

.line-status-pop__row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 0;
}

.line-status-pop__label {
  flex: 0 0 30px;
  font-size: 11.5px;
  font-weight: 650;
  text-align: right;
}

.line-status-pop__bar {
  position: relative;
  display: flex;
  flex: 1 1 auto;
  height: 8px;
  overflow: hidden;
  background: var(--surface-subtle);
  border-radius: 4px;
}

.line-status-pop__fill {
  display: block;
  height: 100%;
  border-radius: 4px;
  transition: width var(--motion-base) var(--ease-standard);
}

.line-status-pop__text {
  display: flex;
  flex: 0 0 104px;
  align-items: baseline;
  justify-content: flex-end;
  color: var(--muted);
  font-size: 10.5px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.line-status-pop__left-cluster {
  display: inline-flex;
  flex: 0 0 52px;
  align-items: baseline;
  justify-content: flex-end;
  gap: 3px;
}

.line-status-pop__left-label {
  flex: 0 0 auto;
  font-style: normal;
}

.line-status-pop__slash {
  flex: 0 0 auto;
  margin-right: 4px;
}

.line-status-pop__slash {
  margin-right: 4px;
}

.line-status-pop__empty {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}

@media (max-width: 1100px) {
  .line-status__geo,
  .line-status__version {
    display: none;
  }
}

@media (max-width: 860px) {
  .line-status-wrap {
    display: none;
  }
}
</style>
