<script setup lang="ts">
import { ref } from 'vue'
import { Check, Copy } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ code: string }>()
const { t } = useI18n()
const copied = ref('')
const failed = ref(false)
const busy = ref(false)
async function copy(): Promise<void> {
  const code = props.code
  failed.value = false
  copied.value = ''
  busy.value = true
  try {
    if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
    await navigator.clipboard.writeText(code)
    if (props.code === code) copied.value = code
  } catch {
    failed.value = true
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <span class="sms-code-action">
    <button type="button" class="sms-code-copy" :disabled="busy" :aria-label="t('homesim.codeTitle')" @click.stop="copy">
      <Check v-if="copied === code" :size="16" aria-hidden="true" /><Copy v-else :size="16" aria-hidden="true" />
      {{ t(copied === code ? 'homesim.copied' : 'homesim.copy') }}
    </button>
    <span v-if="failed" role="alert" class="sms-code-error">{{ t('homesim.copyFailed') }}</span>
  </span>
</template>

<style scoped>
.sms-code-action { display: inline-flex; align-items: center; flex-wrap: wrap; gap: 6px; }
.sms-code-copy { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 44px; padding: 8px 12px; border: 1px solid var(--accent-border); border-radius: 11px; background: var(--accent-soft); color: var(--accent-strong); font-size: .875rem; white-space: nowrap; }
.sms-code-error { color: var(--danger); font-size: .875rem; line-height: 1.5; }
</style>
