<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { simNodeInventoryCopy } from '../i18n/simNodeInventory'
import { nodeResourceCopy } from '../i18n/nodeResources'

// Mirrors nassimhubnode.NodeView. The page never receives SIM identifiers
// (ICCID, IMSI, phone number) or key material, only what an administrator
// needs to recognise and manage the device.
type NodeView = {
  node_id: string
  display_name: string
  platform: string
  model?: string
  fingerprint: string
  pair_state: string
  online: boolean
  revoked: boolean
  compatible: boolean
  incompatible?: string
  transport: string
  usb_endpoint?: string
  wifi_endpoint?: string
  agent_version?: string
  last_seen_at?: string
  last_error?: string
  modem_state?: string
  sim_state?: string
  operator?: string
  registration?: string
  signal_bars: number
  sms_capable: boolean
  volte?: "yes" | "no" | "unknown"
  voice_control?: boolean
  voice_audio?: boolean
  messages_on_device: number
	resources?: {
	  observed_at: string
	  cpu_percent: number | null
	  memory_total_bytes: number
	  memory_available_bytes: number
	  uptime_seconds: number
	  process_count: number
	  processes: { pid: number; name: string; rss_bytes: number }[]
	}
}

// Mirrors nassimhubnode.Candidate.
type Candidate = {
  source: string
  device_id: string
  platform: string
  model?: string
  agent_version?: string
  fingerprint: string
  pairing_state: string
  owned_by_this_nas: boolean
  owned_by_other: boolean
  known: boolean
  key_mismatch: boolean
  compatible: boolean
  incompatible?: string
  ticket?: string
  ticket_expires_at?: string
  error?: string
}

type ApiError = { code?: string; message?: string }

const { locale } = useI18n()
const copy = computed(() => simNodeInventoryCopy(String(locale.value)))
const resourceCopy = computed(() => nodeResourceCopy(String(locale.value)))
function mib(bytes: number) { return `${(Math.max(0, bytes) / 1048576).toFixed(1)} MiB` }
function uptime(seconds: number) { const m = Math.floor(Math.max(0, seconds) / 60); return `${Math.floor(m / 60)}h ${m % 60}m` }

const nodes = ref<NodeView[]>([])
const candidates = ref<Candidate[]>([])
const loading = ref(false)
const discovering = ref(false)
const searched = ref(false)
const error = ref('')
const discoverError = ref('')
const notice = ref('')

const confirming = ref<Candidate | null>(null)
const displayName = ref('')
const pairing = ref(false)
const pairError = ref('')

const busyNode = ref('')
const unpairArmed = ref('')
const nodeError = ref<Record<string, string>>({})

const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
let poll: ReturnType<typeof setInterval> | undefined

function csrfToken(): string {
  for (const part of document.cookie.split(';')) {
    const [name, ...rest] = part.trim().split('=')
    if (name === 'modemdeck_csrf') return decodeURIComponent(rest.join('='))
  }
  return ''
}

class RequestError extends Error {
  constructor(readonly code: string, message: string) {
    super(message)
  }
}

async function request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (method === 'POST') {
    headers['Content-Type'] = 'application/json'
    headers['X-ModemDeck-CSRF'] = csrfToken()
  }
  const response = await fetch(path, {
    method,
    credentials: 'same-origin',
    cache: 'no-store',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body)
  })
  const parsed: unknown = await response.json().catch(() => null)
  if (!response.ok) {
    const failure = (parsed && typeof parsed === 'object' ? parsed : {}) as ApiError
    const code = failure.code || (response.status === 403 ? 'forbidden' : '')
    throw new RequestError(code, copy.value.errors[code] || `${copy.value.genericError} (${response.status})`)
  }
  if (!parsed || typeof parsed !== 'object') throw new RequestError('', copy.value.invalidResponse)
  return parsed as T
}

function messageOf(cause: unknown, fallback: string): string {
  return cause instanceof Error && cause.message ? cause.message : fallback
}

async function refresh(quiet = false) {
  if (!quiet) loading.value = true
  if (!quiet) error.value = ''
  try {
    const body = await request<{ nodes?: unknown }>('GET', '/api/v1/nodes')
    if (!Array.isArray(body.nodes)) throw new RequestError('', copy.value.invalidResponse)
    nodes.value = body.nodes as NodeView[]
    if (quiet) error.value = ''
  } catch (cause) {
    if (!quiet) error.value = messageOf(cause, copy.value.readFailed)
  } finally {
    if (!quiet) loading.value = false
  }
}

async function discover() {
  discovering.value = true
  discoverError.value = ''
  notice.value = ''
  confirming.value = null
  try {
    const body = await request<{ candidates?: unknown }>('GET', '/api/v1/nodes/discover')
    if (!Array.isArray(body.candidates)) throw new RequestError('', copy.value.invalidResponse)
    candidates.value = body.candidates as Candidate[]
    searched.value = true
  } catch (cause) {
    discoverError.value = messageOf(cause, copy.value.discoverFailed)
  } finally {
    discovering.value = false
  }
}

function secondsLeft(candidate: Candidate): number {
  if (!candidate.ticket_expires_at) return 0
  return Math.floor((Date.parse(candidate.ticket_expires_at) - now.value) / 1000)
}

function candidateBlocker(candidate: Candidate): string {
  if (candidate.error) return candidate.error
  if (candidate.owned_by_this_nas) return copy.value.alreadyThisNAS
  if (candidate.owned_by_other) return copy.value.ownedByOther
  if (candidate.key_mismatch) return copy.value.keyMismatch
  if (!candidate.compatible) return candidate.incompatible || copy.value.incompatible
  if (!candidate.ticket) return copy.value.unverified
  if (secondsLeft(candidate) <= 0) return copy.value.expired
  return ''
}

function openConfirm(candidate: Candidate) {
  confirming.value = candidate
  displayName.value = ''
  pairError.value = ''
}

async function confirmPair() {
  const candidate = confirming.value
  if (!candidate || !candidate.ticket) return
  pairing.value = true
  pairError.value = ''
  try {
    await request('POST', '/api/v1/nodes/pair', {
      ticket: candidate.ticket,
      device_id: candidate.device_id,
      // Echo exactly what was shown: the NAS checks it against the key it
      // verified at discovery, so a device swapped in since then is refused.
      fingerprint: candidate.fingerprint,
      display_name: displayName.value.trim()
    })
    confirming.value = null
    candidates.value = candidates.value.filter((entry) => entry.device_id !== candidate.device_id)
    // The list is empty because the device was adopted, not because nothing
    // answered - do not show the "no device" hint.
    if (candidates.value.length === 0) searched.value = false
    notice.value = copy.value.pairDone
    await refresh()
  } catch (cause) {
    pairError.value = messageOf(cause, copy.value.genericError)
    if (cause instanceof RequestError && ['pairing_expired', 'fingerprint_mismatch', 'identity_changed'].includes(cause.code)) {
      // The ticket is spent whatever the reason; the only way on is a fresh search.
      candidates.value = candidates.value.map((entry) =>
        entry.device_id === candidate.device_id ? { ...entry, ticket: undefined } : entry
      )
    }
  } finally {
    pairing.value = false
  }
}

async function checkNode(node: NodeView) {
  busyNode.value = node.node_id
  nodeError.value = { ...nodeError.value, [node.node_id]: '' }
  try {
    const body = await request<{ node?: NodeView }>('POST', '/api/v1/nodes/refresh', { device_id: node.node_id })
    if (body.node) nodes.value = nodes.value.map((entry) => (entry.node_id === node.node_id ? body.node as NodeView : entry))
  } catch (cause) {
    nodeError.value = { ...nodeError.value, [node.node_id]: messageOf(cause, copy.value.genericError) }
  } finally {
    busyNode.value = ''
  }
}

async function unpair(node: NodeView) {
  if (unpairArmed.value !== node.node_id) {
    unpairArmed.value = node.node_id
    return
  }
  busyNode.value = node.node_id
  try {
    const body = await request<{ released_on_device?: boolean }>('POST', '/api/v1/nodes/unpair', { device_id: node.node_id })
    notice.value = body.released_on_device === false ? copy.value.unpairLocalOnly : copy.value.unpairDone
    unpairArmed.value = ''
    await refresh()
  } catch (cause) {
    nodeError.value = { ...nodeError.value, [node.node_id]: messageOf(cause, copy.value.genericError) }
  } finally {
    busyNode.value = ''
  }
}

function transportLabel(value: string): string {
  return copy.value.transports[value] ?? value
}

function pairStateLabel(value: string): string {
  return copy.value.pairStates[value] ?? value
}

function formatTime(value?: string): string {
  if (!value) return copy.value.noRecord
  const parsed = Date.parse(value)
  if (Number.isNaN(parsed)) return copy.value.noRecord
  return new Date(parsed).toLocaleString(String(locale.value))
}

function bars(value: number): string {
  const clamped = Math.max(0, Math.min(5, Math.round(value)))
  return '▮'.repeat(clamped) + '▯'.repeat(5 - clamped)
}

onMounted(() => {
  void refresh()
  clock = setInterval(() => { now.value = Date.now() }, 1000)
  poll = setInterval(() => { void refresh(true) }, 10000)
})

onBeforeUnmount(() => {
  if (clock) clearInterval(clock)
  if (poll) clearInterval(poll)
})
</script>

<template>
  <main class="node-inventory">
    <div class="node-inventory__heading">
      <div>
        <h1>{{ copy.title }}</h1>
        <p>{{ copy.subtitle }}</p>
      </div>
      <button type="button" :disabled="loading" @click="refresh()">{{ loading ? copy.loading : copy.refresh }}</button>
    </div>
    <p class="node-inventory__notice">{{ copy.notice }}</p>
    <p v-if="notice" class="node-inventory__success" role="status">{{ notice }}</p>

    <section class="node-inventory__discovery">
      <div class="node-inventory__heading">
        <h2>{{ copy.candidateTitle }}</h2>
        <button type="button" :disabled="discovering || pairing" @click="discover">
          {{ discovering ? copy.discovering : copy.discover }}
        </button>
      </div>
      <p v-if="discoverError" role="alert" class="node-inventory__error">{{ discoverError }}</p>
      <p v-else-if="searched && !discovering && candidates.length === 0">{{ copy.noCandidates }}</p>
      <ul v-else class="node-inventory__candidates">
        <li v-for="candidate in candidates" :key="candidate.device_id || candidate.source" class="node-inventory__candidate">
          <dl>
            <div><dt>{{ copy.deviceId }}</dt><dd class="node-inventory__id">{{ candidate.device_id || copy.unknown }}</dd></div>
            <div><dt>{{ copy.model }}</dt><dd>{{ candidate.model || candidate.platform || copy.unknown }}</dd></div>
            <div><dt>{{ copy.version }}</dt><dd>{{ candidate.agent_version || copy.unknown }}</dd></div>
            <div><dt>{{ copy.fingerprint }}</dt><dd class="node-inventory__fingerprint">{{ candidate.fingerprint || copy.unknown }}</dd></div>
          </dl>

          <p v-if="candidateBlocker(candidate)" class="node-inventory__blocked">{{ candidateBlocker(candidate) }}</p>

          <template v-else-if="confirming && confirming.device_id === candidate.device_id">
            <div class="node-inventory__confirm" role="group" :aria-label="copy.pairTitle">
              <h3>{{ copy.pairTitle }}</h3>
              <p>{{ copy.pairCompare }}</p>
              <p class="node-inventory__id node-inventory__id--large">{{ candidate.device_id }}</p>
              <label>
                <span>{{ copy.pairName }}</span>
                <input v-model="displayName" type="text" maxlength="64" :placeholder="copy.pairNamePlaceholder" />
              </label>
              <p class="node-inventory__hint">{{ copy.expiresIn(secondsLeft(candidate)) }}</p>
              <p v-if="pairError" role="alert" class="node-inventory__error">{{ pairError }}</p>
              <div class="node-inventory__actions">
                <button type="button" :disabled="pairing || secondsLeft(candidate) <= 0" @click="confirmPair">
                  {{ pairing ? copy.pairing_ : copy.pairConfirm }}
                </button>
                <button type="button" class="node-inventory__secondary" :disabled="pairing" @click="confirming = null">{{ copy.pairCancel }}</button>
              </div>
            </div>
          </template>

          <div v-else class="node-inventory__actions">
            <span class="node-inventory__hint">{{ copy.identityVerifiedUnpaired }} · {{ copy.expiresIn(secondsLeft(candidate)) }}</span>
            <button type="button" @click="openConfirm(candidate)">{{ copy.pair }}</button>
          </div>
        </li>
      </ul>
    </section>

    <p v-if="error" role="alert" class="node-inventory__error">{{ error }}</p>
    <p v-else-if="!loading && nodes.length === 0" class="node-inventory__empty">{{ copy.empty }}</p>
    <div v-else class="node-inventory__list">
      <article v-for="node in nodes" :key="node.node_id" class="node-inventory__card">
        <div class="node-inventory__card-head">
          <h2>{{ node.display_name || node.node_id }}</h2>
          <span
            class="node-inventory__badge"
            :class="node.revoked ? 'is-warn' : node.online ? 'is-ok' : 'is-off'"
          >{{ node.revoked ? copy.revoked : node.online ? copy.online : copy.offline }}</span>
        </div>
        <dl>
          <div><dt>{{ copy.deviceId }}</dt><dd class="node-inventory__id">{{ node.node_id }}</dd></div>
          <div><dt>{{ copy.model }}</dt><dd>{{ node.model || node.platform || copy.unknown }}</dd></div>
          <div><dt>{{ copy.pairing }}</dt><dd>{{ pairStateLabel(node.pair_state) }}</dd></div>
          <div><dt>{{ copy.link }}</dt><dd>{{ transportLabel(node.online ? node.transport : '') }}</dd></div>
          <div v-if="node.operator"><dt>{{ copy.operator }}</dt><dd>{{ node.operator }}</dd></div>
          <div v-if="node.sim_state"><dt>{{ copy.simState }}</dt><dd>{{ copy.simStates[node.sim_state] ?? node.sim_state }}</dd></div>
          <div v-if="node.registration"><dt>{{ copy.registration }}</dt><dd>{{ copy.registrations[node.registration] ?? node.registration }}</dd></div>
          <div v-if="node.online"><dt>{{ copy.signal }}</dt><dd class="node-inventory__bars" :aria-label="`${node.signal_bars}/5`">{{ bars(node.signal_bars) }}</dd></div>
          <div><dt>{{ copy.sms }}</dt><dd>{{ node.sms_capable && node.sim_state === 'ready' ? copy.smsReady : copy.smsNotReady }}</dd></div>
          <div><dt>{{ copy.volte }}</dt><dd>{{ node.online && node.volte === "yes" ? copy.volteRegistered : copy.volteUnknown }}</dd></div>
          <div><dt>{{ copy.callAudio }}</dt><dd>{{ node.voice_audio ? copy.callAudioReady : copy.callAudioUnavailable }}</dd></div>
          <div v-if="node.online"><dt>{{ copy.onDevice }}</dt><dd>{{ node.messages_on_device }}</dd></div>
          <template v-if="node.online && node.resources">
            <div><dt>{{ resourceCopy.cpu }}</dt><dd>{{ node.resources.cpu_percent == null ? copy.unknown : `${node.resources.cpu_percent.toFixed(1)}%` }}</dd></div>
            <div><dt>{{ resourceCopy.memory }}</dt><dd>{{ mib(node.resources.memory_total_bytes - node.resources.memory_available_bytes) }} / {{ mib(node.resources.memory_total_bytes) }}</dd></div>
            <div><dt>{{ resourceCopy.uptime }}</dt><dd>{{ uptime(node.resources.uptime_seconds) }}</dd></div>
            <div><dt>{{ resourceCopy.processes }}</dt><dd>{{ node.resources.process_count }}</dd></div>
            <div><dt>{{ resourceCopy.updated }}</dt><dd>{{ formatTime(node.resources.observed_at) }}</dd></div>
          </template>
          <div><dt>{{ copy.version }}</dt><dd>{{ node.agent_version || copy.unknown }}</dd></div>
          <div><dt>{{ copy.wifi }}</dt><dd>{{ node.wifi_endpoint || copy.missing }}</dd></div>
          <div><dt>{{ copy.lastSeen }}</dt><dd>{{ formatTime(node.last_seen_at) }}</dd></div>
          <div><dt>{{ copy.fingerprint }}</dt><dd class="node-inventory__fingerprint">{{ node.fingerprint }}</dd></div>
          <div v-if="node.last_error && !node.online"><dt>{{ copy.lastError }}</dt><dd>{{ node.last_error }}</dd></div>
          <div v-if="!node.compatible"><dt>{{ copy.incompatible }}</dt><dd>{{ node.incompatible || copy.unknown }}</dd></div>
        </dl>
        <details v-if="node.online && node.resources" class="node-inventory__processes">
          <summary class="ui-disclosure-summary">{{ resourceCopy.top }}</summary>
          <table><thead><tr><th>{{ resourceCopy.pid }}</th><th>{{ resourceCopy.name }}</th><th>{{ resourceCopy.rss }}</th></tr></thead>
            <tbody><tr v-for="process in node.resources.processes" :key="process.pid"><td>{{ process.pid }}</td><td>{{ process.name }}</td><td>{{ mib(process.rss_bytes) }}</td></tr></tbody>
          </table>
        </details>
        <p v-if="nodeError[node.node_id]" role="alert" class="node-inventory__error">{{ nodeError[node.node_id] }}</p>
        <div class="node-inventory__actions">
          <button type="button" :disabled="busyNode === node.node_id" @click="checkNode(node)">{{ copy.refreshNode }}</button>
          <button
            type="button"
            class="node-inventory__danger"
            :disabled="busyNode === node.node_id"
            @click="unpair(node)"
          >{{ unpairArmed === node.node_id ? copy.unpairConfirm : copy.unpair }}</button>
          <button
            v-if="unpairArmed === node.node_id"
            type="button"
            class="node-inventory__secondary"
            @click="unpairArmed = ''"
          >{{ copy.unpairKeep }}</button>
        </div>
      </article>
    </div>
  </main>
</template>

<style scoped>
.node-inventory { width: min(1100px, 100%); margin: 0 auto; padding: 24px; box-sizing: border-box; }
.node-inventory__heading { display: flex; align-items: center; justify-content: space-between; gap: 20px; flex-wrap: wrap; }
h1 { margin: 0 0 8px; font-size: 28px; }
h2 { margin: 0; font-size: 18px; }
h3 { margin: 0 0 8px; font-size: 16px; }
p { margin: 0; }
button { border: 0; border-radius: 9px; padding: 10px 16px; background: #155f81; color: #fff; cursor: pointer; font: inherit; }
button:disabled { opacity: .55; cursor: default; }
.node-inventory__secondary { background: #e4ebef; color: #1d2b34; }
.node-inventory__danger { background: #9b342b; }
.node-inventory__notice, .node-inventory__empty { margin-top: 20px; padding: 16px; border-radius: 10px; background: #edf4f7; line-height: 1.6; }
.node-inventory__success { margin-top: 12px; padding: 12px 16px; border-radius: 10px; background: #e6f4ea; color: #1d5a2e; }
.node-inventory__error { margin-top: 12px; color: #9b342b; }
.node-inventory__discovery { margin-top: 20px; padding: 20px; border: 1px solid #d8e2e8; border-radius: 14px; background: #fff; }
.node-inventory__discovery > p { margin-top: 12px; }
.node-inventory__candidates { list-style: none; margin: 16px 0 0; padding: 0; display: grid; gap: 12px; }
.node-inventory__candidate { border: 1px solid #d8e2e8; border-radius: 12px; padding: 16px; }
.node-inventory__blocked { margin-top: 12px; color: #7a5a12; }
.node-inventory__confirm { margin-top: 14px; padding: 16px; border-radius: 10px; background: #f6f9fb; display: grid; gap: 10px; }
.node-inventory__confirm label { display: grid; gap: 6px; }
.node-inventory__confirm input { padding: 9px 11px; border: 1px solid #c5d2da; border-radius: 8px; font: inherit; max-width: 320px; }
.node-inventory__hint { color: #647482; font-size: 13px; }
.node-inventory__actions { display: flex; align-items: center; justify-content: flex-end; gap: 10px; flex-wrap: wrap; margin-top: 14px; }
.node-inventory__actions .node-inventory__hint { margin-right: auto; }
.node-inventory__id { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-weight: 600; }
.node-inventory__id--large { font-size: 22px; letter-spacing: .04em; }
.node-inventory__fingerprint { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
.node-inventory__list { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 16px; margin-top: 20px; }
.node-inventory__card { border: 1px solid #d8e2e8; border-radius: 14px; background: #fff; padding: 20px; }
.node-inventory__card-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; }
.node-inventory__badge { border-radius: 999px; padding: 3px 10px; font-size: 13px; white-space: nowrap; }
.node-inventory__badge.is-ok { background: #e6f4ea; color: #1d5a2e; }
.node-inventory__badge.is-off { background: #eef1f3; color: #55636d; }
.node-inventory__badge.is-warn { background: #fbefd9; color: #7a5a12; }
.node-inventory__bars { letter-spacing: 1px; color: #155f81; }
dl { display: grid; gap: 10px; margin: 0; }
dl div { display: flex; justify-content: space-between; gap: 14px; }
dt { color: #647482; flex-shrink: 0; }
dd { margin: 0; text-align: right; overflow-wrap: anywhere; }
</style>
