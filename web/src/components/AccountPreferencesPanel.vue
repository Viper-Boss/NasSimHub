<script setup lang="ts">
import AccountSettingsPanel from './AccountSettingsPanel.vue'
import AppearanceToggle from './AppearanceToggle.vue'
import { useI18n } from 'vue-i18n'
import { appearance } from '../state/appearance'
const { t } = useI18n()
</script>

<template>
  <div class="homesim-appearance-setting">
    <span>{{ t(appearance === 'dark' ? 'homesim.light' : 'homesim.dark') }}</span>
    <AppearanceToggle />
  </div>
  <AccountSettingsPanel :show-identity="false" />
</template>
