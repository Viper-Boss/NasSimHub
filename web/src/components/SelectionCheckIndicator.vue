<script setup lang="ts">
import { Check } from '@lucide/vue'

defineProps<{
  checked: boolean
}>()
</script>

<template>
  <span
    class="selection-check-indicator"
    :class="{ 'is-checked': checked }"
  >
    <Check v-if="checked" :size="15" stroke-width="3" />
  </span>
</template>

<style scoped>
.selection-check-indicator {
  display: grid;
  width: 22px;
  height: 22px;
  flex: 0 0 22px;
  place-items: center;
  color: var(--on-accent);
  background: var(--surface);
  border: 2px solid var(--faint);
  /* Pinned literal, not --radius-sm (6px): the 22px checkbox is drawn with a
     5px corner so the 2px border reads as a square check target. Locked by
     contract-tests/batch-selection-favorites.test.mjs. */
  border-radius: 5px;
}

.selection-check-indicator.is-checked {
  background: var(--accent);
  border-color: var(--accent);
}
</style>
