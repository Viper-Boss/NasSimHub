<script setup lang="ts">
import { useAdvancedI18n as useI18n } from '../../i18n/useAdvancedI18n'
const { t } = useI18n()
import { computed, onMounted, ref } from 'vue'
import SensitiveValue from '../SensitiveValue.vue'
import { ShieldCheck, Plus, Trash2 } from '@lucide/vue'
import { getWireGuardSettings, saveWireGuardSettings, type WireGuardResponse } from '../../api/client'
const data = ref<WireGuardResponse | null>(null)
const busy = ref(false)
const error = ref('')
function normalizeWG(value: WireGuardResponse) { value.settings.mtproto ||= {enabled:false,bind_address:'',port:3128,secret:''}; if (!value.settings.mtproto.port) value.settings.mtproto.port = 3128; return value }
async function load() { error.value = ''; try { data.value = normalizeWG(await getWireGuardSettings()) } catch (e) { error.value = e instanceof Error ? e.message : t('homesim.advanced.text54') } }
async function save() { if (!data.value || busy.value) return; busy.value = true; error.value = ''; try { data.value = await saveWireGuardSettings(data.value.settings) } catch (e) { error.value = e instanceof Error ? e.message : t('homesim.advanced.text55') } finally { busy.value = false } }
// These names come from the agent, which reports what the interface is
// actually doing. "up" is the only one that means traffic can flow.
const labels = computed<Record<string,string>>(() => ({
  up: t('homesim.advanced.wgStateUp'),
  down: t('homesim.advanced.wgStateDown'),
  disabled: t('homesim.advanced.wgStateDisabled'),
  unsupported: t('homesim.advanced.wgStateUnsupported'),
  tools_missing: t('homesim.advanced.wgStateToolsMissing'),
  error: t('homesim.advanced.wgStateError'),
  unavailable: t('homesim.advanced.wgStateUnavailable'),
}))
function handshakeText(peer: { last_handshake_unix?: number; current?: boolean }): string {
  if (!peer.last_handshake_unix) return t('homesim.advanced.wgPeerNever')
  if (!peer.current) return t('homesim.advanced.wgPeerStale')
  const seconds = Math.max(0, Math.round(Date.now() / 1000 - peer.last_handshake_unix))
  return t('homesim.advanced.wgPeerAgo', { seconds })
}
function bytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`
}
const mtgLink = computed(() => { const s=data.value?.settings.mtproto; return s?.secret ? 'tg://proxy?' + new URLSearchParams({server:s.bind_address, port:String(s.port), secret:s.secret}).toString() : '' })
async function copyMTG() { try { await navigator.clipboard.writeText(mtgLink.value) } catch { error.value=t('homesim.advanced.text14') } }
onMounted(load)
</script>
<template>
  <section class="wg-panel"><header><ShieldCheck :size="28" /><div><h2>{{ t('homesim.advanced.text66') }}</h2><p>{{ t('homesim.advanced.text67') }}</p></div></header>
    <template v-if="data"><div class="homesim-sim-notice"><strong>{{ labels[data.status.state] || t('homesim.advanced.text60') }}</strong><p>{{ t('homesim.advanced.revisions', {saved: data.settings.revision, applied: data.status.revision}) }}</p><p v-if="data.status.error">{{ data.status.error }}</p><p v-if="data.status.public_key">{{ t('homesim.advanced.text68') }}<code>{{ data.status.public_key }}</code></p></div>
    <section v-if="data.status.peers?.length" class="wg-peers-live">
      <h3>{{ t('homesim.advanced.wgPeersTitle') }}</h3>
      <div v-for="peer in data.status.peers" :key="peer.public_key" class="wg-live-row" :class="{ 'is-current': peer.current }">
        <span class="wg-live-dot" aria-hidden="true" />
        <span class="wg-live-body">
          <strong>{{ peer.name || peer.public_key }}</strong>
          <small>{{ handshakeText(peer) }}</small>
          <small v-if="peer.endpoint">{{ peer.endpoint }}</small>
        </span>
        <span class="wg-live-transfer">{{ t('homesim.advanced.wgPeerTransfer', { rx: bytes(peer.rx_bytes), tx: bytes(peer.tx_bytes) }) }}</span>
      </div>
    </section>
    <div class="wg-grid"><label class="wg-switch"><input v-model="data.settings.enabled" class="ui-check" type="checkbox" />{{ t('homesim.advanced.text69') }}</label><label>{{ t('homesim.advanced.text70') }}<input v-model="data.settings.address" placeholder="10.77.0.1/24" /></label><label>{{ t('homesim.advanced.text71') }}<input v-model.number="data.settings.listen_port" type="number" min="1024" max="65535" /></label><label>{{ t('homesim.advanced.text72') }}<input v-model="data.settings.advertised_endpoint" :placeholder="t('homesim.advanced.text61')" /></label></div>
    <p>{{ t('homesim.advanced.text73') }}</p>
    <h3>{{ t('homesim.advanced.text74') }}</h3><article v-for="(peer,index) in data.settings.peers" :key="index" class="wg-peer"><div class="wg-grid"><label>{{ t('homesim.advanced.text75') }}<input v-model="peer.name" /></label><label>{{ t('homesim.advanced.text76') }}<input v-model="peer.public_key" spellcheck="false" /></label><label>{{ t('homesim.advanced.text77') }}<input v-model="peer.endpoint" :placeholder="t('homesim.advanced.text62')" /></label><label>{{ t('homesim.advanced.text78') }}<input v-model="peer.nkn_address" :placeholder="t('homesim.advanced.text63')" /></label><label>{{ t('homesim.advanced.text79') }}<input :value="peer.allowed_ips.join(', ')" @input="peer.allowed_ips = ($event.target as HTMLInputElement).value.split(',').map(v => v.trim()).filter(Boolean)" placeholder="10.77.0.2/32" /></label></div><button type="button" class="secondary-button" @click="data.settings.peers.splice(index,1)"><Trash2 :size="15" />{{ t('homesim.advanced.text80') }}</button></article>
    <button type="button" class="secondary-button" :disabled="data.settings.peers.length >= 64" @click="data.settings.peers.push({ name:'',public_key:'',endpoint:'',allowed_ips:[],nkn_address:'' })"><Plus :size="16" />{{ t('homesim.advanced.text81') }}</button>
    <div class="wg-actions"><button class="primary-button" type="button" :disabled="busy" @click="save">{{ busy ? t('homesim.advanced.text64') : t('homesim.advanced.text65') }}</button><button class="secondary-button" type="button" :disabled="busy" @click="load">{{ t('homesim.advanced.text26') }}</button></div></template>
    <article class="homesim-sim-notice"><h3>{{ t('homesim.advanced.text82') }}</h3><p>{{ t('homesim.advanced.text83') }}</p><RouterLink :to="{name:'traffic'}">{{ t('homesim.advanced.text84') }}</RouterLink></article>
    <article v-if="data?.settings.mtproto" class="wg-peer">
      <h3>{{ t('homesim.advanced.mtgTitle') }}</h3><p>{{ t('homesim.advanced.mtgHint') }}</p>
      <div class="wg-grid"><label class="wg-switch"><input v-model="data.settings.mtproto.enabled" class="ui-check" type="checkbox" />{{ t('homesim.advanced.mtgEnable') }}</label>
      <label>{{ t('homesim.advanced.mtgBind') }}<input v-model="data.settings.mtproto.bind_address" placeholder="192.168.120.190" /></label>
      <label>{{ t('homesim.advanced.mtgPort') }}<input v-model.number="data.settings.mtproto.port" type="number" min="1024" max="65535" /></label></div>
      <p class="field-error">{{ t('homesim.advanced.wgMtgNotImplemented') }}</p>
      <SensitiveValue v-if="data.settings.mtproto.secret" :value="data.settings.mtproto.secret" :label="t('homesim.advanced.mtgSecret')" />
      <div class="wg-actions"><button type="button" class="primary-button" :disabled="busy" @click="save">{{ t('homesim.advanced.text65') }}</button><button v-if="mtgLink" type="button" class="secondary-button" @click="copyMTG">{{ t('homesim.advanced.mtgCopy') }}</button></div>
    </article>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>
</template>
<style scoped>
.wg-panel { display:grid; gap:18px; } header {display:flex;gap:16px;align-items:center;} h2,h3,p{margin:0;} p {color:var(--muted);line-height:1.8;font-size:13px;} .wg-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;} label{display:grid;gap:8px;font-size:13px;} input{min-width:0;width:100%;padding:11px;border:1px solid var(--border);border-radius: var(--radius-control);background:var(--surface);color:var(--text);} .wg-switch{display:flex;align-items:center;gap:10px;} .wg-switch input{width:auto;} .wg-peer{padding:20px;border:1px solid var(--border);border-radius: var(--radius-surface);display:grid;gap:16px;} button{justify-self:start;display:inline-flex;gap:8px;align-items:center;} .wg-actions{display:flex;gap:12px;} .wg-peers-live{display:grid;gap:10px;} .wg-live-row{display:flex;gap:12px;align-items:center;padding:12px 14px;background:var(--surface-subtle);border-radius:var(--radius-md);} .wg-live-dot{width:8px;height:8px;flex:none;border-radius:50%;background:var(--muted);} .wg-live-row.is-current .wg-live-dot{background:var(--success);} .wg-live-body{display:grid;gap:2px;flex:1 1 auto;min-width:0;} .wg-live-body strong{font-size:13px;overflow-wrap:anywhere;} .wg-live-body small{color:var(--muted);font-size:11.5px;} .wg-live-transfer{color:var(--faint);font-size:11.5px;white-space:nowrap;font-variant-numeric:tabular-nums;} code{overflow-wrap:anywhere;} @media(max-width:860px){.wg-grid{grid-template-columns:1fr;}}
</style>
