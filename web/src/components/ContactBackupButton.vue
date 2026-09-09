<script setup lang="ts">
import { ref } from 'vue'
import { CloudUpload, LoaderCircle } from '@lucide/vue'
import { backupContactsToNAS } from '../api/client'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
const busy = ref(false)
const error = ref('')
const receipt = ref('')
const storagePath = ref('')
async function backup(): Promise<void> {
  if (busy.value) return
  busy.value = true; error.value = ''; receipt.value = ''
  try {
    const result = await backupContactsToNAS()
    receipt.value = `${t('homesim.backupSaved', { count: result.count })} · ${result.filename}`
    storagePath.value = result.directory
  } catch (reason) { error.value = reason instanceof Error ? reason.message : t('homesim.backupFailed') }
  finally { busy.value = false }
}
</script>
<template>
  <section class="homesim-contact-backup">
    <div><strong>{{ t('homesim.nasBackup') }}</strong><small>{{ t('homesim.backupHint') }}</small></div>
    <button class="secondary-button" type="button" :disabled="busy" @click="backup"><LoaderCircle v-if="busy" class="spin" :size="16" /><CloudUpload v-else :size="16" />{{ t(busy ? 'homesim.backupBusy' : 'homesim.backupNow') }}</button>
    <p v-if="receipt" role="status">{{ receipt }}</p>
    <p v-if="storagePath" role="status">{{ t('homesim.backupStorage', { directory: storagePath }) }}</p>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>
</template>
<style scoped>
.homesim-contact-backup { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; justify-content: space-between; padding: 14px 18px; margin: 4px 12px 12px; background: var(--accent-soft); border: 1px solid var(--border); border-radius: var(--radius-surface); }
.homesim-contact-backup > div { display: grid; gap: 4px; }
.homesim-contact-backup strong { font-size: 14px; }
.homesim-contact-backup small, .homesim-contact-backup p { font-size: 12px; color: var(--muted); }
.homesim-contact-backup p { flex-basis: 100%; overflow-wrap: anywhere; }
.homesim-contact-backup .field-error { color: var(--danger); }
</style>
