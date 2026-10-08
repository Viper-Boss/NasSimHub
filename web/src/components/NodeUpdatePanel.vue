<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  NODE_ARTIFACT_LIMIT_BYTES,
  NODE_MANIFEST_LIMIT_BYTES,
  NodeApiError,
  getNodeUpdates,
  importNodeArtifact,
  importNodeManifest,
  removeNodeRelease,
  runNodeUpdateAction
} from '../api/nodeSecurity'
import type {
  NodeRelease,
  NodeReleaseCatalog,
  NodeUpdateAction,
  NodeUpdateDevice
} from '../api/nodeSecurity'
import type { NodeSecurityCopy } from '../i18n/nodeSecurityCopy'

// Agent updates for one SIM Node.
//
// WHAT THE GATEWAY DOES AND DOES NOT VOUCH FOR. It stores a signed manifest and
// its artifact, carries them to the device and asks the device to switch. The
// DEVICE verifies against its own release keys; the gateway verifies too only
// when it has release keys of its own. So every release carries one of two
// badges and never a bare tick: "verified by gateway", or "not verified by
// gateway -- verified on the device". A signature outcome (dual-signed or
// Ed25519 only) is shown only where the gateway established it.
//
// THE DEVICE'S STATE IS NEVER REMEMBERED HERE. It is read from the device on
// every request (the route answers `Cache-Control: no-store`), so this panel
// asks again when it opens, when "read again" is pressed and after every
// action, and replaces what it shows with that answer -- including replacing a
// status with NOTHING when the device stops answering. There is no last-known
// status to fall back to, by design: an old state would describe a device that
// may since have restarted, rolled back or been unplugged.
//
// ONE REQUEST AT A TIME. Every control is disabled while a request is in
// flight. Apply restarts the agent and drops calls in progress on that device,
// so it sits behind an explicit confirmation that says so, and nothing here
// retries: a failed or unconfirmed step is shown as what it is.

const props = defineProps<{
  deviceId: string
  deviceName: string
  copy: NodeSecurityCopy
  locale: string
}>()

const emit = defineEmits<{ close: [] }>()

const labels = computed(() => props.copy.updates)

const catalog = ref<NodeReleaseCatalog | null>(null)
const device = ref<NodeUpdateDevice | null>(null)
const loading = ref(false)
const loadError = ref('')
const forbidden = ref(false)

type Busy = '' | 'manifest' | 'artifact' | 'remove' | NodeUpdateAction
const busy = ref<Busy>('')
const busyRelease = ref('')
const actionError = ref('')
const outcome = ref('')
const outcomeWarn = ref(false)

const applyArmed = ref('')
const removeArmed = ref('')

const manifestFile = ref<File | null>(null)
const artifactFile = ref<File | null>(null)
const artifactRelease = ref('')
const uploadShare = ref(0)
// Bumped after a successful import so the file inputs are recreated empty; a
// browser offers no other way to clear the name a file input is showing.
const inputGeneration = ref(0)
const importError = ref('')

const locked = computed(() => loading.value || busy.value !== '')

function errorText(cause: unknown): string {
  if (cause instanceof NodeApiError) {
    if (cause.code === 'network') return labels.value.offline
    if (cause.code === 'csrf_failed') return labels.value.sessionExpired
    if (cause.status === 403) return labels.value.forbidden
    if (cause.code === 'invalid_response') return labels.value.invalidResponse
    // The gateway's own sentence: for a rejected release it names the release
    // and the reason the device or the gateway gave, which no local text knows.
    if (cause.message) return cause.message
    return `${labels.value.failed} (${cause.status})`
  }
  return labels.value.failed
}

/**
 * One live read. The answer replaces the catalog AND the device state whole;
 * on failure the device state is cleared rather than left on screen, because
 * what is there would otherwise pass for current.
 */
async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const answer = await getNodeUpdates(props.deviceId)
    catalog.value = answer.catalog
    device.value = answer.device
    forbidden.value = false
  } catch (cause) {
    device.value = null
    forbidden.value = cause instanceof NodeApiError && cause.status === 403
    loadError.value = errorText(cause)
  } finally {
    loading.value = false
  }
}

const status = computed(() => device.value?.status ?? null)

/** Reached, and able to install. Anything less disables the device actions. */
const deviceReady = computed(() => device.value?.reachable === true && device.value.supported)

const unsupportedReason = computed(
  () => device.value?.reason || status.value?.unsupported_reason || labels.value.noReason
)

const pendingConfirm = computed(() => status.value?.state === 'PENDING_CONFIRM')

function stateLabel(state: string): string {
  if (!state) return props.copy.unknown
  return labels.value.states[state] ?? state
}

function mib(bytes: number | undefined): string {
  if (bytes === undefined) return props.copy.unknown
  return bytes >= 1048576
    ? `${(bytes / 1048576).toFixed(1)} MiB`
    : `${(bytes / 1024).toFixed(1)} KiB`
}

function formatTime(value: string): string {
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? '' : new Date(parsed).toLocaleString(props.locale)
}

const transfer = computed(() => {
  const current = status.value
  if (!current || !current.expected_bytes) return ''
  return `${mib(current.received_bytes ?? 0)} / ${mib(current.expected_bytes)}`
})

/**
 * Apply is offered only for the release the device itself reports as
 * validated. The gateway refuses anything else (updates.go: the device must be
 * READY on this release), and a button that always fails teaches nothing.
 */
function canApply(release: NodeRelease): boolean {
  return (
    deviceReady.value &&
    status.value?.state === 'READY' &&
    status.value.release_id === release.release_id
  )
}

function canPush(release: NodeRelease): boolean {
  return deviceReady.value && release.artifact_ready
}

async function act(action: NodeUpdateAction, releaseID: string): Promise<void> {
  if (locked.value) return
  busy.value = action
  busyRelease.value = releaseID
  actionError.value = ''
  outcome.value = ''
  outcomeWarn.value = false
  applyArmed.value = ''
  try {
    const result = await runNodeUpdateAction(action, props.deviceId, releaseID)
    if (result.notConfirmed) {
      outcome.value = labels.value.outcomeNotConfirmed
      outcomeWarn.value = true
    } else {
      outcome.value = {
        push: labels.value.outcomePushed,
        apply: labels.value.outcomeApplied,
        confirm: labels.value.outcomeConfirmed,
        rollback: labels.value.outcomeRolledBack
      }[action]
    }
  } catch (cause) {
    actionError.value = errorText(cause)
  } finally {
    busy.value = ''
    busyRelease.value = ''
  }
  // Whatever happened, what the device says NOW is the only thing worth
  // showing, so it is asked again rather than drawn from the action's answer.
  await load()
}

async function remove(release: NodeRelease): Promise<void> {
  if (locked.value) return
  if (removeArmed.value !== release.release_id) {
    removeArmed.value = release.release_id
    return
  }
  busy.value = 'remove'
  busyRelease.value = release.release_id
  actionError.value = ''
  outcome.value = ''
  outcomeWarn.value = false
  try {
    catalog.value = await removeNodeRelease(release.release_id)
    outcome.value = labels.value.outcomeRemoved
    if (artifactRelease.value === release.release_id) artifactRelease.value = ''
  } catch (cause) {
    actionError.value = errorText(cause)
  } finally {
    busy.value = ''
    busyRelease.value = ''
    removeArmed.value = ''
  }
  await load()
}

function fileOf(event: Event): File | null {
  const input = event.target as HTMLInputElement | null
  return input?.files?.[0] ?? null
}

function pickManifest(event: Event): void {
  manifestFile.value = fileOf(event)
  importError.value = ''
}

function pickArtifact(event: Event): void {
  artifactFile.value = fileOf(event)
  importError.value = ''
}

const manifestProblem = computed(() =>
  manifestFile.value && manifestFile.value.size > NODE_MANIFEST_LIMIT_BYTES
    ? labels.value.manifestTooLarge
    : ''
)

/** The releases still waiting for their artifact: what an artifact can be imported for. */
const awaitingArtifact = computed(
  () => catalog.value?.releases.filter(release => !release.artifact_ready) ?? []
)

const artifactTarget = computed(
  () => awaitingArtifact.value.find(release => release.release_id === artifactRelease.value) ?? null
)

const artifactProblem = computed(() => {
  const file = artifactFile.value
  if (!file) return ''
  if (file.size > NODE_ARTIFACT_LIMIT_BYTES) return labels.value.artifactTooLarge
  const expected = artifactTarget.value?.size
  // The manifest states the artifact's exact size, so a file of another size
  // cannot be the right one; say so before sending megabytes to be refused.
  if (expected !== undefined && expected !== file.size) {
    return labels.value.artifactSizeMismatch(mib(expected))
  }
  return ''
})

async function sendManifest(): Promise<void> {
  const file = manifestFile.value
  if (!file || manifestProblem.value || locked.value) return
  busy.value = 'manifest'
  importError.value = ''
  outcome.value = ''
  outcomeWarn.value = false
  try {
    const release = await importNodeManifest(file)
    artifactRelease.value = release.artifact_ready ? '' : release.release_id
    manifestFile.value = null
    inputGeneration.value += 1
    outcome.value = labels.value.manifestImported
  } catch (cause) {
    importError.value = errorText(cause)
  } finally {
    busy.value = ''
  }
  await load()
}

async function sendArtifact(): Promise<void> {
  const file = artifactFile.value
  const target = artifactTarget.value
  if (!file || !target || artifactProblem.value || locked.value) return
  busy.value = 'artifact'
  uploadShare.value = 0
  importError.value = ''
  outcome.value = ''
  outcomeWarn.value = false
  try {
    await importNodeArtifact(target.release_id, file, share => {
      uploadShare.value = share
    })
    artifactFile.value = null
    artifactRelease.value = ''
    inputGeneration.value += 1
    outcome.value = labels.value.artifactImported
  } catch (cause) {
    importError.value = errorText(cause)
  } finally {
    busy.value = ''
  }
  await load()
}

// Keep the artifact target pointing at something that still needs one.
watch(awaitingArtifact, pending => {
  if (!pending.some(release => release.release_id === artifactRelease.value)) {
    artifactRelease.value = pending[0]?.release_id ?? ''
  }
})

// A different device is a different question: drop everything shown and ask.
watch(
  () => props.deviceId,
  () => {
    device.value = null
    outcome.value = ''
    actionError.value = ''
    applyArmed.value = ''
    reveal()
    void load()
  }
)

const root = ref<HTMLElement | null>(null)

/**
 * The panel opens above the device list, and the button that opens it can be
 * several cards further down. Without this the click appears to do nothing.
 */
function reveal(): void {
  root.value?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
}

onMounted(() => {
  reveal()
  void load()
})
</script>

<template>
  <section ref="root" class="node-update" :aria-label="labels.title">
    <header class="node-update__head">
      <div class="node-update__title">
        <h2>{{ labels.title }}</h2>
        <p>
          <span>{{ deviceName }}</span>
          <span class="node-update__mono">{{ deviceId }}</span>
        </p>
      </div>
      <div class="node-update__actions">
        <button type="button" class="secondary-button" :disabled="locked" @click="load">
          {{ loading ? labels.loading : labels.refresh }}
        </button>
        <button type="button" class="secondary-button" :disabled="busy !== ''" @click="emit('close')">
          {{ labels.close }}
        </button>
      </div>
    </header>

    <p v-if="forbidden" role="alert" class="node-update__error">{{ labels.forbidden }}</p>
    <p v-else-if="loadError" role="alert" class="node-update__error">{{ loadError }}</p>
    <p v-if="outcome" role="status" class="node-update__outcome" :class="{ 'is-warn': outcomeWarn }">
      {{ outcome }}
    </p>
    <p v-if="actionError" role="alert" class="node-update__error">{{ actionError }}</p>

    <template v-if="!forbidden">
      <section class="node-update__block">
        <h3>{{ labels.deviceSection }}</h3>
        <p v-if="loading && !device" class="node-update__muted">{{ labels.loading }}</p>
        <template v-else-if="device">
          <!-- Not reached: the sentence, the gateway's reason, and nothing
               else. No status is drawn, because none was read. -->
          <div v-if="!device.reachable" class="node-update__notice is-warn">
            <strong>{{ labels.notAnswering }}</strong>
            <span v-if="device.reason">{{ device.reason }}</span>
            <span>{{ labels.notAnsweringNote }}</span>
          </div>
          <template v-else>
            <div v-if="!device.supported" class="node-update__notice">
              <strong>{{ labels.unsupported }}</strong>
              <span>{{ unsupportedReason }}</span>
            </div>
            <dl v-if="status" class="node-update__facts">
              <div><dt>{{ labels.currentVersion }}</dt><dd>{{ status.current_version || copy.unknown }}</dd></div>
              <div><dt>{{ labels.factoryVersion }}</dt><dd>{{ status.factory_version || copy.unknown }}</dd></div>
              <div>
                <dt>{{ labels.activeRelease }}</dt>
                <dd class="node-update__mono">{{ status.active_release || labels.factoryAgent }}</dd>
              </div>
              <div><dt>{{ labels.state }}</dt><dd>{{ stateLabel(status.state) }}</dd></div>
              <div v-if="status.target_version"><dt>{{ labels.targetVersion }}</dt><dd>{{ status.target_version }}</dd></div>
              <div v-if="status.previous_version"><dt>{{ labels.previousVersion }}</dt><dd>{{ status.previous_version }}</dd></div>
              <div v-if="transfer"><dt>{{ labels.transfer }}</dt><dd>{{ transfer }}</dd></div>
              <div v-if="status.error"><dt>{{ labels.lastError }}</dt><dd>{{ status.error }}</dd></div>
              <div v-if="status.restart_reason">
                <dt>{{ labels.restartReason }}</dt>
                <dd>{{ labels.restartReasons[status.restart_reason] ?? status.restart_reason }}</dd>
              </div>
              <div v-if="pendingConfirm && formatTime(status.confirm_deadline)">
                <dt>{{ labels.confirmDeadline }}</dt>
                <dd>{{ formatTime(status.confirm_deadline) }}</dd>
              </div>
              <div v-if="status.signatures.policy">
                <dt>{{ labels.signaturePolicy }}</dt>
                <dd>{{ status.signatures.policy }}</dd>
              </div>
              <div v-if="status.signatures.outcome">
                <dt>{{ labels.lastSignatures }}</dt>
                <dd>{{ labels.signatureOutcomes[status.signatures.outcome] ?? status.signatures.outcome }}</dd>
              </div>
              <div v-if="formatTime(device.read_at)">
                <dt>{{ labels.readAt }}</dt>
                <dd>{{ formatTime(device.read_at) }}</dd>
              </div>
            </dl>

            <!-- Confirm and roll back exist in exactly one state. -->
            <div v-if="status && pendingConfirm" class="node-update__notice is-warn">
              <strong>{{ labels.pendingTitle }}</strong>
              <span>{{ labels.pendingNote }}</span>
              <div class="node-update__actions">
                <button
                  type="button"
                  class="primary-button"
                  :disabled="locked || !status.release_id"
                  @click="act('confirm', status.release_id)"
                >{{ labels.confirm }}</button>
                <button
                  type="button"
                  class="danger-button"
                  :disabled="locked || !status.release_id"
                  @click="act('rollback', status.release_id)"
                >{{ labels.rollback }}</button>
              </div>
            </div>
          </template>
        </template>
        <!-- The gateway answered without this device's state. Said, not left
             as an empty section that reads like "nothing to report". -->
        <p v-else-if="!loadError" class="node-update__muted">{{ labels.noDeviceStatus }}</p>
      </section>

      <section v-if="catalog" class="node-update__block">
        <h3>{{ labels.releases }}</h3>
        <p v-if="!catalog.available" class="node-update__notice">{{ labels.storeUnavailable }}</p>
        <template v-else>
          <p v-if="!catalog.gateway_verifies" class="node-update__notice">{{ labels.gatewayNoKeys }}</p>
          <p v-if="catalog.releases.length === 0" class="node-update__muted">{{ labels.noReleases }}</p>
          <ul v-else class="node-update__releases">
            <li v-for="release in catalog.releases" :key="release.release_id" class="node-update__release">
              <div class="node-update__release-head">
                <strong>{{ release.version || release.release_id }}</strong>
                <span
                  class="node-update__badge"
                  :class="release.gateway_verification === 'verified' ? 'is-ok' : 'is-neutral'"
                >{{ release.gateway_verification === 'verified' ? labels.gatewayVerified : labels.gatewayUnverified }}</span>
                <span v-if="release.signatures === 'dual_signed'" class="node-update__badge is-ok">{{ labels.dualSigned }}</span>
                <span v-else-if="release.signatures === 'classical_only'" class="node-update__badge is-neutral">{{ labels.classicalOnly }}</span>
                <span v-else class="node-update__badge is-neutral">{{ labels.signaturesUnknown }}</span>
                <span class="node-update__badge" :class="release.artifact_ready ? 'is-neutral' : 'is-warn'">
                  {{ release.artifact_ready ? labels.artifactReady : labels.artifactMissing }}
                </span>
              </div>
              <dl class="node-update__facts">
                <div v-if="release.channel"><dt>{{ labels.channel }}</dt><dd>{{ release.channel }}</dd></div>
                <div v-if="release.platform || release.arch">
                  <dt>{{ labels.platform }}</dt>
                  <dd>{{ [release.platform, release.arch].filter(Boolean).join(' / ') }}</dd>
                </div>
                <div><dt>{{ labels.size }}</dt><dd>{{ mib(release.size) }}</dd></div>
                <div v-if="formatTime(release.released_at)"><dt>{{ labels.releasedAt }}</dt><dd>{{ formatTime(release.released_at) }}</dd></div>
                <div v-if="release.min_core_version"><dt>{{ labels.minCore }}</dt><dd>{{ release.min_core_version }}</dd></div>
                <div v-if="release.notes"><dt>{{ labels.notes }}</dt><dd>{{ release.notes }}</dd></div>
              </dl>
              <details class="node-update__details">
                <summary class="ui-disclosure-summary">{{ labels.details }}</summary>
                <dl class="node-update__facts">
                  <div><dt>{{ labels.releaseId }}</dt><dd class="node-update__mono">{{ release.release_id }}</dd></div>
                  <div v-if="release.sha256"><dt>{{ labels.digest }}</dt><dd class="node-update__mono">{{ release.sha256 }}</dd></div>
                  <div v-if="release.key_id || release.pq_key_id">
                    <dt>{{ labels.keyIds }}</dt>
                    <dd class="node-update__mono">
                      {{ [release.key_id, release.pq_key_id && `${release.pq_algorithm || 'PQ'} ${release.pq_key_id}`].filter(Boolean).join(' · ') }}
                    </dd>
                  </div>
                </dl>
              </details>

              <div
                v-if="applyArmed === release.release_id"
                class="node-update__confirm"
                role="group"
                :aria-label="labels.applyTitle"
              >
                <strong>{{ labels.applyTitle }}</strong>
                <ul>
                  <li v-for="line in labels.applyExplain" :key="line">{{ line }}</li>
                </ul>
                <div class="node-update__actions">
                  <button
                    type="button"
                    class="danger-button"
                    :disabled="locked || !canApply(release)"
                    @click="act('apply', release.release_id)"
                  >{{ labels.applyConfirm }}</button>
                  <button type="button" class="secondary-button" :disabled="locked" @click="applyArmed = ''">
                    {{ labels.cancel }}
                  </button>
                </div>
              </div>
              <div v-else class="node-update__actions">
                <span
                  v-if="deviceReady && release.artifact_ready && !canApply(release)"
                  class="node-update__muted node-update__hint"
                >{{ labels.applyNeedsPush }}</span>
                <button
                  type="button"
                  class="secondary-button"
                  :disabled="locked || !canPush(release)"
                  @click="act('push', release.release_id)"
                >{{ busy === 'push' && busyRelease === release.release_id ? labels.pushing : labels.push }}</button>
                <button
                  type="button"
                  class="primary-button"
                  :disabled="locked || !canApply(release)"
                  @click="applyArmed = release.release_id"
                >{{ busy === 'apply' && busyRelease === release.release_id ? labels.applying : labels.apply }}</button>
                <button
                  type="button"
                  class="secondary-button"
                  :disabled="locked"
                  @click="remove(release)"
                >{{ removeArmed === release.release_id ? labels.removeConfirm : labels.remove }}</button>
                <button
                  v-if="removeArmed === release.release_id"
                  type="button"
                  class="secondary-button"
                  :disabled="locked"
                  @click="removeArmed = ''"
                >{{ labels.keep }}</button>
              </div>
            </li>
          </ul>

          <section class="node-update__import">
            <h3>{{ labels.importTitle }}</h3>
            <p class="node-update__muted">{{ labels.importNote }}</p>
            <div class="node-update__import-row">
              <label>
                <span>{{ labels.manifestFile }}</span>
                <input
                  :key="`manifest-${inputGeneration}`"
                  type="file"
                  accept=".json,application/json"
                  :disabled="locked"
                  @change="pickManifest"
                />
              </label>
              <button
                type="button"
                class="secondary-button"
                :disabled="locked || !manifestFile || manifestProblem !== ''"
                @click="sendManifest"
              >{{ busy === 'manifest' ? labels.importing : labels.importManifest }}</button>
            </div>
            <p v-if="manifestProblem" role="alert" class="node-update__error">{{ manifestProblem }}</p>

            <div class="node-update__import-row">
              <label>
                <span>{{ labels.artifactFile }}</span>
                <input
                  :key="`artifact-${inputGeneration}`"
                  type="file"
                  :disabled="locked || !artifactTarget"
                  @change="pickArtifact"
                />
              </label>
              <button
                type="button"
                class="secondary-button"
                :disabled="locked || !artifactFile || !artifactTarget || artifactProblem !== ''"
                @click="sendArtifact"
              >{{ busy === 'artifact' ? labels.uploading(Math.round(uploadShare * 100)) : labels.importArtifact }}</button>
            </div>
            <p v-if="artifactTarget" class="node-update__muted">
              {{ labels.artifactFor }}
              <span class="node-update__mono">{{ artifactTarget.version || artifactTarget.release_id }}</span>
              · {{ mib(artifactTarget.size) }}
            </p>
            <p v-else class="node-update__muted">{{ labels.chooseRelease }}</p>
            <div v-if="awaitingArtifact.length > 1" class="node-update__choices" role="radiogroup" :aria-label="labels.artifactFor">
              <label v-for="release in awaitingArtifact" :key="release.release_id">
                <input
                  v-model="artifactRelease"
                  class="ui-radio"
                  type="radio"
                  :value="release.release_id"
                  :disabled="locked"
                />
                <span>{{ release.version || release.release_id }}</span>
              </label>
            </div>
            <div
              v-if="busy === 'artifact'"
              class="node-update__progress"
              role="progressbar"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="Math.round(uploadShare * 100)"
            >
              <span :style="{ width: `${Math.round(uploadShare * 100)}%` }"></span>
            </div>
            <p v-if="artifactProblem" role="alert" class="node-update__error">{{ artifactProblem }}</p>
            <p v-if="importError" role="alert" class="node-update__error">{{ importError }}</p>
          </section>
        </template>
      </section>
    </template>
  </section>
</template>

<style scoped>
.node-update {
  display: grid;
  min-width: 0;
  gap: 14px;
  margin-top: 20px;
  padding: 20px;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
}

.node-update__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.node-update__title { min-width: 0; }
.node-update__title h2 { margin: 0; font-size: 18px; }

.node-update__title p {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 13px;
  overflow-wrap: anywhere;
}

.node-update h3 { margin: 0; font-size: 15px; }

.node-update__block,
.node-update__import {
  display: grid;
  min-width: 0;
  gap: 10px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

.node-update__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.node-update__hint { flex: 1 1 220px; }

.node-update__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  overflow-wrap: anywhere;
}

.node-update__muted {
  margin: 0;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.55;
}

.node-update__error {
  margin: 0;
  color: var(--danger-strong);
  font-size: 13px;
  overflow-wrap: anywhere;
}

.node-update__outcome {
  margin: 0;
  padding: 10px 12px;
  color: var(--success);
  background: var(--success-soft);
  border: 1px solid var(--success-border);
  border-radius: var(--radius-sm);
  font-size: 13px;
  line-height: 1.55;
}

.node-update__outcome.is-warn {
  color: var(--warning-strong);
  background: var(--warning-soft);
  border-color: var(--warning-border);
}

.node-update__notice {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 10px 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.node-update__notice.is-warn {
  color: var(--warning-strong);
  background: var(--warning-soft);
  border-color: var(--warning-border);
}

.node-update__facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
  gap: 8px 20px;
  margin: 0;
  font-size: 13px;
}

.node-update__facts > div {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 2px 12px;
  min-width: 0;
}

.node-update__facts dt { color: var(--muted); }

.node-update__facts dd {
  min-width: 0;
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}

.node-update__releases {
  display: grid;
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.node-update__release {
  display: grid;
  min-width: 0;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
}

.node-update__release-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
}

.node-update__release-head strong {
  margin-right: 4px;
  overflow-wrap: anywhere;
}

/* Wraps rather than truncates: the unverified badge is a whole sentence in
   Chinese and must stay readable at 360px. */
.node-update__badge {
  padding: 2px 9px;
  font-size: 12px;
  line-height: 1.5;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.node-update__badge.is-ok {
  color: var(--success);
  background: var(--success-soft);
  border-color: var(--success-border);
}

.node-update__badge.is-warn {
  color: var(--warning-strong);
  background: var(--warning-soft);
  border-color: var(--warning-border);
}

.node-update__badge.is-neutral {
  color: var(--muted-strong);
  background: var(--surface-subtle);
}

.node-update__details { font-size: 13px; }
.node-update__details summary { color: var(--muted); cursor: pointer; }
.node-update__details dl { margin-top: 8px; grid-template-columns: minmax(0, 1fr); }

.node-update__confirm {
  display: grid;
  gap: 8px;
  padding: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.55;
}

.node-update__confirm ul {
  display: grid;
  gap: 4px;
  margin: 0;
  padding-left: 18px;
}

.node-update__import-row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 10px;
}

.node-update__import-row label {
  display: grid;
  flex: 1 1 220px;
  min-width: 0;
  gap: 6px;
  font-size: 13px;
}

.node-update__import-row input[type='file'] {
  max-width: 100%;
  min-width: 0;
  color: var(--text);
  font: inherit;
}

.node-update__choices {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 13px;
}

.node-update__choices label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow-wrap: anywhere;
}

.node-update__progress {
  height: 6px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.node-update__progress > span {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: inherit;
}
</style>
