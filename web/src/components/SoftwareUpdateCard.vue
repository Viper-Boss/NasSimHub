<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CircleCheck,
  CircleOff,
  CloudUpload,
  Download,
  ExternalLink,
  Globe2,
  Info,
  LoaderCircle,
  Network,
  RefreshCw,
  ShieldCheck,
  Waypoints
} from '@lucide/vue'

import type { UpdateComponentName, UpdateOperationComponentState } from '../api/types'
import { i18n, resolvedLocale } from '../i18n'
import { softwareUpdateCardLabels } from '../i18n/softwareUpdateCardLabels'
import { sessionState } from '../state/session'
import {
  applyUpdate,
  attachSoftwareUpdate,
  checkForUpdates,
  ensureInitialUpdateCheck,
  setAutoDownload,
  setSoftwareUpdateDownloadMessages,
  setSoftwareUpdateMessages,
  startUpdateDownload,
  useSoftwareUpdate
} from '../state/softwareUpdate'

// The update card at the foot of the dashboard.
//
// WHAT IT IS FOR. Updating used to live only in settings, three clicks from
// anywhere, which is the wrong place for the one action an operator takes when
// they hear there is a new version. This card puts the version, what it would
// change, and the button on the page they already have open.
//
// IT SAYS WHAT IS AND IS NOT CHECKED, and that is the part to leave alone.
// The layout follows a mock-up that showed three green ticks -- integrity,
// source agreement, signature -- and a "3/3 sources agree" badge. When this
// card was first built only one of those was true, and it said so. Since then
// the release path has been rebuilt: releases are signed, verified against the
// key compiled into this build, and fetched from several mirrors. So the rows
// now light up -- but each one is still DRIVEN BY WHAT THE UPDATER REPORTED on
// this check, never by what the code is supposed to do. The signature row is
// green only when a mirror returned a manifest that verified; the agreement
// row counts the mirrors that did. On a check that reached nothing, they go
// dark again, which is correct: nothing was verified.
//
// THE BUTTON FOLLOWS THE DOWNLOAD. The gateway fetches a new release's images
// in the background as soon as it sees one. While that runs the button is a
// ring counting images fetched; when it is done the button is the green
// "update now", and pressing it is a restart rather than a download.

// THE RING IS HONEST TOO, in a smaller way. It is driven by which stage each
// component has reached, not by bytes: `docker pull` runs through
// CombinedOutput in the gateway, so its progress stream is read and discarded
// and no byte count reaches the API. The ring therefore moves in steps and the
// note under it says so. Making it look smoother would mean inventing the
// motion between the steps.

const { t } = useI18n()
const {
  updateCheck,
  checking,
  applying,
  startingUpdate,
  displayedComponents,
  componentState,
  progress,
  downloading,
  downloadReady,
  downloadFailed,
  downloadProgress,
  autoDownload,
  savingPreference,
  releaseSources,
  sourceAgreement,
  signatureVerified
} = useSoftwareUpdate()

let detach: (() => void) | undefined

// Loaded on demand and merged into the active locale. The watch -- rather than
// a single merge in onMounted -- is what makes the labels appear at all:
// mergeLocaleMessage is a store mutation that t() does not track, so a card
// whose state barely changes can render the raw key names forever. Measured
// once already, on the cards above this one.
watch(
  () => i18n.global.locale.value,
  () => {
    const locale = resolvedLocale()
    i18n.global.mergeLocaleMessage(locale, {
      softwareUpdateCard: softwareUpdateCardLabels(locale)
    })
  },
  { immediate: true, flush: 'sync' }
)

function label(key: string, values?: Record<string, unknown>): string {
  return values ? t(`softwareUpdateCard.${key}`, values) : t(`softwareUpdateCard.${key}`)
}

/**
 * Administrators only, because the endpoints are.
 *
 * Every /api/v1/updates/ route is admin-gated server side, so for anyone else
 * this card could only ever show an error. Hiding it is better than rendering
 * a permanent failure, and the settings page carries the same information for
 * whoever does have the rights.
 */
const visible = computed(() => sessionState.role === 'admin')

const status = computed(() => updateCheck.value?.status)
const currentVersion = computed(
  () => updateCheck.value?.current_version || label('notAvailable')
)
const latestVersion = computed(() => updateCheck.value?.latest_version || '')
const hasUpdate = computed(() => status.value === 'update_available')

const headline = computed(() => {
  if (checking.value && !updateCheck.value) {
    return { key: 'checking', title: label('checkingTitle'), body: '' }
  }
  switch (status.value) {
    case 'update_available':
      return { key: 'available', title: label('availableTitle'), body: label('availableBody') }
    case 'up_to_date':
      return { key: 'current', title: label('upToDateTitle'), body: label('upToDateBody') }
    case 'development':
      return {
        key: 'development',
        title: label('developmentTitle'),
        body: label('developmentBody')
      }
    default:
      return {
        key: 'unavailable',
        title: label('unavailableTitle'),
        body: label('unavailableBody')
      }
  }
})

/**
 * Why the update button is unavailable, when it is.
 *
 * `apply_available: false` alongside `update_available` is a real state the
 * gateway reports when it can see the release but cannot resolve its image
 * manifest, and `updater_unavailable` is a deployment with no updater
 * container at all. Both look identical from the button -- disabled -- so the
 * reason is spelled out rather than left as a greyed-out control.
 */
const blockedReason = computed(() => {
  if (!hasUpdate.value || updateCheck.value?.apply_available) return null
  if (updateCheck.value?.error_code === 'updater_unavailable') {
    return { title: label('updaterUnavailable'), body: label('updaterUnavailableNote') }
  }
  if (updateCheck.value?.error_code === 'release_requires_newer_installation') {
    return { title: label('applyUnavailable'), body: label('needsIntermediateNote') }
  }
  return { title: label('applyUnavailable'), body: label('applyUnavailableNote') }
})

/**
 * Installing waits for a running download rather than racing it: the ring
 * is already showing the fetch, and a second button would start the same
 * pull twice over the one uplink the gateway has.
 */
const canApply = computed(
  () =>
    hasUpdate.value &&
    Boolean(updateCheck.value?.apply_available) &&
    !applying.value &&
    !downloading.value
)

/** Whether the ring in the button is showing the download or the install. */
const showingDownload = computed(() => !applying.value && hasUpdate.value && downloading.value)

/** The switch in the header, or nothing when there is no updater to ask. */
const autoDownloadKnown = computed(() => autoDownload.value !== undefined)

function toggleAutoDownload(): void {
  if (autoDownload.value === undefined) return
  void setAutoDownload(!autoDownload.value)
}

/** A second, quieter button: fetch now, for when automatic fetching is off. */
const canDownloadNow = computed(
  () =>
    hasUpdate.value &&
    Boolean(updateCheck.value?.apply_available) &&
    autoDownload.value !== undefined &&
    !applying.value &&
    !downloading.value &&
    !downloadReady.value
)

/**
 * The update sources and their real state.
 *
 * The release mirrors come from the check itself, one row each, by host: what
 * each one returned this time. DHT and NKN are listed because the gateway
 * already speaks both and fetching a release over them is the planned next
 * channel for networks that reach none of the mirrors; they are marked as not
 * wired up, because they are not.
 */
const sources = computed(() => [
  ...releaseSources.value.map(entry => ({
    key: `mirror-${entry.name}`,
    icon: Globe2,
    name: entry.name,
    wired: entry.state === 'verified',
    detail:
      entry.state === 'verified'
        ? label('sourceVerified', { version: entry.version || '' })
        : label(`sourceState_${entry.state}`)
  })),
  { key: 'dht', icon: Waypoints, name: label('sourceDHT'), wired: false, detail: label('sourceNotWired') },
  { key: 'nkn', icon: Network, name: label('sourceNKN'), wired: false, detail: label('sourceNotWired') }
])

const sourcesSummary = computed(() =>
  sourceAgreement.value.total === 0
    ? label('sourcesNone')
    : label('sourcesAgree', {
        agreeing: sourceAgreement.value.agreeing,
        total: sourceAgreement.value.total
      })
)

/**
 * The three verification rows, each lit only by what this check reported.
 *
 * Integrity is always enforced -- every pull is by digest and Docker checks
 * it. Signature is lit when a mirror returned a manifest that verified against
 * the compiled-in key. Agreement is lit when at least two mirrors returned the
 * same verified release; with one there is nothing to agree with.
 */
const verifications = computed(() => [
  {
    key: 'integrity',
    name: label('verifyIntegrity'),
    value: label('verifyIntegrityValue'),
    note: label('verifyIntegrityNote'),
    active: true
  },
  {
    key: 'consensus',
    name: label('verifyConsensus'),
    value: sourcesSummary.value,
    note: label('verifyConsensusNote'),
    active: sourceAgreement.value.agreeing >= 2
  },
  {
    key: 'signature',
    name: label('verifySignature'),
    value: signatureVerified.value ? label('verifySignatureValue') : label('verifySignatureNone'),
    note: label('verifySignatureNote'),
    active: signatureVerified.value
  }
])

const componentLabels: Record<UpdateComponentName, string> = {
  api: 'API',
  web: 'Web',
  hardware: 'Hardware',
  updater: 'Updater',
  cloudflared: 'Cloudflared'
}

function stageLabel(state?: UpdateOperationComponentState): string {
  if (!state) return ''
  const keys: Record<UpdateOperationComponentState, string> = {
    pending: 'stagePending',
    pulling: 'stagePulling',
    staged: 'stageStaged',
    restarting: 'stageRestarting',
    rolling_back: 'stageRollingBack',
    rolled_back: 'stageRolledBack',
    ready: 'stageReady',
    failed: 'stageFailed'
  }
  return label(keys[state])
}

// The ring. r=13 in a 32-box, so the stroke sits inside the button's edge.
const RING_RADIUS = 13
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS
const ringOffset = computed(
  () => RING_CIRCUMFERENCE * (1 - Math.min(100, Math.max(0, ringValue.value)) / 100)
)

/**
 * Before the first operation frame arrives there is no progress to draw, so
 * the ring spins instead of sitting at zero. A stalled ring and a slow pull
 * look the same otherwise.
 */
const ringIndeterminate = computed(() =>
  showingDownload.value
    ? downloadProgress.value === 0
    : applying.value && (startingUpdate.value || displayedComponents.value.length === 0)
)

/** The ring's number: images fetched while downloading, the ladder while installing. */
const ringValue = computed(() => (showingDownload.value ? downloadProgress.value : progress.value))

async function onUpdate(): Promise<void> {
  if (!canApply.value) return
  await applyUpdate()
}

onMounted(() => {
  setSoftwareUpdateDownloadMessages({
    downloadStartFailed: label('downloadStartFailed'),
    preferenceFailed: label('preferenceFailed')
  })
  setSoftwareUpdateMessages({
    updateFailed: t('about.updateFailed'),
    updateStartFailed: t('about.updateStartFailed'),
    hardwareTitle: t('about.hardwareUpdateTitle'),
    hardwareWarning: t('about.hardwareUpdateWarning'),
    confirmUpdate: t('about.confirmUpdate')
  })
  if (!visible.value) return
  detach = attachSoftwareUpdate()
  void ensureInitialUpdateCheck()
})

onBeforeUnmount(() => {
  detach?.()
  detach = undefined
})
</script>

<template>
  <div v-if="visible" class="update-shell">
    <article class="update-card" :class="`update-card--${headline.key}`">
    <header class="update-card__head">
      <span class="update-card__mark"><CloudUpload :size="15" /></span>
      <div class="update-card__heading">
        <h4>{{ label('title') }}</h4>
        <p class="update-card__eyebrow">{{ label('subtitle') }}</p>
      </div>
      <button
        v-if="autoDownloadKnown"
        type="button"
        class="update-card__auto"
        :class="{ 'is-on': autoDownload }"
        role="switch"
        :aria-checked="autoDownload ? 'true' : 'false'"
        :disabled="savingPreference"
        :title="label('autoDownloadHint')"
        @click="toggleAutoDownload"
      >
        <Download v-if="autoDownload" :size="13" />
        <CircleOff v-else :size="13" />
        {{ autoDownload ? label('autoDownloadOn') : label('autoDownloadOff') }}
      </button>
    </header>

    <div class="update-card__body">
      <div class="update-card__banner">
        <p class="update-card__banner-title">{{ headline.title }}</p>
        <p v-if="headline.body" class="update-card__banner-body">{{ headline.body }}</p>
      </div>

      <div class="update-card__versions">
        <div class="update-card__version">
          <span class="update-card__version-label">{{ label('currentVersion') }}</span>
          <strong class="update-card__version-value">{{ currentVersion }}</strong>
        </div>
        <span v-if="hasUpdate" class="update-card__arrow" aria-hidden="true">→</span>
        <div v-if="hasUpdate" class="update-card__version update-card__version--next">
          <span class="update-card__version-label">{{ label('latestVersion') }}</span>
          <strong class="update-card__version-value">{{ latestVersion }}</strong>
        </div>
      </div>

      <div class="update-card__scrollwrap">
      <div class="update-card__scroll">
      <div class="update-card__columns">
        <section class="update-card__panel">
          <h5>{{ label('sourcesTitle') }}</h5>
          <ul class="update-card__sources">
            <li
              v-for="source in sources"
              :key="source.key"
              :class="{ 'is-idle': !source.wired }"
            >
              <component :is="source.icon" :size="14" />
              <span class="update-card__source-name">{{ source.name }}</span>
              <span class="update-card__source-detail">{{ source.detail }}</span>
            </li>
          </ul>
          <p class="update-card__note" :title="label('sourcesNote')">
            {{ sourcesSummary }}
          </p>
        </section>

        <section class="update-card__panel">
          <h5>{{ label('verifyTitle') }}</h5>
          <ul class="update-card__checks">
            <li
              v-for="entry in verifications"
              :key="entry.key"
              :class="{ 'is-idle': !entry.active }"
            >
              <CircleCheck v-if="entry.active" :size="14" />
              <CircleOff v-else :size="14" />
              <!-- The explanation is the tooltip here; the value is what has
                   to stay legible, because "not enabled" is the whole point of
                   the row. -->
              <div :title="entry.note">
                <span class="update-card__check-name">{{ entry.name }}</span>
                <span class="update-card__check-value">{{ entry.value }}</span>
              </div>
            </li>
          </ul>
        </section>

        <section v-if="hasUpdate" class="update-card__panel">
          <h5>{{ label('componentsTitle') }}</h5>
          <ul v-if="displayedComponents.length" class="update-card__components">
            <li v-for="component in displayedComponents" :key="component.name">
              <span class="update-card__component-tag">
                {{ componentLabels[component.name] }}
              </span>
              <span class="update-card__component-target">
                {{
                  component.target_version
                    ? label('componentTo', { version: component.target_version })
                    : label('componentUnchanged')
                }}
              </span>
              <span
                v-if="applying && componentState(component.name)"
                class="update-card__component-stage"
                :class="`is-${componentState(component.name)}`"
              >
                {{ stageLabel(componentState(component.name)) }}
              </span>
            </li>
          </ul>
          <p v-else class="update-card__note">{{ label('componentsNone') }}</p>
        </section>
      </div>

      </div>
      </div>

      <div v-if="blockedReason" class="update-card__blocked">
        <Info :size="14" />
        <div>
          <strong>{{ blockedReason.title }}</strong>
          <p>{{ blockedReason.body }}</p>
        </div>
      </div>

      <footer class="update-card__foot">
        <a
          v-if="hasUpdate && updateCheck?.release_url"
          class="update-card__link"
          :href="updateCheck.release_url"
          target="_blank"
          rel="noreferrer noopener"
        >
          {{ label('releaseNotes') }}
          <ExternalLink :size="13" />
        </a>

        <button
          type="button"
          class="update-card__recheck"
          :disabled="checking || applying"
          @click="checkForUpdates(true)"
        >
          <RefreshCw :size="14" :class="{ spin: checking }" />
          {{ checking ? label('checking') : label('recheck') }}
        </button>

        <button
          v-if="canDownloadNow"
          type="button"
          class="update-card__recheck"
          @click="startUpdateDownload"
        >
          <Download :size="14" />
          {{ downloadFailed ? label('downloadRetry') : label('downloadNow') }}
        </button>

        <button
          v-if="hasUpdate"
          type="button"
          class="update-card__apply"
          :class="{ 'is-working': applying || showingDownload, 'is-ready': downloadReady }"
          :disabled="!canApply"
          @click="onUpdate"
        >
          <!-- The ring reports how far through the component ladder the
               operation is. role/aria say the same thing to a screen reader,
               and the value is left off while it is indeterminate rather than
               reported as zero. -->
          <span
            v-if="applying || showingDownload"
            class="update-card__ring"
            role="progressbar"
            :aria-label="showingDownload ? label('downloadProgressLabel') : label('progressLabel')"
            :aria-valuenow="ringIndeterminate ? undefined : ringValue"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <svg viewBox="0 0 32 32" aria-hidden="true" :class="{ 'is-spinning': ringIndeterminate }">
              <circle class="update-card__ring-track" cx="16" cy="16" :r="RING_RADIUS" />
              <circle
                class="update-card__ring-fill"
                cx="16"
                cy="16"
                :r="RING_RADIUS"
                :stroke-dasharray="RING_CIRCUMFERENCE"
                :stroke-dashoffset="ringIndeterminate ? RING_CIRCUMFERENCE * 0.72 : ringOffset"
              />
            </svg>
            <span v-if="!ringIndeterminate" class="update-card__ring-value">{{ ringValue }}</span>
            <LoaderCircle v-else :size="13" class="spin" />
          </span>
          <ShieldCheck v-else :size="15" />
          {{ applying ? label('updating') : showingDownload ? label('downloading') : label('update') }}
        </button>
      </footer>

      <p
        v-if="applying"
        class="update-card__note update-card__note--warn"
        :title="label('progressNote')"
      >
        {{ label('updatingHint') }}
      </p>
      <p v-else-if="hasUpdate && showingDownload" class="update-card__note">
        {{ label('downloadingHint') }}
      </p>
      <p v-else-if="hasUpdate && downloadReady" class="update-card__note update-card__note--good">
        {{ label('downloadReadyHint') }}
      </p>
      <p v-else-if="hasUpdate && downloadFailed" class="update-card__note update-card__note--warn">
        {{ label('downloadFailedHint') }}
      </p>
      <p v-else-if="hasUpdate" class="update-card__note" :title="label('autoDownloadHint')">
        {{ autoDownload ? label('downloadQueuedHint') : label('autoDownloadHintShort') }}
      </p>
    </div>
    </article>
  </div>
</template>

<style scoped>
/* Built to the same height as the network drawings it shares a row with. The
   head, the version tiles and the footer are pinned; only the middle scrolls,
   so the version and the update button are always in view. */
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
.update-shell {
  position: relative;
  min-height: 565px;
}

.update-card {
  position: absolute;
  inset: 0;
}

.update-card {
  display: flex;
  flex-direction: column;
  padding: 16px 16px 14px;
  gap: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 14px);
}

.update-card__scrollwrap {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
}

/* A fade over the scroll area's bottom edge, so a row that is half cut off
   reads as "there is more below" rather than as a clipped layout. It is
   painted in the card's own background, so where the content ends before the
   edge it fades nothing and is invisible. */
.update-card__scrollwrap::after {
  content: '';
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 22px;
  background: linear-gradient(to bottom, transparent, var(--surface));
  pointer-events: none;
}

.update-card__scroll {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  gap: 12px;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-right: 2px;
}

.update-card__scroll::-webkit-scrollbar { width: 6px; }
.update-card__scroll::-webkit-scrollbar-thumb {
  background: var(--border);
  border-radius: 999px;
}

.update-card--available {
  border-color: color-mix(in srgb, var(--success) 45%, var(--border));
}

.update-card__head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.update-card__mark {
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

.update-card__heading { min-width: 0; }
.update-card__heading h4 { margin: 0; font-size: 15px; }

.update-card__eyebrow {
  margin: 0;
  color: var(--faint);
  font-size: 9.5px;
  letter-spacing: 0.14em;
}

/* Pushed to the far end rather than floated, so it keeps its place when the
   heading wraps at narrow widths. */
.update-card__auto {
  display: inline-flex;
  align-items: center;
  margin-left: auto;
  padding: 4px 10px;
  gap: 6px;
  color: var(--muted);
  font: inherit;
  font-size: 11.5px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 999px;
  cursor: pointer;
}

/* On is the state worth noticing: it means updates will already be here
   when the operator looks. */
.update-card__auto.is-on {
  color: var(--success);
  border-color: var(--success-border);
}

.update-card__auto:disabled { cursor: progress; opacity: 0.7; }

.update-card__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  gap: 12px;
}

.update-card__banner {
  padding: 12px 14px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md, 10px);
}

.update-card__banner-title { margin: 0; font-size: 14px; font-weight: 600; }
.update-card--available .update-card__banner-title { color: var(--success); }
.update-card__banner-body { margin: 4px 0 0; color: var(--muted); font-size: 12.5px; }

.update-card__versions {
  display: flex;
  align-items: stretch;
  gap: 10px;
  flex-wrap: wrap;
}

.update-card__version {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  padding: 10px 14px;
  gap: 2px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
}

.update-card__version--next {
  color: var(--success);
  border-color: color-mix(in srgb, var(--success) 50%, var(--border));
}

.update-card__version-label { color: var(--muted); font-size: 11px; }
.update-card__version--next .update-card__version-label { color: inherit; }

.update-card__version-value {
  font-size: 17px;
  font-variant-numeric: tabular-nums;
}

.update-card__arrow {
  align-self: center;
  color: var(--faint);
  font-size: 17px;
}

/* Three panels across when there is room, stacking at the width where a column
   would otherwise be too narrow for a sentence.

   `align-items: start` rather than the stretch a grid defaults to: the three
   panels hold very different amounts of text -- three checks with their
   explanations against a list of three component names -- and stretching them
   to a shared height left about three hundred pixels of empty panel under the
   shortest one. Bottoms that line up are worth having when the contents are
   comparable; here they were buying a void. */
.update-card__columns {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.update-card__panel {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 12px 14px;
  gap: 8px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md, 10px);
}

.update-card__panel h5 {
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.04em;
}

.update-card__sources,
.update-card__checks,
.update-card__components {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  gap: 8px;
  list-style: none;
}

.update-card__sources li {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
  font-size: 12.5px;
}

.update-card__sources li > svg { color: var(--accent); flex: none; }
.update-card__source-name { font-weight: 500; }

.update-card__source-detail {
  margin-left: auto;
  color: var(--muted);
  font-size: 11.5px;
  text-align: right;
}

/* A source or a check that is not wired up is drawn as absent, not as failed:
   nothing is wrong, it does not exist yet. */
.update-card__sources li.is-idle,
.update-card__checks li.is-idle {
  color: var(--faint);
}

.update-card__sources li.is-idle > svg,
.update-card__checks li.is-idle > svg {
  color: var(--faint);
}

.update-card__checks li {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 12.5px;
}

.update-card__checks li > svg { margin-top: 2px; color: var(--success, #1a7f4b); flex: none; }
.update-card__checks li > div { display: flex; flex-direction: column; min-width: 0; gap: 1px; }
.update-card__check-name { color: var(--muted); font-size: 11px; }
.update-card__check-value { font-weight: 600; }
.update-card__check-note { color: var(--faint); font-size: 11px; line-height: 1.45; }

.update-card__components li {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
}

.update-card__component-tag {
  padding: 1px 8px;
  color: var(--muted);
  font-size: 11px;
  border: 1px solid var(--border);
  border-radius: 999px;
}

.update-card__component-target { color: var(--muted); font-size: 11.5px; }

.update-card__component-stage {
  margin-left: auto;
  color: var(--accent);
  font-size: 11.5px;
}

.update-card__component-stage.is-ready { color: var(--success, #1a7f4b); }
.update-card__component-stage.is-failed { color: var(--danger, #b3261e); }
.update-card__component-stage.is-rolling_back,
.update-card__component-stage.is-rolled_back { color: var(--warning, #9a6200); }

.update-card__blocked {
  display: flex;
  align-items: flex-start;
  padding: 10px 12px;
  gap: 8px;
  color: var(--warning, #9a6200);
  background: color-mix(in srgb, var(--warning, #9a6200) 12%, transparent);
  border-radius: var(--radius-md, 10px);
  font-size: 12.5px;
}

.update-card__blocked svg { margin-top: 2px; flex: none; }
.update-card__blocked strong { display: block; }
.update-card__blocked p { margin: 2px 0 0; }

.update-card__foot {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.update-card__link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--accent);
  font-size: 12.5px;
  text-decoration: none;
}

.update-card__link:hover { text-decoration: underline; }

.update-card__recheck,
.update-card__apply {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 9px 16px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  border-radius: 9px;
  cursor: pointer;
}

.update-card__recheck {
  color: var(--text);
  background: transparent;
  border: 1px solid var(--border);
}

/* Green rather than the accent, because this button means "go" and the app's
   accent is already the colour of every ordinary control. The text colour is
   the shared on-accent token, not white: both --accent and --success are light
   in this theme, and white on either is the washed-out pairing this started
   out with. */
.update-card__apply {
  margin-left: auto;
  color: var(--on-accent);
  background: var(--success);
  border: 0;
}

.update-card__apply:hover:not(:disabled) {
  background: color-mix(in srgb, var(--success) 88%, #fff);
}

.update-card__recheck:disabled,
.update-card__apply:disabled { opacity: 0.55; cursor: not-allowed; }

/* While it works the button keeps its size, so the row does not reflow the
   moment it is pressed. */
.update-card__apply.is-working { cursor: progress; opacity: 1; }

.update-card__ring {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex: none;
}

.update-card__ring svg { width: 22px; height: 22px; transform: rotate(-90deg); }

.update-card__ring-track {
  fill: none;
  stroke: color-mix(in srgb, var(--on-accent) 28%, transparent);
  stroke-width: 3;
}

.update-card__ring-fill {
  fill: none;
  stroke: var(--on-accent);
  stroke-width: 3;
  stroke-linecap: round;
  /* The app's slow step: the ring moves in jumps of ten to thirty points as
     each component changes stage, and easing those over the shared slow
     duration reads as the arc travelling rather than teleporting. */
  transition: stroke-dashoffset var(--motion-slow) var(--ease-standard);
}

.update-card__ring-value {
  position: absolute;
  font-size: 8.5px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.update-card__ring svg.is-spinning { animation: update-ring-spin 1.1s linear infinite; }

.update-card__note {
  margin: 0;
  color: var(--faint);
  font-size: 11.5px;
  line-height: 1.55;
}

.update-card__note--warn { color: var(--warning, #9a6200); }
.update-card__note--good { color: var(--success); }

.spin { animation: update-ring-spin 1.1s linear infinite; }

@keyframes update-ring-spin {
  to { transform: rotate(360deg); }
}

/* The ring's own rotation is the only thing here that carries meaning while
   moving; with reduced motion it stops and the number carries it instead. */
@media (prefers-reduced-motion: reduce) {
  .update-card__ring svg.is-spinning,
  .spin { animation: none; }
  .update-card__ring-fill { transition: none; }
}

@media (max-width: 560px) {
  .update-card__apply { margin-left: 0; width: 100%; justify-content: center; }
  .update-card__auto { margin-left: 0; }
  .update-card__head { flex-wrap: wrap; }
}
</style>
