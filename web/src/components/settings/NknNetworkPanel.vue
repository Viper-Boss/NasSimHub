<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useAdvancedI18n } from '../../i18n/useAdvancedI18n'
import { Copy, ExternalLink, Globe2, Info, LoaderCircle, RefreshCw, ShieldCheck } from '@lucide/vue'
import { fixtureMode, getNKNSettings, saveNKNSettings, type NKNSettingsResponse } from '../../api/client'
import OverlayDialog from '../OverlayDialog.vue'
import {
  getNKNNetworkPeers,
  getNKNNetworkStatus,
  updateNKNNetwork,
  type NKNNetworkPeers,
  type NKNNetworkStatus
} from '../../api/client'

const { t } = useAdvancedI18n()
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
let savedTimer: number | undefined

// Merged master NKN client (identity + remote messaging) from the former
// standalone NKN panel; both switches live on this single section now.
const client = ref<NKNSettingsResponse | null>(null)
const clientBusy = ref(false)
const clientError = ref('')
const intro = ref(false)

function dismissIntro(): void {
  intro.value = false
  try { localStorage.setItem('homesim.nkn.intro.v1', 'seen') } catch { /* Storage may be unavailable. */ }
}

const clientStateLabel = computed(() => {
  switch (client.value?.status.state) {
    case 'connected': return t('homesim.advanced.text8')
    case 'connecting': return t('homesim.advanced.text9')
    case 'disabled': return t('homesim.advanced.text10')
    default: return t('homesim.advanced.text11')
  }
})

const clientStateClass = computed(() => {
  switch (client.value?.status.state) {
    case 'connected': return 'is-running'
    case 'connecting': return 'is-joining'
    case 'error': return 'is-error'
    default: return 'is-idle'
  }
})

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

async function loadClient(): Promise<void> {
  try {
    client.value = await getNKNSettings()
  } catch (reason) {
    clientError.value = reason instanceof Error ? reason.message : t('homesim.advanced.text12')
  }
}

async function toggleClient(): Promise<void> {
  if (!client.value || clientBusy.value || fixtureMode) return
  clientBusy.value = true
  clientError.value = ''
  try {
    client.value = await saveNKNSettings({ revision: client.value.settings.revision, enabled: !client.value.settings.enabled })
  } catch (reason) {
    clientError.value = reason instanceof Error ? reason.message : t('homesim.advanced.text13')
  } finally {
    clientBusy.value = false
  }
}

async function toggleDirectory(): Promise<void> {
  if (saving.value || !status.value) return
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    const result = await updateNKNNetwork({
      enabled: !enabled.value,
      expected_revision: status.value.settings.revision
    })
    status.value = result
    enabled.value = result.settings.enabled
    saved.value = true
    window.clearTimeout(savedTimer)
    savedTimer = window.setTimeout(() => { saved.value = false }, 2500)
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
  const address = client.value?.status.address || status.value?.runtime.my_address
  if (!address) return
  try {
    await navigator.clipboard.writeText(address)
    copied.value = true
    window.setTimeout(() => { copied.value = false }, 2000)
  } catch {
    copied.value = false
  }
}

let clientTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  void refreshPeers()
  void loadClient()
  try { intro.value = localStorage.getItem('homesim.nkn.intro.v1') !== 'seen' } catch { intro.value = true }
  clientTimer = setInterval(() => { if (!clientBusy.value) void loadClient() }, 10000)
})
onBeforeUnmount(() => {
  if (clientTimer) clearInterval(clientTimer)
  window.clearTimeout(savedTimer)
})
</script>

<template>
  <section class="nkn-network">
    <header class="nkn-network__hero">
      <span class="nkn-network__hero-icon"><Globe2 :size="28" /></span>
      <div>
        <p class="nkn-network__eyebrow">{{ t('homesim.advanced.text19') }}</p>
        <h2>{{ t('homesim.nknNetwork.title') }}</h2>
        <p>{{ t('homesim.advanced.text20') }}</p>
        <p class="nkn-network__hero-sub">{{ t('homesim.nknNetwork.subtitle') }}</p>
      </div>
      <button class="secondary-action" type="button" @click="intro = true"><Info :size="16" />{{ t('homesim.advanced.text21') }}</button>
    </header>
    <p v-if="fixtureMode" class="homesim-sim-notice">{{ t('homesim.advanced.text22') }}</p>

    <div v-if="loading" class="nkn-network__loading"><LoaderCircle class="spin" :size="18" /></div>
    <template v-else>
      <div class="nkn-network__card">
        <div class="nkn-network__switch-row">
          <div>
            <h3>{{ t('homesim.advanced.text15') }}</h3>
            <p class="nkn-network__hint">{{ t('homesim.advanced.text23') }}</p>
          </div>
          <input type="checkbox" class="ui-switch" role="switch"
                 :checked="client?.settings.enabled ?? false"
                 :aria-label="t('homesim.advanced.text15')"
                 :disabled="clientBusy || fixtureMode"
                 @change="toggleClient" />
        </div>
        <div v-if="client" class="nkn-network__status" :class="clientStateClass">
          <div class="nkn-network__status-row">
            <span class="nkn-network__status-dot" aria-hidden="true"></span>
            <strong>{{ t('homesim.nknNetwork.state') }}: {{ clientStateLabel }}</strong>
          </div>
          <dl v-if="client.status.address || status?.runtime.my_address">
            <dt>{{ t('homesim.advanced.text24') }}</dt>
            <dd class="nkn-network__address">
              <code>{{ client.status.address || status?.runtime.my_address }}</code>
              <button class="icon-button" type="button" :aria-label="t('homesim.nknNetwork.copy')" @click="copyAddress">
                <Copy :size="14" />
              </button>
              <span v-if="copied" class="nkn-network__copied">{{ t('homesim.nknNetwork.copied') }}</span>
            </dd>
          </dl>
          <p v-if="client.status.error" class="nkn-network__lasterror" role="alert">{{ client.status.error }}</p>
        </div>
        <p v-if="clientError" class="field-error" role="alert">{{ clientError }}</p>
      </div>

      <div class="nkn-network__card">
        <div class="nkn-network__switch-row">
          <div>
            <h3>{{ t('homesim.nknNetwork.directoryTitle') }}</h3>
            <p class="nkn-network__hint">{{ t('homesim.nknNetwork.enableHint') }}</p>
          </div>
          <input type="checkbox" class="ui-switch" role="switch"
                 :checked="enabled"
                 :aria-label="t('homesim.nknNetwork.enable')"
                 :disabled="saving || !loaded"
                 @change="toggleDirectory" />
        </div>
        <small class="nkn-network__hint nkn-network__privacy">{{ t('homesim.nknNetwork.privacy') }}</small>
        <div v-if="loaded && status" class="nkn-network__status" :class="stateClass">
          <div class="nkn-network__status-row">
            <span class="nkn-network__status-dot" aria-hidden="true"></span>
            <strong>{{ t('homesim.nknNetwork.state') }}: {{ stateLabel }}</strong>
          </div>
          <dl v-if="status.runtime.last_renew">
            <dt>{{ t('homesim.nknNetwork.lastRenew') }}</dt>
            <dd>{{ new Date(status.runtime.last_renew).toLocaleString() }}</dd>
          </dl>
          <p v-if="status.runtime.last_error" class="nkn-network__lasterror" role="alert">{{ t('homesim.nknNetwork.lastError') }}: {{ status.runtime.last_error }}</p>
        </div>
      </div>

      <div class="nkn-network__directory">
        <div class="nkn-network__directory-head">
          <h3>{{ t('homesim.nknNetwork.directoryTitle') }}</h3>
          <button class="secondary-action" type="button" :disabled="peersLoading" @click="refreshPeers">
            <LoaderCircle v-if="peersLoading" class="spin" :size="14" />
            <RefreshCw v-else :size="14" />{{ peersLoading ? t('homesim.nknNetwork.refreshing') : t('homesim.nknNetwork.refresh') }}
          </button>
        </div>
        <dl class="nkn-network__metrics" :aria-busy="peersLoading">
          <div><dt>{{ t('homesim.nknNetwork.active24h') }}</dt><dd>{{ peersError ? '—' : (peers?.active_24h ?? '—') }}</dd></div>
          <div><dt>{{ t('homesim.nknNetwork.activeMonth') }}</dt><dd>{{ peersError ? '—' : (peers?.active_month ?? '—') }}</dd></div>
          <div><dt>{{ t('homesim.nknNetwork.registeredTotal') }}</dt><dd>{{ peersError ? '—' : (peers?.registered_total ?? '—') }}</dd></div>
        </dl>
        <details class="nkn-network__hint">
          <summary class="ui-disclosure-summary">{{ t('homesim.nknNetwork.statisticsDetails') }}</summary>
          <p>{{ t('homesim.nknNetwork.statisticsScope') }}</p>
          <p v-if="peers?.coverage_started_at">{{ t('homesim.nknNetwork.coverageSince', {date: new Date(peers.coverage_started_at).toLocaleString()}) }}</p>
        </details>
        <p v-if="peersError" class="field-error" role="alert">{{ peersError }}</p>
      </div>
    </template>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <p v-if="saved" class="nkn-network__saved" role="status">{{ t('homesim.nknNetwork.saved') }}</p>

    <div class="nkn-network__about">
      <article class="nkn-network__about-card">
        <ShieldCheck :size="22" />
        <h3>{{ t('homesim.advanced.text27') }}</h3>
        <p>{{ t('homesim.advanced.text28') }}</p>
      </article>
      <article class="nkn-network__about-card">
        <Globe2 :size="22" />
        <h3>{{ t('homesim.advanced.text29') }}</h3>
        <p>{{ t('homesim.advanced.text30') }}</p>
        <RouterLink :to="{ name: 'settings', params: { section: 'sms-forward' } }">{{ t('homesim.advanced.text31') }}</RouterLink>
      </article>
    </div>

    <OverlayDialog :open="intro" :label="t('homesim.advanced.text18')" @close="dismissIntro">
      <div class="nkn-network__intro">
        <Globe2 :size="32" />
        <h2>{{ t('homesim.advanced.text32') }}</h2>
        <p>{{ t('homesim.advanced.text33') }}</p>
        <p>{{ t('homesim.advanced.text34') }}</p>
        <p>{{ t('homesim.advanced.text35') }}</p>
        <p>{{ t('homesim.advanced.text36') }}</p>
        <div class="nkn-network__intro-row">
          <a href="https://nkn.org/" target="_blank" rel="noopener noreferrer">{{ t('homesim.advanced.text37') }} <ExternalLink :size="14" /></a>
          <button class="primary-action" type="button" @click="dismissIntro">{{ t('homesim.advanced.text38') }}</button>
        </div>
      </div>
    </OverlayDialog>
  </section>
</template>

<style scoped>
.nkn-network { display: grid; gap: 16px; }
.nkn-network__hero { display: flex; gap: 18px; align-items: center; padding: 22px 24px; border: 1px solid var(--border); border-radius: 18px; background: linear-gradient(125deg, color-mix(in srgb, var(--accent) 13%, var(--surface)), var(--surface)); }
.nkn-network__hero-icon { display: grid; place-items: center; width: 54px; height: 54px; border-radius: 16px; color: var(--accent); background: color-mix(in srgb, var(--accent) 13%, transparent); flex-shrink: 0; }
.nkn-network__hero > div { min-width: 0; }
.nkn-network__hero h2 { margin: 2px 0 0; font-size: 20px; }
.nkn-network__hero > button { margin-left: auto; flex-shrink: 0; }
.nkn-network__eyebrow { margin: 0; color: var(--accent); font-size: 12px; font-weight: 650; }
.nkn-network__hero-sub { margin: 4px 0 0 !important; font-size: 12px; }
.nkn-network__about { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.nkn-network__about-card { display: grid; gap: 8px; align-content: start; padding: 18px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface); font-size: 13px; }
.nkn-network__about-card h3 { margin: 0; font-size: 14px; }
.nkn-network__about-card p { margin: 0; color: var(--muted); line-height: 1.7; }
.nkn-network__about-card a { color: var(--accent); }
.nkn-network__intro { padding: 26px; display: grid; gap: 16px; }
.nkn-network__intro h2 { margin: 0; font-size: 20px; }
.nkn-network__intro p { margin: 0; color: var(--muted); line-height: 1.8; font-size: 13px; }
.nkn-network__intro-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.nkn-network__intro-row a { display: inline-flex; align-items: center; gap: 6px; color: var(--accent); }
@media (max-width: 860px) { .nkn-network__about { grid-template-columns: 1fr; } .nkn-network__hero { flex-wrap: wrap; padding: 18px; } .nkn-network__hero > button { margin-left: 0; } }
.nkn-network__loading { display: grid; place-items: center; padding: 30px; color: var(--muted); }
.nkn-network__card { display: grid; gap: 10px; padding: 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.nkn-network__switch-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.nkn-network__switch-row h3 { margin: 0; font-size: 14px; }
.nkn-network__switch-row .ui-switch { flex-shrink: 0; }
.nkn-network__switch-row p { margin: 4px 0 0; }
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
.nkn-network__metrics { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin: 4px 0; }
.nkn-network__metrics > div { padding: 16px 12px; background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 12px; }
.nkn-network__metrics dt { color: var(--muted); font-size: 12px; }
.nkn-network__metrics dd { margin: 10px 0 0; font-size: 28px; font-weight: 700; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
@media (max-width: 560px) { .nkn-network__metrics { gap: 6px; } .nkn-network__metrics > div { padding: 12px 8px; } .nkn-network__metrics dd { font-size: 24px; } }
.nkn-network__saved { color: var(--success); font-size: 13px; }
.spin { animation: nkn-network-spin 1s linear infinite; }
@keyframes nkn-network-spin { to { transform: rotate(360deg); } }
@keyframes nkn-network-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
</style>
