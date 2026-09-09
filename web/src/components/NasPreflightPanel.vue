<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronDown, FileCheck2, Upload, X } from '@lucide/vue'
import { MAX_PREFLIGHT_BYTES, NasPreflightError, parseNasPreflight, preflightSummary } from '../utils/nasPreflight'
import type { PreflightReport } from '../utils/nasPreflight'

const { t } = useI18n()
const report = ref<PreflightReport | null>(null)
const error = ref('')
const reading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
let generation = 0
const summary = computed(() => t(report.value ? preflightSummary(report.value) : 'nasPreflight.summary.empty'))
const untestedCount = computed(() => report.value?.checks.filter(check => check.status === 'not_tested').length ?? 0)
const developmentPreview = import.meta.env.DEV && import.meta.env.VITE_MODEMDECK_FIXTURE === '1'

function clear(): void {
  generation += 1
  report.value = null
  error.value = ''
  reading.value = false
  if (fileInput.value) fileInput.value.value = ''
}

async function importReport(event: Event): Promise<void> {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  const current = ++generation
  report.value = null
  error.value = ''
  reading.value = true
  try {
    if (file.size > MAX_PREFLIGHT_BYTES) throw new NasPreflightError('size')
    const parsed = parseNasPreflight(await file.text())
    if (current === generation) report.value = parsed
  } catch (cause) {
    if (current === generation) error.value = cause instanceof NasPreflightError ? cause.message : 'nasPreflight.errors.read'
  } finally {
    if (current === generation) {
      reading.value = false
      if (fileInput.value) fileInput.value.value = ''
    }
  }
}
</script>

<template>
  <section class="nas-preflight" aria-labelledby="nas-preflight-title">
    <header class="nas-preflight__header">
      <div>
        <h3 id="nas-preflight-title"><FileCheck2 :size="22" aria-hidden="true" />{{ t('nasPreflight.title') }}</h3>
        <p>{{ t('nasPreflight.description') }}</p>
      </div>
      <div class="nas-preflight__actions">
        <input ref="fileInput" class="nas-preflight__file" type="file" accept="application/json,.json" :aria-label="t('nasPreflight.choose')" @change="importReport" />
        <button type="button" class="button" :disabled="reading" @click="fileInput?.click()"><Upload :size="16" aria-hidden="true" />{{ t(reading ? 'nasPreflight.reading' : 'nasPreflight.import') }}</button>
        <button v-if="report || error" type="button" class="icon-button" :aria-label="t('nasPreflight.clear')" @click="clear"><X :size="18" /></button>
      </div>
    </header>
    <p v-if="developmentPreview" class="nas-preflight__notice">{{ t('nasPreflight.preview') }}</p>
    <p class="nas-preflight__privacy">{{ t('nasPreflight.privacy') }}</p>
    <p v-if="error" role="alert" class="nas-preflight__error">{{ t(error) }}</p>
    <div aria-live="polite" class="nas-preflight__summary">
      <strong>{{ summary }}</strong>
      <span v-if="report">{{ report.host.system }} · {{ report.host.architecture }} · {{ t('nasPreflight.untested', { count: untestedCount }) }}</span>
    </div>
    <template v-if="report">
      <p class="nas-preflight__metadata">{{ t('nasPreflight.metadata', { time: new Date(report.generated_at).toLocaleString() }) }}</p>
      <ul v-if="report.devices.length" class="nas-preflight__devices" :aria-label="t('nasPreflight.devices')">
        <li v-for="device in report.devices" :key="device.label">
          <strong>{{ device.label }} · {{ device.usb_id }}</strong>
          <span>{{ device.family }}</span>
          <small>{{ t('nasPreflight.drivers', { drivers: device.drivers.join(', ') || t('nasPreflight.absent') }) }}</small>
        </li>
      </ul>
      <ul class="nas-preflight__checks" :aria-label="t('nasPreflight.checks')">
        <li v-for="check in report.checks" :key="check.id">
          <div><strong>{{ check.title }}</strong><p>{{ check.detail }}</p></div>
          <span class="nas-preflight__badge" :class="`nas-preflight__badge--${check.status}`">{{ t(`nasPreflight.status.${check.status}`) }}</span>
        </li>
      </ul>
    </template>
    <details class="nas-preflight__help" :open="!report">
      <summary class="ui-disclosure-summary"><ChevronDown :size="16" aria-hidden="true" />{{ t('nasPreflight.help') }}</summary>
      <p>{{ t('nasPreflight.commandHelp') }}</p>
      <pre><code>python3 scripts/fnos_preflight.py &gt; preflight.local.json</code></pre>
      <p>{{ t('nasPreflight.storageHelp') }}</p>
      <pre><code>--data-dir /your/dedicated/data --recordings-dir /your/dedicated/recordings</code></pre>
    </details>
  </section>
</template>

<style scoped>
.nas-preflight { margin-bottom: 1.5rem; padding: 1.25rem; border: 1px solid var(--border); border-radius: var(--radius-surface); background: var(--surface); color: var(--text); font-size: 1rem; line-height: 1.6; }
.nas-preflight__header, .nas-preflight__actions { display: flex; gap: .75rem; align-items: center; justify-content: space-between; flex-wrap: wrap; }
.nas-preflight h3 { display: flex; align-items: center; gap: .5rem; margin: 0; font-size: 1.125rem; }
.nas-preflight p { margin: .4rem 0; }
.nas-preflight__header p, .nas-preflight__privacy, .nas-preflight__metadata { color: var(--muted); font-size: .875rem; }
.nas-preflight__file { display: none; }
.nas-preflight__actions .button { min-height: 44px; display: inline-flex; gap: .5rem; align-items: center; padding: .5rem .8rem; border: 1px solid var(--border-strong); border-radius: var(--radius-control); background: var(--surface); color: var(--text); font: inherit; cursor: pointer; }
.nas-preflight__actions .button:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.nas-preflight__summary { display: grid; gap: .25rem; margin-top: 1rem; padding: .8rem; background: var(--surface-subtle); border-left: 3px solid var(--accent); }
.nas-preflight__summary span { font-size: .875rem; color: var(--muted); }
.nas-preflight__notice { padding: .75rem; border-radius: var(--radius-control); background: var(--warning-soft); color: var(--warning-strong); }
.nas-preflight__error { color: var(--danger); }
.nas-preflight__checks, .nas-preflight__devices { list-style: none; padding: 0; margin: 1rem 0; }
.nas-preflight__checks li { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; padding: .85rem 0; border-bottom: 1px solid var(--border); }
.nas-preflight__checks p { color: var(--muted); font-size: .875rem; overflow-wrap: anywhere; }
.nas-preflight__badge { flex-shrink: 0; white-space: nowrap; padding: .1rem .5rem; border-radius: var(--radius-sm); background: var(--surface-hover); color: var(--muted-strong); font-size: .875rem; }
.nas-preflight__badge--pass { color: var(--success); background: var(--success-soft); }
.nas-preflight__badge--warn { color: var(--warning-strong); background: var(--warning-soft); }
.nas-preflight__badge--fail { color: var(--danger); background: var(--danger-soft); }
.nas-preflight__devices { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr)); gap: .75rem; }
.nas-preflight__devices li { display: grid; border: 1px solid var(--border); padding: .75rem; border-radius: var(--radius-control); overflow-wrap: anywhere; }
.nas-preflight__devices small { font-size: .875rem; color: var(--muted); }
.nas-preflight__help { margin-top: 1rem; }
.nas-preflight__help summary { cursor: pointer; min-height: 44px; }
.nas-preflight__help pre { padding: .75rem; background: var(--surface-subtle); white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 560px) { .nas-preflight { padding: .85rem; } .nas-preflight__checks li { flex-direction: column; gap: .25rem; } }
</style>
