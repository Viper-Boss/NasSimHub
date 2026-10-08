<script setup lang="ts">
import { computed, ref } from 'vue'
import type { NodeSecurity } from '../api/nodeSecurity'
import type { NodeSecurityCopy } from '../i18n/nodeSecurityCopy'
import { formatRemaining } from '../utils/callDuration'
import DevicePolicyStatus from './DevicePolicyStatus.vue'

// The security block of one SIM Node card.
//
// FOUR SEPARATE FACTS, and the layout keeps them separate because none implies
// another: which key identifies the device (Ed25519, pinned at pairing), what
// it proved about a post-quantum key, what the gateway's session with it
// negotiated, and how far the trust engine lets it be used. A dual identity
// says nothing about the link's key exchange, and a hybrid key exchange says
// nothing about identity, so no row here summarises two of them into one word.
//
// UNKNOWN STAYS UNKNOWN. `security` arrives already reduced by
// readNodeSecurity, which turns every absent or unrecognised field into
// "unknown" / null. This component adds nothing on top: a tone is given only
// to a value the gateway sent, a gateway build that cannot verify ML-DSA gets a
// sentence saying so and neither a tick nor a cross, and "requested" and
// "negotiated" are two rows so a shortfall is visible rather than averaged.
//
// MONITOR MODE IS NOT PROTECTION. When the engine only records, the block says
// "recorded only, nothing is blocked" and never the sentence that outgoing use
// is refused, because in that mode it is not.

const props = defineProps<{
  security: NodeSecurity
  copy: NodeSecurityCopy
  /** The device is paired, so its Ed25519 key was pinned at pairing. */
  paired: boolean
  busy: boolean
  locale: string
}>()

const emit = defineEmits<{ reset: [] }>()

const confirming = ref(false)

const identity = computed(() => props.security.identity)
const link = computed(() => props.security.link_security)
const trust = computed(() => props.security.trust)

/**
 * The post-quantum identity line.
 *
 * `available` is about the GATEWAY BUILD. When it is false the outcome field
 * means nothing -- the gateway could not have verified anything -- so the
 * outcome is not shown at all, in any tone.
 */
const pqOutcome = computed(() => {
  const copy = props.copy
  if (identity.value.available === false) {
    return { text: copy.pqUnavailable(identity.value.backend), tone: '' }
  }
  if (identity.value.available === null) return { text: copy.pqNotReported, tone: '' }
  const outcome = identity.value.outcome
  const algorithm = identity.value.algorithm
  const text =
    outcome === 'dual' && algorithm
      ? `${copy.outcomes.dual} (Ed25519 + ${algorithm})`
      : (copy.outcomes[outcome] ?? copy.unknown)
  return {
    text,
    tone: outcome === 'dual' ? 'is-ok' : outcome === 'refused' ? 'is-bad' : ''
  }
})

const pqKnown = computed(() => identity.value.available === true)

const pinTone = computed(() => {
  switch (identity.value.pin_state) {
    case 'pinned':
      return 'is-ok'
    case 'changed':
    case 'withdrawn':
      return 'is-warn'
    default:
      return ''
  }
})

/** The administrator may accept a deliberate key change, and only then. */
const resetOffered = computed(
  () => identity.value.pin_state === 'changed' || identity.value.pin_state === 'withdrawn'
)

const policyText = computed(() => {
  const policy = identity.value.policy
  if (!policy) return ''
  if (identity.value.enforced === true) return `${policy} · ${props.copy.policyEnforced}`
  if (identity.value.enforced === false) return `${policy} · ${props.copy.policyReportOnly}`
  return policy
})

const negotiated = computed(() => {
  if (!link.value.known) return props.copy.unknown
  const parts = [link.value.group, link.value.tls_version].filter(Boolean)
  return parts.length ? parts.join(' · ') : props.copy.unknown
})

/** Only an observed session can fall short; an unobserved one is unknown. */
const shortfall = computed(() => link.value.known && link.value.meets_level === false)

const trustState = computed(() => trust.value.effective_state || trust.value.state)

const trustLabel = computed(() => {
  if (!trust.value.known || !trustState.value) return props.copy.trustUnknown
  return props.copy.trustStates[trustState.value] ?? trustState.value
})

/**
 * Green only for a trusted device on a gateway that enforces. In monitor mode
 * the state is a calculation nothing acts on, and colouring it as a pass would
 * present the mode as protection.
 */
const trustTone = computed(() => {
  if (!trust.value.known) return ''
  switch (trustState.value) {
    case 'TRUSTED':
      return trust.value.enforced === true ? 'is-ok' : ''
    case 'RESTRICTED':
    case 'QUARANTINE':
      return 'is-bad'
    case 'OBSERVATION':
      return 'is-warn'
    default:
      return ''
  }
})

const remaining = computed(() => {
  const seconds = trust.value.remaining_seconds
  if (trustState.value !== 'OBSERVATION' || !seconds) return ''
  const time = formatRemaining(seconds, {
    day: props.copy.day,
    hour: props.copy.hour,
    minute: props.copy.minute,
    underMinute: props.copy.underMinute
  }).trim()
  return time ? props.copy.trustRemaining(time) : ''
})

/** What to say about outgoing use, chosen by whether anything is refused. */
const outgoingNote = computed(() => {
  if (!trust.value.known || trust.value.outgoing_allowed !== false) return ''
  if (trust.value.enforced === true) return props.copy.outgoingBlocked
  if (trust.value.enforced === false) return props.copy.outgoingWouldBlock
  return props.copy.outgoingBlockedUnknownMode
})

function formatTime(value: string): string {
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? '' : new Date(parsed).toLocaleString(props.locale)
}

function confirmReset() {
  confirming.value = false
  emit('reset')
}
</script>

<template>
  <section class="node-security" :aria-label="copy.title">
    <h3>{{ copy.title }}</h3>

    <p v-if="security.identity_refused" role="alert" class="node-security__alert">
      <strong>{{ copy.identityRefused }}</strong>
      <span>{{ identity.reason || copy.identityRefusedNoReason }}</span>
    </p>

    <dl>
      <div v-if="paired">
        <dt>{{ copy.classicalIdentity }}</dt>
        <dd>{{ copy.classicalPinned }}</dd>
      </div>
      <div>
        <dt>{{ copy.pqIdentity }}</dt>
        <dd :class="pqOutcome.tone">{{ pqOutcome.text }}</dd>
      </div>
      <template v-if="pqKnown">
        <div>
          <dt>{{ copy.pqPin }}</dt>
          <dd :class="pinTone">{{ copy.pinStates[identity.pin_state] ?? copy.unknown }}</dd>
        </div>
        <div v-if="identity.fingerprint">
          <dt>{{ copy.pqFingerprint }}</dt>
          <dd class="node-security__mono">
            <template v-if="identity.algorithm">{{ identity.algorithm }} · </template>{{ identity.fingerprint }}
          </dd>
        </div>
        <div v-if="identity.pinned_at && formatTime(identity.pinned_at)">
          <dt>{{ copy.pinnedAt }}</dt>
          <dd>{{ formatTime(identity.pinned_at) }}</dd>
        </div>
      </template>
      <div v-if="policyText">
        <dt>{{ copy.policy }}</dt>
        <dd>{{ policyText }}</dd>
      </div>
      <div v-if="identity.reason && !security.identity_refused">
        <dt>{{ copy.identityNote }}</dt>
        <dd>{{ identity.reason }}</dd>
      </div>

      <!-- Requested and negotiated are two rows on purpose: what was asked for
           and what was obtained are different facts. -->
      <div>
        <dt>{{ copy.requestedLevel }}</dt>
        <dd>{{ link.requested_level || copy.unknown }}</dd>
      </div>
      <div>
        <dt>{{ copy.negotiatedGroup }}</dt>
        <dd :class="{ 'is-warn': shortfall }">{{ negotiated }}</dd>
      </div>
      <div>
        <dt>{{ copy.linkPostQuantum }}</dt>
        <dd :class="{ 'is-ok': link.post_quantum === 'yes' }">
          {{ copy.tristate[link.post_quantum] }}
        </dd>
      </div>

      <div>
        <dt>{{ copy.trust }}</dt>
        <dd :class="trustTone">{{ trustLabel }}</dd>
      </div>
      <div v-if="trust.known && trust.reason">
        <dt>{{ copy.trustReason }}</dt>
        <dd>{{ trust.reason }}</dd>
      </div>
    </dl>
    <DevicePolicyStatus :device="trust.device" :locale="locale" />

    <p v-if="identity.available === true && identity.attestation_supported === false" class="node-security__note">
      {{ copy.noAttestation }}
    </p>
    <p v-if="shortfall" role="alert" class="node-security__warning">
      {{ link.required_group ? copy.linkShortfall(link.required_group) : copy.linkShortfallNoGroup }}
    </p>
    <p v-else-if="!link.known" class="node-security__note">{{ copy.linkNotObserved }}</p>
    <p v-if="link.known && link.node_group && link.node_group !== link.group" class="node-security__note">
      {{ copy.nodeGroupDiffers(link.node_group) }}
    </p>
    <p v-if="remaining" class="node-security__note">{{ remaining }}</p>
    <p v-if="trust.known && trust.enforced === false" class="node-security__warning">
      {{ copy.monitorMode }}
    </p>
    <p v-if="outgoingNote" class="node-security__note">{{ outgoingNote }}</p>

    <template v-if="resetOffered">
      <div v-if="confirming" class="node-security__confirm" role="group" :aria-label="copy.resetTitle">
        <strong>{{ copy.resetTitle }}</strong>
        <ul>
          <li v-for="line in copy.resetExplain" :key="line">{{ line }}</li>
        </ul>
        <div class="node-security__actions">
          <button type="button" class="danger-button" :disabled="busy" @click="confirmReset">
            {{ busy ? copy.resetting : copy.resetConfirm }}
          </button>
          <button type="button" class="secondary-button" :disabled="busy" @click="confirming = false">
            {{ copy.resetCancel }}
          </button>
        </div>
      </div>
      <div v-else class="node-security__actions">
        <button type="button" class="secondary-button" :disabled="busy" @click="confirming = true">
          {{ busy ? copy.resetting : copy.resetOffer }}
        </button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.node-security {
  display: grid;
  min-width: 0;
  gap: 10px;
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

.node-security h3 {
  margin: 0;
  color: var(--muted-strong);
  font-size: 13px;
  font-weight: 700;
}

.node-security dl {
  display: grid;
  gap: 8px;
  margin: 0;
}

/* Label over value below the width at which a long group name or a Chinese
   sentence would otherwise be squeezed beside its label. */
.node-security dl > div {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 2px 14px;
}

.node-security dt {
  color: var(--muted);
}

.node-security dd {
  min-width: 0;
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}

.node-security dd.is-ok { color: var(--success); }
.node-security dd.is-warn { color: var(--warning-strong); }
.node-security dd.is-bad { color: var(--danger-strong); }

.node-security__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.node-security__note,
.node-security__warning,
.node-security__alert {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.node-security__note { color: var(--muted); }

.node-security__warning {
  padding: 8px 10px;
  color: var(--warning-strong);
  background: var(--warning-soft);
  border: 1px solid var(--warning-border);
  border-radius: var(--radius-sm);
}

.node-security__alert {
  display: grid;
  gap: 2px;
  padding: 8px 10px;
  color: var(--danger-strong);
  background: var(--danger-soft);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-sm);
}

.node-security__confirm {
  display: grid;
  gap: 8px;
  padding: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.55;
}

.node-security__confirm ul {
  display: grid;
  gap: 4px;
  margin: 0;
  padding-left: 18px;
}

.node-security__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
</style>
