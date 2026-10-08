<script setup lang="ts">
import { computed } from 'vue'
import { readNodeSecurity } from '../api/nodeSecurity'
import type { DeviceMetricsSource } from '../api/nodeFleet'
import type { Device, NetworkLineStatus } from '../api/types'
import type { DashboardDevice } from '../state/dashboardDevices'
import ModuleCard from './ModuleCard.vue'
import ModuleExtraCards from './ModuleExtraCards.vue'
import ModuleInsightCards from './ModuleInsightCards.vue'

// The card field of ONE device: the module card, its four insight cards and
// its three information cards, in that order. Together with the four metric
// tiles above it this is the set the dashboard was first built with, for a
// single modem.
//
// THERE IS ONE OF THESE TEMPLATES, and every device is drawn with it -- a
// modem on the NAS and a NasSimHub Node alike. What differs is only what is
// handed in: `device` (whose line), `source` (whose metrics). Nothing in here
// looks anything up by itself, so a card cannot come to show a neighbour's
// resource figures. Nodes and clients deliberately shows NasSimHub's shared
// gateway network; a resource card with no source stays in its place
// and says "not provided by this device", so two groups side by side are the
// same shape.
//
// The root element is the grid on purpose: a child's root takes the parent's
// scoped styles, so the dashboard's one `.dashboard-module-grid` rule lays out
// this field and the NAS-services field beside it, rather than two copies of
// a rule that was measured once.
const props = defineProps<{
  device: DashboardDevice
  /** This device's own metrics, or an empty source that says why there are none. */
  source: DeviceMetricsSource
  /** The gateway's device record for this module, when it has one. */
  record?: Device
  /** This line's data-connection state. */
  runtime?: NetworkLineStatus
  defaultLine?: boolean
  /** Whether the viewer may see what locates the device. */
  admin?: boolean
}>()

const emit = defineEmits<{ select: [] }>()

/**
 * The Node's own trust record. Undefined for a module on the NAS (the trust
 * engine does not assess those); null for a Node whose list entry the viewer
 * does not have -- a member, or before the list has answered.
 */
const trust = computed(() => {
  if (props.device.kind !== 'node') return undefined
  return props.device.node ? readNodeSecurity(props.device.node.raw).trust : null
})
</script>

<template>
  <div class="dashboard-module-grid">
    <ModuleCard
      :line="device.line"
      :device="record"
      :runtime="runtime"
      :selectable="device.hasLine && !device.line.module_only"
      :default-line="defaultLine"
      @select="emit('select')"
    />
    <ModuleInsightCards scope="device" :line="device.line" :source="source" :admin="admin" />
    <ModuleExtraCards :line="device.line" :device="record" :trust="trust" mode="all" />
  </div>
</template>
