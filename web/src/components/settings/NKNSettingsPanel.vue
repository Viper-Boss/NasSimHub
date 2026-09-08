<script setup lang="ts">
import { useAdvancedI18n as useI18n } from '../../i18n/useAdvancedI18n'
const { t } = useI18n()
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { Globe2, ShieldCheck, ExternalLink, Info, RefreshCw, Copy } from '@lucide/vue'
import { fixtureMode, getNKNSettings, saveNKNSettings, type NKNSettingsResponse } from '../../api/client'
import OverlayDialog from '../OverlayDialog.vue'

const data = ref<NKNSettingsResponse | null>(null)
const error = ref('')
const busy = ref(false)
const intro = ref(false)
const copied = ref(false)
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
const stateLabel = computed(() => ({ connected: t('homesim.advanced.text8'), connecting: t('homesim.advanced.text9'), disabled: t('homesim.advanced.text10'), error: t('homesim.advanced.text11') }[data.value?.status.state || 'connecting']))
async function load() {
  try { const result = await getNKNSettings(); if (!disposed) data.value = result }
  catch (reason) { if (!disposed) error.value = reason instanceof Error ? reason.message : t('homesim.advanced.text12') }
}
async function toggle() {
  if (!data.value || busy.value) return
  busy.value = true; error.value = ''
  try { data.value = await saveNKNSettings({ ...data.value.settings, enabled: !data.value.settings.enabled }) }
  catch (reason) { error.value = reason instanceof Error ? reason.message : t('homesim.advanced.text13') }
  finally { busy.value = false }
}
function dismiss() {
  intro.value = false
  try { localStorage.setItem('homesim.nkn.intro.v1', 'seen') } catch { /* Storage may be unavailable. */ }
}
async function copy() {
  try { await navigator.clipboard.writeText(data.value?.status.address || ''); copied.value = true }
  catch { error.value = t('homesim.advanced.text14') }
}
onMounted(() => {
  void load()
  try { intro.value = localStorage.getItem('homesim.nkn.intro.v1') !== 'seen' } catch { intro.value = true }
  timer = setInterval(() => { if (!busy.value) void load() }, 10000)
})
onBeforeUnmount(() => { disposed = true; clearInterval(timer) })
</script>

<template>
  <section class="nkn-panel">
    <header class="nkn-hero">
      <span class="nkn-hero__icon"><Globe2 :size="28" /></span>
      <div><p class="nkn-eyebrow">{{ t('homesim.advanced.text19') }}</p><h2>{{ t('homesim.advanced.text1') }}</h2><p>{{ t('homesim.advanced.text20') }}</p></div>
      <button class="secondary-button" type="button" @click="intro = true"><Info :size="16" />{{ t('homesim.advanced.text21') }}</button>
    </header>
    <p v-if="fixtureMode" class="homesim-sim-notice">{{ t('homesim.advanced.text22') }}</p>
    <article class="nkn-card" v-if="data">
      <div class="nkn-row"><div><h3>{{ t('homesim.advanced.text15') }}</h3><p>{{ t('homesim.advanced.text23') }}</p></div>
        <input type="checkbox" class="ui-switch" role="switch" :checked="data.settings.enabled" :aria-label="t('homesim.advanced.text15')" :disabled="busy || fixtureMode" @change="toggle" />
      </div>
      <div class="nkn-status" :class="{ 'is-connected': data.status.state === 'connected' }"><span />{{ stateLabel }}</div>
      <p v-if="data.status.error" role="status">{{ data.status.error }}</p>
      <template v-if="data.status.address"><label>{{ t('homesim.advanced.text24') }}</label><div class="nkn-address"><code>{{ data.status.address }}</code><button type="button" class="icon-button" :aria-label="copied ? t('homesim.advanced.text16') : t('homesim.advanced.text17')" @click="copy"><Copy :size="16" /></button></div><small v-if="copied">{{ t('homesim.advanced.text25') }}</small></template>
      <button class="secondary-button" type="button" @click="load"><RefreshCw :size="15" />{{ t('homesim.advanced.text26') }}</button>
    </article>
    <div class="nkn-grid">
      <article class="nkn-card"><ShieldCheck :size="22" /><h3>{{ t('homesim.advanced.text27') }}</h3><p>{{ t('homesim.advanced.text28') }}</p></article>
      <article class="nkn-card"><Globe2 :size="22" /><h3>{{ t('homesim.advanced.text29') }}</h3><p>{{ t('homesim.advanced.text30') }}</p><RouterLink :to="{ name: 'settings', params: { section: 'sms-forward' } }">{{ t('homesim.advanced.text31') }}</RouterLink></article>
    </div>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <OverlayDialog :open="intro" :label="t('homesim.advanced.text18')" @close="dismiss">
      <div class="nkn-intro"><Globe2 :size="32" /><h2>{{ t('homesim.advanced.text32') }}</h2><p>{{ t('homesim.advanced.text33') }}</p><p>{{ t('homesim.advanced.text34') }}</p><p>{{ t('homesim.advanced.text35') }}</p><p>{{ t('homesim.advanced.text36') }}</p><div class="nkn-row"><a href="https://nkn.org/" target="_blank" rel="noopener noreferrer">{{ t('homesim.advanced.text37') }} <ExternalLink :size="14" /></a><button class="primary-button" type="button" @click="dismiss">{{ t('homesim.advanced.text38') }}</button></div></div>
    </OverlayDialog>
  </section>
</template>

<style scoped>
.nkn-panel { display: grid; gap: 20px; }
.nkn-hero { display: flex; gap: 18px; align-items: center; padding: 24px; border: 1px solid var(--border); border-radius: 20px; background: linear-gradient(125deg, color-mix(in srgb,var(--accent) 13%,var(--surface)),var(--surface)); }
.nkn-hero__icon { display: grid; place-items: center; width: 58px; height: 58px; border-radius: 18px; color: var(--accent); background: color-mix(in srgb,var(--accent) 13%,transparent); flex-shrink: 0; }
.nkn-hero > button { margin-left: auto; flex-shrink: 0; }
.nkn-eyebrow { color: var(--accent); font-size: 12px; }
h2,h3,p { margin: 0; } h2 { font-size: 24px; } h3 { font-size: 16px; }
p { color: var(--muted); line-height: 1.8; }
.nkn-card { padding: 22px; border: 1px solid var(--border); border-radius: 18px; background: var(--surface); display: grid; gap: 14px; }
.nkn-card > button { justify-self: start; }
.nkn-row { display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.nkn-row .ui-switch { flex-shrink: 0; }
.nkn-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.nkn-status { display: flex; gap: 8px; align-items: center; color: var(--muted); }
.nkn-status > span { width: 8px; height: 8px; border-radius: 50%; background: currentColor; }
.nkn-status.is-connected { color: var(--success); }
.nkn-address { display: flex; align-items: center; gap: 12px; padding: 12px; border-radius: 10px; background: var(--surface-subtle); }
.nkn-address code { overflow-wrap: anywhere; flex: 1; }
.nkn-intro { padding: 26px; display: grid; gap: 18px; }
a { color: var(--accent); } .secondary-button { display: inline-flex; align-items: center; gap: 8px; }
@media(max-width: 860px) { .nkn-grid { grid-template-columns: 1fr; } .nkn-hero { flex-wrap: wrap; padding: 18px; } .nkn-hero > button { margin-left: 0; } }
</style>
