<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  CircleAlert,
  CircleCheck,
  CircleOff,
  Clock,
  Cpu,
  ExternalLink,
  FileBadge,
  Gauge,
  Globe2,
  HardDrive,
  Hourglass,
  Info,
  ListFilter,
  LockKeyhole,
  MessageSquareText,
  Network,
  PhoneForwarded,
  RefreshCw,
  ShieldAlert,
  ShieldCheck
} from '@lucide/vue'

import './networkCard.css'
import './panelCard.css'
import { getCallFilter, getVowifiDiagnostics } from '../api/client'
import type { CallFilterPolicy, VowifiEgressProbe } from '../api/client'
import { getModuleSecurity } from '../api/dashboardCards'
import type { ModuleSecurity, ModuleTrust, SecurityRow, SecurityState } from '../api/dashboardCards'
import { getTrustDocument, journalStatus, trustCounts } from '../api/nodeSecurity'
import type { TrustDocument } from '../api/nodeSecurity'
import { i18n, resolvedLocale } from '../i18n'
import { moduleSecurityCardLabels } from '../i18n/moduleSecurityCardLabels'
import { sessionState } from '../state/session'

// The module-security card.
//
// WHY IT EXISTS, because it decides what it is allowed to say. A gateway that
// places calls and sends messages can be used to defraud people, and whoever
// ships it answers for that. This card is where an operator sees what is
// actually restraining the box today.
//
// That purpose is the reason this card is stricter about honesty than the ones
// above it, not more relaxed. It was drawn from a mock-up showing a risk badge
// reading "low", a device-trust observation period counting down, and four
// green ticks for module integrity, system integrity, TPM and firmware
// signature. None of those six things exists anywhere in this product: there
// is no scoring model, no trust tier, no TPM, no secure boot, and nothing that
// checks a firmware signature. Rendering them as passes would be a compliance
// claim with nothing behind it, on the one screen most likely to be
// screenshotted as evidence that the box is safe.
//
// So the tiles are here, because they are the right tiles and the backend is
// being written to this shape -- but each reports what the code does today,
// and the parts that ARE real are wired to their real endpoints:
//
//   the guard's budgets and today's use  GET /api/v1/security/module -> guard
//   the public egress IP and ISP         GET /api/v1/vowifi-diagnostics -> egress
//   the inbound call filter policy       GET /api/v1/settings/homesim/call-filter
//   device trust and the journal's state GET /api/v1/security/trust
//
// DEVICE TRUST IS NOW ONE OF THE REAL ONES. The gateway keeps a trust record
// per device (internal/trustengine), so that tile reads it: how many devices
// are in which state, and -- before anything else -- whether the journal those
// states rest on failed its check, or whether the engine is only monitoring.
// It is a summary; the per-device detail, the reasons and the administrator's
// actions are in the security centre the footer links to.
//
// THE FOUR INTEGRITY TILES AND THE RISK LEVEL STILL HAVE NOTHING BEHIND THEM,
// and no longer say "not wired" as if a wire were on its way: a UFI003 gives
// the gateway no TPM, TEE or boot-chain evidence to check, so they read "not
// supported" with that reason until a payload says otherwise.
//
// THE CENTREPIECE IS REAL. The anti-fraud guard in
// internal/httpapi/call_guard.go is the one protection this product actually
// runs: a daily cap on how many DISTINCT numbers can be reached, five for
// calls and three for messages, enforced at the send and dial handlers with a
// 403. The gateway now reports how much of each allowance today has used; an
// older gateway that does not leaves the count blank rather than invented.
//
// WHAT THIS CARD DELIBERATELY DOES NOT DUPLICATE: the transport-security rows
// (the secure-transport card in ModuleExtraCards.vue, with its own
// /api/v1/security/posture contract) and the 24-hour call and message counts
// (the communication-summary card in that same component, against
// /api/v1/stats/communication). The mock-up's top row of 24-hour counters is
// that card's job; the top row here is the guard's allowance, which is a
// different number -- distinct destinations, not messages -- and only this
// card has it.
//
// NO SCROLLING INSIDE. See panelCard.css: the card is laid out to fit beside
// the network drawings rather than pinned to their height with a scrolling
// middle, so a wheel over it scrolls the page like every other card.

const { t } = useI18n()

const security = ref<ModuleSecurity | null>(null)
// The trust document, or null when the gateway has none to give (an older
// gateway, no engine attached, a failed read). Null falls back to the
// "not assessed" wording; it is never read as "nothing is wrong".
const trustDocument = ref<TrustDocument | null>(null)
const loaded = ref(false)
const egress = ref<VowifiEgressProbe | null>(null)
const egressFailed = ref(false)
const filter = ref<CallFilterPolicy | null>(null)
const filterRuntimeReady = ref(false)
const filterFailed = ref(false)
const checking = ref(false)
// The clock the reset countdown is measured against, moved once a minute.
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
let clock: ReturnType<typeof setInterval> | undefined

// Merged on a locale watch rather than once in onMounted: mergeLocaleMessage
// is a store mutation t() does not track, so a card whose state barely changes
// renders raw key names until something else forces an update. Measured on the
// cards above this one.
watch(
  () => i18n.global.locale.value,
  () => {
    const locale = resolvedLocale()
    i18n.global.mergeLocaleMessage(locale, {
      moduleSecurityCard: moduleSecurityCardLabels(locale)
    })
  },
  { immediate: true, flush: 'sync' }
)

function label(key: string, values?: Record<string, unknown>): string {
  return values ? t(`moduleSecurityCard.${key}`, values) : t(`moduleSecurityCard.${key}`)
}

/**
 * Administrators only.
 *
 * The call filter and the guard unlock are both admin-gated server side, and
 * the egress probe reports the gateway's public address. None of that belongs
 * on a member's dashboard.
 */
const visible = computed(() => sessionState.role === 'admin')

/**
 * The shipped defaults for the guard, used only when the backend has not
 * reported its own.
 *
 * These mirror callGuardDailyLimit and messageGuardDailyLimit in
 * internal/httpapi/call_guard.go. They are shown as POLICY, labelled as the
 * factory default, and never as a reading from this gateway -- because that is
 * what they are, and because if somebody changes the Go constant this card
 * would otherwise quietly keep claiming the old number.
 */
const DEFAULT_CALL_LIMIT = 5
const DEFAULT_MESSAGE_LIMIT = 3

const guard = computed(() => security.value?.guard)

const guardLimits = computed(() => ({
  call: guard.value?.call_limit ?? DEFAULT_CALL_LIMIT,
  message: guard.value?.message_limit ?? DEFAULT_MESSAGE_LIMIT,
  // True when both numbers came from this file rather than the gateway.
  fromDefaults: guard.value?.call_limit === undefined && guard.value?.message_limit === undefined
}))

/**
 * One allowance tile: today's distinct numbers against the cap.
 *
 * `used` is undefined when the gateway did not report it, and then the tile
 * shows the cap alone -- never "0 used", which would be a claim about a day
 * nothing measured.
 */
function allowance(used: number | undefined, limit: number) {
  const known = used !== undefined
  const share = known && limit > 0 ? Math.min(1, used / limit) : 0
  return { used, limit, known, percent: Math.round(share * 100), full: known && used >= limit }
}

const callAllowance = computed(() => allowance(guard.value?.call_used, guardLimits.value.call))
const messageAllowance = computed(() =>
  allowance(guard.value?.message_used, guardLimits.value.message)
)

/**
 * When today's counts go back to zero, from the instant the gateway reported.
 *
 * Never worked out here from the browser's own midnight: the guard rolls over
 * at the GATEWAY's local midnight, and a container with no time zone set is on
 * UTC -- eight in the morning in Beijing. A gateway too old to report it gets
 * "daily at midnight, gateway time", which is true and claims nothing more.
 */
const guardReset = computed(() => {
  const at = guard.value?.resets_at
  if (!at) return null
  const instant = Date.parse(at)
  const remaining = instant - now.value
  const time = new Date(instant).toLocaleTimeString(resolvedLocale(), {
    hour: '2-digit',
    minute: '2-digit'
  })
  if (remaining <= 0) return { amount: '', unit: label('guardResetDue'), time }
  const minutes = Math.ceil(remaining / 60_000)
  return minutes >= 60
    ? { amount: String(Math.floor(minutes / 60)), unit: label('guardResetHours'), time }
    : { amount: String(minutes), unit: label('guardResetMinutes'), time }
})

/**
 * An unlocked guard is the most important state this card can show, so it gets
 * its own treatment rather than a grey row: it is the condition in which
 * nothing caps how many strangers the box can reach.
 */
const guardUnlocked = computed(() => guard.value?.unlocked === true)

/** The pill in the head reports the guard -- the one thing that runs. */
const badge = computed(() => {
  if (guardUnlocked.value) return { tone: 'is-warn', text: label('guardUnlocked') }
  if (!loaded.value) return { tone: 'is-idle', text: label('guardReading') }
  return { tone: '', text: label('guardLocked') }
})

const riskRow = computed<SecurityRow | undefined>(() => security.value?.risk)

const integrityRows = computed(() => {
  const rows = security.value?.integrity
  return [
    { key: 'module', icon: Cpu, name: label('integrityModule'), row: rows?.module },
    { key: 'system', icon: HardDrive, name: label('integritySystem'), row: rows?.system },
    { key: 'secureBoot', icon: LockKeyhole, name: label('integritySecureBoot'), row: rows?.secure_boot },
    { key: 'firmware', icon: FileBadge, name: label('integrityFirmware'), row: rows?.firmware }
  ]
})

const trust = computed(() => security.value?.trust)

// The two values read straight off the payload. Written as functions of the
// row so the fallback sits right beside the read: there is no path by which a
// level or a trust state appears without the gateway having sent it.
function riskValue(riskRow?: SecurityRow): string {
  return riskRow?.value || label('riskPending')
}

function trustValue(trust?: ModuleTrust): string {
  return trust?.value || label('trustPending')
}

const trustRemaining = computed(() => {
  const hours = trust.value?.remaining_hours
  if (hours === undefined) return ''
  return label('trustRemaining', { d: Math.floor(hours / 24), h: hours % 24 })
})

/**
 * The trust tile, from the trust document.
 *
 * In order of what an operator most needs to know: a journal that FAILED its
 * check (every device is then held as restricted), monitor mode (the states
 * are real and nothing is refused), and only then the count per state.
 *
 * The tile is green in exactly one case: the gateway enforces, the journal
 * verified, and every device it knows is trusted. Monitor mode is never green
 * and neither is a mode the gateway did not report -- a tick there would
 * present a calculation nothing acts on as protection.
 */
const trustTile = computed<{ state?: SecurityState; value: string; detail: string } | null>(() => {
  const document = trustDocument.value
  if (!document) return null
  const journal = journalStatus(document.journal)
  if (journal === 'damaged') {
    return { state: 'warn', value: label('trustJournalDamaged'), detail: label('trustJournalDamagedNote') }
  }
  const counts = trustCounts(document)
  const order = ['QUARANTINE', 'RESTRICTED', 'OBSERVATION', 'TRUSTED', 'UNBOUND'] as const
  const parts = order
    .filter(state => counts[state])
    .map(state => `${label(`trustState${state}`)} ${counts[state]}`)
  const summary = parts.length ? parts.join(' · ') : label('trustNoDevices')
  if (document.enforcement !== 'enforce') {
    // The mode and how many devices, and no more: per-state counts beside
    // "monitor only" would read as a verdict nothing acts on, and a tile this
    // size has no room for both. The counts are in the security centre.
    return {
      value: `${label(document.enforcement === 'monitor' ? 'trustMonitor' : 'trustModeUnknown')} · ${label('trustDevices', { n: document.subjects.length })}`,
      detail: label(document.enforcement === 'monitor' ? 'trustMonitorNote' : 'trustModeUnknownNote')
    }
  }
  const held = (counts.QUARANTINE ?? 0) + (counts.RESTRICTED ?? 0)
  const allTrusted =
    journal === 'ok' && (counts.TRUSTED ?? 0) > 0 && counts.TRUSTED === document.subjects.length
  return {
    state: held > 0 ? 'warn' : allTrusted ? 'ok' : undefined,
    value: summary,
    detail: label('trustEnforcedNote')
  }
})

/**
 * The six assessment tiles: four integrity checks, the risk level and device
 * trust. Every value comes from a payload; an absent one reads "not
 * supported" or "not assessed" with the reason, which today is the first five.
 */
const assessmentTiles = computed(() => [
  ...integrityRows.value.map(entry => ({
    key: entry.key,
    icon: entry.icon,
    name: entry.name,
    state: entry.row?.state,
    value: entry.row?.value || label('integrityPending'),
    detail: entry.row?.detail || label('integrityNote')
  })),
  {
    key: 'risk',
    icon: Gauge,
    name: label('riskLabel'),
    state: riskRow.value?.state,
    value: riskValue(riskRow.value),
    detail: riskRow.value?.detail || label('riskPendingNote')
  },
  trustTile.value
    ? { key: 'trust', icon: Hourglass, name: label('trustTitle'), ...trustTile.value }
    : {
        key: 'trust',
        icon: Hourglass,
        name: label('trustTitle'),
        state: trust.value?.state,
        value: trustValue(trust.value),
        detail: trustRemaining.value || label('trustPendingNote')
      }
])

const filterModeLabel = computed(() => {
  switch (filter.value?.mode) {
    case 'blacklist':
      return label('filterBlacklist')
    case 'whitelist':
      return label('filterWhitelist')
    case 'off':
      return label('filterOff')
    default:
      return label('notAvailable')
  }
})

const filterEntryCount = computed(() => {
  const policy = filter.value
  if (!policy) return undefined
  return policy.mode === 'whitelist' ? policy.whitelist.length : policy.blacklist.length
})

/** The filter tile's value: the mode, and the list size when a list is in use. */
const filterSummary = computed(() => {
  const policy = filter.value
  if (!policy) return label('filterUnavailable')
  if (policy.mode === 'off') return filterModeLabel.value
  return `${filterModeLabel.value} · ${label('filterEntriesValue', { n: filterEntryCount.value ?? 0 })}`
})

/** What the mode in force does to an incoming call, in words. Nothing when unread. */
const filterSub = computed(() => {
  switch (filter.value?.mode) {
    case 'blacklist':
      return label('filterSubBlacklist')
    case 'whitelist':
      return label('filterSubWhitelist')
    case 'off':
      return label('filterSubOff')
    default:
      return ''
  }
})

const egressIP = computed(
  () =>
    egress.value?.public_ip ||
    (egressFailed.value || egress.value?.available === false
      ? label('egressUnavailable')
      : label('egressPending'))
)

const egressISP = computed(
  () =>
    egress.value?.isp ||
    (egressFailed.value || egress.value?.available === false
      ? label('egressUnavailable')
      : label('egressPending'))
)

function stateTone(state?: SecurityState): string {
  switch (state) {
    case 'ok':
      return 'is-good'
    case 'warn':
      return 'is-warn'
    default:
      // "off" and "unknown" both draw as absent rather than as a failure:
      // nothing is wrong, the thing is not running.
      return 'is-idle'
  }
}

function stateIcon(state?: SecurityState) {
  switch (state) {
    case 'ok':
      return CircleCheck
    case 'warn':
      return CircleAlert
    default:
      return CircleOff
  }
}

/** Reads the two endpoints that are real. Never throws. */
async function refreshLive(): Promise<void> {
  const [diagnostics, callFilter] = await Promise.allSettled([
    getVowifiDiagnostics(),
    getCallFilter()
  ])
  if (diagnostics.status === 'fulfilled') {
    egress.value = diagnostics.value.egress
    egressFailed.value = false
  } else {
    egress.value = null
    egressFailed.value = true
  }
  if (callFilter.status === 'fulfilled') {
    filter.value = callFilter.value.filter
    filterRuntimeReady.value = callFilter.value.runtime_ready
    filterFailed.value = false
  } else {
    filter.value = null
    filterFailed.value = true
  }
}

async function load(): Promise<void> {
  if (checking.value) return
  checking.value = true
  try {
    security.value = await getModuleSecurity()
    // Read fresh every time and replaced whole: a failed read clears the tile
    // back to "not assessed" instead of leaving the last answer up as current.
    trustDocument.value = await getTrustDocument().catch(() => null)
    await refreshLive()
  } finally {
    checking.value = false
    loaded.value = true
    now.value = Date.now()
  }
}

onMounted(() => {
  if (!visible.value) return
  void load()
  // Five minutes, matching the cards above. The egress probe reaches the
  // public internet, so a tighter poll would be a request every visitor's
  // open dashboard makes for a value that changes about never.
  timer = setInterval(() => void load(), 300_000)
  clock = setInterval(() => {
    now.value = Date.now()
  }, 60_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  if (clock) clearInterval(clock)
  timer = undefined
  clock = undefined
})
</script>

<template>
  <article
    v-if="visible"
    class="modsec-card net-card pc-card"
    :class="{ 'is-unlocked': guardUnlocked }"
  >
    <header class="net-card__head">
      <div>
        <div class="net-card__title" :title="label('purpose')">
          <ShieldAlert v-if="guardUnlocked" class="net-card__icon modsec-card__alarm-icon" />
          <ShieldCheck v-else class="net-card__icon" />
          <h4>{{ label('title') }}</h4>
        </div>
        <p class="net-card__eyebrow">{{ label('subtitle') }}</p>
      </div>
      <span class="net-card__badge" :class="badge.tone">{{ badge.text }}</span>
    </header>

    <div class="pc-body">
      <!-- The one protection that actually runs, and the loudest thing here. -->
      <section class="pc-section modsec-card__guard" :class="{ 'is-unlocked': guardUnlocked }">
        <div class="pc-section__head">
          <ShieldCheck :size="15" />
          <h5>{{ label('guardTitle') }}</h5>
          <!-- A lifted guard takes the heading's second place, in amber: with
               the pill, the border and the header icon it is the one thing on
               this card that reads as an alarm. The full sentence is the
               tooltip, so the panel keeps its height and the row its line. -->
          <span
            v-if="guardUnlocked"
            class="pc-section__aside modsec-card__note--alarm"
            :title="label('guardUnlockedNote')"
          >
            {{ label('guardUnlockedShort') }}
          </span>
          <span v-else class="pc-section__aside" :title="label('guardWhat')">
            {{ label('guardWhatShort') }}
          </span>
        </div>

        <div class="pc-tiles pc-tiles--pair">
          <div
            v-for="entry in [
              { key: 'calls', icon: PhoneForwarded, name: label('guardTodayCalls'), usage: callAllowance },
              { key: 'messages', icon: MessageSquareText, name: label('guardTodayMessages'), usage: messageAllowance }
            ]"
            :key="entry.key"
            class="pc-tile modsec-card__allowance"
          >
            <span class="pc-tile__label">
              <component :is="entry.icon" :size="14" />
              <span>{{ entry.name }}</span>
            </span>
            <span v-if="entry.usage.known" class="modsec-card__count">
              <strong>{{ entry.usage.used }}</strong>
              <span>{{ label('guardOfLimit', { n: entry.usage.limit }) }}</span>
            </span>
            <span v-else class="modsec-card__count" :title="label('guardUsedNote')">
              <strong>{{ entry.usage.limit }}</strong>
              <span>{{ label('guardPerDay') }}</span>
            </span>
            <span
              v-if="entry.usage.known"
              class="pc-bar"
              :class="{ 'is-full': entry.usage.full && !guardUnlocked }"
              role="meter"
              :aria-label="entry.name"
              aria-valuemin="0"
              :aria-valuemax="entry.usage.limit"
              :aria-valuenow="entry.usage.used"
            >
              <span :style="{ width: `${entry.usage.percent}%` }"></span>
            </span>
            <!-- Said on the tile it qualifies, not in a note under the panel:
                 the cap here is the shipped default, not read from this
                 gateway. -->
            <span v-else-if="guardLimits.fromDefaults" class="pc-tile__sub" :title="label('guardUsedNote')">
              {{ label('guardDefaultNote') }}
            </span>
            <span v-else class="pc-tile__sub">{{ label('guardUsedPending') }}</span>
          </div>

          <!-- The inbound side of the same job: the policy that keeps scam
               calls out. Real today, read from the call-filter endpoint. -->
          <div
            class="pc-tile modsec-card__allowance"
            :title="
              filter
                ? label('filterMinReportsValue', { n: filter.min_reports })
                : label('filterUnavailable')
            "
          >
            <span class="pc-tile__label">
              <ListFilter :size="14" />
              <span>{{ label('filterTitle') }}</span>
            </span>
            <span class="modsec-card__word">{{ filterSummary }}</span>
            <!-- Saying so is the point: rules that are stored but not
                 enforced look identical to rules that are blocking calls. -->
            <span
              v-if="filter && filter.mode !== 'off' && !filterRuntimeReady"
              class="pc-tile__sub pc-note--warn"
              :title="label('filterNotReadyNote')"
            >
              {{ label('filterNotReady') }}
            </span>
            <span v-else-if="filterSub" class="pc-tile__sub">{{ filterSub }}</span>
          </div>

          <!-- When the two counts above go back to zero. -->
          <div class="pc-tile modsec-card__allowance">
            <span class="pc-tile__label">
              <Clock :size="14" />
              <span>{{ label('guardResetTitle') }}</span>
            </span>
            <span v-if="guardReset" class="modsec-card__count">
              <strong v-if="guardReset.amount">{{ guardReset.amount }}</strong>
              <span>{{ guardReset.unit }}</span>
            </span>
            <span v-else class="modsec-card__word">{{ label('guardResetDaily') }}</span>
            <span class="pc-tile__sub">
              {{ guardReset ? label('guardResetAt', { time: guardReset.time }) : label('guardResetLocal') }}
            </span>
          </div>
        </div>

      </section>

      <!-- Four integrity tiles and a risk level with nothing behind them, which
           say so with the reason; and device trust, which is read from the
           trust engine. None is green without a payload that says so. -->
      <section class="pc-section">
        <div class="pc-section__head">
          <ShieldCheck :size="15" />
          <h5>{{ label('integrityTitle') }}</h5>
          <span class="pc-section__aside" :title="label('integrityNote')">
            {{ label('integrityNoteShort') }}
          </span>
        </div>
        <div class="pc-tiles modsec-card__assessment">
          <div
            v-for="tile in assessmentTiles"
            :key="tile.key"
            class="pc-tile"
            :class="{ 'is-idle': stateTone(tile.state) === 'is-idle' }"
            :title="tile.detail"
          >
            <span class="pc-tile__label">
              <component :is="tile.icon" :size="13" />
              <span>{{ tile.name }}</span>
            </span>
            <span class="pc-state" :class="stateTone(tile.state)">
              <component :is="stateIcon(tile.state)" :size="13" />
              {{ tile.value }}
            </span>
          </div>
        </div>
      </section>

      <!-- Real today. -->
      <section class="pc-section">
        <div class="pc-section__head">
          <Globe2 :size="15" />
          <h5>{{ label('egressTitle') }}</h5>
          <span class="pc-section__aside" :title="label('egressNote')">
            {{ label('egressNoteShort') }}
          </span>
        </div>
        <div class="pc-tiles pc-tiles--pair">
          <div class="pc-tile">
            <span class="pc-tile__label"><Globe2 :size="13" /><span>{{ label('egressIP') }}</span></span>
            <span class="pc-tile__value pc-tile__value--mono">{{ egressIP }}</span>
          </div>
          <div class="pc-tile">
            <span class="pc-tile__label"><Network :size="13" /><span>{{ label('egressISP') }}</span></span>
            <span class="pc-tile__value">{{ egressISP }}</span>
          </div>
        </div>
      </section>

      <footer class="pc-foot">
        <!-- Named for where it goes: the security centre under Settings ->
             Security, where the trust records this card summarises are listed
             per device with their reasons, the journal and the link
             boundaries. -->
        <RouterLink
          class="pc-button"
          :to="{ name: 'settings', params: { section: 'security' } }"
        >
          {{ label('details') }}
          <ExternalLink :size="14" />
        </RouterLink>
        <button
          type="button"
          class="pc-button pc-button--primary"
          :class="{ 'is-working': checking }"
          :disabled="checking"
          :title="label('recheckNote')"
          @click="load"
        >
          <RefreshCw :size="15" :class="{ 'pc-spin': checking }" />
          {{ checking ? label('rechecking') : label('recheck') }}
        </button>
        <span class="pc-icon-hint" :title="label('recheckNote')">
          <Info :size="14" />
        </span>
      </footer>
    </div>
  </article>
</template>

<style scoped>
/* An unlocked guard is the one state on this card worth interrupting for. */
.modsec-card.is-unlocked {
  border-color: var(--pc-warn-line);
}

.modsec-card__alarm-icon { color: var(--pc-warn); }

.modsec-card__guard .modsec-card__note--alarm {
  color: var(--pc-warn);
  font-weight: 600;
}

.modsec-card__guard {
  background: linear-gradient(150deg, var(--pc-hero-top), var(--pc-hero-bottom));
  border-color: var(--pc-hero-line);
}

.modsec-card__guard.is-unlocked { border-color: var(--pc-warn-line); }

.modsec-card__allowance { gap: 7px; }

.modsec-card__count {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px 6px;
  color: var(--pc-label);
  font-size: 11.5px;
}

.modsec-card__word {
  color: var(--pc-value);
  font-size: 17px;
  font-weight: 600;
  line-height: 1.35;
}

.modsec-card__count strong {
  color: var(--pc-value);
  font-size: 24px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}

/* Two across at the narrowest track, three once there is room, so the six
   tiles make two even rows rather than a row of five and an orphan. */
.modsec-card__assessment {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.modsec-card { container-type: inline-size; }

@container (min-width: 360px) {
  .modsec-card__assessment { grid-template-columns: repeat(3, minmax(0, 1fr)); }
}
</style>
