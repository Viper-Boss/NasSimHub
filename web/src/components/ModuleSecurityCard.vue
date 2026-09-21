<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  CircleAlert,
  CircleCheck,
  CircleOff,
  Clock,
  ExternalLink,
  Globe2,
  Info,
  ListFilter,
  MessageSquareText,
  PhoneForwarded,
  RefreshCw,
  ShieldAlert,
  ShieldCheck
} from '@lucide/vue'

import { getCallFilter, getVowifiDiagnostics } from '../api/client'
import type { CallFilterPolicy, VowifiEgressProbe } from '../api/client'
import { getModuleSecurity } from '../api/dashboardCards'
import type { ModuleSecurity, SecurityRow, SecurityState } from '../api/dashboardCards'
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
// verifies a firmware signature. Rendering them as passes would be a
// compliance claim with nothing behind it, on the one screen most likely to be
// screenshotted as evidence that the box is safe.
//
// So the rows are here, because they are the right rows and the backend is
// being written to this shape -- but each reports what the code does today,
// and the two panels that ARE real are wired to their real endpoints now:
//
//   the public egress IP and ISP   GET /api/v1/vowifi-diagnostics -> egress
//   the inbound call filter policy GET /api/v1/settings/homesim/call-filter
//
// AND THE CENTREPIECE IS REAL. The anti-fraud guard in
// internal/httpapi/call_guard.go is the one protection this product actually
// runs: a daily cap on how many DISTINCT numbers can be reached, five for
// calls and three for messages, enforced at the send and dial handlers with a
// 403. Its counters are not readable over HTTP yet, so the card shows the
// policy and leaves the count blank rather than inventing one -- but the thing
// being described is not a mock-up.
//
// WHAT THIS CARD DELIBERATELY DOES NOT DUPLICATE: the transport-security rows
// (the secure-transport card in ModuleExtraCards.vue, with its own
// /api/v1/security/posture contract) and the 24-hour call and message counts
// (the communication-summary card in that same component, against
// /api/v1/stats/communication). Both already exist. A second card showing the
// same numbers would be two places for them to disagree.

const { t } = useI18n()

const security = ref<ModuleSecurity | null>(null)
const egress = ref<VowifiEgressProbe | null>(null)
const egressFailed = ref(false)
const filter = ref<CallFilterPolicy | null>(null)
const filterRuntimeReady = ref(false)
const filterFailed = ref(false)
const checking = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

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
 * An unlocked guard is the most important state this card can show, so it gets
 * its own treatment rather than a grey row: it is the condition in which
 * nothing caps how many strangers the box can reach.
 */
const guardUnlocked = computed(() => guard.value?.unlocked === true)

const riskRow = computed<SecurityRow | undefined>(() => security.value?.risk)

const integrityRows = computed(() => {
  const rows = security.value?.integrity
  return [
    { key: 'module', name: label('integrityModule'), row: rows?.module },
    { key: 'system', name: label('integritySystem'), row: rows?.system },
    { key: 'secureBoot', name: label('integritySecureBoot'), row: rows?.secure_boot },
    { key: 'firmware', name: label('integrityFirmware'), row: rows?.firmware }
  ]
})

const trust = computed(() => security.value?.trust)

const trustRemaining = computed(() => {
  const hours = trust.value?.remaining_hours
  if (hours === undefined) return ''
  return label('trustRemaining', { d: Math.floor(hours / 24), h: hours % 24 })
})

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

function stateIcon(state?: SecurityState) {
  switch (state) {
    case 'ok':
      return CircleCheck
    case 'warn':
      return CircleAlert
    default:
      // "off" and "unknown" both draw as absent rather than as a failure:
      // nothing is wrong, the thing is not running.
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
    await refreshLive()
  } finally {
    checking.value = false
  }
}

onMounted(() => {
  if (!visible.value) return
  void load()
  // Five minutes, matching the cards above. The egress probe reaches the
  // public internet, so a tighter poll would be a request every visitor's
  // open dashboard makes for a value that changes about never.
  timer = setInterval(() => void load(), 300_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  timer = undefined
})
</script>

<template>
  <div v-if="visible" class="modsec-shell">
    <article class="modsec-card" :class="{ 'is-unlocked': guardUnlocked }">
    <header class="modsec-card__head">
      <span class="modsec-card__mark">
        <ShieldAlert v-if="guardUnlocked" :size="15" />
        <ShieldCheck v-else :size="15" />
      </span>
      <div class="modsec-card__heading">
        <h4>{{ label('title') }}</h4>
        <p class="modsec-card__eyebrow">{{ label('subtitle') }}</p>
      </div>
      <!-- The risk badge from the mock-up. It reports "not assessed" until
           something computes it, because a reassuring badge is the single
           easiest thing on this card to fake and the hardest to notice. -->
      <span
        class="modsec-card__risk"
        :class="riskRow?.state ? `is-${riskRow.state}` : 'is-pending'"
        :title="riskRow ? riskRow.detail : label('riskPendingNote')"
      >
        <component :is="stateIcon(riskRow?.state)" :size="13" />
        {{ label('riskLabel') }}
        <strong>{{ riskRow?.value || label('riskPending') }}</strong>
      </span>
    </header>

    <p class="modsec-card__purpose">{{ label('purpose') }}</p>

    <div class="modsec-card__scrollwrap">
      <div class="modsec-card__scroll">

    <!-- The one protection that actually runs. -->
    <section class="modsec-card__guard" :class="{ 'is-unlocked': guardUnlocked }">
      <div class="modsec-card__guard-head">
        <h5>{{ label('guardTitle') }}</h5>
        <span class="modsec-card__guard-state">
          <component :is="guardUnlocked ? CircleAlert : CircleCheck" :size="13" />
          {{ guardUnlocked ? label('guardUnlocked') : label('guardLocked') }}
        </span>
      </div>

      <div class="modsec-card__guard-grid">
        <div class="modsec-card__quota">
          <span class="modsec-card__quota-icon"><PhoneForwarded :size="14" /></span>
          <div>
            <span class="modsec-card__quota-name">{{ label('guardCalls') }}</span>
            <strong class="modsec-card__quota-value">
              {{ label('guardNumbers', { n: guardLimits.call }) }}
            </strong>
            <small class="modsec-card__quota-note">{{ label('guardLimit') }}</small>
          </div>
        </div>
        <div class="modsec-card__quota">
          <span class="modsec-card__quota-icon"><MessageSquareText :size="14" /></span>
          <div>
            <span class="modsec-card__quota-name">{{ label('guardMessages') }}</span>
            <strong class="modsec-card__quota-value">
              {{ label('guardNumbers', { n: guardLimits.message }) }}
            </strong>
            <small class="modsec-card__quota-note">{{ label('guardLimit') }}</small>
          </div>
        </div>
        <div class="modsec-card__quota is-pending">
          <span class="modsec-card__quota-icon"><Clock :size="14" /></span>
          <div>
            <span class="modsec-card__quota-name">{{ label('guardUsed') }}</span>
            <strong class="modsec-card__quota-value">
              {{
                guard?.call_used !== undefined && guard?.message_used !== undefined
                  ? `${guard.call_used} / ${guard.message_used}`
                  : label('guardUsedPending')
              }}
            </strong>
            <small class="modsec-card__quota-note">{{ label('guardUsedNote') }}</small>
          </div>
        </div>
      </div>

      <!-- The long explanation moves to the tooltip at this width; what has
           to stay on screen is the sentence that stops the number being
           misread, and the caveat that the cap is a default. -->
      <p class="modsec-card__note" :title="label('guardWhat')">{{ label('guardWhatShort') }}</p>
      <p v-if="guardLimits.fromDefaults" class="modsec-card__note">
        {{ label('guardDefaultNote') }}
      </p>
      <p v-if="guardUnlocked" class="modsec-card__note modsec-card__note--alarm">
        {{ label('guardUnlockedNote') }}
      </p>
    </section>

    <div class="modsec-card__columns">
      <!-- Real today. -->
      <section class="modsec-card__panel">
        <h5><Globe2 :size="13" /> {{ label('egressTitle') }}</h5>
        <dl class="modsec-card__facts">
          <dt>{{ label('egressIP') }}</dt>
          <dd class="modsec-card__mono">
            {{
              egress?.public_ip ||
              (egressFailed || egress?.available === false
                ? label('egressUnavailable')
                : label('egressPending'))
            }}
          </dd>
          <dt>{{ label('egressISP') }}</dt>
          <dd>
            {{
              egress?.isp ||
              (egressFailed || egress?.available === false
                ? label('egressUnavailable')
                : label('egressPending'))
            }}
          </dd>
        </dl>
        <p class="modsec-card__note" :title="label('egressNote')">
          {{ label('egressNoteShort') }}
        </p>
      </section>

      <!-- Real today. -->
      <section class="modsec-card__panel">
        <h5><ListFilter :size="13" /> {{ label('filterTitle') }}</h5>
        <dl v-if="filter" class="modsec-card__facts">
          <dt>{{ label('filterMode') }}</dt>
          <dd>{{ filterModeLabel }}</dd>
          <dt>{{ label('filterEntries') }}</dt>
          <dd>{{ label('filterEntriesValue', { n: filterEntryCount ?? 0 }) }}</dd>
          <dt>{{ label('filterMinReports') }}</dt>
          <dd>{{ label('filterMinReportsValue', { n: filter.min_reports }) }}</dd>
        </dl>
        <p v-else class="modsec-card__note">{{ label('filterUnavailable') }}</p>
        <!-- Saying so is the point: rules that are stored but not enforced
             look identical to rules that are blocking calls. -->
        <p
          v-if="filter && !filterRuntimeReady"
          class="modsec-card__note modsec-card__note--warn"
          :title="label('filterNotReadyNote')"
        >
          {{ label('filterNotReady') }}
        </p>
      </section>

      <!-- Not built. Every row says so. -->
      <section class="modsec-card__panel">
        <h5><ShieldCheck :size="13" /> {{ label('integrityTitle') }}</h5>
        <ul class="modsec-card__integrity">
          <li
            v-for="entry in integrityRows"
            :key="entry.key"
            :class="entry.row?.state ? `is-${entry.row.state}` : 'is-pending'"
          >
            <component :is="stateIcon(entry.row?.state)" :size="13" />
            <span class="modsec-card__integrity-name">{{ entry.name }}</span>
            <span class="modsec-card__integrity-value">
              {{ entry.row?.value || label('integrityPending') }}
            </span>
          </li>
        </ul>
        <div class="modsec-card__trust">
          <span class="modsec-card__trust-name">{{ label('trustTitle') }}</span>
          <span class="modsec-card__trust-value">
            {{ trust?.value || label('trustPending') }}
          </span>
          <span v-if="trustRemaining" class="modsec-card__trust-remaining">
            {{ trustRemaining }}
          </span>
        </div>
        <p class="modsec-card__note" :title="label('integrityNote')">
          {{ label('integrityNoteShort') }}
        </p>
      </section>
    </div>
      </div>
    </div>

    <footer class="modsec-card__foot">
      <!-- Named for where it goes. There is no security detail page: the
           call-filter policy this card shows lives inside an experimental
           disclosure under Settings -> Audio, and diagnostics is the section
           that is actually about observing this box. A link labelled only
           "details" would promise a page that does not exist. -->
      <RouterLink
        class="modsec-card__link"
        :to="{ name: 'settings', params: { section: 'diagnostics' } }"
      >
        {{ label('details') }}
        <ExternalLink :size="13" />
      </RouterLink>
      <button type="button" class="modsec-card__recheck" :disabled="checking" @click="load">
        <RefreshCw :size="14" :class="{ spin: checking }" />
        {{ checking ? label('rechecking') : label('recheck') }}
      </button>
      <span class="modsec-card__foot-note" :title="label('recheckNote')">
        <Info :size="12" />
      </span>
    </footer>
    </article>
  </div>
</template>

<style scoped>
/* Built to the same height as the two network drawings beside it, because it
   shares their row. The head, the purpose line and the footer are pinned; only
   the middle scrolls, so the thing an operator opens this card for -- whether
   the guard is in force, and the re-check button -- is always in view without
   having to scroll to find it. */
/* THE SHELL IS THE GRID ITEM, AND THE CARD FILLS IT ABSOLUTELY.
   
   This card shares its row with the two network drawings, which are 713px
   tall. A grid row is as tall as its tallest item and `align-items: stretch`
   then stretches the rest to match -- so a card whose content wanted 874px
   would not be clipped, it would make the whole row 874px and leave 161px of
   void inside each drawing. Taking the card out of flow means its content can
   never set the row height: the drawings decide it, this fills whatever they
   decided, and anything that does not fit scrolls inside.
   
   The min-height is the floor for when this card is alone in a row, which is
   what happens at narrower widths where the grid drops to two tracks. */
.modsec-shell {
  position: relative;
  min-height: 565px;
}

.modsec-card {
  position: absolute;
  inset: 0;
}

.modsec-card {
  display: flex;
  flex-direction: column;
  padding: 16px 16px 14px;
  gap: 10px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 14px);
}

.modsec-card__scrollwrap {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
}

/* A fade over the scroll area's bottom edge, so a row that is half cut off
   reads as "there is more below" rather than as a clipped layout. It is
   painted in the card's own background, so where the content ends before the
   edge it fades nothing and is invisible. */
.modsec-card__scrollwrap::after {
  content: '';
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 22px;
  background: linear-gradient(to bottom, transparent, var(--surface));
  pointer-events: none;
}

.modsec-card__scroll {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  gap: 10px;
  overflow-y: auto;
  overscroll-behavior: contain;
  /* Room for the scrollbar so it never sits on top of a value. */
  padding-right: 2px;
}

.modsec-card__scroll::-webkit-scrollbar { width: 6px; }
.modsec-card__scroll::-webkit-scrollbar-thumb {
  background: var(--border);
  border-radius: 999px;
}

/* An unlocked guard is the one state on this card worth interrupting for. */
.modsec-card.is-unlocked {
  border-color: color-mix(in srgb, var(--warning) 50%, var(--border));
}

.modsec-card__head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.modsec-card__mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  border-radius: 9px;
  flex: none;
}

.modsec-card.is-unlocked .modsec-card__mark {
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 16%, transparent);
}

.modsec-card__heading { min-width: 0; }
.modsec-card__heading h4 { margin: 0; font-size: 15px; }

.modsec-card__eyebrow {
  margin: 0;
  color: var(--faint);
  font-size: 9.5px;
  letter-spacing: 0.14em;
}

.modsec-card__risk {
  display: inline-flex;
  align-items: center;
  margin-left: auto;
  padding: 4px 11px;
  gap: 6px;
  color: var(--muted);
  font-size: 11.5px;
  border: 1px solid var(--border);
  border-radius: 999px;
  cursor: help;
}

.modsec-card__risk strong { color: var(--text); font-size: 12px; }
.modsec-card__risk.is-ok { color: var(--success); border-color: var(--success-border, var(--border)); }
.modsec-card__risk.is-ok strong { color: var(--success); }
.modsec-card__risk.is-warn { color: var(--warning); }
.modsec-card__risk.is-warn strong { color: var(--warning); }

.modsec-card__purpose {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.55;
}

/* The guard panel is the loudest thing here because it is the only protection
   that actually runs. */
.modsec-card__guard {
  display: flex;
  flex-direction: column;
  padding: 13px 15px;
  gap: 10px;
  background: var(--surface-subtle);
  border: 1px solid color-mix(in srgb, var(--accent) 28%, var(--border));
  border-radius: var(--radius-md, 10px);
}

.modsec-card__guard.is-unlocked {
  border-color: color-mix(in srgb, var(--warning) 45%, var(--border));
}

.modsec-card__guard-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.modsec-card__guard-head h5 { margin: 0; font-size: 13px; }

.modsec-card__guard-state {
  display: inline-flex;
  align-items: center;
  margin-left: auto;
  gap: 5px;
  color: var(--success);
  font-size: 11.5px;
}

.modsec-card__guard.is-unlocked .modsec-card__guard-state { color: var(--warning); }

/* Two caps side by side, the pending count on its own line under them: the
   two numbers are meant to be compared with each other, and the third tile is
   not a number at all. */
.modsec-card__guard-grid {
  display: grid;
  gap: 8px;
  grid-template-columns: 1fr 1fr;
}

.modsec-card__guard-grid > :nth-child(3) { grid-column: 1 / -1; }

.modsec-card__quota {
  display: flex;
  align-items: flex-start;
  padding: 10px 12px;
  gap: 9px;
  background: var(--surface);
  border-radius: var(--radius-md, 10px);
}

.modsec-card__quota > div { display: flex; flex-direction: column; min-width: 0; gap: 1px; }
.modsec-card__quota-icon { color: var(--accent); flex: none; margin-top: 2px; }
.modsec-card__quota.is-pending .modsec-card__quota-icon { color: var(--faint); }
.modsec-card__quota-name { color: var(--muted); font-size: 11px; }

.modsec-card__quota-value {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}

.modsec-card__quota.is-pending .modsec-card__quota-value { color: var(--faint); font-size: 13px; }
.modsec-card__quota-note { color: var(--faint); font-size: 10.5px; line-height: 1.5; }

.modsec-card__columns {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.modsec-card__panel {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 12px 14px;
  gap: 8px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md, 10px);
}

.modsec-card__panel h5 {
  display: flex;
  align-items: center;
  margin: 0;
  gap: 6px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.04em;
}

.modsec-card__facts {
  display: grid;
  margin: 0;
  gap: 3px 12px;
  grid-template-columns: max-content 1fr;
  font-size: 12.5px;
}

.modsec-card__facts dt { color: var(--muted); font-size: 11px; align-self: center; }
.modsec-card__facts dd { margin: 0; text-align: right; word-break: break-all; }
.modsec-card__mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; }

.modsec-card__integrity {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  gap: 7px;
  list-style: none;
}

.modsec-card__integrity li {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
}

.modsec-card__integrity li > svg { flex: none; }
.modsec-card__integrity li.is-ok > svg { color: var(--success); }
.modsec-card__integrity li.is-warn > svg { color: var(--warning); }
/* Not built, and not running, both draw as absent rather than as a fault. */
.modsec-card__integrity li.is-pending,
.modsec-card__integrity li.is-off,
.modsec-card__integrity li.is-unknown { color: var(--faint); }
.modsec-card__integrity li.is-pending > svg,
.modsec-card__integrity li.is-off > svg,
.modsec-card__integrity li.is-unknown > svg { color: var(--faint); }

.modsec-card__integrity-value { margin-left: auto; font-size: 11.5px; }

.modsec-card__trust {
  display: flex;
  align-items: center;
  padding: 8px 10px;
  gap: 8px;
  background: var(--surface);
  border-radius: var(--radius-md, 10px);
  font-size: 12px;
  flex-wrap: wrap;
}

.modsec-card__trust-name { color: var(--muted); font-size: 11px; }
.modsec-card__trust-value { color: var(--faint); }
.modsec-card__trust-remaining { margin-left: auto; color: var(--faint); font-size: 11px; }

.modsec-card__note {
  margin: 0;
  color: var(--faint);
  font-size: 11.5px;
  line-height: 1.6;
}

.modsec-card__note--warn { color: var(--warning); }

.modsec-card__note--alarm {
  padding: 8px 10px;
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 12%, transparent);
  border-radius: var(--radius-md, 10px);
}

.modsec-card__foot {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.modsec-card__recheck { margin-left: auto; }

.modsec-card__link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--accent);
  font-size: 12.5px;
  text-decoration: none;
}

.modsec-card__link:hover { text-decoration: underline; }

.modsec-card__recheck {
  display: inline-flex;
  align-items: center;
  padding: 8px 15px;
  gap: 8px;
  color: var(--text);
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 9px;
  cursor: pointer;
}

.modsec-card__recheck:disabled { opacity: 0.55; cursor: progress; }

.modsec-card__foot-note {
  display: inline-flex;
  align-items: center;
  color: var(--faint);
  cursor: help;
}

.spin { animation: modsec-spin 1.1s linear infinite; }

@keyframes modsec-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spin { animation: none; }
}

@media (max-width: 560px) {
  .modsec-card__risk { margin-left: 0; }
  .modsec-card__head { flex-wrap: wrap; }
  .modsec-card__facts dd { text-align: left; }
}
</style>
