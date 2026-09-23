import { computed, ref } from 'vue'

import { API_ROOT, fixtureMode, gateway, get, parseUpdateCheck, writeJSON } from '../api/client'
import type {
  UpdateCheck,
  UpdateComponent,
  UpdateComponentName,
  UpdateDownload,
  UpdateDownloadState,
  UpdateOperation,
  UpdateOperationComponentState,
  UpdateSource
} from '../api/types'
import { requestConfirmation } from './confirmation'
import { showError } from './feedback'
import { monitorRuntimeUpdateOperation, subscribeRuntimeUpdateOperations } from './runtimeEvents'
import {
  clearApplicationUpdateNotice,
  refreshApplication,
  setApplicationUpdateNoticeSuppressed
} from './staleAssetRecovery'

// The software update flow, owned in one place.
//
// WHY THIS IS A MODULE AND NOT A COMPONENT'S OWN STATE. Two screens can start
// an update: the About panel in settings, and the update card at the foot of
// the dashboard. The thing that makes them unable to own this separately is
// that progress arrives over ONE shared server-sent-events stream, and the
// call that points that stream at an operation --
// monitorRuntimeUpdateOperation -- is global. Two components each calling it
// means whichever runs last decides what the other one sees, and a component
// unmounting mid-update would clear the stream out from under the one still
// showing a progress ring.
//
// The second reason is duller and matters more over time: applying an update
// has rules that are easy to leave out of a copy. Hardware changes need a
// confirmation first. The page has to be told to stop nagging about a stale
// bundle while its own replacement is being installed. A successful operation
// ends by reloading the page, because the running JavaScript is now the old
// version. A second implementation of this would start out identical and
// drift, and the first thing to fall out of it would be one of those rules.
//
// So: one controller, any number of screens reading it.

const updateCheck = ref<UpdateCheck | null>(null)
const checking = ref(false)
const applying = ref(false)
const startingUpdate = ref(false)
const downloadStarting = ref(false)
const savingPreference = ref(false)

/** How many mounted screens are watching. The listener lives while any are. */
let watchers = 0
let closeRuntimeSubscription: (() => void) | undefined
let initialCheckStarted = false

/**
 * The operation this screen should be drawing.
 *
 * Guarded rather than taken straight from the check result, because a stale
 * operation is worse than none: an operation for a version other than the one
 * on offer would drive a progress ring for an update the operator is not
 * watching, and `startingUpdate` covers the gap between the apply request
 * leaving and the first real operation arriving, where the previous
 * operation's components are still in hand.
 */
const activeOperation = computed<UpdateOperation | undefined>(() => {
  const current = updateCheck.value
  const operation = current?.operation
  if (
    !applying.value ||
    startingUpdate.value ||
    !operation ||
    operation.target_version !== current?.latest_version
  ) {
    return undefined
  }
  return operation
})

const changedComponents = computed(
  () => updateCheck.value?.components?.filter(component => component.changed) || []
)

/**
 * The components to list: what the check says will change, or -- once an
 * operation is running -- what that operation is actually working through.
 */
const displayedComponents = computed<UpdateComponent[]>(() => {
  const current = updateCheck.value
  const operationComponents = activeOperation.value?.components
  if (!applying.value || !operationComponents?.length) return changedComponents.value
  return operationComponents.map(
    component =>
      current?.components?.find(candidate => candidate.name === component.name) || {
        name: component.name,
        changed: true
      }
  )
})

const componentStates = computed(() => {
  const states = new Map<UpdateComponentName, UpdateOperationComponentState>()
  for (const component of activeOperation.value?.components || []) {
    states.set(component.name, component.state)
  }
  return states
})

/**
 * How far through its own work each component state is.
 *
 * THESE ARE WEIGHTS, NOT MEASUREMENTS, and the difference is worth stating
 * because the number they produce looks like a measurement. The gateway pulls
 * images with `docker pull` and reads the result, not the progress stream, so
 * no byte count exists anywhere to divide. What is real is the state each
 * component is in; these weights turn that ladder into a number that only ever
 * moves forwards, which is what a progress ring needs in order not to lie in
 * the other direction -- by jumping backwards, or by sitting at zero through a
 * long pull.
 *
 * A rollback counts as finished rather than failed. The work really is over at
 * that point, and the outcome is told in words beside the ring.
 */
const componentProgressWeights: Record<UpdateOperationComponentState, number> = {
  pending: 0,
  pulling: 0.2,
  staged: 0.45,
  restarting: 0.72,
  rolling_back: 0.78,
  rolled_back: 1,
  ready: 1,
  failed: 1
}

/** 0-100, for a ring or a bar. Zero when nothing is being applied. */
const progress = computed(() => {
  const components = displayedComponents.value
  if (!applying.value || components.length === 0) return 0
  const total = components.reduce((sum, component) => {
    const state = componentStates.value.get(component.name) || 'pending'
    return sum + componentProgressWeights[state]
  }, 0)
  return Math.round((total / components.length) * 100)
})

function componentState(name: UpdateComponentName): UpdateOperationComponentState | undefined {
  return componentStates.value.get(name)
}

// ---------------------------------------------------------------------------
// Background download.
//
// The gateway fetches a new release's images on its own, ahead of the operator
// pressing anything, so that installing is a restart rather than a transfer.
// What arrives here is the updater's record of that. The shell's parser passes
// these three fields through unchecked -- it is in the startup bundle and this
// module is not -- so they are validated here, where they are used.
// ---------------------------------------------------------------------------

const DOWNLOAD_STATES = new Set<UpdateDownloadState>(['pending', 'downloading', 'ready', 'failed'])
const SOURCE_STATES = new Set<UpdateSource['state']>([
  'verified',
  'unreachable',
  'invalid_signature',
  'malformed'
])

/**
 * The download for the version ON OFFER, or nothing.
 *
 * A record for any other version is discarded: "ready" beside a newer release
 * than the one downloaded would invite pressing install on the strength of
 * work done for something else. The updater already filters this; checking
 * again here costs one comparison and does not depend on it.
 */
const download = computed<UpdateDownload | undefined>(() => {
  const current = updateCheck.value
  const candidate = current?.download
  if (
    !candidate ||
    typeof candidate !== 'object' ||
    typeof candidate.version !== 'string' ||
    candidate.version !== current?.latest_version ||
    !DOWNLOAD_STATES.has(candidate.state)
  ) {
    return undefined
  }
  return candidate
})

const downloading = computed(
  () =>
    downloadStarting.value ||
    download.value?.state === 'downloading' ||
    download.value?.state === 'pending'
)
const downloadReady = computed(() => download.value?.state === 'ready')
const downloadFailed = computed(() => download.value?.state === 'failed')

/** Undefined when this deployment has no updater to hold a preference. */
const autoDownload = computed<boolean | undefined>(() =>
  typeof updateCheck.value?.auto_download === 'boolean' ? updateCheck.value.auto_download : undefined
)

/**
 * How many of the release's changed images are on the machine, 0-100.
 *
 * COUNTED, NOT ESTIMATED. Unlike the install ring, which weights a ladder of
 * stages, this is simply images present out of images needed -- the updater
 * reports each one as it lands. It moves in steps because the thing it counts
 * comes in steps.
 */
const downloadProgress = computed(() => {
  const parts = download.value?.components || []
  if (parts.length === 0) return downloadReady.value ? 100 : 0
  const done = parts.filter(part => part.state === 'ready').length
  return Math.round((done / parts.length) * 100)
})

/** The release mirrors, as the last check found them. */
const releaseSources = computed<UpdateSource[]>(() => {
  const list = updateCheck.value?.sources
  if (!Array.isArray(list)) return []
  return list.filter(
    (entry): entry is UpdateSource =>
      !!entry &&
      typeof entry === 'object' &&
      typeof entry.name === 'string' &&
      SOURCE_STATES.has(entry.state)
  )
})

/**
 * Mirrors that returned a verifying manifest for the version on offer, out of
 * all mirrors asked. Counted from what the updater reported, never assumed.
 */
const sourceAgreement = computed(() => {
  const latest = updateCheck.value?.latest_version
  const all = releaseSources.value
  return {
    agreeing: all.filter(entry => entry.state === 'verified' && entry.version === latest).length,
    total: all.length
  }
})

/**
 * Whether the release on offer came through signature verification.
 *
 * True exactly when a mirror reported a VERIFIED manifest for latest_version:
 * the updater only ever reports a version it has verified against the key
 * compiled into this build, so a latest_version with no verified source
 * behind it would be a version nobody vouched for.
 */
const signatureVerified = computed(() => sourceAgreement.value.agreeing > 0)

let downloadPoll: ReturnType<typeof setTimeout> | undefined

/**
 * While a download runs, look again every few seconds.
 *
 * Polled rather than streamed: the download is the updater's own background
 * work, not an operation on the runtime event stream, and it changes a handful
 * of times over minutes. Only while something is watching, and only while
 * there is something to watch.
 */
function scheduleDownloadPoll(): void {
  if (downloadPoll) globalThis.clearTimeout(downloadPoll)
  downloadPoll = undefined
  if (!downloading.value || watchers === 0) return
  downloadPoll = globalThis.setTimeout(() => {
    downloadPoll = undefined
    void checkForUpdates()
  }, 8000)
}

let downloadStartFailedMessage = 'The download could not be started.'
let preferenceFailedMessage = 'The setting could not be saved.'

export function setSoftwareUpdateDownloadMessages(messages: {
  downloadStartFailed: string
  preferenceFailed: string
}): void {
  downloadStartFailedMessage = messages.downloadStartFailed
  preferenceFailedMessage = messages.preferenceFailed
}

/** Fetch the offered release's images now. Never installs anything. */
export async function startUpdateDownload(): Promise<void> {
  const current = updateCheck.value
  if (!current?.apply_available || downloading.value || applying.value) return
  downloadStarting.value = true
  try {
    if (!fixtureMode) {
      const started = (await writeJSON(`${API_ROOT}/updates/download`, 'POST', {}, 202)) as UpdateDownload
      if (updateCheck.value) updateCheck.value = { ...updateCheck.value, download: started }
    }
  } catch {
    showError(downloadStartFailedMessage)
  } finally {
    downloadStarting.value = false
    scheduleDownloadPoll()
  }
}

/**
 * Switch background downloading on or off.
 *
 * Shown as changed at once and put back if the save fails, because a switch
 * that waits on a network round trip before moving reads as broken.
 */
export async function setAutoDownload(enabled: boolean): Promise<void> {
  if (savingPreference.value || autoDownload.value === undefined) return
  const previous = autoDownload.value
  savingPreference.value = true
  if (updateCheck.value) updateCheck.value = { ...updateCheck.value, auto_download: enabled }
  try {
    if (!fixtureMode) {
      const saved = (await writeJSON(
        `${API_ROOT}/updates/preferences`,
        'PUT',
        { auto_download: enabled },
        200
      )) as { auto_download?: unknown }
      if (updateCheck.value && typeof saved?.auto_download === 'boolean') {
        updateCheck.value = { ...updateCheck.value, auto_download: saved.auto_download }
      }
    }
  } catch {
    if (updateCheck.value) updateCheck.value = { ...updateCheck.value, auto_download: previous }
    showError(preferenceFailedMessage)
  } finally {
    savingPreference.value = false
  }
}

/**
 * The check, including the background-download fields.
 *
 * The shell's parser leaves download, auto_download and sources out: it is in
 * the startup bundle every visitor downloads, and only these screens use them.
 * So they are read here from the same response, and validated where they are
 * used (see `download` and `releaseSources` above). The fixture gateway builds
 * the whole object itself, so it goes through the gateway unchanged.
 */
async function fetchUpdateCheck(refresh: boolean): Promise<UpdateCheck> {
  if (fixtureMode) return gateway.checkForUpdates(refresh)
  const raw = await get(`${API_ROOT}/updates/check${refresh ? '?refresh=1' : ''}`)
  const source = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  return {
    ...parseUpdateCheck(raw),
    download: source.download as UpdateCheck['download'],
    auto_download: source.auto_download as UpdateCheck['auto_download'],
    sources: source.sources as UpdateCheck['sources']
  }
}

export async function checkForUpdates(refresh = false): Promise<void> {
  if (checking.value) return
  checking.value = true
  try {
    const result = await fetchUpdateCheck(refresh)
    updateCheck.value = result
    if (result.operation?.state === 'running') {
      // An update started before this screen was open -- a reload during an
      // update, or the other screen started it. Pick it up rather than showing
      // an idle card while the gateway is restarting underneath it.
      applying.value = true
      monitorRuntimeUpdateOperation(result.operation.id)
    } else {
      applying.value = false
      monitorRuntimeUpdateOperation()
    }
  } catch {
    // A failed check is reported as "unavailable" rather than thrown, because
    // every caller would otherwise have to invent this same fallback, and the
    // card has a real state for it. The endpoint also answers 200 with
    // status `unavailable` for its own failures, so this keeps one shape.
    updateCheck.value = {
      status: 'unavailable',
      current_version: updateCheck.value?.current_version || '',
      checked_at: new Date().toISOString(),
      apply_available: false,
      hardware_confirmation_required: false
    }
    applying.value = false
    monitorRuntimeUpdateOperation()
  } finally {
    checking.value = false
    scheduleDownloadPoll()
  }
}

function handleOperation(operation: UpdateOperation): void {
  const trackedID = updateCheck.value?.operation?.id
  if (trackedID && trackedID !== operation.id) return
  if (updateCheck.value) updateCheck.value = { ...updateCheck.value, operation }
  startingUpdate.value = false
  if (operation.state === 'running') {
    applying.value = true
    return
  }
  monitorRuntimeUpdateOperation()
  clearApplicationUpdateNotice()
  if (operation.state === 'succeeded') {
    // The code running this page has just been replaced on disk. Reloading is
    // not a courtesy; continuing to run the old bundle against the new API is
    // how a version skew bug gets reported as a broken page.
    refreshApplication()
  } else {
    applying.value = false
    showError(updateFailedMessage)
  }
}

function acceptRuntimeOperation(operation: UpdateOperation): void {
  const trackedID = updateCheck.value?.operation?.id
  if (!applying.value && trackedID !== operation.id) return
  handleOperation(operation)
}

/**
 * The two sentences this module needs from the locale.
 *
 * Passed in by whichever screen mounts first rather than imported, because
 * this module is shared by a settings panel and a dashboard card whose strings
 * live in different bundles, and a module-level `useI18n()` outside a
 * component's setup is not available here.
 */
let updateFailedMessage = 'Update failed.'
let updateStartFailedMessage = 'The update could not be started.'
let hardwareConfirmation = {
  title: 'Hardware update',
  message: 'This update changes hardware components.',
  confirmLabel: 'Update'
}

export function setSoftwareUpdateMessages(messages: {
  updateFailed: string
  updateStartFailed: string
  hardwareTitle: string
  hardwareWarning: string
  confirmUpdate: string
}): void {
  updateFailedMessage = messages.updateFailed
  updateStartFailedMessage = messages.updateStartFailed
  hardwareConfirmation = {
    title: messages.hardwareTitle,
    message: messages.hardwareWarning,
    confirmLabel: messages.confirmUpdate
  }
}

export async function applyUpdate(): Promise<void> {
  const current = updateCheck.value
  if (!current?.apply_available || !current.latest_version || applying.value) return

  let confirmHardware = false
  if (current.hardware_confirmation_required) {
    // Asked before anything is sent, not after: this is the update that can
    // leave a modem on a firmware the operator did not choose.
    confirmHardware = await requestConfirmation(hardwareConfirmation)
    if (!confirmHardware) return
  }

  startingUpdate.value = true
  applying.value = true
  setApplicationUpdateNoticeSuppressed(true)
  try {
    const operation = await gateway.applySoftwareUpdate(current.latest_version, confirmHardware)
    // The event stream can deliver the first frame before this promise
    // resolves. When it has, that frame is newer than this response, so it
    // wins -- otherwise a ring that had already started moving would snap back
    // to the operation's opening state.
    const observed = updateCheck.value?.operation
    updateCheck.value = {
      ...current,
      operation: observed?.id === operation.id ? observed : operation
    }
    startingUpdate.value = false
    monitorRuntimeUpdateOperation(operation.id)
  } catch {
    startingUpdate.value = false
    applying.value = false
    setApplicationUpdateNoticeSuppressed(false)
    showError(updateStartFailedMessage)
  }
}

/**
 * Attach a screen. Returns the detach function to call on unmount.
 *
 * Reference counted, so the runtime listener is registered once however many
 * screens are open, and torn down only when the last one goes. The first
 * attach also runs the opening check; later ones reuse its result rather than
 * spending another call on the GitHub API, which is rate limited and
 * unauthenticated.
 */
export function attachSoftwareUpdate(): () => void {
  watchers += 1
  if (watchers === 1) {
    closeRuntimeSubscription = subscribeRuntimeUpdateOperations(acceptRuntimeOperation)
  }
  let detached = false
  return () => {
    if (detached) return
    detached = true
    watchers = Math.max(0, watchers - 1)
    if (watchers === 0) {
      closeRuntimeSubscription?.()
      closeRuntimeSubscription = undefined
      scheduleDownloadPoll()
    }
  }
}

/** Runs the opening check once per page load, whichever screen asks first. */
export async function ensureInitialUpdateCheck(): Promise<void> {
  if (initialCheckStarted) return
  initialCheckStarted = true
  await checkForUpdates()
}

export function useSoftwareUpdate() {
  return {
    updateCheck,
    checking,
    applying,
    startingUpdate,
    activeOperation,
    changedComponents,
    displayedComponents,
    componentState,
    progress,
    download,
    downloading,
    downloadReady,
    downloadFailed,
    downloadProgress,
    autoDownload,
    savingPreference,
    releaseSources,
    sourceAgreement,
    signatureVerified
  }
}
