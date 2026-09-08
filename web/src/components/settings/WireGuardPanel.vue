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
const labels = computed<Record<string,string>>(() => ({ not_installed: t('homesim.advanced.text56'), disabled: t('homesim.advanced.text10'), running: t('homesim.advanced.text57'), error: t('homesim.advanced.text58'), offline: t('homesim.advanced.text59') }))
const mtgLink = computed(() => { const s=data.value?.settings.mtproto; return s?.secret ? 'tg://proxy?' + new URLSearchParams({server:s.bind_address, port:String(s.port), secret:s.secret}).toString() : '' })
async function copyMTG() { try { await navigator.clipboard.writeText(mtgLink.value) } catch { error.value=t('homesim.advanced.text14') } }
onMounted(load)
</script>
<template>
  <section class="wg-panel"><header><ShieldCheck :size="28" /><div><h2>{{ t('homesim.advanced.text66') }}</h2><p>{{ t('homesim.advanced.text67') }}</p></div></header>
    <template v-if="data"><div class="homesim-sim-notice"><strong>{{ labels[data.status.state] || t('homesim.advanced.text60') }}</strong><p>{{ t('homesim.advanced.revisions', {saved: data.settings.revision, applied: data.status.revision}) }}</p><p v-if="data.status.error">{{ data.status.error }}</p><p v-if="data.status.public_key">{{ t('homesim.advanced.text68') }}<code>{{ data.status.public_key }}</code></p></div>
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
      <p>{{ labels[data.status.mtproto_state || 'not_installed'] || t('homesim.advanced.text60') }}</p>
      <SensitiveValue v-if="data.settings.mtproto.secret" :value="data.settings.mtproto.secret" :label="t('homesim.advanced.mtgSecret')" />
      <div class="wg-actions"><button type="button" class="primary-button" :disabled="busy" @click="save">{{ t('homesim.advanced.text65') }}</button><button v-if="mtgLink" type="button" class="secondary-button" @click="copyMTG">{{ t('homesim.advanced.mtgCopy') }}</button></div>
    </article>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>
</template>
<style scoped>
.wg-panel { display:grid; gap:18px; } header {display:flex;gap:16px;align-items:center;} h2,h3,p{margin:0;} p {color:var(--muted);line-height:1.8;font-size:13px;} .wg-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;} label{display:grid;gap:8px;font-size:13px;} input{min-width:0;width:100%;padding:11px;border:1px solid var(--border);border-radius:10px;background:var(--surface);color:var(--text);} .wg-switch{display:flex;align-items:center;gap:10px;} .wg-switch input{width:auto;} .wg-peer{padding:20px;border:1px solid var(--border);border-radius:16px;display:grid;gap:16px;} button{justify-self:start;display:inline-flex;gap:8px;align-items:center;} .wg-actions{display:flex;gap:12px;} code{overflow-wrap:anywhere;} @media(max-width:860px){.wg-grid{grid-template-columns:1fr;}}
</style>
