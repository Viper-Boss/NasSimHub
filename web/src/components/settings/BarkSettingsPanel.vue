<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { BellRing, LoaderCircle, Send } from '@lucide/vue'
import {
  getHomeSIMBarkSettings,
  saveHomeSIMBarkSettings,
  sendHomeSIMBarkTestPush,
  type HomeSIMBarkSettings
} from '../../api/client'

const { t } = useI18n()
const loading = ref(true)
const saving = ref(false)
const testing = ref(false)
const loadError = ref('')
const saveError = ref('')
const saved = ref(false)
const testSent = ref(false)
const settings = ref<HomeSIMBarkSettings | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    settings.value = await getHomeSIMBarkSettings()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!settings.value || saving.value) return
  saving.value = true
  saveError.value = ''
  saved.value = false
  try {
    settings.value = await saveHomeSIMBarkSettings(settings.value)
    saved.value = true
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
  } finally {
    saving.value = false
  }
}

async function testPush(): Promise<void> {
  if (testing.value) return
  testing.value = true
  saveError.value = ''
  testSent.value = false
  try {
    await sendHomeSIMBarkTestPush()
    testSent.value = true
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
  } finally {
    testing.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="bark-panel">
    <header class="bark-panel__heading">
      <span class="bark-panel__icon" aria-hidden="true"><BellRing :size="18" /></span>
      <div>
        <h3>{{ t('homesim.bark.title') }}</h3>
        <small>{{ t('homesim.bark.description') }}</small>
      </div>
    </header>

    <div v-if="loading" class="bark-panel__state" role="status">
      <LoaderCircle class="spin" :size="18" />
      {{ t('common.loading') }}
    </div>

    <template v-else-if="settings">
      <div class="settings-form-grid">
        <label class="field">
          <span>{{ t('homesim.bark.serverUrl') }}</span>
          <input v-model="settings.server_url" type="url" autocomplete="off" :disabled="saving || testing" />
          <small class="field-status">{{ t('homesim.bark.serverUrlHint') }}</small>
        </label>
        <label class="field">
          <span>{{ t('homesim.bark.credentialRef') }}</span>
          <input v-model="settings.credential_ref" type="text" autocomplete="off" :disabled="saving || testing" />
          <small class="field-status">{{ t('homesim.bark.credentialRefHint') }}</small>
        </label>
        <label class="field">
          <span>{{ t('homesim.bark.group') }}</span>
          <input v-model="settings.group" type="text" autocomplete="off" :disabled="saving || testing" />
        </label>
        <label class="field">
          <span>{{ t('homesim.bark.sound') }}</span>
          <input v-model="settings.sound" type="text" autocomplete="off" :disabled="saving || testing" />
          <small class="field-status">{{ t('homesim.bark.soundHint') }}</small>
        </label>
      </div>

      <p v-if="saveError" class="field-error" role="alert">{{ saveError }}</p>
      <p v-else-if="saved" class="bark-panel__saved" role="status">{{ t('homesim.bark.saved') }}</p>
      <p v-else-if="testSent" class="bark-panel__saved" role="status">{{ t('homesim.bark.testSent') }}</p>

      <footer class="bark-panel__actions">
        <button class="primary-button" type="button" :disabled="saving || testing" @click="save">
          <LoaderCircle v-if="saving" class="spin" :size="16" />
          <Send v-else :size="16" />
          {{ t('homesim.bark.save') }}
        </button>
        <button class="secondary-button" type="button" :disabled="saving || testing" @click="testPush">
          <LoaderCircle v-if="testing" class="spin" :size="16" />
          <BellRing v-else :size="16" />
          {{ t('homesim.bark.test') }}
        </button>
        <small class="bark-panel__draft">{{ t('homesim.bark.stateDraft') }}</small>
      </footer>
    </template>

    <p v-else class="field-error" role="alert">
      {{ loadError }}
      <button class="text-button" type="button" @click="load">{{ t('common.retry') }}</button>
    </p>
  </section>
</template>

<style scoped>
.bark-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.bark-panel__heading {
  display: flex;
  align-items: center;
  gap: 10px;
}

.bark-panel__heading h3 {
  margin: 0;
  font-size: 15px;
}

.bark-panel__heading small {
  display: block;
  color: var(--muted);
  font-size: 11.5px;
}

.bark-panel__icon {
  display: inline-grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 50%;
}

.bark-panel__state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}

.bark-panel__saved {
  margin: 0;
  color: var(--success);
  font-size: 12px;
}

.bark-panel__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.bark-panel__actions .primary-button,
.bark-panel__actions .secondary-button {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 7px;
  padding: 0 14px;
  font-size: 12.5px;
}

.bark-panel__draft {
  color: var(--muted);
  font-size: 11px;
}
</style>
