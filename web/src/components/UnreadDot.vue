<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    compact?: boolean
  }>(),
  {
    compact: false
  }
)
</script>

<template>
  <span
    class="unread-dot"
    :class="{ 'is-compact': compact }"
    role="img"
    :aria-label="label"
    :title="label"
  />
</template>

<style scoped>
.unread-dot {
  display: inline-block;
  width: 9px;
  height: 9px;
  flex: 0 0 9px;
  background: var(--danger);
  border-radius: 50%;
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--danger) 16%, transparent);
}

.unread-dot.is-compact {
  width: 10px;
  height: 10px;
  flex-basis: 10px;
  border: 2px solid var(--surface);
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--danger) 52%, transparent),
    0 1px 2px rgb(16 24 40 / 18%);
}
</style>
