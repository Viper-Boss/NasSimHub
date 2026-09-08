<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, LoaderCircle, RefreshCw, Save } from '@lucide/vue'
import {
  getNKNNetworkPeers,
  getNKNNetworkStatus,
  updateNKNNetwork,
  type NKNNetworkPeers,
  type NKNNetworkStatus
} from '../../api/client'

const { t } = useI18n()
const status = ref<NKNNetworkStatus | null>(null)
const peers = ref<NKNNetworkPeers | null>(null)
const enabled = ref(false)
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const peersLoading = ref(false)
const error = ref('')
const peersError = ref('')
const saved = ref(false)
const copied = ref(false)

const stateLabel = computed(() => {
  switch (status.value?.runtime.state) {
    case 'joining': return t('homesim.nknNetwork.stateJoining')
    case 'active': return t('homesim.nknNetwork.stateActive')
    case 'error': return t('homesim.nknNetwork.stateError')
    default: return t('homesim.nknNetwork.stateDisabled')
  }
})

const stateClass = computed(() => {
  switch (status.value?.runtime.state) {
    case 'active': return 'is-running'
    case 'joining': return 'is-joining'
    case 'error': return 'is-error'
    default: return 'is-idle'
  }
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await getNKNNetworkStatus()
    status.value = result
    enabled.value = result.settings.enabled
    loaded.value = true
  } catch (reason) {
    loaded.value = false
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNetwork.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (saving.value || !status.value) return
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    const result = await updateNKNNetwork({
      enabled: enabled.value,
      expected_revision: status.value.settings.revision
    })
    status.value = result
    saved.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNetwork.loadFailed')
  } finally {
    saving.value = false
  }
}

async function refreshPeers(): Promise<void> {
  if (peersLoading.value) return
  peersLoading.value = true
  peersError.value = ''
  try {
    peers.value = await getNKNNetworkPeers()
  } catch {
    peersError.value = t('homesim.nknNetwork.fetchFailed')
  } finally {
    peersLoading.value = false
  }
}

async function copyAddress(): Promise<void> {
  const address = status.value?.runtime.my_address
  if (!address) return
  try {
    await navigator.clipboard.writeText(address)
    copied.value = true
    window.setTimeout(() => { copied.value = false }, 2000)
  } catch {
    copied.value = false
  }
}

onMounted(() => {
  void load()
  void refreshPeers()
})
</script>

<template>
  <section class="nkn-network">
    <header class="nkn-network__header">
      <h2>{{ t('homesim.nknNetwork.title') }}</h2>
      <p>{{ t('homesim.nknNetwork.subtitle') }}</p>
    </header>

    <div v-if="loading" class="nkn-network__loading"><LoaderCircle class="spin" :size="18" /></div>
    <template v-else-if="loaded && status">
      <div class="nkn-network__card">
        <label class="nkn-network__enabled">
          <input v-model="enabled" class="ui-check" type="checkbox" :disabled="saving" />
          <span>{{ t('homesim.nknNetwork.enable') }}</span>
        </label>
        <small class="nkn-network__hint">{{ t('homesim.nknNetwork.enableHint') }}</small>
        <small class="nkn-network__hint nkn-network__privacy">{{ t('homesim.nknNetwork.privacy') }}</small>
      </div>

      <div class="nkn-network__status" :class="stateClass">
        <div class="nkn-network__status-row">
          <span class="nkn-network__status-dot" aria-hidden="true"></span>
          <strong>{{ t('homesim.nknNetwork.state') }}: {{ stateLabel }}</strong>
        </div>
        <dl v-if="status.runtime.my_address">
          <dt>{{ t('homesim.nknNetwork.myAddress') }}</dt>
          <dd class="nkn-network__address">
            <code>{{ status.runtime.my_address }}</code>
            <button class="icon-button" type="button" :aria-label="t('homesim.nknNetwork.copy')" @click="copyAddress">
              <Copy :size="14" />
            </button>
            <span v-if="copied" class="nkn-network__copied">{{ t('homesim.nknNetwork.copied') }}</span>
          </dd>
        </dl>
        <dl v-if="status.runtime.last_renew">
          <dt>{{ t('homesim.nknNetwork.lastRenew') }}</dt>
          <dd>{{ new Date(status.runtime.last_renew).toLocaleString() }}</dd>
        </dl>
        <p v-if="status.runtime.last_error" class="nkn-network__lasterror" role="alert">{{ t('homesim.nknNetwork.lastError') }}: {{ status.runtime.last_error }}</p>
      </div>
    </template>

    <div class="nkn-network__directory">
      <div class="nkn-network__directory-head">
        <h3>{{ t('homesim.nknNetwork.directoryTitle') }}</h3>
        <button class="secondary-action" type="button" :disabled="peersLoading" @click="refreshPeers">
          <LoaderCircle v-if="peersLoading" class="spin" :size="14" />
          <RefreshCw v-else :size="14" />{{ peersLoading ? t('homesim.nknNetwork.refreshing') : t('homesim.nknNetwork.refresh') }}
        </button>
      </div>
      <p v-if="peers" class="nkn-network__count">{{ t('homesim.nknNetwork.count', { count: peers.count }) }}</p>
      <p v-if="peersError" class="field-error" role="alert">{{ peersError }}</p>
      <ul v-if="peers && peers.peers.length" class="nkn-network__peers">
        <li v-for="peer in peers.peers" :key="peer.address" class="nkn-network__peer">
          <code>{{ peer.address }}</code>
          <span v-if="peer.is_self" class="nkn-network__self">{{ t('homesim.nknNetwork.selfBadge') }}</span>
        </li>
      </ul>
      <p v-else-if="peers && !peers.peers.length && !peersError" class="nkn-network__hint">{{ t('homesim.nknNetwork.empty') }}</p>
    </div>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <p v-if="saved" class="nkn-network__saved" role="status">{{ t('homesim.nknNetwork.saved') }}</p>
    <footer class="nkn-network__footer">
      <button class="primary-action" type="button" :disabled="saving || !loaded" @click="save">
        <LoaderCircle v-if="saving" class="spin" :size="16" />
        <Save v-else :size="16" />{{ t('homesim.nknNetwork.save') }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.nkn-network { display: grid; gap: 16px; }
.nkn-network__header h2 { font-size: 18px; }
.nkn-network__header p { margin-top: 4px; color: var(--muted); font-size: 13px; }
.nkn-network__loading { display: grid; place-items: center; padding: 30px; color: var(--muted); }
.nkn-network__card { display: grid; gap: 10px; padding: 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.nkn-network__enabled { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 650; }
.nkn-network__hint { color: var(--muted); font-size: 11.5px; }
.nkn-network__privacy { padding: 8px 10px; border: 1px dashed var(--border); border-radius: 9px; }
.nkn-network__status { display: grid; gap: 10px; padding: 14px 16px; border: 1px solid var(--border); border-radius: 14px; font-size: 13px; }
.nkn-network__status.is-running { border-color: var(--success-border); background: var(--success-soft); }
.nkn-network__status.is-joining { border-color: var(--warning-border, var(--border)); background: var(--warning-soft, var(--surface-subtle)); }
.nkn-network__status.is-error { border-color: var(--danger-border, var(--border)); background: var(--danger-soft, var(--surface-subtle)); }
.nkn-network__status dl { display: grid; gap: 4px; margin: 0; }
.nkn-network__status dt { color: var(--muted); font-size: 11.5px; }
.nkn-network__status dd { margin: 0; overflow-wrap: anywhere; }
.nkn-network__status-row { display: flex; align-items: center; gap: 8px; }
.nkn-network__status-dot { width: 9px; height: 9px; border-radius: 50%; background: var(--muted); }
.is-running .nkn-network__status-dot { background: var(--success); }
.is-joining .nkn-network__status-dot { background: var(--warning, var(--muted)); animation: nkn-network-pulse 1.6s ease-in-out infinite; }
.is-error .nkn-network__status-dot { background: var(--danger, var(--muted)); }
.nkn-network__address { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.nkn-network__address code { font-size: 12px; }
.nkn-network__copied { color: var(--success); font-size: 12px; }
.nkn-network__lasterror { margin: 0; overflow-wrap: anywhere; color: var(--danger, inherit); font-size: 12.5px; }
.nkn-network__directory { display: grid; gap: 10px; padding: 14px 16px; border: 1px solid var(--border); border-radius: 14px; font-size: 13px; }
.nkn-network__directory-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.nkn-network__directory-head h3 { margin: 0; font-size: 14px; }
.nkn-network__count { margin: 0; color: var(--muted); font-size: 12px; }
.nkn-network__peers { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
.nkn-network__peer { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.nkn-network__peer code { font-size: 12px; overflow-wrap: anywhere; }
.nkn-network__self { padding: 1px 7px; color: var(--success); font-size: 11px; font-weight: 650; border: 1px solid var(--success-border); border-radius: 999px; background: var(--success-soft); }
.nkn-network__saved { color: var(--success); font-size: 13px; }
.nkn-network__footer { display: flex; justify-content: flex-end; }
.spin { animation: nkn-network-spin 1s linear infinite; }
@keyframes nkn-network-spin { to { transform: rotate(360deg); } }
@keyframes nkn-network-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
</style>
