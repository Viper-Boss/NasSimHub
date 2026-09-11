<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Network, Plus, Trash2 } from '@lucide/vue'
import { getDHTSettings, saveDHTSettings, type DHTResponse } from '../../api/client'
import { dhtPanelMessages } from '../../i18n/dhtPanelMessages'

// The DHT is the second control path beside NKN: a way for two instances to
// find each other when NKN is unavailable or blocked. It carries discovery and
// routing only -- no messages, no call audio, no keys.
//
// It runs by default, like NKN beside it. That is only safe because the node is
// confined to this project's own protocol prefix and relays nothing for anyone:
// see agent/internal/dht for what that confinement actually is.

const i18n = useI18n()
const { t } = i18n

// The panel's own copy is merged in when the panel loads rather than shipped in
// every user's startup locale. Flushed synchronously so the first render already
// has the strings instead of briefly painting raw key names.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, { dht: dhtPanelMessages(locale) })
  },
  { immediate: true, flush: 'sync' }
)

const data = ref<DHTResponse | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const draftPeer = ref('')

let timer: ReturnType<typeof setInterval> | undefined

async function load(): Promise<void> {
  error.value = ''
  try {
    data.value = await getDHTSettings()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}

// The status half changes without anyone touching the form, so it is refreshed
// on a timer. The settings half is only replaced by a save, or the operator's
// half-typed bootstrap address would vanish under them.
async function refreshStatus(): Promise<void> {
  if (busy.value) return
  try {
    const next = await getDHTSettings()
    if (data.value) data.value.status = next.status
    else data.value = next
  } catch {
    // A failed poll leaves the last known status on screen rather than
    // blanking a panel the operator is reading.
  }
}

async function save(): Promise<void> {
  if (!data.value || busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    data.value = await saveDHTSettings(data.value.settings)
    notice.value = t('dht.saved')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    busy.value = false
  }
}

function addPeer(): void {
  const value = draftPeer.value.trim()
  if (!value || !data.value) return
  if (data.value.settings.bootstrap_peers.includes(value)) {
    draftPeer.value = ''
    return
  }
  data.value.settings.bootstrap_peers.push(value)
  draftPeer.value = ''
}

function removePeer(index: number): void {
  data.value?.settings.bootstrap_peers.splice(index, 1)
}

const draftAuthorized = ref('')

function addAuthorized(): void {
  const value = draftAuthorized.value.trim()
  if (!value || !data.value) return
  if (!data.value.settings.authorized_peers.includes(value)) {
    data.value.settings.authorized_peers.push(value)
  }
  draftAuthorized.value = ''
}

function removeAuthorized(index: number): void {
  data.value?.settings.authorized_peers.splice(index, 1)
}

// The allowlist holds identities, not addresses. A multiaddr pasted here would
// look like it authorized something while authorizing nobody, so it is refused
// with the fix rather than accepted and quietly ignored.
const draftAuthorizedProblem = computed(() => {
  const value = draftAuthorized.value.trim()
  if (!value) return ''
  if (value.includes('/')) return t('dht.authorizedNotMultiaddr')
  return ''
})

// Empty means "anyone speaking our protocol". That is what a fresh node needs
// so its partner can reach it at all, but it is not the same as authorized, and
// the panel says which of the two is in force rather than leaving it implied.
const gateIsOpen = computed(() => (data.value?.settings.authorized_peers.length ?? 0) === 0)

// A bootstrap address without /p2p/<PeerID> names a host but not an identity,
// which is exactly what a DHT address has to carry. The API refuses these, so
// flagging them here saves a round trip and explains why.
const draftPeerProblem = computed(() => {
  const value = draftPeer.value.trim()
  if (!value) return ''
  if (!value.startsWith('/')) return t('dht.peerNotMultiaddr')
  if (!value.includes('/p2p/')) return t('dht.peerNeedsIdentity')
  return ''
})

const stateLabel = computed(() => {
  const state = data.value?.status.state
  switch (state) {
    case 'ready': return t('dht.stateReady')
    case 'isolated': return t('dht.stateIsolated')
    case 'starting': return t('dht.stateStarting')
    case 'error': return t('dht.stateError')
    case 'disabled': return t('dht.stateDisabled')
    case 'unavailable': return t('dht.stateUnavailable')
    default: return t('dht.statePending')
  }
})

// "isolated" is the state worth explaining: the node is running perfectly and
// has found nobody, which looks like a fault and is not one.
const isolated = computed(() => data.value?.status.state === 'isolated')

const maxPeersReached = computed(
  () => (data.value?.settings.bootstrap_peers.length ?? 0) >= 8
)

onMounted(() => {
  void load()
  timer = setInterval(() => { void refreshStatus() }, 15_000)
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section class="dht-panel">
    <header class="dht-panel__head">
      <Network :size="28" />
      <div>
        <h2>{{ t('dht.title') }}</h2>
        <p>{{ t('dht.subtitle') }}</p>
      </div>
    </header>

    <p v-if="error" class="dht-panel__error" role="alert">{{ error }}</p>
    <p v-else-if="notice" class="dht-panel__notice" role="status">{{ notice }}</p>

    <template v-if="data">
      <div class="homesim-sim-notice">
        <strong>{{ stateLabel }}</strong>
        <p v-if="isolated">{{ t('dht.isolatedHint') }}</p>
        <p v-if="data.status.error">{{ data.status.error }}</p>
        <p>
          {{ t('dht.peerCounts', {
            connected: data.status.connected_peers,
            routing: data.status.routing_peers
          }) }}
        </p>
        <p>
          {{ data.status.lan_discovery
            ? t('dht.lanOn', { count: data.status.lan_peers })
            : t('dht.lanOff') }}
        </p>
        <p v-if="data.status.peer_id">
          {{ t('dht.peerIdLabel') }}<code>{{ data.status.peer_id }}</code>
        </p>
      </div>

      <div class="dht-grid">
        <label class="dht-switch">
          <input v-model="data.settings.enabled" class="ui-check" type="checkbox" />
          {{ t('dht.enable') }}
        </label>
        <label>
          {{ t('dht.listenPort') }}
          <input v-model.number="data.settings.listen_port" type="number" min="1024" max="65535" />
        </label>
        <label class="dht-switch">
          <input
            :checked="!data.settings.disable_lan_discovery"
            class="ui-check"
            type="checkbox"
            @change="data.settings.disable_lan_discovery = !($event.target as HTMLInputElement).checked"
          />
          {{ t('dht.lanDiscovery') }}
        </label>
      </div>
      <p class="dht-hint">{{ t('dht.lanHint') }}</p>
      <p class="dht-hint">{{ t('dht.scopeNote') }}</p>

      <h3>{{ t('dht.bootstrapTitle') }}</h3>
      <p class="dht-hint">{{ t('dht.bootstrapHint') }}</p>

      <ul v-if="data.settings.bootstrap_peers.length" class="dht-peers">
        <li v-for="(peer, index) in data.settings.bootstrap_peers" :key="peer">
          <code>{{ peer }}</code>
          <button
            type="button"
            class="secondary-button"
            :aria-label="t('dht.removePeer')"
            @click="removePeer(index)"
          >
            <Trash2 :size="15" />
          </button>
        </li>
      </ul>
      <p v-else class="dht-hint">{{ t('dht.noBootstrap') }}</p>

      <div class="dht-add">
        <input
          v-model="draftPeer"
          spellcheck="false"
          :placeholder="'/ip4/203.0.113.9/tcp/4001/p2p/12D3Koo...'"
          :aria-label="t('dht.addPeer')"
          @keydown.enter.prevent="addPeer"
        />
        <button
          type="button"
          class="secondary-button"
          :disabled="!draftPeer.trim() || Boolean(draftPeerProblem) || maxPeersReached"
          @click="addPeer"
        >
          <Plus :size="16" />{{ t('dht.addPeer') }}
        </button>
      </div>
      <p v-if="draftPeerProblem" class="dht-panel__error">{{ draftPeerProblem }}</p>
      <p v-else-if="maxPeersReached" class="dht-hint">{{ t('dht.tooManyPeers') }}</p>

      <h3>{{ t('dht.authorizedTitle') }}</h3>
      <p class="dht-hint">{{ t('dht.authorizedHint') }}</p>
      <p class="dht-gate" :class="gateIsOpen ? 'is-open' : 'is-strict'">
        {{ gateIsOpen ? t('dht.gateOpen') : t('dht.gateStrict') }}
      </p>

      <ul v-if="data.settings.authorized_peers.length" class="dht-peers">
        <li v-for="(id, index) in data.settings.authorized_peers" :key="id">
          <code>{{ id }}</code>
          <button
            type="button"
            class="secondary-button"
            :aria-label="t('dht.removePeer')"
            @click="removeAuthorized(index)"
          >
            <Trash2 :size="15" />
          </button>
        </li>
      </ul>

      <div class="dht-add">
        <input
          v-model="draftAuthorized"
          spellcheck="false"
          placeholder="12D3KooW..."
          :aria-label="t('dht.addAuthorized')"
          @keydown.enter.prevent="addAuthorized"
        />
        <button
          type="button"
          class="secondary-button"
          :disabled="!draftAuthorized.trim() || Boolean(draftAuthorizedProblem)"
          @click="addAuthorized"
        >
          <Plus :size="16" />{{ t('dht.addAuthorized') }}
        </button>
      </div>
      <p v-if="draftAuthorizedProblem" class="dht-panel__error">{{ draftAuthorizedProblem }}</p>

      <p class="dht-hint dht-scope">{{ t('dht.containmentNote') }}</p>

      <div class="dht-actions">
        <button class="primary-button" type="button" :disabled="busy" @click="save">
          {{ busy ? t('dht.saving') : t('dht.save') }}
        </button>
        <button class="secondary-button" type="button" :disabled="busy" @click="load">
          {{ t('dht.reload') }}
        </button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.dht-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.dht-panel__head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.dht-panel__head h2 {
  margin: 0 0 2px;
  font-size: 16px;
}

.dht-panel__head p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}

.dht-panel__error {
  margin: 0;
  color: var(--danger);
  font-size: 12px;
}

.dht-panel__notice {
  margin: 0;
  color: var(--success);
  font-size: 12px;
}

.dht-grid {
  display: grid;
  gap: 10px 16px;
  /* Bottom-aligned so the toggle sits on the same line as the port input
     rather than floating in the middle of its own taller cell. */
  align-items: end;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
}

.dht-grid label {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
  color: var(--muted);
  font-size: 12px;
}

.dht-switch {
  flex-direction: row !important;
  align-items: center;
  gap: 8px !important;
}

.dht-hint {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
}

.dht-peers {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  gap: 6px;
  list-style: none;
}

.dht-peers li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface-subtle);
}

.dht-peers code {
  min-width: 0;
  flex: 1;
  overflow-x: auto;
  font-size: 11.5px;
  white-space: nowrap;
}

.dht-add {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.dht-add input {
  min-width: 0;
  flex: 1 1 280px;
}

.dht-gate {
  margin: 0;
  padding: 6px 10px;
  border-radius: var(--radius-control);
  font-size: 11.5px;
}

.dht-gate.is-open {
  color: var(--warning, #b45309);
  background: var(--warning-soft, rgba(251, 191, 36, 0.14));
}

.dht-gate.is-strict {
  color: var(--success);
  background: var(--success-soft, rgba(52, 211, 153, 0.14));
}

.dht-scope {
  padding-top: 8px;
  border-top: 1px solid var(--border);
}

.dht-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
