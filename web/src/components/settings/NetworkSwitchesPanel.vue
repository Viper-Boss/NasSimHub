<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAdvancedI18n as useI18n } from '../../i18n/useAdvancedI18n'
import { LoaderCircle, Network, RefreshCw, Save } from '@lucide/vue'
import {
  getHomeSIMNetworkSwitches,
  saveHomeSIMNetworkSwitches,
  type HomeSIMNetworkSwitches
} from '../../api/client'

const { t } = useI18n()
const loading = ref(true)
const saving = ref(false)
const loadError = ref('')
const saveError = ref('')
const saved = ref(false)
const switches = ref<HomeSIMNetworkSwitches | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    switches.value = await getHomeSIMNetworkSwitches()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!switches.value || saving.value) return
  saving.value = true
  saveError.value = ''
  saved.value = false
  try {
    switches.value = await saveHomeSIMNetworkSwitches(switches.value)
    saved.value = true
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="net-switches">
    <div class="homesim-sim-notice"><strong>{{ t('homesim.advanced.text85') }}</strong><p>{{ t('homesim.advanced.text86') }}</p><RouterLink :to="{ name: 'traffic' }">{{ t('homesim.advanced.text87') }}</RouterLink></div>
    <div v-if="loading" class="net-switches__state" role="status">
      <LoaderCircle class="spin" :size="18" />
      {{ t('common.loading') }}
    </div>

    <template v-else-if="switches">
      <!-- Switch A: cellular share -->
      <div class="net-switches__card">
        <header>
          <Network :size="17" />
          <div>
            <h3>{{ t('homesim.netSwitches.shareTitle') }}</h3>
            <small>{{ t('homesim.advanced.text88') }}</small>
          </div>
        </header>
        <p class="net-switches__desc">{{ t('homesim.netSwitches.shareDesc') }}</p>
        <button
          class="net-switches__toggle"
          :class="{ 'is-on': switches.cellular_share }"
          type="button"
          role="switch"
          :aria-checked="switches.cellular_share"
          :aria-label="t('homesim.netSwitches.shareTitle')"
          @click="switches.cellular_share = !switches.cellular_share"
        >
          <span class="net-switches__knob" />
        </button>
        <div v-if="switches.cellular_share" class="net-switches__guide">
          <p class="net-switches__guide-title">{{ t('homesim.netSwitches.guideTitle') }}</p>
          <ol>
            <li>{{ t('homesim.netSwitches.guideStep1') }}</li>
            <li>{{ t('homesim.netSwitches.guideStep2') }}</li>
            <li>{{ t('homesim.netSwitches.guideStep3') }}</li>
          </ol>
          <p class="net-switches__guide-hint">{{ t('homesim.netSwitches.guideHint') }}</p>
        </div>
      </div>

      <!-- Switch B: HomeSIM only -->
      <div class="net-switches__card">
        <header>
          <Network :size="17" />
          <div>
            <h3>{{ t('homesim.netSwitches.onlyTitle') }}</h3>
            <small>{{ t('homesim.advanced.text89') }}</small>
          </div>
        </header>
        <p class="net-switches__desc">{{ t('homesim.netSwitches.onlyDesc') }}</p>
        <button
          class="net-switches__toggle"
          :class="{ 'is-on': switches.home_sim_only }"
          type="button"
          role="switch"
          :aria-checked="switches.home_sim_only"
          :aria-label="t('homesim.netSwitches.onlyTitle')"
          @click="switches.home_sim_only = !switches.home_sim_only"
        >
          <span class="net-switches__knob" />
        </button>
        <p class="net-switches__note">{{ t('homesim.netSwitches.onlyNote') }}</p>
      </div>

      <p v-if="saveError" class="field-error" role="alert">{{ saveError }}</p>
      <p v-else-if="saved" class="net-switches__saved" role="status">{{ t('homesim.bark.saved') }}</p>

      <footer class="net-switches__footer">
        <button class="primary-button" type="button" :disabled="saving" @click="save">
          <LoaderCircle v-if="saving" class="spin" :size="16" />
          <Save v-else :size="16" />
          {{ t('homesim.bark.save') }}
        </button>
        <button class="secondary-button" type="button" :disabled="loading" @click="load">
          <RefreshCw :size="15" />
          {{ t('common.refresh') }}
        </button>
        <small class="net-switches__default">{{ t('homesim.netSwitches.defaultNote') }}</small>
      </footer>
    </template>

    <p v-else class="field-error" role="alert">
      {{ loadError }}
      <button type="button" @click="load">{{ t('common.retry') }}</button>
    </p>
  </section>
</template>

<style scoped>
.net-switches {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.net-switches__state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}

.net-switches__card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  background: color-mix(in srgb, var(--surface) 60%, transparent);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}

.net-switches__card header {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--accent);
}

.net-switches__card header h3 {
  margin: 0;
  font-size: 14px;
  color: var(--text);
}

.net-switches__card header small {
  display: block;
  color: var(--muted);
  font-size: 11px;
}

.net-switches__desc {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}

.net-switches__toggle {
  position: absolute;
  top: 14px;
  right: 14px;
  display: inline-flex;
  width: 44px;
  height: 24px;
  align-items: center;
  padding: 2px;
  cursor: pointer;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  transition: background var(--motion-fast) var(--ease-standard);
}

.net-switches__knob {
  display: block;
  width: 18px;
  height: 18px;
  background: var(--muted);
  border-radius: 50%;
  transition: transform var(--motion-fast) var(--ease-standard);
}

.net-switches__toggle.is-on {
  background: color-mix(in srgb, var(--success) 30%, transparent);
  border-color: color-mix(in srgb, var(--success) 45%, transparent);
}

.net-switches__toggle.is-on .net-switches__knob {
  transform: translateX(20px);
  background: var(--success);
}

.net-switches__guide {
  padding: 10px 12px;
  background: var(--surface-subtle);
  border-radius: var(--radius-control);
}

.net-switches__guide-title {
  margin: 0 0 6px;
  font-size: 11.5px;
  font-weight: 700;
  color: var(--text);
}

.net-switches__guide ol {
  margin: 0;
  padding-left: 18px;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.7;
}

.net-switches__guide-hint {
  margin: 6px 0 0;
  color: var(--faint);
  font-size: 10.5px;
}

.net-switches__note {
  margin: 0;
  color: var(--muted);
  font-size: 11px;
}

.net-switches__saved {
  margin: 0;
  color: var(--success);
  font-size: 12px;
}

.net-switches__footer {
  display: flex;
  align-items: center;
  gap: 10px;
}

.net-switches__footer .primary-button,
.net-switches__footer .secondary-button {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 7px;
  padding: 0 14px;
  font-size: 12.5px;
}

.net-switches__default {
  margin-left: auto;
  color: var(--muted);
  font-size: 10.5px;
}
</style>
