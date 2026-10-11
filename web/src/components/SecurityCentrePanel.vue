<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  EVIDENCE_KEYS,
  NodeApiError,
  REASON_LIMIT_BYTES,
  TRUST_ACTIONS,
  getTrustDocument,
  journalStatus,
  reasonBytes,
  reasonProblem,
  resealJournal,
  runTrustAction,
  subjectState,
  trustActionsFor,
  verifyJournal
} from '../api/nodeSecurity'
import type {
  TrustAction,
  TrustAdminAction,
  TrustDocument,
  TrustSubject
} from '../api/nodeSecurity'
import { getSecurityPosture } from '../api/dashboardCards'
import type { SecurityPosture } from '../api/dashboardCards'
import { securityCentreLabels } from '../i18n/securityCentreLabels'
import { sessionState } from '../state/session'
import { loadBootstrap } from '../state/workspace'
import { formatRemaining } from '../utils/callDuration'
import LinkBoundaryList from './LinkBoundaryList.vue'
import SecurityJournalViewer from './SecurityJournalViewer.vue'
import DevicePolicyStatus from './DevicePolicyStatus.vue'

// The administrator's security centre.
//
// WHAT IT IS FOR. The gateway now keeps a trust record per device and a
// hash-chained journal of every decision about one. This panel is where an
// administrator reads them and acts on them: which state each device is in and
// why, what that state allows, what the engine looks at and what it cannot,
// whether the journal still verifies, and what protects each link.
//
// THREE RULES IT KEEPS, each of which a test holds:
//
// 1. MONITOR MODE IS NEVER DRAWN AS PROTECTION. In monitor mode the states are
//    real and nothing is refused. The banner says so, a trusted device is not
//    tinted green, and an action the state forbids reads "would be refused
//    (not blocked)". The same goes for a gateway that did not say which mode
//    it is in.
//
// 2. NOTHING WITHOUT EVIDENCE IS GREEN. An input the engine cannot observe is
//    listed as unsupported with the reason. Secure boot, TPM/TEE, kernel and
//    rootfs signatures and location risk have no backend at all; they are a
//    fixed list of "not supported" rows that no payload can light up, because
//    there is no field that feeds them.
//
// 3. AN ADMINISTRATOR'S ACTION CARRIES ITS REASON. Restrict, quarantine,
//    restore and reseal each open a form that explains the consequence and
//    will not send without a reason the journal accepts (1-200 bytes).
//
// NOTHING IS CACHED. Every read is one request and the document it returns
// replaces what was shown; a failed read clears the page instead of leaving
// the previous answer up as if it were current.

const { locale } = useI18n()
const labels = computed(() => securityCentreLabels(String(locale.value)))

/** Administrators only, like the routes behind it. A member sees one line. */
const admin = computed(() => sessionState.role === 'admin')

const trust = ref<TrustDocument | null>(null)
const loading = ref(false)
const loadError = ref('')
const forbidden = ref(false)

type Pending =
  | { kind: 'trust'; action: TrustAdminAction; device: string }
  | { kind: 'reseal' }
const pending = ref<Pending | null>(null)
const reason = ref('')
const reasonTouched = ref(false)
const working = ref(false)
const verifying = ref(false)
const actionError = ref('')
const notice = ref('')

const journalOpen = ref(false)
const viewer = ref<{ reload: () => Promise<void> } | null>(null)
// The posture the boundary list is drawn from: replaced whole by every read,
// and null when the gateway did not answer, so an old list is never left up.
const posture = ref<SecurityPosture | null>(null)
const postureLoaded = ref(false)

async function loadBoundaries(): Promise<void> {
  posture.value = await getSecurityPosture()
  postureLoaded.value = true
}

function errorText(cause: unknown): string {
  if (cause instanceof NodeApiError) {
    if (cause.code === 'network') return labels.value.offline
    if (cause.code === 'csrf_failed') return labels.value.sessionExpired
    if (cause.status === 403) return labels.value.adminOnly
    if (cause.code === 'trust_unavailable') return labels.value.unavailable
    if (cause.code === 'invalid_response') return labels.value.invalidResponse
    if (cause.code === 'reason_required') return labels.value.reasonRequired
    if (cause.code === 'reason_too_long') return labels.value.reasonTooLong
    // Otherwise the gateway's own sentence: for a refused action it says which
    // rule refused it, which no text written here could know.
    if (cause.message) return cause.message
    return `${labels.value.failed} (${cause.status})`
  }
  return labels.value.failed
}

async function load(): Promise<void> {
  if (!admin.value || loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    trust.value = await getTrustDocument()
    forbidden.value = false
  } catch (cause) {
    trust.value = null
    forbidden.value = cause instanceof NodeApiError && cause.status === 403
    loadError.value = errorText(cause)
  } finally {
    loading.value = false
  }
}

async function refreshAll(): Promise<void> {
  notice.value = ''
  actionError.value = ''
  await load()
  void loadBoundaries()
  if (journalOpen.value) void viewer.value?.reload()
}

/** True only when the gateway said it refuses what a state forbids. */
const issuerAdminNote = computed(() => labels.value.issuerAdminNote)

const enforcing = computed(() => trust.value?.enforcement === 'enforce')

function stateLabel(subject: TrustSubject): string {
  const state = subjectState(subject)
  return state ? (labels.value.states[state] ?? state) : labels.value.stateUnknown
}

/**
 * Green for a trusted device only where trust is enforced. In monitor mode, or
 * when the mode was not reported, "trusted" is a calculation nothing acts on.
 */
function stateTone(subject: TrustSubject): string {
  switch (subjectState(subject)) {
    case 'TRUSTED':
      return enforcing.value ? 'is-ok' : ''
    case 'OBSERVATION':
      return 'is-warn'
    case 'RESTRICTED':
    case 'QUARANTINE':
      return 'is-bad'
    default:
      return ''
  }
}

function remainingText(subject: TrustSubject): string {
  if (subjectState(subject) !== 'OBSERVATION' || !subject.remaining_seconds) return ''
  const time = formatRemaining(subject.remaining_seconds, {
    day: labels.value.day,
    hour: labels.value.hour,
    minute: labels.value.minute,
    underMinute: labels.value.underMinute
  }).trim()
  return time ? labels.value.remaining(time) : ''
}

/** One action's chip: its words and its tone, from what the gateway listed. */
function allowance(subject: TrustSubject, action: TrustAction): { text: string; tone: string } {
  const value = subject.allowed[action]
  if (value === null) return { text: labels.value.allowedUnknown, tone: '' }
  if (value) return { text: labels.value.allowed, tone: enforcing.value ? 'is-ok' : '' }
  return enforcing.value
    ? { text: labels.value.notAllowed, tone: 'is-bad' }
    : { text: labels.value.notAllowedMonitor, tone: 'is-warn' }
}

function count(value: number | undefined): string {
  return value === undefined ? labels.value.unknown : String(value)
}

function formatTime(value: string): string {
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? labels.value.unknown : new Date(parsed).toLocaleString(String(locale.value))
}

// The hardware evidence nothing provides. A fixed list with no payload behind
// it, on purpose: there is no field that could turn one of these into a pass.
const HARDWARE_ITEMS = ['secure_boot', 'tpm', 'system_signature', 'geo_risk'] as const

const journal = computed(() => trust.value?.journal ?? null)
const journalState = computed(() => (journal.value ? journalStatus(journal.value) : 'unknown'))
const deviceIDs = computed(() => trust.value?.subjects.map(subject => subject.node_id) ?? [])

function open(next: Pending): void {
  pending.value = next
  reason.value = ''
  reasonTouched.value = false
  actionError.value = ''
  notice.value = ''
}

function cancel(): void {
  if (working.value) return
  pending.value = null
}

const reasonState = computed(() => reasonProblem(reason.value))
const reasonMessage = computed(() => {
  if (reasonState.value === 'too_long') return labels.value.reasonTooLong
  if (reasonState.value === 'required' && reasonTouched.value) return labels.value.reasonRequired
  return ''
})

function isPendingTrust(subject: TrustSubject): TrustAdminAction | '' {
  const current = pending.value
  return current?.kind === 'trust' && current.device === subject.node_id ? current.action : ''
}

async function confirm(): Promise<void> {
  const current = pending.value
  if (!current || working.value) return
  reasonTouched.value = true
  // No reason, no request: the journal would refuse it, and an administrator's
  // action with no stated reason is the one entry the journal cannot explain.
  if (reasonState.value !== '') return
  working.value = true
  actionError.value = ''
  try {
    if (current.kind === 'trust') {
      trust.value = await runTrustAction(current.action, current.device, reason.value)
      void loadBootstrap(true)
      notice.value = labels.value.adminActionDone[current.action] ?? ''
    } else {
      await resealJournal(reason.value)
      notice.value = labels.value.journalResealDone
      // A reseal moves every device back to observation, so the whole
      // document is read again instead of patched from the reseal's answer.
      await load()
    }
    pending.value = null
    if (journalOpen.value) void viewer.value?.reload()
  } catch (cause) {
    actionError.value = errorText(cause)
  } finally {
    working.value = false
  }
}

async function verify(): Promise<void> {
  if (verifying.value || working.value) return
  verifying.value = true
  actionError.value = ''
  notice.value = ''
  try {
    await verifyJournal()
    notice.value = labels.value.journalVerifyDone
    // The result changes what every device is allowed, so re-read it all.
    await load()
  } catch (cause) {
    actionError.value = errorText(cause)
  } finally {
    verifying.value = false
  }
}

onMounted(() => {
  if (!admin.value) return
  void load()
  void loadBoundaries()
})
</script>

<template>
  <section class="security-centre" aria-labelledby="security-centre-title">
    <header class="security-centre__head">
      <div>
        <h2 id="security-centre-title">{{ labels.title }}</h2>
        <p>{{ labels.subtitle }}</p>
      </div>
      <button v-if="admin" type="button" class="secondary-button" :disabled="loading" @click="refreshAll">
        {{ loading ? labels.loading : labels.refresh }}
      </button>
    </header>

    <!-- A member gets one line. Nothing is requested on their behalf: every
         route behind this panel is administrator-only. -->
    <p v-if="!admin || forbidden" class="security-centre__notice">{{ labels.adminOnly }}</p>

    <template v-else>
      <p v-if="notice" role="status" class="security-centre__success">{{ notice }}</p>
      <p v-if="actionError" role="alert" class="security-centre__error">{{ actionError }}</p>

      <p v-if="loading && !trust" class="security-centre__muted">{{ labels.loading }}</p>
      <p v-else-if="loadError" role="alert" class="security-centre__error">{{ loadError }}</p>

      <template v-if="trust">
        <div
          v-if="trust.enforcement !== 'enforce'"
          class="security-centre__banner"
          role="note"
        >
          <strong>
            {{ trust.enforcement === 'monitor' ? labels.monitorMode : labels.enforcementModes.unknown }}
          </strong>
          <span>
            {{ trust.enforcement === 'monitor' ? labels.monitorNote : labels.enforcementUnknownNote }}
          </span>
        </div>

        <section class="security-centre__block">
          <div class="security-centre__block-head">
            <h3>{{ labels.devicesTitle }}</h3>
            <span class="security-centre__tag">
              {{ labels.enforcement }}: {{ labels.enforcementModes[trust.enforcement] }}
            </span>
          </div>
          <p v-if="trust.subjects.length === 0" class="security-centre__muted">{{ labels.devicesEmpty }}</p>
          <ul v-else class="security-centre__devices">
            <li v-for="subject in trust.subjects" :key="subject.node_id" class="security-centre__device">
              <div class="security-centre__device-head">
                <span class="security-centre__mono">{{ subject.node_id }}</span>
                <span class="security-centre__state" :class="stateTone(subject)">{{ stateLabel(subject) }}</span>
              </div>

              <p
                v-if="subject.state && subject.effective_state && subject.state !== subject.effective_state"
                class="security-centre__muted"
              >
                {{ labels.recordedState(labels.states[subject.state] ?? subject.state) }}
              </p>
              <dl class="security-centre__facts">
                <div>
                  <dt>{{ labels.reason }}</dt>
                  <dd>{{ subject.effective_reason || subject.reason || labels.noReason }}</dd>
                </div>
                <div v-if="remainingText(subject)">
                  <dt>{{ labels.states.OBSERVATION }}</dt>
                  <dd>{{ remainingText(subject) }}</dd>
                </div>
              </dl>
              <DevicePolicyStatus :device="subject.device" :evidence="subject.evidence" :locale="String(locale)" />
              <p v-if="subject.record_verified === false" role="alert" class="security-centre__warning">
                {{ labels.recordDamaged }}
              </p>
              <p v-if="subject.otp_limited === true" class="security-centre__warning">{{ labels.otpLimited }}</p>
              <p v-if="subject.manual === true" class="security-centre__muted">{{ labels.manualHold }}</p>

              <div class="security-centre__allowed" role="group" :aria-label="labels.allowedTitle">
                <span class="security-centre__allowed-title">{{ labels.allowedTitle }}</span>
                <ul>
                  <li v-for="action in TRUST_ACTIONS" :key="action">
                    <span>{{ labels.actions[action] }}</span>
                    <span class="security-centre__chip" :class="allowance(subject, action).tone">
                      {{ allowance(subject, action).text }}
                    </span>
                  </li>
                </ul>
              </div>

              <details class="security-centre__more">
                <summary class="ui-disclosure-summary">{{ labels.counters }}</summary>
                <dl class="security-centre__facts">
                  <div><dt>{{ labels.simChanges }}</dt><dd>{{ count(subject.sim_changes) }}</dd></div>
                  <div><dt>{{ labels.identityChanges }}</dt><dd>{{ count(subject.identity_changes) }}</dd></div>
                  <div><dt>{{ labels.integrityFailures }}</dt><dd>{{ count(subject.integrity_failures) }}</dd></div>
                  <div :title="labels.riskScoreNote">
                    <dt>{{ labels.riskScore }}</dt><dd>{{ count(subject.risk_score) }}</dd>
                  </div>
                  <div><dt>{{ labels.updatedAt }}</dt><dd>{{ formatTime(subject.updated_at) }}</dd></div>
                </dl>
                <p class="security-centre__muted">{{ labels.riskScoreNote }}</p>
              </details>

              <div
                v-if="isPendingTrust(subject)"
                class="security-centre__confirm"
                role="group"
                :aria-label="labels.adminActionTitles[isPendingTrust(subject)]?.(subject.node_id)"
              >
                <strong>{{ labels.adminActionTitles[isPendingTrust(subject)]?.(subject.node_id) }}</strong>
                <ul>
                  <li v-for="line in labels.adminActionExplain[isPendingTrust(subject)]" :key="line">{{ line }}</li>
                  <li v-if="!enforcing">{{ labels.adminActionMonitorNote }}</li>
                </ul>
                <label class="security-centre__reason">
                  <span>{{ labels.reasonLabel }}</span>
                  <textarea
                    v-model="reason"
                    rows="2"
                    :placeholder="labels.reasonPlaceholder"
                    :disabled="working"
                    :aria-invalid="reasonMessage !== ''"
                    @blur="reasonTouched = true"
                  ></textarea>
                  <small :class="{ 'is-bad': reasonState === 'too_long' }">
                    {{ labels.reasonCount(reasonBytes(reason), REASON_LIMIT_BYTES) }}
                  </small>
                </label>
                <p v-if="reasonMessage" role="alert" class="security-centre__error">{{ reasonMessage }}</p>
                <div class="security-centre__actions">
                  <button
                    type="button"
                    :class="isPendingTrust(subject) === 'restore' ? 'primary-button' : 'danger-button'"
                    :disabled="working || reasonState !== ''"
                    @click="confirm"
                  >{{ working ? labels.working : labels.confirm }}</button>
                  <button type="button" class="secondary-button" :disabled="working" @click="cancel">
                    {{ labels.cancel }}
                  </button>
                </div>
              </div>
              <div v-else-if="trustActionsFor(subject).length" class="security-centre__actions">
                <button
                  v-for="action in trustActionsFor(subject)"
                  :key="action"
                  type="button"
                  class="secondary-button"
                  :disabled="working || verifying || pending !== null"
                  @click="open({ kind: 'trust', action, device: subject.node_id })"
                >{{ labels.adminActions[action] }}</button>
              </div>
              <p v-if="subjectState(subject) !== 'TRUSTED'" class="security-centre__muted">{{ issuerAdminNote }}</p>
              <button v-if="!isPendingTrust(subject) && subjectState(subject) !== 'TRUSTED'"
                type="button" class="secondary-button"
                :disabled="working || verifying || pending !== null || trust.issuer_admin_allowed !== true || subject.record_verified !== true || journalState !== 'ok'"
                :title="issuerAdminNote"
                @click="open({ kind: 'trust', action: 'approve', device: subject.node_id })"
              >{{ labels.adminActions.approve }}</button>
            </li>
          </ul>
        </section>

        <section class="security-centre__block">
          <h3>{{ labels.evidenceTitle }}</h3>
          <p class="security-centre__muted">{{ labels.evidenceNote }}</p>
          <ul class="security-centre__rows">
            <li v-for="key in EVIDENCE_KEYS" :key="key">
              <div class="security-centre__row-head">
                <strong>{{ labels.evidenceInputs[key] }}</strong>
                <span class="security-centre__chip" :class="{ 'is-ok': trust.evidence[key] === 'observed' }">
                  {{ labels.evidenceStates[trust.evidence[key]] }}
                </span>
              </div>
              <span class="security-centre__muted">
                {{ trust.evidence[key] === 'unknown' ? labels.evidenceReasons.unknown : labels.evidenceReasons[key] }}
              </span>
            </li>
          </ul>
        </section>

        <section class="security-centre__block">
          <h3>{{ labels.hardwareTitle }}</h3>
          <p class="security-centre__muted">{{ labels.hardwareNote }}</p>
          <ul class="security-centre__rows">
            <li v-for="item in HARDWARE_ITEMS" :key="item">
              <div class="security-centre__row-head">
                <strong>{{ labels.hardwareItems[item] }}</strong>
                <span class="security-centre__chip">{{ labels.hardwareUnsupported }}</span>
              </div>
              <span class="security-centre__muted">{{ labels.hardwareReasons[item] }}</span>
            </li>
          </ul>
        </section>

        <section v-if="journal" class="security-centre__block">
          <div class="security-centre__block-head">
            <h3>{{ labels.journalTitle }}</h3>
            <span
              class="security-centre__state"
              :class="journalState === 'ok' ? 'is-ok' : journalState === 'damaged' ? 'is-bad' : ''"
            >{{ labels.journalStates[journalState] }}</span>
          </div>
          <p v-if="journalState === 'unknown'" class="security-centre__muted">{{ labels.journalUnknownNote }}</p>
          <div v-if="journalState === 'damaged'" role="alert" class="security-centre__warning is-bad">
            <strong v-if="journal.first_bad_seq !== undefined">
              {{ labels.journalDamagedAt(journal.first_bad_seq) }}
            </strong>
            <span v-if="journal.problem">{{ labels.journalProblem }}: {{ journal.problem }}</span>
          </div>
          <p v-if="journal.sealed_from !== undefined" class="security-centre__warning">
            {{ labels.journalResealedFrom(journal.sealed_from) }}
          </p>
          <dl v-if="journalState !== 'unknown'" class="security-centre__facts">
            <div><dt>{{ labels.journalEvents }}</dt><dd>{{ count(journal.events) }}</dd></div>
            <div><dt>{{ labels.journalHead }}</dt><dd>{{ journal.head_seq === undefined ? labels.unknown : `#${journal.head_seq}` }}</dd></div>
            <div><dt>{{ labels.journalVerifiedAt }}</dt><dd>{{ formatTime(journal.verified_at) }}</dd></div>
          </dl>

          <div
            v-if="pending?.kind === 'reseal'"
            class="security-centre__confirm"
            role="group"
            :aria-label="labels.journalResealTitle"
          >
            <strong>{{ labels.journalResealTitle }}</strong>
            <ul>
              <li v-for="line in labels.journalResealExplain" :key="line">{{ line }}</li>
            </ul>
            <label class="security-centre__reason">
              <span>{{ labels.reasonLabel }}</span>
              <textarea
                v-model="reason"
                rows="2"
                :placeholder="labels.reasonPlaceholder"
                :disabled="working"
                :aria-invalid="reasonMessage !== ''"
                @blur="reasonTouched = true"
              ></textarea>
              <small :class="{ 'is-bad': reasonState === 'too_long' }">
                {{ labels.reasonCount(reasonBytes(reason), REASON_LIMIT_BYTES) }}
              </small>
            </label>
            <p v-if="reasonMessage" role="alert" class="security-centre__error">{{ reasonMessage }}</p>
            <div class="security-centre__actions">
              <button
                type="button"
                class="danger-button"
                :disabled="working || reasonState !== ''"
                @click="confirm"
              >{{ working ? labels.working : labels.journalResealConfirm }}</button>
              <button type="button" class="secondary-button" :disabled="working" @click="cancel">
                {{ labels.cancel }}
              </button>
            </div>
          </div>
          <div v-else class="security-centre__actions">
            <button
              type="button"
              class="secondary-button"
              :disabled="verifying || working || pending !== null"
              @click="verify"
            >{{ verifying ? labels.journalVerifying : labels.journalVerify }}</button>
            <!-- Offered for a journal that FAILED its check and for nothing
                 else: there is nothing to reseal in one that verifies, and one
                 never checked is not known to be damaged. -->
            <button
              v-if="journal.ok === false && journalState === 'damaged'"
              type="button"
              class="danger-button"
              :disabled="verifying || working || pending !== null"
              @click="open({ kind: 'reseal' })"
            >{{ labels.journalReseal }}</button>
          </div>

          <details class="security-centre__more" @toggle="journalOpen = ($event.target as HTMLDetailsElement).open">
            <summary class="ui-disclosure-summary">{{ labels.viewerOpen }}</summary>
            <!-- Mounted on first open, so the journal is not read by a visit
                 that never looks at it. -->
            <SecurityJournalViewer
              v-if="journalOpen"
              ref="viewer"
              :labels="labels"
              :locale="String(locale)"
              :sealed-from="journal.sealed_from"
              :devices="deviceIDs"
            />
          </details>
        </section>
      </template>

      <section class="security-centre__block">
        <h3>{{ labels.boundariesTitle }}</h3>
        <LinkBoundaryList :labels="labels" :posture="posture" :loaded="postureLoaded" />
      </section>
    </template>
  </section>
</template>

<style scoped>
.security-centre {
  display: grid;
  min-width: 0;
  gap: 16px;
  padding-top: 22px;
  border-top: 1px solid var(--border);
}

.security-centre__head,
.security-centre__block-head,
.security-centre__device-head,
.security-centre__row-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px 12px;
  min-width: 0;
}

.security-centre__head { align-items: flex-start; }
.security-centre__head > div { min-width: 0; }
.security-centre h2 { margin: 0; font-size: 16px; }
.security-centre h3 { margin: 0; font-size: 14px; }

.security-centre__head p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}

.security-centre__block {
  display: grid;
  min-width: 0;
  gap: 10px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.security-centre__muted,
.security-centre__notice,
.security-centre__error,
.security-centre__success,
.security-centre__warning {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.security-centre__muted { color: var(--muted); }
.security-centre__error { color: var(--danger-strong); }

.security-centre__notice {
  padding: 12px 14px;
  color: var(--muted-strong);
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.security-centre__success {
  padding: 10px 12px;
  color: var(--success);
  background: var(--success-soft);
  border: 1px solid var(--success-border);
  border-radius: var(--radius-sm);
}

.security-centre__warning,
.security-centre__banner {
  display: grid;
  gap: 3px;
  padding: 10px 12px;
  color: var(--warning-strong);
  background: var(--warning-soft);
  border: 1px solid var(--warning-border);
  border-radius: var(--radius-sm);
}

.security-centre__banner {
  font-size: 13px;
  line-height: 1.55;
}

.security-centre__warning.is-bad {
  color: var(--danger-strong);
  background: var(--danger-soft);
  border-color: var(--danger-border);
}

.security-centre__devices,
.security-centre__rows {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.security-centre__device,
.security-centre__rows > li {
  display: grid;
  min-width: 0;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.security-centre__rows > li { gap: 4px; }

.security-centre__row-head strong {
  min-width: 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}

/* A device ID is long and unbreakable by default; it must wrap inside the
   card at 360px, never push the state badge off the edge. */
.security-centre__mono {
  min-width: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.security-centre__state,
.security-centre__chip,
.security-centre__tag {
  padding: 2px 9px;
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.5;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.security-centre__state.is-ok,
.security-centre__chip.is-ok {
  color: var(--success);
  background: var(--success-soft);
  border-color: var(--success-border);
}

.security-centre__state.is-warn,
.security-centre__chip.is-warn {
  color: var(--warning-strong);
  background: var(--warning-soft);
  border-color: var(--warning-border);
}

.security-centre__state.is-bad,
.security-centre__chip.is-bad {
  color: var(--danger-strong);
  background: var(--danger-soft);
  border-color: var(--danger-border);
}

.security-centre__facts {
  display: grid;
  gap: 6px;
  margin: 0;
  font-size: 13px;
}

.security-centre__facts > div {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 2px 12px;
  min-width: 0;
}

.security-centre__facts dt { color: var(--muted); }

.security-centre__facts dd {
  min-width: 0;
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}

.security-centre__allowed {
  display: grid;
  gap: 6px;
  font-size: 13px;
}

.security-centre__allowed-title { color: var(--muted); font-size: 12px; }

.security-centre__allowed ul {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 210px), 1fr));
  gap: 6px 14px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.security-centre__allowed li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 2px 8px;
  min-width: 0;
}

.security-centre__more { font-size: 13px; }
.security-centre__more summary { color: var(--muted); cursor: pointer; }
.security-centre__more > :not(summary) { margin-top: 10px; }

.security-centre__confirm {
  display: grid;
  gap: 8px;
  padding: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.55;
}

.security-centre__confirm strong { overflow-wrap: anywhere; }

.security-centre__confirm ul {
  display: grid;
  gap: 4px;
  margin: 0;
  padding-left: 18px;
}

.security-centre__reason {
  display: grid;
  gap: 5px;
}

.security-centre__reason textarea {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  padding: 8px 10px;
  color: var(--text);
  font: inherit;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  resize: vertical;
}

.security-centre__reason small { color: var(--muted); font-size: 12px; }
.security-centre__reason small.is-bad { color: var(--danger-strong); }

.security-centre__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
</style>
