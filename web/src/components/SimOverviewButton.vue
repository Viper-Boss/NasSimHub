<script setup lang="ts">
import { ref } from 'vue'
import { CardSim, X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { bootstrapResource, loadBootstrap, lineKey } from '../state/workspace'
import OverlayDialog from './OverlayDialog.vue'
import ModuleCard from './ModuleCard.vue'
const open = ref(false)
const busy = ref(false)
const { t } = useI18n()
async function refresh(): Promise<void> {
  busy.value = true
  try { await loadBootstrap(true) } finally { busy.value = false }
}
</script>
<template>
  <button type="button" class="icon-button" :title="t('homesim.simOverview')" :aria-label="t('homesim.simOverview')" @click="open = true"><CardSim :size="19" /></button>
  <OverlayDialog :open="open" size="large" :label="t('homesim.simOverview')" @close="open = false">
    <section class="homesim-sim-overview">
      <header><h2>{{ t('homesim.simOverview') }}</h2><button type="button" class="icon-button" :aria-label="t('common.close')" @click="open = false"><X :size="20" /></button></header>
      <div class="homesim-sim-overview__cards"><ModuleCard v-for="line in bootstrapResource.data?.lines || []" :key="lineKey(line)" :line="line" :selectable="false" /></div>
      <p v-if="!bootstrapResource.data?.lines.length">{{ t('homesim.noSIM') }}</p>
      <p v-if="bootstrapResource.error" class="field-error" role="alert">{{ bootstrapResource.error }}</p>
      <footer><RouterLink :to="{ name: 'settings', params: { section: 'devices' } }" @click="open = false">{{ t('homesim.simManagement') }}</RouterLink><button class="secondary-button" type="button" :disabled="busy" @click="refresh">{{ t('common.refresh') }}</button></footer>
    </section>
  </OverlayDialog>
</template>
<style scoped>
.homesim-sim-overview { max-height: calc(100dvh - 48px); overflow-y: auto; padding: 22px; }
.homesim-sim-overview header, .homesim-sim-overview footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.homesim-sim-overview h2 { font-size: 20px; }
.homesim-sim-overview__cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(270px, 100%), 1fr)); gap: 14px; margin: 20px 0; }
.homesim-sim-overview footer { font-size: 14px; }
.homesim-sim-overview footer a { color: var(--accent); }
</style>
