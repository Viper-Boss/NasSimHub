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
import { requestConfirmation } from '../state/confirmation'
import { connectData, disconnectData, setRadioEnabled } from '../state/deviceConfiguration'
import { gateway } from '../api/client'
import { getNKNNetworkNodes, getNKNNetworkPeers, getNKNNetworkStatus, getVowifiDiagnostics } from '../api/client'
import type { NKNNetworkNodes, NKNNetworkPeers, NKNNetworkStatus, VowifiDiagnosticsSnapshot, VowifiPathProbe } from '../api/client'
import { getDHTSettings, type DHTStatus } from '../api/client'
import { dhtUnlockState } from '../state/dhtUnlock'
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

// --- DHT indicator ----------------------------------------------------------
// The second control path, shown beside NKN once the operator has asked for it
// by name. The two are separate paths, so one being healthy says nothing about
// the other and they get separate lights.
const dhtStatus = ref<DHTStatus | null>(null)
const dhtState = computed(() => dhtStatus.value?.state || 'unknown')
const dhtDotClass = computed(() => {
  // "ready" is the only state that can carry a lookup. "isolated" means the
  // node is running and has found nobody, which is not an error and is also
  // not working: it gets amber rather than green or red.
  if (dhtState.value === 'ready') return 'is-green'
  if (dhtState.value === 'starting' || dhtState.value === 'isolated') return 'is-amber'
  return 'is-red'
})
const dhtStateLabel = computed(() => {
  switch (dhtState.value) {
    case 'ready': return t('dht.stateReady')
    case 'isolated': return t('dht.stateIsolated')
    case 'starting': return t('dht.stateStarting')
    case 'error': return t('dht.stateError')
    case 'disabled': return t('dht.stateDisabled')
    case 'unavailable': return t('dht.stateUnavailable')
    default: return t('dht.statePending')
  }
})

async function refreshDhtStatus(): Promise<void> {
  if (!dhtUnlockState.unlocked) return
  try {
    dhtStatus.value = (await getDHTSettings()).status
  } catch {
    dhtStatus.value = null
  }
}

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
  const code = value.serving_operator_code || value.home_operator_code || value.operator || ''
  const name = cnOperatorNameOf(code)
  if (name) return name
  if (code && !/^\d+$/.test(code)) return code
  return facts.value.carrier || ''
})

// A null data state means unknown, not disconnected.
const dataOn = ref<boolean | null>(null)
const wifiCapability = ref<boolean | null>(null)
const vowifiWritable = ref<boolean | null>(null)
const dataSwitching = ref(false)
// Flight mode is the inverse of the radio switch. Unknown stays null so the
// control reads as indeterminate rather than claiming the radio is on.
const flightOn = ref<boolean | null>(null)
const flightWritable = ref<boolean | null>(null)
const flightSwitching = ref(false)
const vowifiOn = computed(() => callState.sessions.some(session => session.line_id === line.value?.id && session.phase === 'active' && session.bearer === 'vowifi'))
const vowifiHint = computed(() => t(vowifiOn.value ? 'homesim.strip.vowifiActive' : wifiCapability.value === false ? 'homesim.strip.vowifiUnavailable' : 'homesim.strip.vowifiHint'))
let deviceRequest = 0

watch(() => line.value?.id, () => { void refreshDataState() }, { immediate: true })

async function refreshDataState(): Promise<void> {
  const request = ++deviceRequest
  const value = line.value
  dataOn.value = null
  wifiCapability.value = null
  vowifiWritable.value = null
  flightOn.value = null
  flightWritable.value = null
  if (!value?.id) {
    dataOn.value = null
    return
  }
  try {
    const config = await gateway.getDeviceConfiguration(value.id)
    if (request !== deviceRequest || line.value?.id !== value.id) return
    dataOn.value = (config.hardware?.data_connections || []).some(connection => connection.connected)
    wifiCapability.value = config.hardware?.capabilities.vowifi.implemented ?? null
    vowifiWritable.value = config.hardware?.capabilities.vowifi.writable ?? null
    flightOn.value = config.hardware?.flight_mode_known ? config.hardware.flight_mode : null
    flightWritable.value = Boolean(
      config.hardware?.flight_mode_known &&
      config.hardware.capabilities.flight_mode.writable &&
      config.hardware.capabilities.radio.writable
    )
  } catch {
    if (request === deviceRequest) {
      dataOn.value = null
      wifiCapability.value = null
      vowifiWritable.value = null
      flightOn.value = null
      flightWritable.value = null
    }
  }
}

// Quota popover switches: quick mobile-data connect/disconnect; the VoWiFi
// control stays capability-gated until a driver exposes it.
async function toggleDataSwitch(): Promise<void> {
  const lineID = line.value?.id
  if (!lineID || dataSwitching.value) return
  dataSwitching.value = true
  try {
    if (dataOn.value === true) {
      await disconnectData(lineID)
    } else {
      await connectData(lineID, '', 'ipv4v6')
    }
  } catch { /* fall through to a state refresh */ }
  dataSwitching.value = false
  await refreshDataState()
}

// Turning flight mode ON cuts the line this NAS exists to serve, so it asks
// first — the same confirmation the settings panel uses. Turning it back off
// needs no confirmation.
async function toggleFlightSwitch(event: Event): Promise<void> {
  const control = event.target as HTMLInputElement
  const lineID = line.value?.id
  const wanted = control.checked
  if (!lineID || flightSwitching.value || flightWritable.value !== true) {
    control.checked = flightOn.value === true
    return
  }
  if (wanted && !(await requestConfirmation({
    title: t('device.enableFlightModeTitle'),
    message: t('device.enableFlightModeMessage'),
    confirmLabel: t('device.turnOn')
  }))) {
    control.checked = flightOn.value === true
    return
  }
  flightSwitching.value = true
  try {
    await setRadioEnabled(lineID, !wanted)
  } catch { /* fall through to a state refresh */ }
  flightSwitching.value = false
  await refreshDataState()
}

// VoWiFi honest diagnostics: run the reachability probes when the popover
// opens (cached) and let the user re-run them on demand. A full sweep takes
// up to ~15s, hence the dedicated loading state.
const vowifiDiag = ref<VowifiDiagnosticsSnapshot | null>(null)
const vowifiDiagLoading = ref(false)
const vowifiDiagError = ref('')

async function loadVowifiDiagnostics(): Promise<void> {
  if (vowifiDiagLoading.value) return
  vowifiDiagLoading.value = true
  vowifiDiagError.value = ''
  try {
    vowifiDiag.value = await getVowifiDiagnostics()
  } catch (error) {
    vowifiDiagError.value = error instanceof Error ? error.message : String(error)
  }
  vowifiDiagLoading.value = false
}

function vowifiDiscoveryRow(path: VowifiPathProbe): { text: string; level: string } {
  if (!path.available) {
    return { text: path.error || t('homesim.strip.vowifiDiagUnknown'), level: 'muted' }
  }
  if (path.answer_code === 'NXDOMAIN' || (path.answer_code === 'NOERROR' && (path.addresses?.length ?? 0) === 0)) {
    // NXDOMAIN or an empty NOERROR (NODATA): the carrier publishes nothing
    // under the standard 3GPP discovery name on this path.
    return { text: t('homesim.strip.vowifiDiagDiscoveryNone', { code: path.answer_code }), level: 'bad' }
  }
  if ((path.addresses?.length ?? 0) > 0) {
    return { text: t('homesim.strip.vowifiDiagDiscoveryOk', { count: path.addresses?.length ?? 0 }), level: 'ok' }
  }
  return { text: t('homesim.strip.vowifiDiagDiscoveryTimeout', { code: path.answer_code || '-' }), level: 'warn' }
}

const vowifiDiagRows = computed(() => {
  const diag = vowifiDiag.value
  if (!diag) return []
  const rows: Array<{ label: string; text: string; level: string }> = []
  rows.push({
    label: t('homesim.strip.vowifiDiagFirmware'),
    text: wifiCapability.value === true
      ? t('homesim.strip.vowifiDiagFirmwareOk')
      : t('homesim.strip.vowifiDiagFirmwareMissing'),
    level: wifiCapability.value === true ? 'ok' : 'bad'
  })
  rows.push({ label: t('homesim.strip.vowifiDiagEpdgBroadband'), ...vowifiDiscoveryRow(diag.broadband_path) })
  if (!diag.data_connected) {
    rows.push({ label: t('homesim.strip.vowifiDiagEpdgCellular'), text: t('homesim.strip.vowifiDiagDataOff'), level: 'muted' })
  } else {
    rows.push({ label: t('homesim.strip.vowifiDiagEpdgCellular'), ...vowifiDiscoveryRow(diag.cellular_path) })
  }
  const ike = diag.broadband_path.ike.attempted ? diag.broadband_path.ike : diag.cellular_path.ike
  rows.push({
    label: t('homesim.strip.vowifiDiagIke'),
    text: !ike.attempted
      ? t('homesim.strip.vowifiDiagIkeSkipped')
      : ike.reachable
        ? t('homesim.strip.vowifiDiagIkeOk')
        : t('homesim.strip.vowifiDiagIkeNone'),
    level: !ike.attempted ? 'muted' : ike.reachable ? 'ok' : 'warn'
  })
  rows.push({
    label: t('homesim.strip.vowifiDiagEgress'),
    text: diag.egress.available
      ? (diag.egress.isp || diag.egress.public_ip || t('homesim.strip.vowifiDiagUnknown'))
      : t('homesim.strip.vowifiDiagEgressUnknown'),
    level: 'muted'
  })
  return rows
})

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
const RING_DISC = 2 * Math.PI * 24
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
  if (open.value && !vowifiDiag.value && !vowifiDiagLoading.value) {
    void loadVowifiDiagnostics()
  }
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
  void refreshDhtStatus()
  nknTimer = setInterval(() => {
    void refreshNknStatus()
    void refreshDhtStatus()
    void refreshDataState()
  }, 60_000)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  if (nknTimer) clearInterval(nknTimer)
})
</script>

<template>
  <div ref="root" class="line-status-wrap">
    <template v-if="line">
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
          <span class="line-status__number">{{ facts.formatted || t('calls.unknownNumber') }}</span>
          <span v-if="geoLine" class="line-status__geo" :title="geoLine">{{ geoLine }}</span>
          <span v-else-if="line.operator && !/^\d+$/.test(line.operator)" class="line-status__geo">{{ line.operator }}</span>
        </div>
      </div>
      <span v-if="version" class="line-status__version">{{ version }}</span>
    </div>
    </template>

    <!-- No module attached. The strip stays, greyed and flat-lined, rather than
         vanishing: removing it left the NKN badge alone in the flex row, which
         made the badge visibly shrink and read as a rendering fault. A flat line
         where the signal bars were says "nothing is being measured" instead. -->
    <div v-else class="line-status is-vacant" role="status" :aria-label="t('shell.noModuleStatus')">
      <span class="line-status__carrier">{{ t('shell.noModuleStatus') }}</span>
      <span class="line-status__divider" aria-hidden="true" />
      <span class="line-status__flatline" aria-hidden="true" />
      <span v-if="version" class="line-status__version">{{ version }}</span>
    </div>

    <!-- NKN network: standalone box beside the status strip, independent of any line -->
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

    <!-- DHT: the second control path, hidden until asked for by name -->
    <RouterLink
      v-if="dhtUnlockState.unlocked"
      class="nkn-badge is-dht"
      :class="dhtDotClass"
      :to="{ name: 'settings', params: { section: 'dht' } }"
      :title="dhtStateLabel"
      :aria-label="dhtStateLabel"
    >
      <span class="nkn-badge__dot" aria-hidden="true" />
      <span>DHT</span>
      <span class="nkn-badge__state">{{ dhtStateLabel }}</span>
    </RouterLink>

    <!-- Quota popover: one quota block, the line switches, then diagnostics -->
    <div v-if="open" class="line-status-pop">
      <header class="line-status-pop__head">
        <span class="line-status-pop__title">{{ t('homesim.quota.title') }}</span>
        <span v-if="facts.formatted" class="line-status-pop__number">{{ facts.formatted || t('calls.unknownNumber') }}</span>
      </header>

      <div v-if="quotaRings.length" class="line-status-pop__quota">
        <div
          v-for="ring in quotaRings"
          :key="ring.label"
          class="line-status-pop__meter"
          :class="{ 'is-low': ring.low }"
          :title="ring.hint"
        >
          <span class="line-status-pop__ring-disc">
            <svg width="60" height="60" viewBox="0 0 56 56" aria-hidden="true">
              <circle cx="28" cy="28" r="24" fill="none" stroke="var(--border)" stroke-width="4" />
              <circle
                cx="28"
                cy="28"
                r="24"
                fill="none"
                :stroke="ring.low ? 'var(--danger)' : ring.color"
                stroke-width="4"
                stroke-linecap="round"
                :stroke-dasharray="RING_DISC"
                :stroke-dashoffset="RING_DISC - RING_DISC * ring.pct"
                transform="rotate(-90 28 28)"
              />
            </svg>
            <em>{{ Math.round(ring.pct * 100) }}<i>%</i></em>
          </span>
          <span class="line-status-pop__meter-label">{{ ring.label }}</span>
          <span class="line-status-pop__meter-left">{{ ring.leftText }}</span>
          <span class="line-status-pop__meter-total">/ {{ ring.totalText }}</span>
        </div>
      </div>
      <p v-else class="line-status-pop__empty">{{ t('homesim.quotaEmpty') }}</p>

      <div class="line-status-pop__switches">
        <label class="line-status-pop__switch-row">
          <span class="line-status-pop__switch-name">{{ t('homesim.strip.dataSwitch') }}</span>
          <input
            class="ui-switch"
            type="checkbox"
            role="switch"
            :checked="dataOn === true"
            :indeterminate="dataOn === null"
            :disabled="dataSwitching"
            @change="toggleDataSwitch"
          />
        </label>
        <label class="line-status-pop__switch-row">
          <span class="line-status-pop__switch-name" :title="vowifiHint">{{ t('homesim.strip.vowifiSwitch') }}</span>
          <input
            class="ui-switch"
            type="checkbox"
            role="switch"
            :checked="vowifiOn"
            :disabled="vowifiWritable !== true"
            :title="vowifiHint"
          />
        </label>
        <p v-if="vowifiWritable !== true" class="line-status-pop__switch-hint">{{ t('homesim.strip.vowifiUnavailable') }}</p>
        <label class="line-status-pop__switch-row line-status-pop__switch-row--danger">
          <span class="line-status-pop__switch-name">{{ t('device.flightMode') }}</span>
          <input
            class="ui-switch"
            type="checkbox"
            role="switch"
            :checked="flightOn === true"
            :indeterminate="flightOn === null"
            :disabled="flightSwitching || flightWritable !== true"
            @change="toggleFlightSwitch"
          />
        </label>
        <p v-if="flightWritable !== true" class="line-status-pop__switch-hint">{{ t('device.notWritable') }}</p>
      </div>

      <div class="line-status-pop__vowifi-diag">
        <div class="vowifi-diag__head">
          <span>{{ t('homesim.strip.vowifiDiagTitle') }}</span>
          <button
            type="button"
            class="vowifi-diag__refresh"
            :disabled="vowifiDiagLoading"
            @click="loadVowifiDiagnostics"
          >{{ vowifiDiagLoading ? t('homesim.strip.vowifiDiagProbing') : t('homesim.strip.vowifiDiagRecheck') }}</button>
        </div>
        <p v-if="vowifiDiagError" class="vowifi-diag__error">{{ vowifiDiagError }}</p>
        <div v-else class="vowifi-diag__rows">
          <div v-for="row in vowifiDiagRows" :key="row.label" class="vowifi-diag__row">
            <span class="vowifi-diag__dot" :class="`is-${row.level}`" aria-hidden="true" />
            <span class="vowifi-diag__label">{{ row.label }}</span>
            <span class="vowifi-diag__value">{{ vowifiDiagLoading && !vowifiDiag ? '…' : row.text }}</span>
          </div>
        </div>
      </div>
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
          <strong>{{ nknDetailLoading ? '…' : (nknPeers?.active_90d ?? '—') }}</strong>
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
.line-status-picker { max-width: 140px; min-width: 70px; margin-right: 8px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); padding: 6px; }
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
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--surface) 72%, transparent);
  transition: border-color var(--motion-fast) var(--ease-standard);
}

/* The vacant strip is not interactive: there is nothing to open. */
.line-status.is-vacant {
  cursor: default;
  opacity: 0.62;
}

/* A flat line occupying the same width the signal bars had, so the header does
   not change size when a module is pulled. */
.line-status__flatline {
  display: block;
  width: 26px;
  height: 1px;
  background: currentColor;
  opacity: 0.75;
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
  border-radius: var(--radius-xs) 1px 0 0;
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
  border-radius: var(--radius-xs);
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
  display: grid;
  width: 328px;
  gap: 12px;
  padding: 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  box-shadow: var(--elevation-card, 0 12px 32px rgb(16 24 40 / 16%));
}

.line-status-pop__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
}

.line-status-pop__title {
  color: var(--text);
  font-size: 12.5px;
  font-weight: 700;
}

.line-status-pop__number {
  color: var(--faint);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

/* One quota block: a dial per allowance, remaining above the total. The panel
   used to render the same three figures twice — rings here and bars below. */
.line-status-pop__quota {
  display: grid;
  gap: 6px;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: 12px 4px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.line-status-pop__meter {
  display: grid;
  gap: 2px;
  justify-items: center;
  min-width: 0;
  text-align: center;
}

.line-status-pop__ring-disc {
  position: relative;
  display: inline-grid;
  place-items: center;
  margin-bottom: 4px;
}

.line-status-pop__ring-disc svg {
  display: block;
}

.line-status-pop__ring-disc em {
  position: absolute;
  color: var(--text);
  font-size: 14px;
  font-style: normal;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.line-status-pop__ring-disc em i {
  font-size: 9px;
  font-style: normal;
  font-weight: 600;
  opacity: 0.65;
}

.line-status-pop__meter-label {
  color: var(--muted);
  font-size: 11px;
}

.line-status-pop__meter-left {
  color: var(--text);
  font-size: 12px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.line-status-pop__meter-total {
  color: var(--faint);
  font-size: 10.5px;
  font-variant-numeric: tabular-nums;
}

.line-status-pop__meter.is-low .line-status-pop__meter-left { color: var(--danger); }

.line-status-pop__empty {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}

/* Popover remaining-quota rings: one disc per quota, percentage inside */
.line-status-pop__ring-disc {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.line-status-pop__ring-disc svg {
  display: block;
}

.line-status-pop__ring-disc em {
  position: absolute;
  font-size: 12px;
  font-style: normal;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

/* Quota popover quick switches */
.line-status-pop__switches {
  display: flex;
  flex-direction: column;
  padding: 2px 10px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

/* Rows are separated by hairlines rather than gaps, so the group reads as one
   list of controls instead of three floating pills. */
.line-status-pop__switch-row {
  display: flex;
  min-height: 38px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  color: var(--text);
  font-size: 12px;
  font-weight: 650;
  cursor: pointer;
}

.line-status-pop__switch-row + .line-status-pop__switch-row,
.line-status-pop__switch-hint + .line-status-pop__switch-row {
  border-top: 1px solid var(--border);
}

.line-status-pop__switch-row--danger .line-status-pop__switch-name { color: var(--warning, var(--text)); }

.line-status-pop__switch-row input:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.line-status-pop__switch-hint {
  margin: 0 0 8px;
  color: var(--faint);
  font-size: 10.5px;
  line-height: 1.45;
}

/* VoWiFi honest-diagnostics panel inside the quota popover */
.line-status-pop__vowifi-diag {
  padding: 10px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.vowifi-diag__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 11.5px;
  font-weight: 650;
  color: var(--text);
}

.vowifi-diag__refresh {
  padding: 2px 8px;
  font-size: 10.5px;
  color: var(--text);
  cursor: pointer;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: transparent;
}

.vowifi-diag__refresh:disabled {
  opacity: 0.5;
  cursor: wait;
}

.vowifi-diag__refresh:not(:disabled):hover {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
}

.vowifi-diag__error {
  margin: 6px 0 0;
  font-size: 10.5px;
  line-height: 1.45;
  color: var(--danger);
  word-break: break-all;
}

.vowifi-diag__rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 6px;
}

.vowifi-diag__row {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 10.5px;
  line-height: 1.45;
}

.vowifi-diag__dot {
  flex: 0 0 auto;
  align-self: center;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--muted);
}

.vowifi-diag__dot.is-ok { background: var(--success, #34d399); }
.vowifi-diag__dot.is-warn { background: var(--warning, #fbbf24); }
.vowifi-diag__dot.is-bad { background: var(--danger, #f87171); }
.vowifi-diag__dot.is-muted { background: var(--muted); }

.vowifi-diag__label {
  flex: 0 0 auto;
  color: var(--muted);
}

.vowifi-diag__value {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
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
  border-radius: var(--radius-control);
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

.nkn-badge.is-green .nkn-badge__dot,
.nkn-badge__dot.is-green {
  background: var(--success, #34d399);
  box-shadow: 0 0 4px color-mix(in srgb, var(--success, #34d399) 60%, transparent);
}

.nkn-badge.is-amber .nkn-badge__dot,
.nkn-badge__dot.is-amber {
  background: var(--warning, #fbbf24);
}

.nkn-badge.is-red .nkn-badge__dot,
.nkn-badge__dot.is-red {
  background: var(--danger, #f87171);
}

/* The DHT light borrows the NKN badge's shape so the two read as a pair, and
   its own hue so they are never mistaken for one path reported twice. */
.nkn-badge.is-dht {
  color: var(--muted);
  text-decoration: none;
}

.nkn-badge.is-dht:hover {
  border-color: color-mix(in srgb, var(--dht-hue, #b07cff) 45%, transparent);
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
  border-radius: var(--radius-control);
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
  border-radius: var(--radius-md);
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
  border-radius: var(--radius-sm);
}

.nkn-pop__copy {
  flex: 0 0 auto;
  padding: 4px 9px;
  font-size: 11px;
  color: var(--text);
  cursor: pointer;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
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
  border-radius: var(--radius-md);
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
