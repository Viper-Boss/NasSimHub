<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { LoaderCircle, Phone, RefreshCw, Signal, Wifi } from '@lucide/vue'
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

async function loadSettings(): Promise<void> {
  settingsLoading.value = true
  settingsError.value = ''
  try {
    settings.value = await getHomeSIMF50Settings()
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
    settings.value = await saveHomeSIMF50Settings(settings.value)
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

function formatBytes(raw: string): string {
  const value = Number(raw)
  if (!Number.isFinite(value) || value <= 0) return '--'
  const gb = value / (1024 * 1024 * 1024)
  if (gb >= 1) return gb.toFixed(1) + ' GB'
  const mb = value / (1024 * 1024)
  if (mb >= 1) return mb.toFixed(0) + ' MB'
  return (value / 1024).toFixed(0) + ' KB'
}

function formatSMSDate(raw: string): string {
  const parts = raw.split(';')
  if (parts.length < 6) return raw
  return `20${parts[0]}-${parts[1]}-${parts[2]} ${parts[3]}:${parts[4]}`
}

const monthlyUsage = computed(() => {
  if (!status.value) return '--'
  return `${formatBytes(status.value.MonthlyRxBytes)} ↓ / ${formatBytes(status.value.MonthlyTxBytes)} ↑`
})

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
    <!-- Connection settings -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Wifi :size="17" />
        <h3>{{ t('homesim.f50.connection') }}</h3>
      </header>
      <div v-if="settingsLoading" class="f50-state" role="status">
        <LoaderCircle class="spin" :size="18" />
        {{ t('common.loading') }}
      </div>
      <template v-else-if="settings">
        <div class="settings-form-grid">
          <label class="field">
            <span>{{ t('homesim.f50.baseUrl') }}</span>
            <input v-model="settings.base_url" type="url" autocomplete="off" :disabled="saving" />
          </label>
          <label class="field">
            <span>{{ t('homesim.f50.username') }}</span>
            <input v-model="settings.username" type="text" autocomplete="off" :disabled="saving" />
          </label>
          <label class="field">
            <span>{{ t('homesim.f50.credentialRef') }}</span>
            <input v-model="settings.credential_ref" type="text" autocomplete="off" :disabled="saving" />
            <small class="field-status">{{ t('homesim.f50.credentialRefHint') }}</small>
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

    <!-- Live status -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Signal :size="17" />
        <h3>{{ t('homesim.f50.statusTitle') }}</h3>
        <button class="secondary-button f50-refresh" type="button" :disabled="statusLoading" @click="refreshStatus">
          <RefreshCw :size="15" />
          {{ t('common.refresh') }}
        </button>
      </header>
      <div v-if="statusLoading && !status" class="f50-state" role="status">
        <LoaderCircle class="spin" :size="18" />
        {{ t('common.loading') }}
      </div>
      <p v-else-if="statusError" class="field-error" role="alert">{{ statusError }}</p>
      <template v-else-if="status">
        <div class="f50-metrics">
          <div class="f50-metric">
            <small>{{ t('homesim.f50.network') }}</small>
            <strong>{{ status.NetworkType || '--' }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.carrier') }}</small>
            <strong>{{ status.Provider || '--' }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.signal') }}</small>
            <strong>{{ status.Signalbar || '--' }} / 5</strong>
          </div>
          <div class="f50-metric">
            <small>RSRP</small>
            <strong>{{ status.RSRP || '--' }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.band') }}</small>
            <strong>{{ status.Bands || '--' }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.monthlyTraffic') }}</small>
            <strong>{{ monthlyUsage }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.billingDay') }}</small>
            <strong>{{ status.BillingDay || '--' }}</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.temperature') }}</small>
            <strong>{{ status.Temperature || '--' }}°C</strong>
          </div>
          <div class="f50-metric">
            <small>{{ t('homesim.f50.wifiClients') }}</small>
            <strong>{{ status.WifiStaNum || '0' }}</strong>
          </div>
        </div>
      </template>
    </div>

    <!-- Messages -->
    <div class="f50-section">
      <header class="f50-section__heading">
        <Phone :size="17" />
        <h3>{{ t('homesim.f50.smsTitle') }}</h3>
        <button class="secondary-button f50-refresh" type="button" :disabled="smsLoading" @click="refreshSMS">
          <RefreshCw :size="15" />
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
          role="button"
          tabindex="0"
          :title="message.Tag === '0' ? t('homesim.f50.markRead') : ''"
          @click="message.Tag === '0' && markRead(message)"
          @keydown.enter.prevent="message.Tag === '0' && markRead(message)"
        >
          <div class="f50-sms__meta">
            <span class="f50-sms__number">{{ message.Number }}</span>
            <span class="f50-sms__date">{{ formatSMSDate(message.Date) }}</span>
          </div>
          <div class="f50-sms__content">{{ message.Content }}</div>
        </div>
        <p v-if="!messages.length" class="f50-state">{{ t('homesim.f50.noMessages') }}</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.f50-panel {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.f50-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 12px;
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
  font-size: 12px;
}

.f50-state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}

.f50-saved {
  margin: 0;
  color: var(--success);
  font-size: 12px;
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

.f50-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 10px;
}

.f50-metric {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  background: var(--surface-subtle);
  border-radius: 10px;
}

.f50-metric small {
  color: var(--muted);
  font-size: 10.5px;
}

.f50-metric strong {
  font-size: 13.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
}

.f50-send {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--surface-subtle);
  border-radius: 10px;
}

.f50-send-feedback {
  color: var(--muted);
  font-size: 11.5px;
}

.f50-sms-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.f50-sms {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  cursor: pointer;
  background: var(--surface-subtle);
  border: 1px solid transparent;
  border-radius: 10px;
  transition: border-color var(--motion-fast) var(--ease-standard);
}

.f50-sms:hover {
  border-color: color-mix(in srgb, var(--accent) 40%, transparent);
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
  font-weight: 700;
  font-size: 13px;
}

.f50-sms__date {
  color: var(--muted);
  font-size: 10.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
}

.f50-sms__content {
  color: var(--muted);
  font-size: 12.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
</style>
