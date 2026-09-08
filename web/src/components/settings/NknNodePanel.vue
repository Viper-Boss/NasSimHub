<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, Download, KeyRound, LoaderCircle, QrCode, Save, Trash2 } from '@lucide/vue'
import QRCode from 'qrcode'
import {
  downloadNKNNodeWalletBackup,
  getNKNNodeLogs,
  getNKNNodeStatus,
  updateNKNNode,
  walletNKNNodeAction,
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

// Wallet self-custody state
const walletMode = ref<'create' | 'import'>('create')
const walletBusy = ref(false)
const newPassword = ref('')
const importKeystore = ref('')
const importPassword = ref('')
const passwordBackedUp = ref(false)
const confirmDelete = ref(false)
const qrOpen = ref(false)
const qrDataUrl = ref('')

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

const walletAddress = computed(() => status.value?.runtime.wallet_address ?? '')

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

// --- wallet lifecycle -------------------------------------------------------

// 24 chars from an unambiguous alphabet via the browser CSPRNG. The password
// only ever exists on this page: it is sent once to the NAS to encrypt the
// keystore / unlock nknd, and never returned by any API afterwards.
function generateStrongPassword(): void {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789!@#$%^&*-_=+'
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  newPassword.value = Array.from(bytes, b => alphabet[b % alphabet.length]).join('')
  passwordBackedUp.value = false
}

async function createWallet(): Promise<void> {
  if (walletBusy.value || !status.value) return
  if (newPassword.value.trim().length < 8) {
    error.value = t('homesim.nknNode.walletPasswordTooShort')
    return
  }
  if (!passwordBackedUp.value) {
    error.value = t('homesim.nknNode.walletBackupCheckboxRequired')
    return
  }
  await runWalletAction(() => walletNKNNodeAction({ action: 'create', password: newPassword.value.trim() }), 'walletCreated')
}

async function importWallet(): Promise<void> {
  if (walletBusy.value || !status.value) return
  if (!importKeystore.value.trim()) {
    error.value = t('homesim.nknNode.walletKeystoreRequired')
    return
  }
  await runWalletAction(() => walletNKNNodeAction({ action: 'import', keystore: importKeystore.value.trim(), password: importPassword.value.trim() }), 'walletImported')
}

async function runWalletAction(action: () => Promise<NKNNodeStatus>, savedKey: string): Promise<void> {
  walletBusy.value = true
  error.value = ''
  saved.value = false
  try {
    status.value = await action()
    newPassword.value = ''
    importKeystore.value = ''
    importPassword.value = ''
    passwordBackedUp.value = false
    saved.value = false
    error.value = ''
    walletActionDone.value = savedKey
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNode.loadFailed')
  } finally {
    walletBusy.value = false
  }
}

const walletActionDone = ref('')

async function deleteWallet(): Promise<void> {
  if (walletBusy.value || !status.value) return
  // Two-step confirmation: the first click arms the button for 5 seconds.
  if (!confirmDelete.value) {
    confirmDelete.value = true
    window.setTimeout(() => { confirmDelete.value = false }, 5000)
    return
  }
  confirmDelete.value = false
  await runWalletAction(() => walletNKNNodeAction({ action: 'delete', confirm: true }), 'walletDeleted')
}

async function backupWallet(): Promise<void> {
  error.value = ''
  try {
    await downloadNKNNodeWalletBackup()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.nknNode.loadFailed')
  }
}

async function showQr(): Promise<void> {
  if (!walletAddress.value) return
  try {
    qrDataUrl.value = await QRCode.toDataURL(walletAddress.value, { width: 240, margin: 2, errorCorrectionLevel: 'M' })
    qrOpen.value = true
  } catch {
    qrOpen.value = false
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

      <!-- Self-custody wallet card -->
      <div class="nkn-node__card nkn-node__wallet">
        <h3>{{ t('homesim.nknNode.walletCardTitle') }}</h3>

        <template v-if="status.wallet_exists">
          <dl>
            <dt>{{ t('homesim.nknNode.walletAddress') }}</dt>
            <dd class="nkn-node__address">
              <code>{{ walletAddress }}</code>
              <button class="icon-button" type="button" :aria-label="t('homesim.nknNode.copy')" @click="copyWalletAddress">
                <Copy :size="14" />
              </button>
              <button class="icon-button" type="button" :aria-label="t('homesim.nknNode.walletQr')" @click="showQr">
                <QrCode :size="14" />
              </button>
              <span v-if="copied" class="nkn-node__copied">{{ t('homesim.nknNode.copied') }}</span>
            </dd>
          </dl>
          <div class="nkn-node__wallet-actions">
            <button class="secondary-button" type="button" :disabled="walletBusy" @click="backupWallet">
              <Download :size="14" />{{ t('homesim.nknNode.walletBackup') }}
            </button>
            <button class="secondary-button nkn-node__danger" :class="{ armed: confirmDelete }" type="button" :disabled="walletBusy" @click="deleteWallet">
              <Trash2 :size="14" />{{ confirmDelete ? t('homesim.nknNode.walletDeleteConfirm') : t('homesim.nknNode.walletDelete') }}
            </button>
          </div>
          <small class="nkn-node__hint">{{ t('homesim.nknNode.walletBackupHint') }}</small>
        </template>

        <template v-else>
          <p class="nkn-node__hint">{{ t('homesim.nknNode.walletMissing') }}</p>
          <div class="nkn-node__wallet-tabs" role="tablist">
            <button type="button" role="tab" :aria-selected="walletMode === 'create'" :class="{ active: walletMode === 'create' }" @click="walletMode = 'create'">{{ t('homesim.nknNode.walletCreate') }}</button>
            <button type="button" role="tab" :aria-selected="walletMode === 'import'" :class="{ active: walletMode === 'import' }" @click="walletMode = 'import'">{{ t('homesim.nknNode.walletImport') }}</button>
          </div>

          <div v-if="walletMode === 'create'" class="nkn-node__wallet-form">
            <label>
              <span>{{ t('homesim.nknNode.walletPasswordLabel') }}</span>
              <div class="nkn-node__password-row">
                <input v-model="newPassword" type="text" spellcheck="false" autocomplete="off" :placeholder="t('homesim.nknNode.walletPasswordPlaceholder')" :disabled="walletBusy" />
                <button class="secondary-button" type="button" :disabled="walletBusy" @click="generateStrongPassword">{{ t('homesim.nknNode.walletPasswordGenerate') }}</button>
              </div>
              <small>{{ t('homesim.nknNode.walletPasswordHintNew') }}</small>
            </label>
            <label class="nkn-node__check">
              <input v-model="passwordBackedUp" class="ui-check" type="checkbox" />
              <span>{{ t('homesim.nknNode.walletBackupCheckbox') }}</span>
            </label>
            <button class="primary-action" type="button" :disabled="walletBusy" @click="createWallet">
              <LoaderCircle v-if="walletBusy" class="spin" :size="16" />
              <KeyRound v-else :size="16" />{{ t('homesim.nknNode.walletCreate') }}
            </button>
          </div>

          <div v-else class="nkn-node__wallet-form">
            <label>
              <span>{{ t('homesim.nknNode.walletImportKeystore') }}</span>
              <textarea v-model="importKeystore" rows="5" spellcheck="false" autocomplete="off" :placeholder="t('homesim.nknNode.walletKeystorePlaceholder')" :disabled="walletBusy"></textarea>
            </label>
            <label>
              <span>{{ t('homesim.nknNode.walletImportPassword') }}</span>
              <input v-model="importPassword" type="password" autocomplete="off" :disabled="walletBusy" />
            </label>
            <button class="primary-action" type="button" :disabled="walletBusy" @click="importWallet">
              <LoaderCircle v-if="walletBusy" class="spin" :size="16" />
              <KeyRound v-else :size="16" />{{ t('homesim.nknNode.walletImport') }}
            </button>
          </div>
        </template>
      </div>

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
        <dl v-if="status.runtime.wallet_balance">
          <dt>{{ t('homesim.nknNode.walletBalance') }}</dt>
          <dd><strong>{{ status.runtime.wallet_balance }}</strong></dd>
          <small class="nkn-node__hint">{{ t('homesim.nknNode.walletBalanceHint') }}</small>
        </dl>
        <dl v-if="status.runtime.relay_count">
          <dt>{{ t('homesim.nknNode.relayCount') }}</dt>
          <dd>{{ status.runtime.relay_count.toLocaleString() }}</dd>
          <small class="nkn-node__hint">{{ t('homesim.nknNode.relayCountHint') }}</small>
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
        <summary class="ui-disclosure-summary">{{ t('homesim.nknNode.logs') }}</summary>
        <div v-if="logsLoading" class="nkn-node__loading"><LoaderCircle class="spin" :size="16" /></div>
        <pre v-else-if="logs.length" class="nkn-node__logtail">{{ logs.join('\n') }}</pre>
        <p v-else class="nkn-node__hint">{{ t('homesim.nknNode.noLogs') }}</p>
      </details>
    </template>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <p v-if="walletActionDone" class="nkn-node__saved" role="status">{{ t(`homesim.nknNode.${walletActionDone}`) }}</p>
    <p v-else-if="saved" class="nkn-node__saved" role="status">{{ t('homesim.nknNode.saved') }}</p>
    <footer class="nkn-node__footer">
      <button class="primary-action" type="button" :disabled="saving || !loaded" @click="save">
        <LoaderCircle v-if="saving" class="spin" :size="16" />
        <Save v-else :size="16" />{{ t('homesim.nknNode.save') }}
      </button>
    </footer>

    <!-- Wallet address QR overlay -->
    <Teleport to="body">
      <div v-if="qrOpen" class="nkn-node__qr-overlay" role="dialog" aria-modal="true" @click.self="qrOpen = false">
        <div class="nkn-node__qr-card">
          <h3>{{ t('homesim.nknNode.walletQr') }}</h3>
          <img v-if="qrDataUrl" :src="qrDataUrl" :alt="t('homesim.nknNode.walletQr')" />
          <code>{{ walletAddress }}</code>
          <button class="secondary-button" type="button" @click="qrOpen = false">{{ t('homesim.nknNode.walletQrClose') }}</button>
        </div>
      </div>
    </Teleport>
  </section>
</template>

<style scoped>
.nkn-node { display: grid; gap: 16px; }
.nkn-node__header h2 { font-size: 18px; }
.nkn-node__header p { margin-top: 4px; color: var(--muted); font-size: 13px; }
.nkn-node__loading { display: grid; place-items: center; padding: 30px; color: var(--muted); }
.nkn-node__warn { padding: 10px 12px; color: var(--text); font-size: 12.5px; background: var(--warning-soft, var(--surface-subtle)); border: 1px solid var(--border); border-radius: 9px; }
.nkn-node__card { display: grid; gap: 12px; padding: 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.nkn-node__card h3 { margin: 0; font-size: 14.5px; }
.nkn-node__card label { display: grid; gap: 6px; font-size: 13px; }
.nkn-node__card label > span { font-weight: 650; }
.nkn-node__card input:not([type=checkbox]):not([type=password]) { width: 100%; min-width: 0; box-sizing: border-box; padding: 9px 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; }
.nkn-node__card label input[type=password] { width: 100%; min-width: 0; box-sizing: border-box; padding: 9px 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; }
.nkn-node__card textarea { width: 100%; box-sizing: border-box; padding: 9px 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; font-family: var(--mono, monospace); font-size: 11.5px; resize: vertical; }
.nkn-node__enabled { display: inline-flex !important; flex-direction: row !important; align-items: center; gap: 8px; font-size: 13px; font-weight: 650; }
.nkn-node__check { display: inline-flex !important; flex-direction: row !important; align-items: center; gap: 8px; }
.nkn-node__hint { color: var(--muted); font-size: 11.5px; }
.nkn-node__wallet-tabs { display: flex; gap: 8px; }
.nkn-node__wallet-tabs button { flex: 1; padding: 8px 10px; color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; font-size: 13px; cursor: pointer; }
.nkn-node__wallet-tabs button.active { color: var(--text); font-weight: 650; border-color: var(--accent, var(--border)); }
.nkn-node__wallet-form { display: grid; gap: 12px; }
.nkn-node__password-row { display: flex; gap: 8px; }
.nkn-node__password-row input { flex: 1; }
.nkn-node__wallet-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.nkn-node__danger.armed { color: var(--danger-fg, #fff); background: var(--danger, #b33); border-color: var(--danger, #b33); }
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
.nkn-node__qr-overlay { position: fixed; inset: 0; z-index: 60; display: grid; place-items: center; background: rgb(0 0 0 / 45%); }
.nkn-node__qr-card { display: grid; gap: 12px; justify-items: center; max-width: 320px; padding: 20px; background: var(--surface, #fff); border: 1px solid var(--border); border-radius: 14px; }
.nkn-node__qr-card h3 { margin: 0; font-size: 15px; }
.nkn-node__qr-card img { width: 240px; height: 240px; }
.nkn-node__qr-card code { max-width: 100%; overflow-wrap: anywhere; color: var(--text); font-size: 11.5px; text-align: center; }
.spin { animation: nkn-node-spin 1s linear infinite; }
@keyframes nkn-node-spin { to { transform: rotate(360deg); } }
</style>
