<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, LoaderCircle, Save } from '@lucide/vue'
import SensitiveValue from '../SensitiveValue.vue'
import {
  getNKNNodeLogs,
  getNKNNodeStatus,
  updateNKNNode,
  type NKNNodeStatus
} from '../../api/client'

const { t } = useI18n()
const status = ref<NKNNodeStatus | null>(null)
const beneficiary = ref('')
const seedOverrides = ref('')
const enabled = ref(false)
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref(false)
const logs = ref<string[]>([])
const logsLoading = ref(false)
const logsOpen = ref(false)
const copied = ref(false)

const stateLabel = computed(() => {
  switch (status.value?.runtime.state) {
    case 'running': return t('homesim.nknNode.stateRunning')
    case 'error': return t('homesim.nknNode.stateError')
    case 'unavailable': return t('homesim.nknNode.stateUnavailable')
    default: return t('homesim.nknNode.stateStopped')
  }
})

const stateClass = computed(() => {
  switch (status.value?.runtime.state) {
    case 'running': return 'is-running'
    case 'error': return 'is-error'
    default: return 'is-idle'
  }
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await getNKNNodeStatus()
    status.value = result
    enabled.value = result.settings.enabled
    beneficiary.value = result.settings.beneficiary_addr
    seedOverrides.value = result.settings.seed_overrides ?? ''
    loaded.value = true
  } catch (reason) {
    loaded.value = false
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNode.loadFailed')
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
    const result = await updateNKNNode({
      enabled: enabled.value,
      beneficiary_addr: beneficiary.value.trim(),
      seed_overrides: seedOverrides.value.trim(),
      expected_revision: status.value.settings.revision
    })
    status.value = result
    saved.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNode.loadFailed')
  } finally {
    saving.value = false
  }
}

async function toggleLogs(open: boolean): Promise<void> {
  logsOpen.value = open
  if (!open || logs.value.length) return
  logsLoading.value = true
  try {
    logs.value = await getNKNNodeLogs()
  } catch {
    logs.value = []
  } finally {
    logsLoading.value = false
  }
}

async function copyWalletAddress(): Promise<void> {
  const address = status.value?.runtime.wallet_address
  if (!address) return
  try {
    await navigator.clipboard.writeText(address)
    copied.value = true
    window.setTimeout(() => { copied.value = false }, 2000)
  } catch {
    copied.value = false
  }
}

onMounted(() => { void load() })
</script>

<template>
  <section class="nkn-node">
    <header class="nkn-node__header">
      <h2>{{ t('homesim.nknNode.title') }}</h2>
      <p>{{ t('homesim.nknNode.subtitle') }}</p>
    </header>

    <div v-if="loading" class="nkn-node__loading"><LoaderCircle class="spin" :size="18" /></div>
    <template v-else-if="loaded && status">
      <p v-if="!status.binary_present" class="nkn-node__warn" role="status">{{ t('homesim.nknNode.binaryMissing') }}</p>

      <div class="nkn-node__card">
        <label class="nkn-node__enabled">
          <input v-model="enabled" class="ui-check" type="checkbox" :disabled="saving" />
          <span>{{ t('homesim.nknNode.enable') }}</span>
        </label>
        <small class="nkn-node__hint">{{ t('homesim.nknNode.enableHint') }}</small>
        <label>
          <span>{{ t('homesim.nknNode.beneficiary') }}</span>
          <input v-model="beneficiary" spellcheck="false" autocomplete="off" placeholder="NKN…" :disabled="saving" />
          <small>{{ t('homesim.nknNode.beneficiaryHint') }}</small>
        </label>
        <label>
          <span>{{ t('homesim.nknNode.seedOverrides') }}</span>
          <input v-model="seedOverrides" spellcheck="false" autocomplete="off" placeholder="http://my-seed.example.com:30003" :disabled="saving" />
          <small>{{ t('homesim.nknNode.seedOverridesHint') }}</small>
        </label>
        <label>
          <span>{{ t('homesim.nknNode.idRegistration') }}</span>
          <small>{{ t('homesim.nknNode.idRegistrationHint') }}</small>
        </label>
      </div>

      <div class="nkn-node__status" :class="stateClass">
        <div class="nkn-node__status-row">
          <span class="nkn-node__status-dot" aria-hidden="true"></span>
          <strong>{{ t('homesim.nknNode.state') }}: {{ stateLabel }}</strong>
        </div>
        <dl v-if="status.runtime.wallet_address">
          <dt>{{ t('homesim.nknNode.walletAddress') }}</dt>
          <dd class="nkn-node__address">
            <code>{{ status.runtime.wallet_address }}</code>
            <button class="icon-button" type="button" :aria-label="t('homesim.nknNode.copy')" @click="copyWalletAddress">
              <Copy :size="14" />
            </button>
            <span v-if="copied" class="nkn-node__copied">{{ t('homesim.nknNode.copied') }}</span>
          </dd>
        </dl>
        <dl v-if="status.wallet_password">
          <dt>{{ t('homesim.nknNode.walletPassword') }}</dt>
          <dd><SensitiveValue :value="status.wallet_password" :label="t('homesim.nknNode.walletPassword')" /></dd>
          <small class="nkn-node__hint">{{ t('homesim.nknNode.walletPasswordHint') }}</small>
        </dl>
        <dl>
          <dt>{{ t('homesim.nknNode.lightSync') }}</dt>
          <dd>{{ t('homesim.nknNode.lightSyncValue') }}</dd>
        </dl>
        <dl v-if="status.runtime.sync_state">
          <dt>{{ t('homesim.nknNode.syncState') }}</dt>
          <dd>{{ status.runtime.sync_state }}</dd>
        </dl>
        <dl v-if="status.runtime.started_at">
          <dt>{{ t('homesim.nknNode.startedAt') }}</dt>
          <dd>{{ new Date(status.runtime.started_at).toLocaleString() }}</dd>
        </dl>
        <p v-if="status.runtime.error" class="nkn-node__lasterror" role="alert">{{ t('homesim.nknNode.lastError') }}: {{ status.runtime.error }}</p>
      </div>

      <details class="nkn-node__logs" @toggle="toggleLogs(($event.target as HTMLDetailsElement).open)">
        <summary>{{ t('homesim.nknNode.logs') }}</summary>
        <div v-if="logsLoading" class="nkn-node__loading"><LoaderCircle class="spin" :size="16" /></div>
        <pre v-else-if="logs.length" class="nkn-node__logtail">{{ logs.join('\n') }}</pre>
        <p v-else class="nkn-node__hint">{{ t('homesim.nknNode.noLogs') }}</p>
      </details>
    </template>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <p v-if="saved" class="nkn-node__saved" role="status">{{ t('homesim.nknNode.saved') }}</p>
    <footer class="nkn-node__footer">
      <button class="primary-action" type="button" :disabled="saving || !loaded" @click="save">
        <LoaderCircle v-if="saving" class="spin" :size="16" />
        <Save v-else :size="16" />{{ t('homesim.nknNode.save') }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.nkn-node { display: grid; gap: 16px; }
.nkn-node__header h2 { font-size: 18px; }
.nkn-node__header p { margin-top: 4px; color: var(--muted); font-size: 13px; }
.nkn-node__loading { display: grid; place-items: center; padding: 30px; color: var(--muted); }
.nkn-node__warn { padding: 10px 12px; color: var(--text); font-size: 12.5px; background: var(--warning-soft, var(--surface-subtle)); border: 1px solid var(--border); border-radius: 9px; }
.nkn-node__card { display: grid; gap: 12px; padding: 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.nkn-node__card label { display: grid; gap: 6px; font-size: 13px; }
.nkn-node__card label > span { font-weight: 650; }
.nkn-node__card input:not([type=checkbox]) { width: 100%; min-width: 0; box-sizing: border-box; padding: 9px 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; }
.nkn-node__enabled { display: inline-flex !important; flex-direction: row !important; align-items: center; gap: 8px; font-size: 13px; font-weight: 650; }
.nkn-node__hint { color: var(--muted); font-size: 11.5px; }
.nkn-node__status { display: grid; gap: 10px; padding: 14px 16px; border: 1px solid var(--border); border-radius: 14px; font-size: 13px; }
.nkn-node__status.is-running { border-color: var(--success-border); background: var(--success-soft); }
.nkn-node__status.is-error { border-color: var(--danger-border, var(--border)); background: var(--danger-soft, var(--surface-subtle)); }
.nkn-node__status dl { display: grid; gap: 4px; margin: 0; }
.nkn-node__status dt { color: var(--muted); font-size: 11.5px; }
.nkn-node__status dd { margin: 0; overflow-wrap: anywhere; }
.nkn-node__status-row { display: flex; align-items: center; gap: 8px; }
.nkn-node__status-dot { width: 9px; height: 9px; border-radius: 50%; background: var(--muted); }
.is-running .nkn-node__status-dot { background: var(--success); }
.is-error .nkn-node__status-dot { background: var(--danger, var(--muted)); }
.nkn-node__address { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.nkn-node__address code { font-size: 12px; }
.nkn-node__copied { color: var(--success); font-size: 12px; }
.nkn-node__lasterror { margin: 0; overflow-wrap: anywhere; color: var(--danger, inherit); font-size: 12.5px; }
.nkn-node__logs summary { cursor: pointer; color: var(--muted); font-size: 13px; }
.nkn-node__logtail { max-height: 260px; overflow: auto; margin: 8px 0 0; padding: 10px; color: var(--text); font-size: 11.5px; line-height: 1.5; background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 9px; white-space: pre-wrap; word-break: break-all; }
.nkn-node__saved { color: var(--success); font-size: 13px; }
.nkn-node__footer { display: flex; justify-content: flex-end; }
.spin { animation: nkn-node-spin 1s linear infinite; }
@keyframes nkn-node-spin { to { transform: rotate(360deg); } }
</style>
