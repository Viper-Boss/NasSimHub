<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { LoaderCircle, PhoneForwarded, RefreshCw } from '@lucide/vue'
import {
  getCallServices,
  setCallForwarding,
  setCallWaiting,
  type CallForwardReason,
  type CallServices,
  type CallServicesResponse
} from '../../api/callServices'
import { callServicesMessages } from '../../i18n/callServicesMessages'
import {
  bootstrapResource,
  devicesResource,
  displayModuleLines,
  lineKey
} from '../../state/workspace'
import { lineTagFallback } from '../../utils/lineIdentity'
import SelectControl from '../SelectControl.vue'

// Forwarding and call waiting, read from the operator rather than from disk.
//
// The panel is built around one fact: this software does not own this state.
// Everything follows from that.
//
// There is no save-on-blur and no optimistic update. A change goes to the
// network and the panel then shows what the network reports BACK, because
// networks normalise numbers, round delays, and quietly decline services the
// subscription does not include. Showing the operator their own input would be
// showing them a rule that may not exist -- and forwarding is the one setting
// where believing a wrong answer means missing every call.
//
// There is also no polling. Every read costs a round trip to the HLR and takes
// seconds; a timer would hammer the operator's network to refresh a page nobody
// is looking at. It reads on open, and after a change, and when asked.

const i18n = useI18n()
const { t } = i18n

watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, { callServices: callServicesMessages(locale) })
  },
  { immediate: true, flush: 'sync' }
)

const lines = computed(() =>
  displayModuleLines(bootstrapResource.data?.lines || [], devicesResource.data)
)
const selectedLineKey = ref('')
const activeLineKey = computed(
  () => selectedLineKey.value || (lines.value.length ? lineKey(lines.value[0]!) : '')
)
// The selector's own value has to be a real line key, not "". An empty model
// matches no option, so the control rendered with a blank label and an
// aria-label ending in a bare colon -- which is what it did on first build.
watch(
  lines,
  available => {
    if (!selectedLineKey.value && available.length) {
      selectedLineKey.value = lineKey(available[0]!)
    }
  },
  { immediate: true }
)
// The same name the rest of the app shows for a line. line_label is blank on
// most installs, so using it alone produced an empty dropdown -- which is what
// the first render of this panel actually did.
const defaultLineID = computed(
  () => bootstrapResource.data?.line_settings.default_line_id || ''
)
const lineOptions = computed(() =>
  lines.value.map(line => ({
    value: lineKey(line),
    label:
      line.line_label?.trim() ||
      lineTagFallback(line, lines.value, defaultLineID.value, lineKey(line)) ||
      lineKey(line)
  }))
)

const response = ref<CallServicesResponse | null>(null)
const services = computed<CallServices | null>(() => response.value?.call_services ?? null)
const reading = ref(false)
// Keyed by reason, so one row being applied does not spin every row.
const applying = ref('')
const error = ref('')

// Draft state per row. The network's value seeds it, and an operator's
// half-typed number is never overwritten by a read they did not ask for.
const draftNumber = ref<Record<string, string>>({})
const draftSeconds = ref<Record<string, number>>({})

const forwardingRows = computed(() => services.value?.forwarding ?? [])

function seedDrafts(next: CallServices): void {
  for (const rule of next.forwarding) {
    draftNumber.value[rule.reason] = rule.number
    if (rule.reason === 'no_reply') draftSeconds.value[rule.reason] = rule.seconds || 20
  }
}

async function read(): Promise<void> {
  const line = activeLineKey.value
  if (!line) return
  reading.value = true
  error.value = ''
  try {
    const next = await getCallServices(line)
    response.value = next
    if (next.call_services) seedDrafts(next.call_services)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    reading.value = false
  }
}

onMounted(() => { void read() })
// Switching line is a different subscriber's services, so the old answer must
// not linger on screen looking like this line's.
watch(activeLineKey, () => {
  response.value = null
  void read()
})

function invalidNumber(value: string): boolean {
  return !/^\+?[0-9*#]+$/.test(value.trim())
}

async function applyForwarding(reason: CallForwardReason, enabled: boolean): Promise<void> {
  const line = activeLineKey.value
  if (!line) return
  const number = (draftNumber.value[reason] || '').trim()
  if (enabled) {
    if (!number) {
      error.value = t('callServices.numberRequired')
      return
    }
    if (invalidNumber(number)) {
      error.value = t('callServices.numberInvalid')
      return
    }
  }
  applying.value = reason
  error.value = ''
  try {
    const next = await setCallForwarding(line, {
      reason,
      enabled,
      number,
      seconds: reason === 'no_reply' ? draftSeconds.value[reason] || 0 : 0
    })
    response.value = next
    // The read-back is authoritative, including when it disagrees with what
    // was just sent.
    if (next.call_services) seedDrafts(next.call_services)
    if (next.error) error.value = t('callServices.networkError', { error: next.error })
  } catch (reason_) {
    error.value = reason_ instanceof Error ? reason_.message : String(reason_)
  } finally {
    applying.value = ''
  }
}

async function applyWaiting(enabled: boolean): Promise<void> {
  const line = activeLineKey.value
  if (!line) return
  applying.value = 'waiting'
  error.value = ''
  try {
    const next = await setCallWaiting(line, enabled)
    response.value = next
    if (next.error) error.value = t('callServices.networkError', { error: next.error })
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    applying.value = ''
  }
}

const observedAt = computed(() => {
  const stamp = services.value?.observed_at
  if (!stamp) return ''
  const parsed = new Date(stamp)
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toLocaleTimeString()
})

const delayChoices = [5, 10, 15, 20, 25, 30].map(seconds => ({
  value: String(seconds),
  label: String(seconds)
}))
</script>

<template>
  <section class="call-services">
    <header class="call-services__head">
      <PhoneForwarded :size="16" />
      <h3>{{ t('callServices.title') }}</h3>
    </header>
    <p class="call-services__lede">{{ t('callServices.subtitle') }}</p>
    <p class="call-services__note">{{ t('callServices.slowNote') }}</p>

    <p v-if="!lines.length" class="call-services__empty">{{ t('callServices.noLine') }}</p>

    <template v-else>
      <div class="call-services__toolbar">
        <SelectControl
          v-if="lineOptions.length > 1"
          v-model="selectedLineKey"
          :options="lineOptions"
          :label="t('callServices.lineLabel')"
        />
        <span v-if="observedAt" class="call-services__stamp">
          {{ t('callServices.observed', { time: observedAt }) }}
        </span>
        <button
          type="button"
          class="secondary-button call-services__refresh"
          :disabled="reading || Boolean(applying)"
          @click="read"
        >
          <LoaderCircle v-if="reading" class="spin" :size="15" />
          <RefreshCw v-else :size="15" />
          {{ reading ? t('callServices.reading') : t('callServices.refresh') }}
        </button>
      </div>

      <p v-if="response?.unavailable" class="call-services__unavailable">
        <strong>{{ t('callServices.unavailable') }}</strong>
        <span>{{ t('callServices.unavailableHint') }}</span>
      </p>

      <p v-else-if="error" class="call-services__error" role="alert">{{ error }}</p>

      <template v-if="!response?.unavailable">
        <h4 class="call-services__section">{{ t('callServices.forwardingTitle') }}</h4>
        <ul class="call-services__rules">
          <li
            v-for="rule in forwardingRows"
            :key="rule.reason"
            class="call-rule"
            :class="{ 'is-active': rule.active, 'is-unreadable': rule.unreadable }"
          >
            <div class="call-rule__head">
              <div class="call-rule__naming">
                <strong>{{ t(`callServices.${rule.reason}`) }}</strong>
                <span class="call-rule__hint">{{ t(`callServices.${rule.reason}Hint`) }}</span>
              </div>
              <span class="call-rule__state">
                <template v-if="rule.unreadable">{{ t('callServices.unreadable') }}</template>
                <template v-else-if="rule.active && rule.number">
                  {{ t('callServices.activeTo', { number: rule.number }) }}
                </template>
                <template v-else-if="rule.active">{{ t('callServices.activeNoNumber') }}</template>
                <template v-else>{{ t('callServices.off') }}</template>
              </span>
            </div>

            <div v-if="!rule.unreadable" class="call-rule__form">
              <label class="call-rule__field">
                <span>{{ t('callServices.destination') }}</span>
                <input
                  v-model="draftNumber[rule.reason]"
                  type="tel"
                  inputmode="tel"
                  :placeholder="t('callServices.destinationPlaceholder')"
                  :disabled="Boolean(applying)"
                />
              </label>
              <label v-if="rule.reason === 'no_reply'" class="call-rule__field call-rule__field--delay">
                <span>{{ t('callServices.delay') }}</span>
                <SelectControl
                  :model-value="String(draftSeconds[rule.reason] || 20)"
                  :options="delayChoices"
                  :label="t('callServices.delay')"
                  :disabled="Boolean(applying)"
                  compact
                  @update:model-value="value => (draftSeconds[rule.reason] = Number(value))"
                />
              </label>
              <div class="call-rule__actions">
                <button
                  type="button"
                  class="primary-button"
                  :disabled="Boolean(applying)"
                  @click="applyForwarding(rule.reason, true)"
                >
                  <LoaderCircle v-if="applying === rule.reason" class="spin" :size="15" />
                  {{ applying === rule.reason ? t('callServices.applying') : t('callServices.save') }}
                </button>
                <button
                  v-if="rule.active"
                  type="button"
                  class="secondary-button"
                  :disabled="Boolean(applying)"
                  @click="applyForwarding(rule.reason, false)"
                >
                  {{ t('callServices.clear') }}
                </button>
              </div>
            </div>
          </li>
        </ul>

        <h4 class="call-services__section">{{ t('callServices.waitingTitle') }}</h4>
        <div class="call-waiting">
          <p class="call-waiting__hint">{{ t('callServices.waitingHint') }}</p>
          <div class="call-waiting__row">
            <span class="call-waiting__state">
              <template v-if="!services?.waiting_known">{{ t('callServices.waitingUnknown') }}</template>
              <template v-else-if="services.waiting_active">{{ t('callServices.waitingOn') }}</template>
              <template v-else>{{ t('callServices.waitingOff') }}</template>
            </span>
            <div class="call-waiting__actions">
              <button
                type="button"
                class="primary-button"
                :disabled="Boolean(applying) || (services?.waiting_known && services.waiting_active)"
                @click="applyWaiting(true)"
              >
                <LoaderCircle v-if="applying === 'waiting'" class="spin" :size="15" />
                {{ t('callServices.waitingOn') }}
              </button>
              <button
                type="button"
                class="secondary-button"
                :disabled="Boolean(applying) || (services?.waiting_known && !services.waiting_active)"
                @click="applyWaiting(false)"
              >
                {{ t('callServices.waitingOff') }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </template>
  </section>
</template>

<style scoped>
.call-services {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.call-services__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}

.call-services__head h3 {
  margin: 0;
  color: var(--text);
  font-size: 15px;
}

.call-services__lede,
.call-services__note,
.call-services__empty {
  margin: 0;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.65;
}

.call-services__note {
  color: var(--faint);
  font-size: 11.5px;
}

.call-services__toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.call-services__stamp {
  color: var(--faint);
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
}

.call-services__refresh {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.call-services__section {
  margin: 6px 0 0;
  color: var(--text);
  font-size: 13px;
}

.call-services__unavailable,
.call-services__error {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  background: var(--surface-subtle);
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}

.call-services__error {
  border-color: var(--danger-border, var(--border));
  color: var(--danger, var(--text));
}

.call-services__unavailable strong {
  color: var(--text);
}

.call-services__rules {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.call-rule {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  background: var(--surface-subtle);
}

/* An active rule is called out, because "my calls are being diverted" is the
   single most important thing this panel can tell someone. */
.call-rule.is-active {
  border-color: var(--warning-border, var(--accent-border));
  background: var(--warning-soft, var(--surface-selected));
}

.call-rule.is-unreadable {
  opacity: 0.72;
}

.call-rule__head {
  /* Two columns rather than a wrapping flex row. With flex, a long hint pushed
     the state onto its own line below the description, so "forwarding is on"
     stopped sitting beside the condition it belongs to. The state column sizes
     to its content and the naming column takes the rest. */
  display: grid;
  gap: 4px 12px;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
}

.call-rule__naming {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.call-rule__naming strong {
  color: var(--text);
  font-size: 13px;
}

.call-rule__hint {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.55;
}

.call-rule__state {
  color: var(--muted-strong, var(--muted));
  font-size: 11.5px;
  text-align: right;
}

.call-rule.is-active .call-rule__state {
  color: var(--warning-strong, var(--text));
  font-weight: 600;
}

.call-rule__form {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 10px;
}

.call-rule__field {
  display: grid;
  gap: 4px;
  flex: 1 1 200px;
  min-width: 0;
  color: var(--muted);
  font-size: 11.5px;
}

.call-rule__field--delay {
  flex: 0 0 auto;
  min-width: 140px;
}

.call-rule__field input {
  min-height: 36px;
  padding: 0 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.call-rule__actions,
.call-waiting__actions {
  display: flex;
  gap: 8px;
}

.call-rule__actions button,
.call-waiting__actions button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.call-waiting {
  display: grid;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  background: var(--surface-subtle);
}

.call-waiting__hint {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}

.call-waiting__row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.call-waiting__state {
  color: var(--text);
  font-size: 13px;
  font-weight: 600;
}

.call-waiting__actions {
  margin-left: auto;
}

.spin {
  animation: call-services-spin 1s linear infinite;
}

@keyframes call-services-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}
</style>
