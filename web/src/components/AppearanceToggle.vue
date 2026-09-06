<script setup lang="ts">
import { Moon, Sun } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { appearance, toggleAppearance } from '../state/appearance'
const { t } = useI18n()
</script>

<template>
  <button type="button" class="icon-button homesim-appearance" :title="t(appearance === 'dark' ? 'homesim.light' : 'homesim.dark')" :aria-label="t(appearance === 'dark' ? 'homesim.light' : 'homesim.dark')" @click="toggleAppearance">
    <Sun v-if="appearance === 'dark'" :size="19" aria-hidden="true" />
    <Moon v-else :size="19" aria-hidden="true" />
  </button>
</template>
