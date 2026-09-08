<script setup lang="ts">
import { useAdvancedI18n as useI18n } from '../i18n/useAdvancedI18n'
const { t } = useI18n()
import { onBeforeUnmount, ref, computed } from 'vue'
import QRCode from 'qrcode'
import { Smartphone, QrCode, ShieldCheck } from '@lucide/vue'
import { createMobileEnrollment } from '../api/client'

const serverURL = ref(window.location.origin)
const image = ref('')
const expires = ref(0)
const now = ref(Date.now())
const busy = ref(false)
const error = ref('')
let alive = true
const timer = setInterval(() => { now.value = Date.now(); if (expires.value && now.value >= expires.value) image.value = '' }, 1000)
const remaining = computed(() => Math.max(0, Math.ceil((expires.value - now.value) / 1000)))
async function generate() {
  if (busy.value) return
  busy.value = true; error.value = ''; image.value = ''
  try {
    const result = await createMobileEnrollment(serverURL.value)
    const qr = await QRCode.toDataURL(JSON.stringify(result.payload), { width: 320, margin: 2, errorCorrectionLevel: 'M' })
    if (alive) { image.value = qr; expires.value = Date.parse(result.payload.expires_at); now.value = Date.now() }
  } catch (reason) { if (alive) error.value = reason instanceof Error ? reason.message : t('homesim.advanced.text39') }
  finally { if (alive) busy.value = false }
}
onBeforeUnmount(() => { alive = false; clearInterval(timer); image.value = '' })
</script>
<template>
  <section class="mobile-enrollment">
    <header><span class="mobile-enrollment__icon"><Smartphone :size="28" /></span><div><h2>{{ t('homesim.advanced.text45') }}</h2><p>{{ t('homesim.advanced.text46') }}</p></div></header>
    <div class="mobile-enrollment__layout">
      <div class="mobile-enrollment__form"><h3>{{ t('homesim.advanced.text47') }}</h3><p>{{ t('homesim.advanced.text48') }}</p><label>{{ t('homesim.advanced.text49') }}<input v-model="serverURL" type="url" placeholder="https://192.168.120.190:7577" :disabled="busy" /></label><h3>{{ t('homesim.advanced.text50') }}</h3><p>{{ t('homesim.advanced.text51') }}</p><button class="primary-button" type="button" :disabled="busy" @click="generate"><QrCode :size="16" />{{ busy ? t('homesim.advanced.text40') : t('homesim.advanced.text41') }}</button><p v-if="error" class="field-error" role="alert">{{ error }}</p></div>
      <div class="mobile-enrollment__qr"><img v-if="image" :src="image" :alt="t('homesim.advanced.text42')" /><QrCode v-else :size="86" /><p>{{ image ? t('homesim.advanced.remaining', {seconds: remaining}) : expires ? t('homesim.advanced.text43') : t('homesim.advanced.text44') }}</p></div>
    </div>
    <footer><ShieldCheck :size="18" /><span>{{ t('homesim.advanced.text52') }}</span></footer>
  </section>
</template>
<style scoped>
.mobile-enrollment { display: grid; gap: 24px; padding: 24px; border: 1px solid var(--border); border-radius: 20px; background: var(--surface); }
header { display: flex; align-items: center; gap: 16px; } .mobile-enrollment__icon { display: grid; place-items: center; width: 56px; height: 56px; background: color-mix(in srgb,var(--accent) 12%,transparent); border-radius: 16px; color: var(--accent); }
h2,h3,p { margin: 0; } h2 { font-size: 22px; } h3 { font-size: 15px; } p,footer { color: var(--muted); line-height: 1.8; font-size: 13px; }
.mobile-enrollment__layout { display: grid; grid-template-columns: 1fr 270px; gap: 24px; }
.mobile-enrollment__form { display: grid; gap: 14px; } label { display: grid; gap: 8px; } input { width: 100%; padding: 12px; border-radius: 10px; border: 1px solid var(--border); background: var(--surface-subtle); color: var(--text); }
button { display: inline-flex; align-items: center; gap: 8px; justify-self: start; }
.mobile-enrollment__qr { display: grid; place-items: center; align-content: center; gap: 14px; background: var(--surface-subtle); padding: 16px; border-radius: 16px; color: var(--muted); min-height: 270px; }
img { width: 100%; max-width: 250px; border-radius: 10px; } footer { display: flex; gap: 10px; padding-top: 16px; border-top: 1px solid var(--border); } footer svg { flex-shrink: 0; margin-top: 4px; }
@media(max-width: 860px) { .mobile-enrollment__layout { grid-template-columns: 1fr; } }
</style>
