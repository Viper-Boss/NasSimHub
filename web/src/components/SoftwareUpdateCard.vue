<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  BadgeCheck,
  ChevronRight,
  CircleCheck,
  CircleDashed,
  CircleDot,
  CircleOff,
  CloudDownload,
  CloudOff,
  Download,
  ExternalLink,
  Globe2,
  Hash,
  Layers,
  LoaderCircle,
  Network,
  Package,
  RefreshCw,
  RotateCcw,
  Share2,
  ShieldCheck,
  TriangleAlert,
  Waypoints
} from '@lucide/vue'

import './networkCard.css'
import './panelCard.css'
import type { UpdateComponentName, UpdateOperationComponentState } from '../api/types'
import { i18n, resolvedLocale } from '../i18n'
import { softwareUpdateCardLabels } from '../i18n/softwareUpdateCardLabels'
import { sessionState } from '../state/session'
import {
  applyUpdate,
  attachSoftwareUpdate,
  checkForUpdates,
  ensureInitialUpdateCheck,
  loadReleaseSharing,
  setAutoDownload,
  setReleaseSharing,
  setSoftwareUpdateDownloadMessages,
  setSoftwareUpdateMessages,
  startUpdateDownload,
  useSoftwareUpdate
} from '../state/softwareUpdate'

// The update card on the dashboard.
//
// WHAT IT IS FOR. Updating used to live only in settings, three clicks from
// anywhere, which is the wrong place for the one action an operator takes when
// they hear there is a new version. This card puts the version, what it would
// change, and the button on the page they already have open.
//
// IT SAYS WHAT IS AND IS NOT CHECKED, and that is the part to leave alone.
// The layout follows a mock-up that showed green ticks everywhere -- integrity,
// source agreement, signature, hardware compatibility, a rollback point -- and
// a "3/3 sources agree" badge. Each tile here is DRIVEN BY WHAT THE UPDATER
// REPORTED on this check, never by what the code is supposed to do. The
// signature tile is green only when a mirror returned a manifest that
// verified; the agreement tile counts the mirrors that did; the rollback tile
// is lit only when there is an updater to roll back with. On a check that
// reached nothing, they go dark again, which is correct: nothing was verified.
// There is no hardware-compatibility tile, because nothing checks that.
//
// THE HERO FOLLOWS THE DOWNLOAD. The gateway fetches a new release's images
// in the background as soon as it sees one. While that runs the hero's ring
// counts images fetched; when it is done the ring is full, the steps read
// "waiting to install", and the button is the green "update now" -- pressing
// it is a restart rather than a download.
//
// THE RING IS HONEST TOO, in a smaller way. It is driven by which stage each
// component has reached, not by bytes: `docker pull` runs through
// CombinedOutput in the gateway, so its progress stream is read and discarded
// and no byte count reaches the API. The ring therefore moves in steps.
// Making it look smoother would mean inventing the motion between the steps.
//
// NO SCROLLING INSIDE. See panelCard.css.

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
  signatureVerified,
  releaseSharing,
  savingSharing
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

/**
 * Whether this deployment has an updater at all. The updater is what reports
 * the auto-download preference, so its absence from the check is the signal.
 */
const updaterPresent = computed(() => autoDownload.value !== undefined)

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

/** Whether the ring is showing the download or the install. */
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
 * The hero strip: one sentence for where things stand, and what happens next.
 */
const hero = computed(() => {
  if (checking.value && !updateCheck.value) {
    return { key: 'checking', tone: '', icon: LoaderCircle, spin: true, title: label('checkingTitle'), body: '' }
  }
  if (applying.value) {
    return { key: 'applying', tone: '', icon: Download, spin: false, title: label('heroApplyingTitle'), body: label('updatingHint') }
  }
  switch (status.value) {
    case 'update_available':
      if (blockedReason.value) {
        return { key: 'available', tone: 'is-warn', icon: TriangleAlert, spin: false, title: label('availableTitle'), body: blockedReason.value.body }
      }
      if (downloadReady.value) {
        return { key: 'ready', tone: 'is-good', icon: CircleCheck, spin: false, title: label('heroReadyTitle'), body: label('downloadReadyHint') }
      }
      if (showingDownload.value) {
        return { key: 'downloading', tone: '', icon: CloudDownload, spin: false, title: label('heroDownloadingTitle'), body: label('downloadingHint') }
      }
      if (downloadFailed.value) {
        return { key: 'failed', tone: 'is-warn', icon: TriangleAlert, spin: false, title: label('heroFailedTitle'), body: label('downloadFailedHint') }
      }
      return {
        key: 'available',
        tone: '',
        icon: CloudDownload,
        spin: false,
        title: label('availableTitle'),
        body: autoDownload.value ? label('downloadQueuedHint') : label('autoDownloadHintShort')
      }
    case 'up_to_date':
      return { key: 'current', tone: 'is-good', icon: CircleCheck, spin: false, title: label('upToDateTitle'), body: label('upToDateBody') }
    case 'development':
      return { key: 'development', tone: 'is-idle', icon: Package, spin: false, title: label('developmentTitle'), body: label('developmentBody') }
    default:
      return { key: 'unavailable', tone: 'is-idle', icon: CloudOff, spin: false, title: label('unavailableTitle'), body: label('unavailableBody') }
  }
})

/**
 * The three steps under the hero, each from reported state: the manifest
 * verified, the images on the machine, and the install.
 */
const steps = computed(() => {
  const downloadStep = downloadReady.value
    ? 'is-done'
    : showingDownload.value
      ? 'is-active'
      : ''
  const installStep = applying.value || downloadReady.value ? 'is-active' : ''
  return [
    { key: 'signature', tone: signatureVerified.value ? 'is-done' : '', name: label('stepSignature') },
    { key: 'download', tone: downloadStep, name: label('stepDownload') },
    {
      key: 'install',
      tone: installStep,
      name: applying.value ? label('stepInstalling') : label('stepInstall')
    }
  ]
})

/**
 * A step's mark: ticked when done, turning while it is actually running, a
 * filled dot when it is the next thing and waiting on the operator, dashed
 * when it has not been reached.
 */
function stepIcon(step: { key: string; tone: string }) {
  if (step.tone === 'is-done') return CircleCheck
  if (step.tone !== 'is-active') return CircleDashed
  return stepRunning(step) ? LoaderCircle : CircleDot
}

function stepRunning(step: { key: string; tone: string }): boolean {
  if (step.tone !== 'is-active') return false
  return step.key === 'download' || (step.key === 'install' && applying.value)
}

/** Friendly names for the mirrors this build ships with; anything else by host. */
const MIRROR_NAMES: Record<string, string> = {
  'raw.githubusercontent.com': 'GitHub',
  'cdn.jsdelivr.net': 'jsDelivr',
  'fastly.jsdelivr.net': 'Fastly'
}

/**
 * The HTTPS mirrors, one tile each: what each returned this time. Lit only
 * when it returned a manifest that verified.
 */
const sources = computed(() =>
  releaseSources.value
    .filter(entry => entry.kind === 'mirror')
    .map(entry => ({
      key: `mirror-${entry.name}`,
      icon: Globe2,
      name: MIRROR_NAMES[entry.name] || entry.name,
      host: entry.name,
      wired: entry.state === 'verified',
      tone: entry.state === 'verified' ? 'is-good' : entry.state === 'unreachable' ? 'is-idle' : 'is-bad',
      detail: entry.state === 'verified' ? label('sourceVerified') : label(`sourceState_${entry.state}`),
      version: entry.version || ''
    }))
)

const mirrorTiles = computed(() => sources.value)

/**
 * The two peer networks, as this check found them: the same signed manifest,
 * handed over by other gateways.
 *
 * Absent from the check means this gateway did not ask over that network --
 * it is switched off, or the gateway predates the peer networks -- and that
 * is "not taking part", never a pass. Asked with no answer is its own state:
 * early on, with few gateways holding a release, it is the common one, and it
 * must not read as a failure.
 */
function peerSource(kind: 'dht' | 'nkn') {
  const entry = releaseSources.value.find(source => source.kind === kind)
  const base = {
    key: kind,
    icon: kind === 'dht' ? Waypoints : Network,
    name: label(kind === 'dht' ? 'sourceDHT' : 'sourceNKN')
  }
  if (!entry) return { ...base, tone: '', detail: label('sourcePeerIdle'), title: label('sourcePeerIdleNote') }
  if (entry.state === 'verified') {
    return {
      ...base,
      tone: 'is-good',
      detail: entry.peers ? label('sourcePeerVerified', { count: entry.peers }) : label('sourceVerified'),
      title: entry.version || ''
    }
  }
  if (entry.state === 'unreachable') {
    return { ...base, tone: '', detail: label('sourcePeerSilent'), title: label('sourcePeerSilentNote') }
  }
  return { ...base, tone: 'is-bad', detail: label(`sourceState_${entry.state}`), title: '' }
}

const peerSources = computed(() => [peerSource('dht'), peerSource('nkn')])

function toggleSharing(): void {
  if (!releaseSharing.value) return
  void setReleaseSharing(!releaseSharing.value.enabled)
}

const sourcesSummary = computed(() =>
  sourceAgreement.value.total === 0
    ? label('sourcesNone')
    : label('sourcesAgree', {
        agreeing: sourceAgreement.value.agreeing,
        total: sourceAgreement.value.total
      })
)

/** The agreement as a fraction, counted from what the mirrors returned. */
const agreementFraction = computed(() =>
  sourceAgreement.value.total === 0
    ? label('notAvailable')
    : `${sourceAgreement.value.agreeing}/${sourceAgreement.value.total}`
)

/**
 * The verification tiles, each lit only by what this check reported.
 *
 * Integrity is always enforced -- every pull is by digest and Docker checks
 * it. Signature is lit when a mirror returned a manifest that verified against
 * the compiled-in key. Agreement is lit when at least two mirrors returned the
 * same verified release; with one there is nothing to agree with. Rollback is
 * lit when there is an updater: it is the updater that restores the previous
 * images when a restarted component does not come up.
 */
const verifications = computed(() => [
  {
    key: 'integrity',
    icon: Hash,
    name: label('verifyIntegrity'),
    value: label('verifyIntegrityValue'),
    note: label('verifyIntegrityNote'),
    active: true
  },
  {
    key: 'consensus',
    icon: Layers,
    name: label('verifyConsensus'),
    value: agreementFraction.value,
    note: `${sourcesSummary.value}. ${label('verifyConsensusNote')}`,
    active: sourceAgreement.value.agreeing >= 2
  },
  {
    key: 'signature',
    icon: BadgeCheck,
    name: label('verifySignature'),
    value: signatureVerified.value ? label('verifySignatureValue') : label('verifySignatureNone'),
    note: signingKey.value
      ? `${label('verifySignatureNote')} ${label('signingKeyInline', { fingerprint: signingKey.value })}`
      : label('verifySignatureNote'),
    active: signatureVerified.value
  },
  {
    key: 'rollback',
    icon: RotateCcw,
    name: label('verifyRollback'),
    value: updaterPresent.value ? label('verifyRollbackValue') : label('verifyRollbackNone'),
    note: label('verifyRollbackNote'),
    active: updaterPresent.value
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

function stageTone(state?: UpdateOperationComponentState): string {
  switch (state) {
    case 'ready':
      return 'is-good'
    case 'failed':
      return 'is-bad'
    case 'rolling_back':
    case 'rolled_back':
      return 'is-warn'
    default:
      return 'is-accent'
  }
}

/**
 * The key this gateway verifies releases against, as the first four bytes in
 * hex -- the form the signing console prints beside its own key. The two
 * matching is how an operator tells that this gateway will accept what they
 * sign. Reported by the gateway; never derived here.
 */
const signingKey = computed(() => updateCheck.value?.signing_key || '')

function formatWhen(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(resolvedLocale(), {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

/**
 * The last install that finished, as the updater recorded it. Nothing when
 * there is no record -- and "none yet" only when there is an updater that
 * would have kept one, because without one the absence says nothing.
 */
const lastUpdate = computed(() => {
  const operation = updateCheck.value?.last_operation
  if (!operation) return null
  return {
    version: operation.target_version,
    succeeded: operation.state === 'succeeded',
    when: formatWhen(operation.finished_at || operation.started_at)
  }
})

/** When the check ran, as a local time -- or nothing if it never did. */
const checkedAt = computed(() => {
  const value = updateCheck.value?.checked_at
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString(resolvedLocale(), { hour: '2-digit', minute: '2-digit' })
})

// The ring. r=30 in a 70-box, 5px stroke.
const RING_RADIUS = 30
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

/** The ring is shown while there is a download or an install to report. */
const ringShown = computed(
  () => hasUpdate.value && (showingDownload.value || applying.value || downloadReady.value)
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

/**
 * The ring's number: images fetched while downloading -- and once they are all
 * here, the same count, which is then full -- or the ladder while installing.
 */
const ringValue = computed(() =>
  !applying.value && downloadReady.value
    ? downloadProgress.value
    : showingDownload.value ? downloadProgress.value : progress.value
)

const ringOffset = computed(
  () => RING_CIRCUMFERENCE * (1 - Math.min(100, Math.max(0, ringValue.value)) / 100)
)

const ringCaption = computed(() => {
  if (applying.value) return label('updating')
  if (showingDownload.value) return label('downloading')
  return label('ringDownloaded')
})

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
  void loadReleaseSharing()
})

onBeforeUnmount(() => {
  detach?.()
  detach = undefined
})
</script>

<template>
  <article v-if="visible" class="update-card net-card pc-card" :class="`update-card--${hero.key}`">
    <header class="net-card__head">
      <div>
        <div class="net-card__title">
          <RefreshCw class="net-card__icon" />
          <h4>{{ label('title') }}</h4>
        </div>
        <p class="net-card__eyebrow">{{ label('subtitle') }}</p>
      </div>
      <button
        v-if="autoDownloadKnown"
        type="button"
        class="net-card__badge update-card__auto"
        :class="{ 'is-idle': !autoDownload }"
        role="switch"
        :aria-checked="autoDownload ? 'true' : 'false'"
        :disabled="savingPreference"
        :title="label('autoDownloadHint')"
        @click="toggleAutoDownload"
      >
        {{ autoDownload ? label('autoDownloadOn') : label('autoDownloadOff') }}
      </button>
    </header>

    <div class="pc-body">
      <section class="pc-hero" :class="hero.tone" aria-live="polite">
        <span class="pc-hero__mark">
          <component :is="hero.icon" :size="21" :class="{ 'pc-spin': hero.spin }" />
        </span>
        <div class="pc-hero__text">
          <p class="pc-hero__title">{{ hero.title }}</p>
          <p v-if="hero.body" class="pc-hero__body">{{ hero.body }}</p>
        </div>
        <!-- The ring reports the download while it runs and the component
             ladder while installing. role/aria say the same thing to a screen
             reader, and the value is left off while it is indeterminate rather
             than reported as zero. -->
        <span
          v-if="ringShown"
          class="pc-ring"
          :class="{ 'is-done': !applying && downloadReady }"
          role="progressbar"
          :aria-label="showingDownload || downloadReady ? label('downloadProgressLabel') : label('progressLabel')"
          :aria-valuenow="ringIndeterminate ? undefined : ringValue"
          aria-valuemin="0"
          aria-valuemax="100"
          :title="applying ? label('progressNote') : undefined"
        >
          <svg viewBox="0 0 70 70" aria-hidden="true" :class="{ 'is-spinning': ringIndeterminate }">
            <circle class="pc-ring__track" cx="35" cy="35" :r="RING_RADIUS" />
            <circle
              class="pc-ring__fill"
              cx="35"
              cy="35"
              :r="RING_RADIUS"
              :stroke-dasharray="RING_CIRCUMFERENCE"
              :stroke-dashoffset="ringIndeterminate ? RING_CIRCUMFERENCE * 0.72 : ringOffset"
            />
          </svg>
          <span class="pc-ring__label">
            <span v-if="!ringIndeterminate" class="pc-ring__value">{{ ringValue }}%</span>
            <span class="pc-ring__caption">{{ ringCaption }}</span>
          </span>
        </span>
        <ol v-if="hasUpdate" class="pc-steps">
          <template v-for="(step, index) in steps" :key="step.key">
            <li
              v-if="index > 0"
              class="pc-step__line"
              :class="{ 'is-done': steps[index - 1]?.tone === 'is-done' }"
              aria-hidden="true"
            ></li>
            <li class="pc-step" :class="step.tone">
              <component
                :is="stepIcon(step)"
                :size="14"
                :class="{ 'pc-spin': stepRunning(step) }"
              />
              {{ step.name }}
            </li>
          </template>
        </ol>
      </section>

      <section class="pc-section">
        <div class="pc-section__head">
          <Package :size="15" />
          <h5>{{ label('versionTitle') }}</h5>
          <a
            v-if="hasUpdate && updateCheck?.release_url"
            class="pc-section__aside update-card__notes"
            :href="updateCheck.release_url"
            target="_blank"
            rel="noreferrer noopener"
          >
            {{ label('releaseNotes') }}
            <ExternalLink :size="12" />
          </a>
          <span v-else-if="checkedAt" class="pc-section__aside">
            {{ label('lastChecked', { time: checkedAt }) }}
          </span>
        </div>
        <ul class="pc-rows">
          <!-- One row for the move from this version to the next: the arrow
               says "update" without a chip to say it again. -->
          <li v-if="hasUpdate" class="pc-row">
            <span class="pc-row__name">{{ label('versionChange') }}</span>
            <span class="pc-row__value">
              {{ currentVersion }}
              <span class="pc-arrow" aria-hidden="true">→</span>
              <span class="pc-next">{{ latestVersion }}</span>
            </span>
          </li>
          <li v-else class="pc-row">
            <span class="pc-row__name">{{ label('currentVersion') }}</span>
            <span class="pc-row__value">
              {{ currentVersion }}
              <span v-if="status === 'up_to_date'" class="pc-chip is-good">{{ label('chipLatest') }}</span>
            </span>
          </li>
          <!-- With no release on offer the section has room for the facts
               around the version: where releases come from, which key they
               must be signed with, and what happened last time. Each row is
               there only when the gateway reported it. -->
          <template v-if="!hasUpdate">
            <li class="pc-row" :title="label('channelNote')">
              <span class="pc-row__name">{{ label('channelTitle') }}</span>
              <span class="pc-row__value">{{ label('channelStable') }}</span>
            </li>
            <li v-if="signingKey" class="pc-row" :title="label('signingKeyNote')">
              <span class="pc-row__name">{{ label('signingKeyTitle') }}</span>
              <span class="pc-row__value update-card__mono">{{ signingKey }}</span>
            </li>
            <li v-if="lastUpdate" class="pc-row">
              <span class="pc-row__name">{{ label('lastUpdateTitle') }}</span>
              <span class="pc-row__value">
                {{ lastUpdate.version }}
                <span class="pc-chip" :class="lastUpdate.succeeded ? 'is-good' : 'is-bad'">
                  {{ lastUpdate.succeeded ? label('lastUpdateSucceeded') : label('lastUpdateFailed') }}
                </span>
                <span v-if="lastUpdate.when" class="update-card__muted">{{ lastUpdate.when }}</span>
              </span>
            </li>
            <li v-else-if="updaterPresent" class="pc-row">
              <span class="pc-row__name">{{ label('lastUpdateTitle') }}</span>
              <span class="pc-row__value update-card__muted">{{ label('lastUpdateNone') }}</span>
            </li>
          </template>
          <li v-if="hasUpdate" class="pc-row">
            <span class="pc-row__name">{{ label('componentsTitle') }}</span>
            <span v-if="displayedComponents.length" class="pc-row__value">
              <span
                v-for="component in displayedComponents"
                :key="component.name"
                class="pc-chip"
                :class="applying ? stageTone(componentState(component.name)) : 'is-accent'"
                :title="
                  applying && componentState(component.name)
                    ? stageLabel(componentState(component.name))
                    : component.target_version
                      ? label('componentTo', { version: component.target_version })
                      : label('componentUnchanged')
                "
              >
                {{ componentLabels[component.name] }}
                <template v-if="applying && componentState(component.name)">
                  · {{ stageLabel(componentState(component.name)) }}
                </template>
              </span>
            </span>
            <span v-else class="pc-row__value update-card__muted">{{ label('componentsNone') }}</span>
          </li>
        </ul>
      </section>

      <section class="pc-section">
        <div class="pc-section__head">
          <Globe2 :size="15" />
          <h5>{{ label('sourcesTitle') }}</h5>
          <span class="pc-section__aside" :title="label('sourcesNote')">{{ sourcesSummary }}</span>
        </div>
        <div v-if="mirrorTiles.length" class="pc-tiles update-card__mirrors">
          <div
            v-for="source in mirrorTiles"
            :key="source.key"
            class="pc-tile"
            :class="{ 'is-idle': !source.wired }"
            :title="source.version ? `${source.host} · ${source.version}` : source.host"
          >
            <span class="pc-tile__label">
              <component :is="source.icon" :size="13" />
              <span>{{ source.name }}</span>
            </span>
            <span class="pc-state" :class="source.tone">
              <CircleCheck v-if="source.wired" :size="13" />
              <CircleOff v-else :size="13" />
              {{ source.detail }}
            </span>
          </div>
        </div>
        <div class="update-card__planned">
          <span
            v-for="source in peerSources"
            :key="source.key"
            class="update-card__planned-item"
            :title="source.title || undefined"
          >
            <component :is="source.icon" :size="13" />
            {{ source.name }}
            <span class="pc-chip" :class="source.tone">{{ source.detail }}</span>
          </span>
          <button
            v-if="releaseSharing"
            type="button"
            class="update-card__share"
            :class="{ 'is-idle': !releaseSharing.enabled }"
            role="switch"
            :aria-checked="releaseSharing.enabled ? 'true' : 'false'"
            :disabled="savingSharing"
            :title="label('shareHint')"
            @click="toggleSharing"
          >
            <Share2 :size="12" />
            {{ releaseSharing.enabled ? label('shareOn') : label('shareOff') }}
          </button>
        </div>
      </section>

      <section class="pc-section">
        <div class="pc-section__head">
          <ShieldCheck :size="15" />
          <h5>{{ label('verifyTitle') }}</h5>
        </div>
        <div class="pc-tiles update-card__checks">
          <!-- The explanation is the tooltip; the value is what has to stay
               legible, because "not verified" is the whole point of a tile. -->
          <div
            v-for="entry in verifications"
            :key="entry.key"
            class="pc-tile"
            :class="{ 'is-idle': !entry.active }"
            :title="entry.note"
          >
            <span class="pc-tile__label">
              <component :is="entry.icon" :size="13" />
              <span>{{ entry.name }}</span>
            </span>
            <span class="pc-state" :class="entry.active ? 'is-good' : 'is-idle'">
              <CircleCheck v-if="entry.active" :size="13" />
              <CircleOff v-else :size="13" />
              {{ entry.value }}
            </span>
          </div>
        </div>
      </section>

      <footer class="pc-foot">
        <!-- Where the update history and the full settings live: the
             mock-up's "view log", named for what is there. -->
        <RouterLink
          class="pc-button"
          :to="{ name: 'settings', params: { section: 'about' } }"
        >
          {{ label('updateDetails') }}
          <ChevronRight :size="15" />
        </RouterLink>
        <button
          type="button"
          class="pc-button"
          :class="{ 'pc-button--primary': !hasUpdate }"
          :disabled="checking || applying"
          @click="checkForUpdates(true)"
        >
          <RefreshCw :size="15" :class="{ 'pc-spin': checking }" />
          {{ checking ? label('checking') : label('recheck') }}
        </button>

        <button
          v-if="canDownloadNow"
          type="button"
          class="pc-button"
          @click="startUpdateDownload"
        >
          <Download :size="15" />
          {{ downloadFailed ? label('downloadRetry') : label('downloadNow') }}
        </button>

        <button
          v-if="hasUpdate"
          type="button"
          class="pc-button pc-button--primary"
          :class="{ 'is-working': applying || showingDownload, 'pc-button--go': downloadReady }"
          :disabled="!canApply"
          @click="onUpdate"
        >
          <LoaderCircle v-if="applying || showingDownload" :size="15" class="pc-spin" />
          <Download v-else :size="15" />
          {{ applying ? label('updating') : showingDownload ? label('downloading') : label('update') }}
        </button>
      </footer>
    </div>
  </article>
</template>

<style scoped>
.update-card--ready { border-color: var(--pc-good-line); }

/* The switch wears the head's pill, and is a button. */
.update-card__auto {
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.update-card__auto:disabled { cursor: progress; opacity: 0.7; }

.update-card__auto:focus-visible {
  outline: 2px solid var(--pc-accent);
  outline-offset: 2px;
}

.update-card__notes {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
  color: var(--pc-accent);
  font-size: 11.5px;
  font-weight: 500;
  text-decoration: none;
}

.update-card__notes:hover { text-decoration: underline; }

.update-card__muted { color: var(--pc-faint); font-weight: 400; }

.update-card__mono {
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  letter-spacing: 0.04em;
}

.update-card__planned {
  display: flex;
  flex-wrap: wrap;
  margin: 0;
  gap: 6px 14px;
}

.update-card__planned-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--pc-idle);
  font-size: 11.5px;
}

/* Sharing sits at the end of the peer row: it is about those two networks,
   and a line of its own would make the card taller than the row allows. */
.update-card__share {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: auto;
  padding: 2px 8px;
  border: 1px solid var(--pc-good-line);
  border-radius: 999px;
  background: var(--pc-good-soft);
  color: var(--pc-good);
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.update-card__share.is-idle {
  border-color: var(--pc-idle-line);
  background: none;
  color: var(--pc-idle);
}

.update-card__share:focus-visible {
  outline: 2px solid var(--pc-accent);
  outline-offset: 2px;
}

.update-card__share:disabled { cursor: progress; opacity: 0.7; }

/* One row each, at every width this card is drawn in the tall row: three
   mirrors across, four checks across. Wrapping either to a second row is what
   made this card taller than the drawings beside it at the narrower tracks
   (measured 876px against their 734px at a 386px track), and a row as tall as
   its tallest card leaves the drawings with a void under them. A fourth or
   fifth mirror, set by the operator, wraps -- rarely, and honestly. */
.update-card { container-type: inline-size; }

.update-card__mirrors { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.update-card__checks { grid-template-columns: repeat(4, minmax(0, 1fr)); }

/* At the narrow tracks the check icons go, so the four names still fit. */
@container (max-width: 440px) {
  .update-card__checks .pc-tile,
  .update-card__mirrors .pc-tile { padding: 8px 9px; }
  .update-card__checks .pc-tile__label > svg { display: none; }
}

/* On a phone the card has the width to itself and no row to match. */
@container (max-width: 330px) {
  .update-card__checks { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
