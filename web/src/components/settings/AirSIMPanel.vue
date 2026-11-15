<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyRound, LoaderCircle, Smartphone, Trash2 } from '@lucide/vue'
import {
  cancelAirSIMPairing,
  getAirSIMSettings,
  revokeAirSIMDevice,
  saveAirSIMSettings,
  startAirSIMPairing,
  type AirSIMDevice,
  type AirSIMPairingWindow,
  type AirSIMStatus
} from '../../api/airsim'
import { airsimMessages } from '../../i18n/airsimMessages'
import { requestConfirmation } from '../../state/confirmation'
import {
  bootstrapResource,
  devicesResource,
  displayModuleLines,
  lineKey
} from '../../state/workspace'
import { lineTagFallback } from '../../utils/lineIdentity'

// Settings for the AirSIM iPhone app gateway (internal/airsimgw). The
// gateway stands in for the Android phone the app pairs with: the
// administrator opens a 120 s window here, and the iPhone claims it with the
// 6-digit code. The code is shown only on this page; it never leaves the
// gateway any other way.

const i18n = useI18n()
const { t } = i18n
watch(
  i18n.locale,
  (locale) => {
    i18n.mergeLocaleMessage(locale, { airsim: airsimMessages(locale) })
  },
  { immediate: true, flush: 'sync' }
)

const loading = ref(true)
const available = ref(true)
const status = ref<AirSIMStatus | null>(null)
const loadError = ref('')
const actionError = ref('')
const saving = ref(false)
const saved = ref(false)
const pairingBusy = ref(false)
const enabled = ref(false)
const lineIDs = ref<string[]>([])
const lineID = computed(() => lineIDs.value[0] || '')
const pairing = ref<AirSIMPairingWindow | null>(null)
const pairedNow = ref(false)
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | undefined
let poller: ReturnType<typeof setInterval> | undefined

const lines = computed(() =>
  displayModuleLines(bootstrapResource.data?.lines || [], devicesResource.data)
)
const defaultLineID = computed(
  () => bootstrapResource.data?.line_settings.default_line_id || ''
)
const lineOptions = computed(() =>
  lines.value.map((line) => ({
    value: lineKey(line),
    label:
      line.line_label?.trim() ||
      lineTagFallback(line, lines.value, defaultLineID.value, lineKey(line)) ||
      lineKey(line)
  }))
)
const host = computed(() => window.location.hostname)
const secondsLeft = computed(() => {
  if (!pairing.value) return 0
  return Math.max(
    0,
    Math.ceil((Date.parse(pairing.value.expires_at) - now.value) / 1000)
  )
})
const pairingExpired = computed(
  () => pairing.value !== null && secondsLeft.value === 0
)
const dirty = computed(
  () =>
    !!status.value &&
    (status.value.enabled !== enabled.value ||
      JSON.stringify(
        status.value.line_ids ||
          (status.value.line_id ? [status.value.line_id] : [])
      ) !== JSON.stringify(lineIDs.value))
)

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function apply(next: AirSIMStatus | undefined): void {
  if (!next) return
  const firstLoad = status.value === null
  status.value = next
  if (firstLoad || !dirty.value) {
    enabled.value = next.enabled
    lineIDs.value = [...(next.line_ids || (next.line_id ? [next.line_id] : []))]
  }
  if (next.pairing) {
    pairing.value = next.pairing
  } else if (pairing.value && next.last_paired) {
    pairing.value = null
    pairedNow.value = true
  }
}

async function load(): Promise<void> {
  loadError.value = ''
  try {
    const result = await getAirSIMSettings()
    available.value = result.available
    apply(result.status)
  } catch (error) {
    loadError.value = errorText(error)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (saving.value) return
  saving.value = true
  saved.value = false
  actionError.value = ''
  try {
    const result = await saveAirSIMSettings(enabled.value, [...lineIDs.value])
    status.value = null
    apply(result.status)
    saved.value = true
  } catch (error) {
    actionError.value = errorText(error)
  } finally {
    saving.value = false
  }
}

async function startPairing(): Promise<void> {
  if (pairingBusy.value) return
  pairingBusy.value = true
  pairedNow.value = false
  actionError.value = ''
  try {
    pairing.value = await startAirSIMPairing()
    now.value = Date.now()
  } catch (error) {
    actionError.value = errorText(error)
  } finally {
    pairingBusy.value = false
  }
}

async function cancelPairing(): Promise<void> {
  pairing.value = null
  try {
    await cancelAirSIMPairing()
  } catch (error) {
    actionError.value = errorText(error)
  }
}

async function revoke(device: AirSIMDevice): Promise<void> {
  const confirmed = await requestConfirmation({
    title: t('airsim.revoke'),
    message: t('airsim.revokeConfirm'),
    confirmLabel: t('airsim.revoke'),
    tone: 'danger'
  })
  if (!confirmed) return
  actionError.value = ''
  try {
    await revokeAirSIMDevice(device.id)
    await load()
  } catch (error) {
    actionError.value = errorText(error)
  }
}

function formatTime(value?: string): string {
  if (!value) return ''
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime())
    ? value
    : parsed.toLocaleString(i18n.locale.value)
}

onMounted(() => {
  void load()
  ticker = setInterval(() => {
    now.value = Date.now()
  }, 1000)
  // While a window is open, watch for the claim so the page can say so.
  poller = setInterval(() => {
    if (pairing.value && !pairingExpired.value) void load()
  }, 2000)
})

onBeforeUnmount(() => {
  if (ticker) clearInterval(ticker)
  if (poller) clearInterval(poller)
})
</script>

<template>
  <section class="airsim-panel">
    <header class="airsim-panel__heading">
      <span class="airsim-panel__icon" aria-hidden="true"
        ><Smartphone :size="18"
      /></span>
      <div>
        <h3>{{ t('airsim.title') }}</h3>
        <small>{{ t('airsim.subtitle') }}</small>
      </div>
    </header>

    <div v-if="loading" class="airsim-panel__state" role="status">
      <LoaderCircle class="spin" :size="18" />
      {{ t('common.loading') }}
    </div>

    <p v-else-if="loadError" class="field-error" role="alert">
      {{ t('airsim.loadFailed', { error: loadError }) }}
      <button class="text-button" type="button" @click="load">
        {{ t('common.retry') }}
      </button>
    </p>

    <p v-else-if="!available" class="airsim-panel__note">
      {{ t('airsim.unavailable') }}
    </p>

    <template v-else-if="status">
      <p v-if="status.listening" class="airsim-panel__note">
        {{
          t('airsim.listening', {
            control: status.control_port,
            pcm: status.pcm_port,
            pairing: status.pairing_port
          })
        }}
      </p>
      <p v-else class="field-error" role="alert">
        {{ t('airsim.notListening', { error: status.listen_error || '—' }) }}
      </p>
      <p class="airsim-panel__note">{{ t('airsim.portsNote') }}</p>

      <div class="settings-form-grid">
        <label class="airsim-panel__toggle">
          <input
            v-model="enabled"
            class="ui-switch"
            role="switch"
            type="checkbox"
            :disabled="saving"
          />
          <span>
            <strong>{{ t('airsim.enable') }}</strong>
            <small>{{ t('airsim.enableHint') }}</small>
          </span>
        </label>
        <div v-if="enabled" class="field">
          <p v-if="!lineOptions.length" class="airsim-panel__note">
            {{ t('airsim.noLines') }}
          </p>
          <fieldset v-else class="airsim-panel__lines">
            <legend>{{ t('airsim.line') }}</legend>
            <label
              v-for="option in lineOptions"
              :key="option.value"
              class="airsim-panel__line"
              ><input
                v-model="lineIDs"
                type="checkbox"
                class="ui-check"
                :value="option.value"
                :disabled="
                  saving ||
                  (lineIDs.length >= 32 && !lineIDs.includes(option.value))
                "
              />{{ option.label
              }}<small v-if="lineIDs.includes(option.value)">
                · {{ lineIDs.indexOf(option.value) + 1 }}</small
              ></label
            >
          </fieldset>
        </div>
      </div>

      <footer class="airsim-panel__actions">
        <button
          class="primary-button"
          type="button"
          :disabled="saving || !dirty || (enabled && !lineID)"
          @click="save"
        >
          <LoaderCircle v-if="saving" class="spin" :size="16" />
          {{ t('airsim.save') }}
        </button>
        <small
          v-if="saved && !dirty"
          class="airsim-panel__saved"
          role="status"
          >{{ t('airsim.saved') }}</small
        >
      </footer>

      <h4 class="airsim-panel__section">{{ t('airsim.pairingTitle') }}</h4>
      <p v-if="!status.enabled || !status.line_id" class="airsim-panel__note">
        {{ t('airsim.needEnabled') }}
      </p>
      <template v-else>
        <ol class="airsim-panel__steps">
          <li>{{ t('airsim.step1') }}</li>
          <li>{{ t('airsim.step2', { host, port: status.pairing_port }) }}</li>
          <li v-if="pairing && !pairingExpired">
            {{ t('airsim.step3') }}
            <span class="airsim-panel__code" aria-live="polite">{{
              pairing.code
            }}</span>
            <small>{{ t('airsim.expiresIn', { seconds: secondsLeft }) }}</small>
          </li>
        </ol>
        <p v-if="pairingExpired" class="airsim-panel__note">
          {{ t('airsim.expired') }}
        </p>
        <p v-if="pairedNow" class="airsim-panel__saved" role="status">
          {{ t('airsim.pairedNow') }}
        </p>
        <footer class="airsim-panel__actions">
          <button
            class="primary-button"
            type="button"
            :disabled="pairingBusy"
            @click="startPairing"
          >
            <LoaderCircle v-if="pairingBusy" class="spin" :size="16" />
            <KeyRound v-else :size="16" />
            {{
              pairing && !pairingExpired
                ? t('airsim.restart')
                : t('airsim.start')
            }}
          </button>
          <button
            v-if="pairing && !pairingExpired"
            class="secondary-button"
            type="button"
            @click="cancelPairing"
          >
            {{ t('airsim.cancel') }}
          </button>
        </footer>
      </template>

      <p v-if="actionError" class="field-error" role="alert">
        {{ actionError }}
      </p>

      <h4 class="airsim-panel__section">{{ t('airsim.devicesTitle') }}</h4>
      <p v-if="!status.devices.length" class="airsim-panel__note">
        {{ t('airsim.noDevices') }}
      </p>
      <ul v-else class="airsim-panel__devices">
        <li v-for="device in status.devices" :key="device.id">
          <div>
            <strong>{{
              device.device_hint
                ? t('airsim.deviceHint', { hint: device.device_hint })
                : t('airsim.device')
            }}</strong>
            <small>{{
              t('airsim.pairedAt', { time: formatTime(device.created_at) })
            }}</small>
            <small v-if="device.last_seen_at && device.last_peer">
              {{
                t('airsim.lastSeen', {
                  time: formatTime(device.last_seen_at),
                  peer: device.last_peer
                })
              }}
            </small>
          </div>
          <button
            class="secondary-button"
            type="button"
            @click="revoke(device)"
          >
            <Trash2 :size="15" />
            {{ t('airsim.revoke') }}
          </button>
        </li>
      </ul>

      <h4 class="airsim-panel__section">{{ t('airsim.limitsTitle') }}</h4>
      <p class="airsim-panel__note">{{ t('airsim.limits') }}</p>
    </template>
  </section>
</template>

<style scoped>
.airsim-panel__lines {
  min-width: 0;
  border: 1px solid #8885;
  border-radius: 0.75rem;
  padding: 0.8rem;
}
.airsim-panel__line {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0;
  overflow-wrap: anywhere;
}
.airsim-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.airsim-panel__heading {
  display: flex;
  align-items: center;
  gap: 10px;
}

.airsim-panel__heading h3 {
  margin: 0;
  font-size: 15px;
}

.airsim-panel__heading small,
.airsim-panel__note {
  display: block;
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.5;
}

.airsim-panel__icon {
  display: inline-grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 50%;
}

.airsim-panel__state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}

.airsim-panel__toggle {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  font-size: 13px;
}

.airsim-panel__toggle small {
  display: block;
  color: var(--muted);
  font-size: 11.5px;
}

.airsim-panel__section {
  margin: 6px 0 0;
  font-size: 13px;
}

.airsim-panel__steps {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding-left: 18px;
  font-size: 12.5px;
}

.airsim-panel__code {
  display: block;
  margin: 6px 0 2px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 28px;
  font-weight: 600;
  letter-spacing: 0.2em;
}

.airsim-panel__steps small {
  color: var(--muted);
  font-size: 11px;
}

.airsim-panel__saved {
  margin: 0;
  color: var(--success);
  font-size: 12px;
}

.airsim-panel__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.airsim-panel__actions .primary-button,
.airsim-panel__actions .secondary-button,
.airsim-panel__devices .secondary-button {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 7px;
  padding: 0 14px;
  font-size: 12.5px;
}

.airsim-panel__devices {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.airsim-panel__devices li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
}

.airsim-panel__devices small {
  display: block;
  color: var(--muted);
  font-size: 11px;
}
</style>
