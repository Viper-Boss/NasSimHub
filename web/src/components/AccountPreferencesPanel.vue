<script setup lang="ts">
import AccountSettingsPanel from './AccountSettingsPanel.vue'
import AppearanceToggle from './AppearanceToggle.vue'
import { useI18n } from 'vue-i18n'
import { appearance, toggleAppearance } from '../state/appearance'
const { t } = useI18n()
</script>

<template>
  <!-- The whole card toggles the theme; the inner icon button stops
       propagation so a click never counts twice. -->
  <div
    class="homesim-appearance-setting"
    role="button"
    tabindex="0"
    @click="toggleAppearance"
    @keydown.enter.prevent="toggleAppearance"
    @keydown.space.prevent="toggleAppearance"
  >
    <span>{{ t(appearance === 'dark' ? 'homesim.light' : 'homesim.dark') }}</span>
    <AppearanceToggle @click.stop />
  </div>
  <AccountSettingsPanel :show-identity="false" />
</template>

<style scoped>
.homesim-appearance-setting {
  cursor: pointer;
  user-select: none;
}

.homesim-appearance-setting:focus-visible {
  outline: 2px solid var(--accent, #4f8cff);
  outline-offset: 2px;
}
</style>
