<script setup lang="ts">
import { computed } from 'vue'
import { devicePolicyOutcome } from '../api/nodeSecurity'
import type { DevicePolicyStatus, EvidenceKey, EvidenceState } from '../api/nodeSecurity'
import { devicePolicyCopy } from '../i18n/devicePolicyCopy'

const props = defineProps<{
  device: DevicePolicyStatus
  locale: string
  evidence?: Record<EvidenceKey, EvidenceState>
}>()
const copy = computed(() => devicePolicyCopy(props.locale))
const outcome = computed(() => devicePolicyOutcome(props.device))
const checked = computed(() => props.device.checked_at
  ? new Date(props.device.checked_at).toLocaleString(props.locale) : '')
</script>

<template>
  <section class="device-policy" :aria-label="copy.title">
    <div class="device-policy__heading">
      <strong>{{ copy.title }}</strong>
      <span :class="{ 'is-ok': outcome === 'enforced', 'is-bad': outcome === 'damaged' }">
        {{ copy.outcomes[outcome] }}
      </span>
    </div>
    <p v-if="device.reason">{{ device.reason }}</p>
    <p v-if="device.push_error" role="status" class="is-bad">{{ copy.delivery }}: {{ device.push_error }}</p>
    <dl v-if="evidence">
      <div><dt>{{ copy.hardware }}</dt><dd>{{ copy.evidence[evidence.modem_change] }}</dd></div>
      <div><dt>{{ copy.local }}</dt><dd>{{ copy.evidence[evidence.node_local] }}</dd></div>
    </dl>
    <small v-if="checked">{{ copy.checked }}: {{ checked }}</small>
  </section>
</template>

<style scoped>
.device-policy { display: grid; gap: 8px; min-width: 0; padding: 12px; background: var(--surface-subtle); border: 1px solid var(--border); border-radius: var(--radius-md); font-size: 13px; }
.device-policy__heading, dl > div { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; }
.device-policy p, dl, dd { margin: 0; }
.device-policy span, p, dd { overflow-wrap: anywhere; }
.device-policy span, dd { text-align: right; }
.device-policy p, dt, small { color: var(--muted); line-height: 1.55; }
.device-policy dl { display: grid; gap: 6px; }
.device-policy .is-ok { color: var(--success); }
.device-policy .is-bad { color: var(--danger-strong); }
@media (max-width: 560px) { .device-policy span, dd { text-align: left; } }
</style>
