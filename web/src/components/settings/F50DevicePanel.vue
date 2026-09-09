<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowDown, ArrowUp, BatteryCharging, Copy, Check, Cpu, Gauge,
  LoaderCircle, Lock, MemoryStick, Phone, RefreshCw, Signal, Thermometer,
  Wifi, WifiOff
} from '@lucide/vue'
import {
  getF50SMSList,
  getF50Status,
  getHomeSIMF50Settings,
  markF50SMSRead,
  saveHomeSIMF50Settings,
  sendF50SMS,
  type HomeSIMF50Message,
  type HomeSIMF50Settings,
  type HomeSIMF50Status
} from '../../api/client'

const { t } = useI18n()

// Connection settings
const settings = ref<HomeSIMF50Settings | null>(null)
const settingsLoading = ref(true)
const saving = ref(false)
const settingsError = ref('')
const settingsSaved = ref(false)
const password = ref('')
const passwordSet = ref(false)

// Live status
const status = ref<HomeSIMF50Status | null>(null)
const statusLoading = ref(false)
const statusError = ref('')

// SMS
const messages = ref<HomeSIMF50Message[]>([])
const smsLoading = ref(false)
const smsError = ref('')
const sendNumber = ref('')
const sendContent = ref('')
const sending = ref(false)
const sendFeedback = ref('')
const copiedMessageId = ref('')

async function loadSettings(): Promise<void> {
  settingsLoading.value = true
  settingsError.value = ''
  try {
    const result = await getHomeSIMF50Settings()
    settings.value = result.settings
    passwordSet.value = result.passwordSet
  } catch (error) {
    settingsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    settingsLoading.value = false
  }
}

async function saveSettings(): Promise<void> {
  if (!settings.value || saving.value) return
  saving.value = true
  settingsError.value = ''
  settingsSaved.value = false
  try {
    const payload: HomeSIMF50Settings = { ...settings.value }
    if (password.value.trim()) payload.password = password.value
    const result = await saveHomeSIMF50Settings(payload)
    settings.value = result.settings
    if (password.value.trim()) {
      passwordSet.value = true
      password.value = ''
    }
    settingsSaved.value = true
    await refreshStatus()
  } catch (error) {
    settingsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    saving.value = false
  }
}

async function refreshStatus(): Promise<void> {
  statusLoading.value = true
  statusError.value = ''
  try {
    status.value = await getF50Status()
  } catch (error) {
    statusError.value = error instanceof Error ? error.message : String(error)
  } finally {
    statusLoading.value = false
  }
}

async function refreshSMS(): Promise<void> {
  smsLoading.value = true
  smsError.value = ''
  try {
    messages.value = await getF50SMSList(0)
  } catch (error) {
    smsError.value = error instanceof Error ? error.message : String(error)
  } finally {
    smsLoading.value = false
  }
}

async function markRead(message: HomeSIMF50Message): Promise<void> {
  try {
    await markF50SMSRead([message.ID])
    message.Tag = '1'
  } catch (error) {
    smsError.value = error instanceof Error ? error.message : String(error)
  }
}

async function send(): Promise<void> {
  if (sending.value) return
  sending.value = true
  sendFeedback.value = ''
  try {
    await sendF50SMS(sendNumber.value.trim(), sendContent.value.trim())
    sendFeedback.value = t('homesim.f50.sendDone')
    sendContent.value = ''
    await refreshSMS()
  } catch (error) {
    sendFeedback.value = error instanceof Error ? error.message : String(error)
  } finally {
    sending.value = false
  }
}

async function copyCode(message: HomeSIMF50Message): Promise<void> {
  const code = extractCode(message.Content)
  if (!code) return
  try {
    await navigator.clipboard.writeText(code)
    copiedMessageId.value = message.ID
    window.setTimeout(() => { if (copiedMessageId.value === message.ID) copiedMessageId.value = '' }, 2000)
  } catch {
    // clipboard unavailable: ignore
  }
}

function formatBytes(raw: string): string {
  const value = Number(raw)
  if (!Number.isFinite(value) || value <= 0) return '--'
  const gb = value / (1024 * 1024 * 1024)
  if (gb >= 1) return gb.toFixed(2) + ' GB'
  const mb = value / (1024 * 1024)
  if (mb >= 1) return mb.toFixed(1) + ' MB'
  return (value / 1024).toFixed(0) + ' KB'
}

function formatSMSDate(raw: string): string {
  const parts = raw.split(';')
  if (parts.length < 6) return raw
  return `20${parts[0]}-${parts[1]}-${parts[2]} ${parts[3]}:${parts[4]}`
}

function extractCode(content: string): string {
  const match = content.match(/(?<![\d])(\d{4,8})(?![\d])/)
  return match?.[1] ?? ''
}

function parseDb(raw: string): number {
  const value = Number.parseFloat(raw)
  return Number.isFinite(value) ? value : Number.NaN
}

type Quality = 'excellent' | 'good' | 'fair' | 'weak'

function rateQuality(raw: string, good: number, ok: number, poor: number): Quality {
  const value = parseDb(raw)
  if (Number.isNaN(value)) return 'weak'
  if (value >= good) return 'excellent'
  if (value >= ok) return 'good'
  if (value >= poor) return 'fair'
  return 'weak'
}

const rsrpQuality = computed<Quality>(() => rateQuality(status.value?.RSRP ?? '', -85, -95, -105))
const rsrqQuality = computed<Quality>(() => rateQuality(status.value?.RSRQ ?? '', -10, -15, -20))
const snrQuality = computed<Quality>(() => rateQuality(status.value?.SNR ?? '', 20, 10, 3))

function qualityLabel(quality: Quality): string {
  return t(`homesim.f50.quality_${quality}`)
}

const signalBars = computed(() => {
  const level = Math.min(5, Math.max(0, Number.parseInt(status.value?.Signalbar ?? '0', 10) || 0))
  return Array.from({ length: 5 }, (_, index) => index < level)
})

const combinedBands = computed(() => {
  const parts: string[] = []
  if (status.value?.NRBand) parts.push('n' + status.value.NRBand.replace(/^n/, ''))
  if (status.value?.LTEBand) parts.push('B' + status.value.LTEBand.replace(/^B/i, ''))
  if (!parts.length && status.value?.Bands) parts.push(status.value.Bands)
  return parts.join(' + ')
})

const monthlyUsage = computed(() => {
  if (!status.value) return null
  return {
    down: formatBytes(status.value.MonthlyRxBytes),
    up: formatBytes(status.value.MonthlyTxBytes)
  }
})

const dataLimit = computed(() => {
  const raw = status.value?.DataLimitSize
  const size = Number(raw)
  if (!raw || !Number.isFinite(size) || size <= 0) return null
  const unit = (status.value?.DataLimitUnit || 'GB').toUpperCase()
  const limitBytes = unit.startsWith('G') ? size * 1024 ** 3 : unit.startsWith('M') ? size * 1024 ** 2 : size
  const used = (Number(status.value?.MonthlyRxBytes) || 0) + (Number(status.value?.MonthlyTxBytes) || 0)
  return { label: `${size} ${unit}`, percent: Math.min(100, Math.round((used / limitBytes) * 100)) }
})

const resetCountdown = computed(() => {
  const billingDay = Number.parseInt(status.value?.BillingDay ?? '', 10)
  if (!Number.isFinite(billingDay) || billingDay < 1 || billingDay > 31) return null
  const now = new Date()
  const today = now.getDate()
  const daysInMonth = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate()
  const days = billingDay > today ? billingDay - today : daysInMonth - today + billingDay
  return days
})

const firmware = computed(() => status.value?.FirmwareWA || status.value?.FirmwareCR || '')

let pollTimer: ReturnType<typeof setInterval> | undefined

onMounted(async () => {
  await Promise.all([loadSettings(), refreshStatus(), refreshSMS()])
  pollTimer = setInterval(() => { void refreshStatus() }, 15000)
})

onBeforeUnmount(() => {
  clearInterval(pollTimer)
})
</script>

<template>
  <section class="f50-panel">
    <!-- Live status hero -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Signal :size="17" />
        <h3>{{ t('homesim.f50.statusTitle') }}</h3>
        <button class="f50-refresh" type="button" :disabled="statusLoading" @click="refreshStatus">
          <RefreshCw class="f50-refresh__icon" :class="{ spin: statusLoading }" :size="14" />
          {{ t('common.refresh') }}
        </button>
      </header>

      <div v-if="statusLoading && !status" class="f50-state" role="status">
        <LoaderCircle class="spin" :size="18" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="statusError" class="f50-offline" role="alert">
        <WifiOff :size="18" />
        <p>{{ statusError }}</p>
      </div>
      <template v-else-if="status">
        <!-- Hero -->
        <div class="f50-hero">
          <div class="f50-hero__main">
            <div class="f50-signalbars" aria-hidden="true">
              <span
                v-for="(active, index) in signalBars"
                :key="index"
                class="f50-signalbars__bar"
                :class="{ 'is-active': active, [`is-level-${index}`]: true }"
              ></span>
            </div>
            <div class="f50-hero__title">
              <strong class="f50-hero__network">{{ status.NetworkType || '--' }}</strong>
              <span class="f50-hero__carrier">{{ status.Provider || '--' }}</span>
            </div>
          </div>
          <div class="f50-hero__rates">
            <div class="f50-rate">
              <ArrowDown :size="15" />
              <div>
                <small>{{ t('homesim.f50.downlink') }}</small>
                <strong>{{ status.RealtimeRxThrpt || '--' }}</strong>
              </div>
            </div>
            <div class="f50-rate">
              <ArrowUp :size="15" />
              <div>
                <small>{{ t('homesim.f50.uplink') }}</small>
                <strong>{{ status.RealtimeTxThrpt || '--' }}</strong>
              </div>
            </div>
          </div>
        </div>

        <!-- Quality ratings -->
        <div class="f50-quality">
          <div class="f50-quality__item">
            <span class="f50-quality__dot" :class="`is-${rsrpQuality}`"></span>
            <div>
              <small>RSRP <em>{{ status.RSRP || '--' }} dBm</em></small>
              <strong>{{ qualityLabel(rsrpQuality) }}</strong>
            </div>
          </div>
          <div class="f50-quality__item">
            <span class="f50-quality__dot" :class="`is-${rsrqQuality}`"></span>
            <div>
              <small>RSRQ <em>{{ status.RSRQ || '--' }} dB</em></small>
              <strong>{{ qualityLabel(rsrqQuality) }}</strong>
            </div>
          </div>
          <div class="f50-quality__item">
            <span class="f50-quality__dot" :class="`is-${snrQuality}`"></span>
            <div>
              <small>SNR <em>{{ status.SNR || '--' }} dB</em></small>
              <strong>{{ qualityLabel(snrQuality) }}</strong>
            </div>
          </div>
          <div class="f50-quality__item">
            <span class="f50-quality__band">{{ combinedBands || '--' }}</span>
            <div><small>{{ t('homesim.f50.band') }}</small></div>
          </div>
        </div>

        <!-- Traffic -->
        <div class="f50-traffic">
          <div class="f50-traffic__usage">
            <Gauge :size="16" />
            <div class="f50-traffic__numbers">
              <span><ArrowDown :size="12" /> {{ monthlyUsage?.down ?? '--' }}</span>
              <span><ArrowUp :size="12" /> {{ monthlyUsage?.up ?? '--' }}</span>
            </div>
            <small>{{ t('homesim.f50.monthlyTraffic') }}</small>
          </div>
          <div v-if="dataLimit" class="f50-traffic__bar" role="img" :aria-label="`${dataLimit.percent}%`">
            <div class="f50-traffic__bar-fill" :style="{ width: dataLimit.percent + '%' }"></div>
          </div>
          <div class="f50-traffic__meta">
            <span v-if="dataLimit">{{ t('homesim.f50.dataLimit') }} {{ dataLimit.label }}</span>
            <span v-if="resetCountdown !== null">{{ t('homesim.f50.resetIn', { days: resetCountdown ?? 0 }) }}</span>
          </div>
        </div>

        <!-- Hardware tiles -->
        <div class="f50-metrics">
          <div class="f50-metric">
            <small><Thermometer :size="11" /> {{ t('homesim.f50.temperature') }}</small>
            <strong>{{ status.Temperature || '--' }}°C</strong>
          </div>
          <div class="f50-metric">
            <small><Cpu :size="11" /> SoC</small>
            <strong>{{ status.CPUTemp || '--' }}°C</strong>
          </div>
          <div class="f50-metric">
            <small><Cpu :size="11" /> CPU</small>
            <strong>{{ status.CPUUtility || '--' }}%</strong>
          </div>
          <div class="f50-metric">
            <small><MemoryStick :size="11" /> {{ t('homesim.f50.memory') }}</small>
            <strong>{{ status.MemUtility || '--' }}%</strong>
          </div>
          <div class="f50-metric">
            <small><Wifi :size="11" /> {{ t('homesim.f50.wifiClients') }}</small>
            <strong>{{ status.WifiStaNum || '0' }}</strong>
          </div>
          <div v-if="firmware" class="f50-metric">
            <small><BatteryCharging :size="11" /> {{ t('homesim.f50.firmware') }}</small>
            <strong class="f50-metric__fw">{{ firmware }}</strong>
          </div>
        </div>
      </template>
    </div>

    <!-- Messages -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Phone :size="17" />
        <h3>{{ t('homesim.f50.smsTitle') }}</h3>
        <span v-if="status?.SMSUnread && Number(status.SMSUnread) > 0" class="f50-unread-badge">{{ status.SMSUnread }}</span>
        <button class="f50-refresh" type="button" :disabled="smsLoading" @click="refreshSMS">
          <RefreshCw class="f50-refresh__icon" :class="{ spin: smsLoading }" :size="14" />
          {{ t('common.refresh') }}
        </button>
      </header>

      <div class="f50-send">
        <label class="field">
          <span>{{ t('homesim.f50.toNumber') }}</span>
          <input v-model="sendNumber" type="text" autocomplete="off" :placeholder="t('homesim.f50.toNumberPlaceholder')" />
        </label>
        <label class="field">
          <span>{{ t('homesim.f50.message') }}</span>
          <textarea v-model="sendContent" rows="2" :placeholder="t('homesim.f50.messagePlaceholder')"></textarea>
        </label>
        <div class="f50-actions">
          <button class="primary-button" type="button" :disabled="sending || !sendNumber || !sendContent" @click="send">
            <LoaderCircle v-if="sending" class="spin" :size="16" />
            {{ t('homesim.f50.send') }}
          </button>
          <small v-if="sendFeedback" class="f50-send-feedback">{{ sendFeedback }}</small>
        </div>
      </div>

      <div v-if="smsLoading" class="f50-state" role="status">
        <LoaderCircle class="spin" :size="18" />
        {{ t('common.loading') }}
      </div>
      <p v-else-if="smsError" class="field-error" role="alert">{{ smsError }}</p>
      <div v-else class="f50-sms-list">
        <div
          v-for="message in messages"
          :key="message.ID"
          class="f50-sms"
          :class="{ 'is-unread': message.Tag === '0' }"
        >
          <div class="f50-sms__meta">
            <span class="f50-sms__number">
              <span v-if="message.Tag === '0'" class="f50-sms__dot" aria-hidden="true"></span>
              {{ message.Number }}
            </span>
            <span class="f50-sms__date">{{ formatSMSDate(message.Date) }}</span>
          </div>
          <div class="f50-sms__content">{{ message.Content }}</div>
          <div v-if="message.Tag === '0' || extractCode(message.Content)" class="f50-sms__actions">
            <button
              v-if="extractCode(message.Content)"
              type="button"
              class="f50-chip"
              @click="copyCode(message)"
            >
              <Check v-if="copiedMessageId === message.ID" :size="12" />
              <Copy v-else :size="12" />
              {{ copiedMessageId === message.ID ? t('homesim.f50.codeCopied') : t('homesim.f50.copyCode') }}
            </button>
            <button
              v-if="message.Tag === '0'"
              type="button"
              class="f50-chip f50-chip--ghost"
              @click="markRead(message)"
            >
              {{ t('homesim.f50.markRead') }}
            </button>
          </div>
        </div>
        <p v-if="!messages.length" class="f50-state">{{ t('homesim.f50.noMessages') }}</p>
      </div>
    </div>

    <!-- Connection settings -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Lock :size="16" />
        <h3>{{ t('homesim.f50.connection') }}</h3>
      </header>
      <div v-if="settingsLoading" class="f50-state" role="status">
        <LoaderCircle class="spin" :size="18" />
        {{ t('common.loading') }}
      </div>
      <p v-else-if="settingsError && !settings" class="field-error" role="alert">{{ settingsError }}</p>
      <template v-else-if="settings">
        <div class="f50-form">
          <label class="field">
            <span>{{ t('homesim.f50.baseUrl') }}</span>
            <input v-model="settings.base_url" type="url" autocomplete="off" :disabled="saving" />
          </label>
          <label class="field">
            <span>{{ t('homesim.f50.username') }}</span>
            <input v-model="settings.username" type="text" autocomplete="off" :disabled="saving" />
          </label>
          <label class="field">
            <span>{{ t('homesim.f50.password') }}</span>
            <input
              v-model="password"
              type="password"
              autocomplete="new-password"
              :disabled="saving"
              :placeholder="passwordSet ? t('homesim.f50.passwordSetPlaceholder') : t('homesim.f50.passwordPlaceholder')"
            />
            <small class="field-status">{{ t('homesim.f50.passwordHint') }}</small>
          </label>
        </div>
        <p v-if="settingsError" class="field-error" role="alert">{{ settingsError }}</p>
        <p v-else-if="settingsSaved" class="f50-saved" role="status">{{ t('homesim.f50.settingsSaved') }}</p>
        <div class="f50-actions">
          <button class="primary-button" type="button" :disabled="saving" @click="saveSettings">
            <LoaderCircle v-if="saving" class="spin" :size="16" />
            {{ t('homesim.f50.save') }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.f50-panel {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.f50-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: color-mix(in srgb, var(--surface) 60%, transparent);
}

.f50-section__heading {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
}

.f50-section__heading h3 {
  margin: 0;
  font-size: 14px;
}

.f50-refresh {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  padding: 0 10px;
  min-height: 28px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: transparent;
  color: var(--muted);
  font-size: 12px;
  cursor: pointer;
  transition: color var(--motion-fast) var(--ease-standard), border-color var(--motion-fast) var(--ease-standard);
}

.f50-refresh:hover {
  color: var(--text);
  border-color: color-mix(in srgb, var(--accent) 45%, var(--border));
}

.f50-state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}

/* Hero */
.f50-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding: 18px;
  border-radius: 14px;
  background:
    radial-gradient(120% 160% at 0% 0%, color-mix(in srgb, var(--accent) 22%, transparent), transparent 60%),
    radial-gradient(120% 160% at 100% 100%, color-mix(in srgb, var(--accent) 12%, transparent), transparent 55%),
    var(--surface-subtle);
  border: 1px solid color-mix(in srgb, var(--accent) 22%, var(--border));
}

.f50-hero__main {
  display: flex;
  align-items: center;
  gap: 14px;
}

.f50-signalbars {
  display: flex;
  align-items: flex-end;
  gap: 4px;
  height: 34px;
}

.f50-signalbars__bar {
  width: 7px;
  border-radius: 3px;
  background: color-mix(in srgb, var(--text) 14%, transparent);
}

.f50-signalbars__bar.is-level-0 { height: 30%; }
.f50-signalbars__bar.is-level-1 { height: 48%; }
.f50-signalbars__bar.is-level-2 { height: 66%; }
.f50-signalbars__bar.is-level-3 { height: 82%; }
.f50-signalbars__bar.is-level-4 { height: 100%; }

.f50-signalbars__bar.is-active.is-level-0,
.f50-signalbars__bar.is-active.is-level-1 { background: color-mix(in srgb, var(--danger, #e5484d) 75%, var(--accent)); }
.f50-signalbars__bar.is-active.is-level-2 { background: color-mix(in srgb, var(--warning, #f5a524) 80%, var(--accent)); }
.f50-signalbars__bar.is-active.is-level-3,
.f50-signalbars__bar.is-active.is-level-4 { background: var(--accent); }

.f50-hero__title {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.f50-hero__network {
  font-size: 24px;
  letter-spacing: 0.5px;
  color: var(--text);
}

.f50-hero__carrier {
  color: var(--muted);
  font-size: 12.5px;
}

.f50-hero__rates {
  display: flex;
  gap: 18px;
}

.f50-rate {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--accent);
}

.f50-rate small {
  display: block;
  color: var(--muted);
  font-size: 10.5px;
}

.f50-rate strong {
  color: var(--text);
  font-size: 14px;
  font-family: var(--font-mono, ui-monospace, monospace);
}

/* Quality */
.f50-quality {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
}

.f50-quality__item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface-subtle);
}

.f50-quality__dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex-shrink: 0;
}

.f50-quality__dot.is-excellent { background: #30a46c; box-shadow: 0 0 8px color-mix(in srgb, #30a46c 60%, transparent); }
.f50-quality__dot.is-good { background: #6cbd45; }
.f50-quality__dot.is-fair { background: #f5a524; }
.f50-quality__dot.is-weak { background: #e5484d; }

.f50-quality__item small {
  display: block;
  color: var(--muted);
  font-size: 10.5px;
}

.f50-quality__item small em {
  font-style: normal;
  color: var(--text);
  font-family: var(--font-mono, ui-monospace, monospace);
}

.f50-quality__item strong {
  font-size: 12px;
  color: var(--text);
}

.f50-quality__band {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
  font-family: var(--font-mono, ui-monospace, monospace);
}

/* Traffic */
.f50-traffic {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface-subtle);
}

.f50-traffic__usage {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--accent);
}

.f50-traffic__numbers {
  display: flex;
  gap: 14px;
}

.f50-traffic__numbers span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--text);
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 13.5px;
  font-weight: 700;
}

.f50-traffic__usage small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}

.f50-traffic__bar {
  height: 7px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--text) 10%, transparent);
  overflow: hidden;
}

.f50-traffic__bar-fill {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, var(--accent), color-mix(in srgb, var(--accent) 55%, #e5484d));
  transition: width var(--motion-normal, 0.3s) var(--ease-standard);
}

.f50-traffic__meta {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  color: var(--muted);
  font-size: 11px;
}

/* Hardware tiles */
.f50-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 10px;
}

.f50-metric {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.f50-metric small {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--muted);
  font-size: 10.5px;
}

.f50-metric strong {
  font-size: 14px;
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--text);
}

.f50-metric__fw {
  font-size: 10.5px;
  word-break: break-all;
}

/* Unread badge */
.f50-unread-badge {
  min-width: 18px;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  font-weight: 700;
  text-align: center;
}

/* Send form */
.f50-send {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.f50-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.f50-actions .primary-button {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 7px;
  padding: 0 14px;
  font-size: 12.5px;
}

.f50-send-feedback {
  color: var(--muted);
  font-size: 11.5px;
}

/* SMS list */
.f50-sms-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.f50-sms {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 10px 12px;
  background: var(--surface-subtle);
  border: 1px solid transparent;
  border-radius: 12px;
  transition: border-color var(--motion-fast) var(--ease-standard);
}

.f50-sms.is-unread {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
}

.f50-sms__meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.f50-sms__number {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 700;
  font-size: 13px;
  color: var(--text);
}

.f50-sms__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 6px color-mix(in srgb, var(--accent) 70%, transparent);
}

.f50-sms__date {
  color: var(--muted);
  font-size: 10.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
}

.f50-sms__content {
  color: var(--text);
  font-size: 12.5px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
}

.f50-sms__actions {
  display: flex;
  gap: 8px;
}

.f50-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 10px;
  border: 1px solid color-mix(in srgb, var(--accent) 45%, var(--border));
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--text);
  font-size: 11px;
  cursor: pointer;
  transition: background var(--motion-fast) var(--ease-standard);
}

.f50-chip:hover {
  background: color-mix(in srgb, var(--accent) 22%, transparent);
}

.f50-chip--ghost {
  border-color: var(--border);
  background: transparent;
  color: var(--muted);
}

/* Connection form */
.f50-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

.f50-saved {
  margin: 0;
  color: var(--success, #30a46c);
  font-size: 12px;
}

@keyframes f50-spin {
  to { transform: rotate(360deg); }
}

.spin {
  animation: f50-spin 0.9s linear infinite;
}
</style>
