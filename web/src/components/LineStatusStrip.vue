<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { Phone } from '@lucide/vue'
import { bootstrapResource } from '../state/workspace'
import { requestPhoneGeo } from '../state/phoneGeo'
import { accessTechnologyLabel, cnOperatorNameOf, ownNumberFacts } from '../utils/phoneRegion'
import { voiceStatus, chineseLineState } from '../utils/voiceStatus'
import { isRegisteredNetwork } from '../utils/operatorNetwork'
import { callState } from '../state/call'
import { gateway } from '../api/client'
import { getNKNNetworkNodes, getNKNNetworkPeers, getNKNNetworkStatus } from '../api/client'
import type { NKNNetworkNodes, NKNNetworkPeers, NKNNetworkStatus } from '../api/client'
const LineSelector = defineAsyncComponent(() => import('./LineSelector.vue'))

const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const open = ref(false)
const selectedLineID = ref('')
const viewedLineID = computed({ get: () => line.value?.id || '', set: value => { selectedLineID.value = value } })
const lines = computed(() => bootstrapResource.data?.lines || [])

const BUILD_ID = import.meta.env.VITE_MODEMDECK_BUILD_ID
const version = typeof BUILD_ID === 'string' && BUILD_ID.trim() ? `v${BUILD_ID.trim().replace(/^v/, '')}` : ''

// --- NKN network badge + popover -------------------------------------------
const nknOpen = ref(false)
const nknStatus = ref<NKNNetworkStatus | null>(null)
const nknPeers = ref<NKNNetworkPeers | null>(null)
const nknNodes = ref<NKNNetworkNodes | null>(null)
const nknDetailLoading = ref(false)
const nknDetailError = ref('')
const nknQr = ref('')
const nknCopied = ref(false)

const nknState = computed(() => nknStatus.value?.runtime.state || 'unknown')
const nknDotClass = computed(() => {
  if (nknState.value === 'active') return 'is-green'
  if (nknState.value === 'joining') return 'is-amber'
  return 'is-red'
})
const nknStateLabel = computed(() => t(`homesim.nknBadge.state_${nknState.value === 'unknown' ? 'offline' : nknState.value}`))
const nknAddress = computed(() => nknStatus.value?.runtime.my_address || '')

let nknTimer: ReturnType<typeof setInterval> | undefined

async function refreshNknStatus(): Promise<void> {
  try {
    nknStatus.value = await getNKNNetworkStatus()
  } catch {
    nknStatus.value = null
  }
}

async function loadNknDetails(): Promise<void> {
  nknDetailLoading.value = true
  nknDetailError.value = ''
  nknPeers.value = null
  nknNodes.value = null
  const [peers, nodes] = await Promise.allSettled([getNKNNetworkPeers(), getNKNNetworkNodes()])
  if (peers.status === 'fulfilled') nknPeers.value = peers.value
  if (nodes.status === 'fulfilled') nknNodes.value = nodes.value
  if (peers.status === 'rejected' && nodes.status === 'rejected') {
    nknDetailError.value = peers.reason instanceof Error ? peers.reason.message : String(peers.reason)
  }
  nknDetailLoading.value = false
  if (nknAddress.value) {
    try { nknQr.value = await QRCode.toDataURL(nknAddress.value, { width: 200, margin: 2, errorCorrectionLevel: 'M' }) } catch { nknQr.value = '' }
  }
}

function toggleNkn(): void {
  nknOpen.value = !nknOpen.value
  if (nknOpen.value) {
    nknCopied.value = false
    void loadNknDetails()
  }
}

async function copyNknAddress(): Promise<void> {
  try {
    await navigator.clipboard.writeText(nknAddress.value)
    nknCopied.value = true
    setTimeout(() => { nknCopied.value = false }, 2000)
  } catch { /* clipboard unavailable */ }
}
// ---------------------------------------------------------------------------

const line = computed(() => {
  return lines.value.find(item => item.id === selectedLineID.value) || lines.value.find(item => isRegisteredNetwork(item)) || lines.value[0]
})

const facts = computed(() => ownNumberFacts(line.value?.phone_number || '', line.value?.operator))

const remoteGeo = ref<{ province: string; carrier: string } | undefined>()

watch(
  () => line.value?.phone_number || '',
  number => {
    remoteGeo.value = undefined
    if (number) void requestPhoneGeo(number).then(geo => { if (number === line.value?.phone_number) remoteGeo.value = geo })
  },
  { immediate: true }
)

const geoLine = computed(() => {
  if (remoteGeo.value) {
    const parts = [remoteGeo.value.province, remoteGeo.value.carrier].filter(Boolean)
    if (parts.length) return parts.join(' · ')
  }
  // Keep readable region/carrier labels; hide raw PLMN identifiers.
  const parts = [facts.value.region, facts.value.carrier].filter(Boolean)
  return parts.join(' · ')
})

const networkLabel = computed(() => accessTechnologyLabel(line.value?.serving_radio?.access_technology))

// Prefer the mapped carrier name over a raw PLMN identifier.
const carrierName = computed(() => {
  const value = line.value
  if (!value) return ''
  const code = value.serving_operator_code || value.operator || ''
  const name = cnOperatorNameOf(code)
  if (name) return name
  if (code && !/^\d+$/.test(code)) return code
  return facts.value.carrier || ''
})

// A null data state means unknown, not disconnected.
const dataOn = ref<boolean | null>(null)
const wifiCapability = ref<boolean | null>(null)
const vowifiOn = computed(() => callState.sessions.some(session => session.line_id === line.value?.id && session.phase === 'active' && session.bearer === 'vowifi'))
const vowifiHint = computed(() => t(vowifiOn.value ? 'homesim.strip.vowifiActive' : wifiCapability.value === false ? 'homesim.strip.vowifiUnavailable' : 'homesim.strip.vowifiHint'))
let deviceRequest = 0

watch(() => line.value?.id, () => { void refreshDataState() }, { immediate: true })

async function refreshDataState(): Promise<void> {
  const request = ++deviceRequest
  const value = line.value
  dataOn.value = null
  wifiCapability.value = null
  if (!value?.id) {
    dataOn.value = null
    return
  }
  try {
    const config = await gateway.getDeviceConfiguration(value.id)
    if (request !== deviceRequest || line.value?.id !== value.id) return
    dataOn.value = (config.hardware?.data_connections || []).some(connection => connection.connected)
    wifiCapability.value = config.hardware?.capabilities.vowifi.implemented ?? null
  } catch {
    if (request === deviceRequest) { dataOn.value = null; wifiCapability.value = null }
  }
}

const registered = computed(() => Boolean(line.value && isRegisteredNetwork(line.value)))

const volte = computed(() => voiceStatus({ registered: registered.value, network: networkLabel.value,
  activeBearer: callState.sessions.find(session => session.line_id === line.value?.id && session.phase === 'active')?.bearer
}))
const volteState = computed(() => volte.value.state)

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
      leftText: `${Number(Math.max(0, left).toFixed(1))}${unit}`,
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
  if (root.value && !root.value.contains(event.target as Node)) {
    open.value = false
    nknOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  void refreshNknStatus()
  nknTimer = setInterval(() => {
    void refreshNknStatus()
    void refreshDataState()
  }, 60_000)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  if (nknTimer) clearInterval(nknTimer)
})
</script>

<template>
  <div v-if="line" ref="root" class="line-status-wrap">
    <LineSelector v-if="lines.length > 1" v-model="viewedLineID" :lines="lines" compact />
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
          <span v-if="carrierName" class="line-status__carrier">{{ carrierName }}</span>
          <span class="line-status__divider" aria-hidden="true" />
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
            :class="{ 'is-unknown': volteState === 'unknown' }"
            :title="volte.hint"
          >VoLTE</span>
          <span
            class="line-status__vowifi"
            :class="{ 'is-on': vowifiOn }"
            :title="vowifiHint"
            :aria-label="vowifiHint"
            role="img"
          >
            <Phone :size="11" :stroke-width="2.6" aria-hidden="true" />
            <svg width="9" height="5" viewBox="0 0 10 6" fill="none" aria-hidden="true">
              <path d="M1 4.8a6.4 6.4 0 0 1 8 0" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
          </span>
          <span class="line-status__divider" aria-hidden="true" />
          <span
            class="line-status__data"
            :class="{ 'is-on': dataOn === true }"
            :title="dataOn === true ? t('homesim.strip.dataOn') : dataOn === false ? t('homesim.strip.dataOff') : t('homesim.strip.dataUnknown')"
            :aria-label="dataOn === true ? t('homesim.strip.dataOn') : dataOn === false ? t('homesim.strip.dataOff') : t('homesim.strip.dataUnknown')"
          >
            <svg width="12" height="9" viewBox="0 0 12 9" aria-hidden="true">
              <path d="M4.2.4 7.4 4.6H1Z" fill="currentColor" />
              <path d="M7.8 8.6 4.6 4.4h6.4Z" fill="currentColor" />
            </svg>
          </span>
          <span v-if="!registered" class="line-status__state">{{ chineseLineState(line.state) }}</span>
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
          <span v-else-if="line.operator && !/^\d+$/.test(line.operator)" class="line-status__geo">{{ line.operator }}</span>
        </div>
      </div>
      <span v-if="version" class="line-status__version">{{ version }}</span>
    </div>

    <!-- NKN network: standalone box beside the status strip -->
    <button
      type="button"
      class="nkn-badge"
      :class="nknDotClass"
      :title="nknStateLabel"
      :aria-label="nknStateLabel"
      :aria-expanded="nknOpen"
      @click.stop="toggleNkn"
      @keydown.enter.prevent="toggleNkn"
      @keydown.space.prevent="toggleNkn"
    >
      <span class="nkn-badge__dot" aria-hidden="true" />
      <span>NKN</span>
      <span class="nkn-badge__state">{{ nknStateLabel }}</span>
    </button>

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

    <!-- NKN network popover: live status, directory stats, node connectivity, own address QR -->
    <div v-if="nknOpen" class="nkn-pop" @click.stop>
      <header>
        <span class="nkn-badge__dot" :class="nknDotClass" aria-hidden="true" />
        <span>{{ t('homesim.nknBadge.title') }}</span>
        <span class="nkn-pop__state">{{ nknStateLabel }}</span>
      </header>
      <p v-if="nknState === 'disabled'" class="nkn-pop__hint">{{ t('homesim.nknBadge.disabledHint') }}</p>
      <p v-else-if="nknState === 'error' && nknStatus?.runtime.last_error" class="nkn-pop__hint nkn-pop__hint--error">{{ nknStatus.runtime.last_error }}</p>
      <div class="nkn-pop__stats">
        <div class="nkn-pop__stat">
          <em>{{ t('homesim.nknBadge.daily') }}</em>
          <strong>{{ nknDetailLoading ? '…' : (nknPeers?.active_24h ?? '—') }}</strong>
        </div>
        <div class="nkn-pop__stat">
          <em>{{ t('homesim.nknBadge.monthly') }}</em>
          <strong>{{ nknDetailLoading ? '…' : (nknPeers?.active_month ?? '—') }}</strong>
        </div>
        <div class="nkn-pop__stat">
          <em>{{ t('homesim.nknBadge.registered') }}</em>
          <strong>{{ nknDetailLoading ? '…' : (nknPeers?.registered_total ?? '—') }}</strong>
        </div>
        <div class="nkn-pop__stat">
          <em>{{ t('homesim.nknBadge.nodes') }}</em>
          <strong>{{ nknDetailLoading ? '…' : (nknNodes ? `${nknNodes.reachable}/${nknNodes.probed}` : '—') }}</strong>
        </div>
      </div>
      <p v-if="nknDetailError" class="nkn-pop__hint nkn-pop__hint--error">{{ nknDetailError }}</p>
      <p v-else-if="!nknDetailLoading && nknNodes && !nknNodes.reachable" class="nkn-pop__hint">{{ t('homesim.nknBadge.noNodes') }}</p>
      <div v-if="nknAddress" class="nkn-pop__address">
        <code :title="nknAddress">{{ nknAddress }}</code>
        <button type="button" class="nkn-pop__copy" @click="copyNknAddress">{{ nknCopied ? t('homesim.nknBadge.copied') : t('homesim.nknBadge.copy') }}</button>
      </div>
      <div v-if="nknQr" class="nkn-pop__qr">
        <img :src="nknQr" :alt="t('homesim.nknBadge.qrAlt')" width="176" height="176" />
        <span>{{ t('homesim.nknBadge.qrHint') }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.line-status-picker { max-width: 140px; min-width: 70px; margin-right: 8px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 6px; }
.line-status__tag.is-unknown { color: var(--muted); background: var(--surface-subtle); }
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

/* Carrier name at the far left, in front of the signal bars; same size as the strip text */
.line-status__carrier {
  flex: 0 0 auto;
  font-weight: 700;
  color: var(--text);
  letter-spacing: 0.02em;
}

/* VoWiFi: handset + wave, sits after the VoLTE tag */
.line-status__vowifi {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: flex-start;
  gap: 1px;
  color: var(--muted);
}

.line-status__vowifi.is-on { color: var(--success); }

.line-status__vowifi svg {
  display: block;
}

/* Mobile data arrows: gray when off/unknown, green and blinking when on */
.line-status__data {
  display: inline-flex;
  flex: 0 0 auto;
  color: var(--muted);
}

.line-status__data svg {
  display: block;
}

.line-status__data.is-on {
  color: var(--success, #34d399);
}

@keyframes line-status-data-blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}

/* Divider separating carrier group from the radio group */
.line-status__divider {
  flex: 0 0 auto;
  align-self: stretch;
  width: 1px;
  background: var(--border);
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
  font-size: 11px;
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
  font-size: 11px;
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

/* NKN badge: standalone box beside the status strip, same surface look */
.nkn-badge {
  display: inline-flex;
  flex: 0 0 auto;
  align-self: stretch;
  align-items: center;
  gap: 6px;
  margin-left: 8px;
  padding: 0 10px;
  font: inherit;
  font-size: 11.5px;
  font-weight: 700;
  color: var(--muted);
  letter-spacing: 0.02em;
  cursor: pointer;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: color-mix(in srgb, var(--surface) 72%, transparent);
  transition: border-color var(--motion-fast) var(--ease-standard);
}

.nkn-badge:hover {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
}

.nkn-badge__state {
  font-size: 10px;
  font-weight: 600;
}

.nkn-badge__dot {
  width: 7px;
  height: 7px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--muted);
}

.nkn-badge.is-green .nkn-badge__dot {
  background: var(--success, #34d399);
  box-shadow: 0 0 4px color-mix(in srgb, var(--success, #34d399) 60%, transparent);
}

.nkn-badge.is-amber .nkn-badge__dot {
  background: var(--warning, #fbbf24);
}

.nkn-badge.is-red .nkn-badge__dot {
  background: var(--danger, #f87171);
}

/* NKN popover */
.nkn-pop {
  position: absolute;
  z-index: var(--layer-popover);
  top: calc(100% + 8px);
  right: 0;
  width: 300px;
  padding: 12px 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  box-shadow: 0 12px 32px rgb(16 24 40 / 16%);
}

.nkn-pop header {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 8px;
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
}

.nkn-pop__state {
  margin-left: auto;
  font-size: 11px;
  font-weight: 600;
  color: var(--muted);
}

.nkn-pop__stats {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 6px;
  margin: 8px 0;
}

.nkn-pop__stat {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding: 6px 9px;
  background: var(--surface-subtle);
  border-radius: 7px;
}

.nkn-pop__stat em {
  font-size: 10.5px;
  font-style: normal;
  color: var(--muted);
}

.nkn-pop__stat strong {
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

.nkn-pop__hint {
  margin: 4px 0 0;
  font-size: 11px;
  line-height: 1.5;
  color: var(--muted);
  word-break: break-all;
}

.nkn-pop__hint--error {
  color: var(--danger);
}

.nkn-pop__address {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.nkn-pop__address code {
  flex: 1 1 auto;
  overflow: hidden;
  padding: 5px 7px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 10.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
  background: var(--surface-subtle);
  border-radius: 6px;
}

.nkn-pop__copy {
  flex: 0 0 auto;
  padding: 4px 9px;
  font-size: 11px;
  color: var(--text);
  cursor: pointer;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
}

.nkn-pop__copy:hover {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
}

.nkn-pop__qr {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  margin-top: 10px;
}

.nkn-pop__qr img {
  display: block;
  padding: 6px;
  background: #fff;
  border-radius: 8px;
}

.nkn-pop__qr span {
  font-size: 10.5px;
  color: var(--muted);
  text-align: center;
}

@media (max-width: 1479px) {
  .line-status__geo,
  .line-status__version,
  .line-status__ring {
    display: none;
  }
  .line-status__row { gap: 5px; }
  .line-status { min-width: 190px; }
}

@media (max-width: 860px) {
  .line-status-wrap {
    display: none;
  }
}
</style>
