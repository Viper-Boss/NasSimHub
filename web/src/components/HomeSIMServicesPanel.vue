<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SelectControl from './SelectControl.vue'
const { t } = useI18n()
import { Bot, ShieldCheck, RadioTower, Save, LoaderCircle } from '@lucide/vue'
import { getHomeSIMIntegrationDraft, saveHomeSIMIntegrationDraft } from '../api/client'
import { emptyIntegrationDraft, integrationDraftProblem } from '../api/homesim'
const draft = ref(emptyIntegrationDraft())
const busy = ref(false)
const loaded = ref(false)
const error = ref('')
const saved = ref(false)
const tab = ref('spam')
const tabs = computed(() => [{ id: 'spam', title: t('homesim.services.spam'), icon: ShieldCheck }, { id: 'ai', title: t('homesim.services.ai'), icon: Bot }, { id: 'identity', title: t('homesim.services.identity'), icon: RadioTower }])
const blacklist = computed({ get: () => draft.value.spam.blacklist.join('\n'), set: value => { draft.value.spam.blacklist = [...new Set(value.split(/\r?\n/).map(s => s.trim()).filter(Boolean))] } })
const whitelist = computed({ get: () => draft.value.spam.whitelist.join('\n'), set: value => { draft.value.spam.whitelist = [...new Set(value.split(/\r?\n/).map(s => s.trim()).filter(Boolean))] } })
async function load(): Promise<void> {
  busy.value = true; error.value = ''; saved.value = false
  try { draft.value = await getHomeSIMIntegrationDraft(); loaded.value = true }
  catch (reason) { loaded.value = false; error.value = reason instanceof Error ? reason.message : t('homesim.services.loadFailed') }
  finally { busy.value = false }
}
async function save(): Promise<void> {
  if (busy.value || !loaded.value) return
  error.value = integrationDraftProblem(draft.value)
  if (error.value) return
  busy.value = true; saved.value = false
  try { draft.value = await saveHomeSIMIntegrationDraft(draft.value); saved.value = true }
  catch (reason) { error.value = reason instanceof Error ? reason.message : t('homesim.services.saveFailed') }
  finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <section id="homesim-services" class="homesim-services">
    <header><div><h2>{{ t('homesim.services.title') }}</h2><p>{{ t('homesim.services.subtitle') }}</p></div><span class="homesim-service-state">{{ t('homesim.services.preparing') }}</span></header>
    <p class="homesim-service-warning">{{ t('homesim.services.warning') }}</p>
    <nav :aria-label="t('homesim.services.configuration')"><button v-for="item in tabs" :key="item.id" type="button" :class="{ active: tab === item.id }" :aria-pressed="tab === item.id" @click="tab = item.id"><component :is="item.icon" :size="18" />{{ item.title }}</button></nav>
    <form @submit.prevent="save">
      <fieldset :disabled="busy || !loaded">
        <div v-if="tab === 'spam'" class="homesim-service-fields">
          <div class="homesim-service-toggle"><div><strong>{{ t('homesim.services.reject') }}</strong><small>{{ t('homesim.services.rejectHint') }}</small></div><input class="ui-check" type="checkbox" disabled :aria-label="t('homesim.services.rejectOff')" /></div>
          <label>{{ t('homesim.services.provider') }}<SelectControl v-model="draft.spam.provider" :label="t('homesim.services.provider')" :options="[{ value: 'local', label: t('homesim.services.local') }, { value: 'juhe', label: t('homesim.services.juheSpam') }]" :disabled="busy || !loaded" /></label>
          <label v-if="draft.spam.provider === 'juhe'">{{ t('homesim.services.credential') }}<input v-model="draft.spam.credential_ref" spellcheck="false" autocomplete="off" /><small>{{ t('homesim.services.credentialHint') }}</small></label>
          <label>{{ t('homesim.services.threshold') }}<input v-model.number="draft.spam.min_reports" type="number" min="1" max="10000" /><small>{{ t('homesim.services.thresholdHint') }}</small></label>
          <div class="homesim-service-columns"><label>{{ t('homesim.services.blacklist') }}<textarea v-model="blacklist" rows="4" :placeholder="t('homesim.services.blackPlaceholder')" /></label><label>{{ t('homesim.services.whitelist') }}<textarea v-model="whitelist" rows="4" :placeholder="t('homesim.services.whitePlaceholder')" /></label></div>
          <a href="https://www.juhe.cn/docs/api/id/511" target="_blank" rel="noopener noreferrer">{{ t('homesim.services.spamDocs') }}</a>
        </div>
        <div v-if="tab === 'ai'" class="homesim-service-fields">
          <div class="homesim-service-toggle"><div><strong>{{ t('homesim.services.autoAnswer') }}</strong><small>{{ t('homesim.services.answerHint') }}</small></div><input class="ui-check" type="checkbox" disabled :aria-label="t('homesim.services.answerOff')" /></div>
          <label>{{ t('homesim.services.endpoint') }}<input v-model="draft.ai.endpoint" type="url" :placeholder="t('homesim.services.endpointPlaceholder')" spellcheck="false" /><small>{{ t('homesim.services.endpointHint') }}</small></label>
          <div class="homesim-service-columns"><label>{{ t('homesim.services.model') }}<input v-model="draft.ai.model" :placeholder="t('homesim.services.modelPlaceholder')" maxlength="100" /></label><label>{{ t('homesim.services.credential') }}<input v-model="draft.ai.credential_ref" spellcheck="false" autocomplete="off" /></label></div>
          <div class="homesim-service-columns"><label>{{ t('homesim.services.delay') }}<input v-model.number="draft.ai.answer_after_seconds" type="number" min="5" max="60" /></label><label>{{ t('homesim.services.duration') }}<input v-model.number="draft.ai.max_duration_seconds" type="number" min="30" max="600" /></label></div>
          <label>{{ t('homesim.services.greeting') }}<textarea v-model="draft.ai.greeting" maxlength="500" rows="3" /><small>{{ t('homesim.services.greetingHint') }}</small></label>
          <div class="homesim-service-columns"><label class="homesim-service-check"><input v-model="draft.ai.transcript" class="ui-check" type="checkbox" />{{ t('homesim.services.transcript') }}</label><label class="homesim-service-check"><input v-model="draft.ai.summary" class="ui-check" type="checkbox" />{{ t('homesim.services.summary') }}</label></div>
          <label>{{ t('homesim.services.push') }}<SelectControl v-model="draft.ai.push_channel" :label="t('homesim.services.push')" :options="[{ value: 'none', label: t('homesim.services.noPush') }, { value: 'telegram', label: t('homesim.services.telegram') }]" :disabled="busy || !loaded" /></label>
          <ol class="homesim-ai-stages"><li>{{ t('homesim.services.audioStage') }}<small>{{ t('homesim.services.bridgePending') }}</small></li><li>{{ t('homesim.services.transcriptStage') }}<small>{{ t('homesim.services.asrPending') }}</small></li><li>{{ t('homesim.services.push') }}<small>{{ t('homesim.services.workerPending') }}</small></li></ol>
          <small>{{ t('homesim.services.privacy') }}</small>
        </div>
        <div v-if="tab === 'identity'" class="homesim-service-fields">
          <label>{{ t('homesim.services.lookup') }}<SelectControl v-model="draft.identity.provider" :label="t('homesim.services.lookup')" :options="[{ value: 'off', label: t('homesim.services.lookupOff') }, { value: 'juhe', label: t('homesim.services.juheIdentity') }]" :disabled="busy || !loaded" /></label>
          <label v-if="draft.identity.provider === 'juhe'">{{ t('homesim.services.credential') }}<input v-model="draft.identity.credential_ref" spellcheck="false" /></label>
          <p>{{ t('homesim.services.lookupHint') }}</p>
          <p>{{ t('homesim.services.signatureHint') }}</p>
          <a href="https://www.juhe.cn/docs/api/id/11" target="_blank" rel="noopener noreferrer">{{ t('homesim.services.identityDocs') }}</a>
        </div>
      </fieldset>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
      <p v-if="saved" class="homesim-draft-saved" role="status">{{ t('homesim.services.saved') }}</p>
      <footer><button class="text-button" type="button" :disabled="busy" @click="load">{{ t('homesim.services.reload') }}</button><button class="primary-action" type="submit" :disabled="busy || !loaded"><LoaderCircle v-if="busy" class="spin" :size="16" /><Save v-else :size="16" />{{ t('homesim.services.save') }}</button></footer>
    </form>
  </section>
</template>

<style scoped>
.homesim-services { padding: 24px; margin-bottom: 24px; border: 1px solid var(--border); border-radius: 20px; background: var(--surface); }
.homesim-services header, .homesim-services footer, .homesim-service-toggle { display: flex; gap: 16px; align-items: center; justify-content: space-between; }
.homesim-services h2 { font-size: 20px; }
.homesim-services header p { margin-top: 6px; color: var(--muted); font-size: 14px; }
.homesim-service-state { flex-shrink: 0; padding: 6px 10px; border-radius: 20px; background: var(--accent-soft); color: var(--accent); font-size: 12px; }
.homesim-service-warning { padding: 14px; margin: 20px 0; background: var(--surface-subtle); border-left: 3px solid var(--warning); font-size: 14px; color: var(--muted); line-height: 1.7; }
.homesim-services nav { display: flex; gap: 4px; border-bottom: 1px solid var(--border); margin-bottom: 22px; }
.homesim-services nav button { display: flex; flex: 1; align-items: center; justify-content: center; gap: 7px; padding: 12px 4px; color: var(--muted); background: transparent; font-size: 14px; border-bottom: 2px solid transparent; }
.homesim-services nav button.active { color: var(--accent); border-color: var(--accent); }
.homesim-services fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
.homesim-services fieldset:disabled { opacity: .6; }
.homesim-service-fields { display: grid; gap: 20px; font-size: 14px; line-height: 1.6; }
.homesim-service-fields label { display: grid; gap: 8px; }
.homesim-service-fields input:not([type=checkbox]), .homesim-service-fields select, .homesim-service-fields textarea { width: 100%; min-width: 0; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: 10px; color: var(--text); background: var(--surface-subtle); }
.homesim-service-fields textarea { resize: vertical; }
.homesim-service-fields small { font-size: 12px; color: var(--muted); }
.homesim-service-fields a { color: var(--accent); }
.homesim-service-columns { display: grid; grid-template-columns: repeat(2, minmax(0,1fr)); gap: 16px; }
.homesim-service-toggle { padding: 14px; background: var(--surface-subtle); border-radius: 12px; }
.homesim-service-toggle > div { display: grid; gap: 5px; }
.homesim-service-fields label.homesim-service-check { display: flex; align-items: center; gap: 9px; }
.homesim-ai-stages { display: grid; grid-template-columns: repeat(3,1fr); gap: 10px; padding: 0; list-style: none; }
.homesim-ai-stages li { display: grid; gap: 5px; padding: 14px; background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 12px; }
.homesim-services footer { margin-top: 24px; }
.homesim-draft-saved { margin-top: 16px; color: var(--success); font-size: 14px; }
@media(max-width:560px) { .homesim-services { padding: 16px; } .homesim-service-columns { grid-template-columns: 1fr; } .homesim-services header { flex-wrap: wrap; } .homesim-ai-stages { grid-template-columns: 1fr; } }
</style>
