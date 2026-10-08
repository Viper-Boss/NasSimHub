<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { deviceGroupLabels } from '../i18n/deviceGroupLabels'
import { nodeSecurityCopy } from '../i18n/nodeSecurityCopy'
import {
  acquireNodeFleet,
  nodeFleetState,
  refreshNodeFleet,
  releaseNodeFleet
} from '../state/nodeFleet'
import { sessionState } from '../state/session'
import SettingsModuleCard from './settings/SettingsModuleCard.vue'

// The update centre's entry for the Nodes' own software: which paired devices
// there are, which agent each one runs, and the way into the per-device update
// panel.
//
// NOTHING HERE IS REMEMBERED. The list is the gateway's node list as the one
// shared poll last read it (state/nodeFleet.ts), which is emptied when its
// last reader leaves, so opening this page never shows a version from an
// earlier visit. What the list can say about a version is limited to what
// that document carries:
//
//   an ONLINE device    the version its agent reported on this connection
//   an OFFLINE device   "offline", and at most the version it reported the
//                       last time it connected -- labelled as exactly that,
//                       because it may have been updated, rolled back or
//                       reflashed since and nothing here would know
//
// Whether an update is available, staged or pending is not in that document
// at all. It is read from the device by the panel, when the panel is opened,
// and is not summarised here from anything older.

// Opened for one device at a time and most visits never open it, so its code
// waits for the first click (the same boundary the nodes view uses).
const NodeUpdatePanel = defineAsyncComponent(() => import('./NodeUpdatePanel.vue'))

const { locale } = useI18n()
const labels = computed(() => deviceGroupLabels(String(locale.value)))
const securityCopy = computed(() => nodeSecurityCopy(String(locale.value)))

// The node list and every update route are administrators-only. A member is
// shown nothing rather than a card that can only refuse.
const visible = computed(() => sessionState.role === 'admin')
const nodes = computed(() => nodeFleetState.nodes)
const loading = computed(
  () => nodeFleetState.status === 'idle' || nodeFleetState.status === 'loading'
)

/** The device whose update panel is open; '' when none is. */
const openID = ref('')
// Looked up in the LIVE list each time: a device that is unpaired while its
// panel is open takes the panel with it.
const openNode = computed(() => nodes.value.find(node => node.node_id === openID.value))

function nameOf(node: { display_name: string; node_id: string }): string {
  return node.display_name || node.node_id
}

function formatTime(value: string): string {
  const parsed = Date.parse(value)
  return Number.isNaN(parsed)
    ? labels.value.agentNeverSeen
    : new Date(parsed).toLocaleString(String(locale.value))
}

let held = false
onMounted(() => {
  if (!visible.value) return
  held = true
  acquireNodeFleet()
})
onBeforeUnmount(() => {
  if (held) releaseNodeFleet()
})
</script>

<template>
  <SettingsModuleCard
    v-if="visible"
    class="node-agents"
    :title="labels.agentsTitle"
    title-id="node-agents-title"
    :description="labels.agentsSubtitle"
  >
    <p v-if="loading" class="node-agents__state" role="status">{{ labels.agentsLoading }}</p>
    <p v-else-if="nodeFleetState.status === 'forbidden'" class="node-agents__state" role="alert">
      {{ labels.agentsAdminOnly }}
    </p>
    <div v-else-if="nodeFleetState.status === 'error'" class="node-agents__state" role="alert">
      <span>{{ labels.agentsUnavailable }}</span>
      <button type="button" class="secondary-button" @click="refreshNodeFleet()">
        {{ labels.agentsRetry }}
      </button>
    </div>
    <p v-else-if="nodes.length === 0" class="node-agents__state">{{ labels.agentsEmpty }}</p>
    <ul v-else class="node-agents__list">
      <li v-for="node in nodes" :key="node.node_id" class="node-agents__row">
        <div class="node-agents__identity">
          <strong>{{ nameOf(node) }}</strong>
          <span class="node-agents__id">{{ node.node_id }}</span>
        </div>
        <dl class="node-agents__facts">
          <div>
            <dt>{{ labels.agentVersion }}</dt>
            <dd v-if="node.online">{{ node.agent_version || labels.agentVersionUnknown }}</dd>
            <!-- Offline: the version is a fact about the last connection and
                 is never shown without saying so. -->
            <dd v-else-if="node.agent_version" class="node-agents__past">
              {{ node.agent_version }}
              <small>{{ labels.agentVersionLastSeen }}</small>
            </dd>
            <dd v-else class="node-agents__past">{{ labels.agentVersionUnknown }}</dd>
          </div>
          <div v-if="!node.online">
            <dt>{{ labels.agentLastSeen }}</dt>
            <dd class="node-agents__past">{{ formatTime(node.last_seen_at) }}</dd>
          </div>
        </dl>
        <span class="node-agents__badge" :class="{ 'is-online': node.online }">
          {{ node.online ? labels.agentOnline : labels.agentOffline }}
        </span>
        <button
          type="button"
          class="secondary-button node-agents__open"
          :aria-label="labels.agentOpen(nameOf(node))"
          :aria-expanded="openID === node.node_id"
          @click="openID = openID === node.node_id ? '' : node.node_id"
        >
          {{ labels.agentOpenShort }}
        </button>
      </li>
    </ul>

    <!-- Keyed by device: choosing another one discards the first one's state
         and reads the second from the device, rather than relabelling a panel
         that still holds the previous device's answer. -->
    <NodeUpdatePanel
      v-if="openNode"
      :key="openNode.node_id"
      class="node-agents__panel"
      :device-id="openNode.node_id"
      :device-name="nameOf(openNode)"
      :copy="securityCopy"
      :locale="String(locale)"
      @close="openID = ''"
    />
  </SettingsModuleCard>
</template>

<style scoped>
.node-agents__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin: 0;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.node-agents__list {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

/* One row per device on a wide card; below that the four parts stack, so the
   button is never pushed out of reach by a long device name. */
.node-agents__row {
  display: grid;
  min-width: 0;
  align-items: center;
  gap: 8px 14px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 1.4fr) auto auto;
}

.node-agents__identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.node-agents__identity strong {
  min-width: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.node-agents__id {
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  overflow-wrap: anywhere;
}

.node-agents__facts {
  display: grid;
  min-width: 0;
  gap: 4px;
  margin: 0;
  font-size: 13px;
}

.node-agents__facts > div {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: 2px 10px;
}

.node-agents__facts dt {
  color: var(--muted);
}

.node-agents__facts dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.node-agents__past {
  color: var(--muted-strong);
}

.node-agents__past small {
  color: var(--muted);
  font-size: 12px;
}

.node-agents__badge {
  padding: 2px 9px;
  color: var(--muted-strong);
  font-size: 12px;
  white-space: nowrap;
  background: var(--surface-hover);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.node-agents__badge.is-online {
  color: var(--success);
  background: var(--success-soft);
  border-color: var(--success-border);
}

.node-agents__open {
  min-height: 36px;
  white-space: nowrap;
}

.node-agents__panel {
  margin-top: 14px;
}

@media (max-width: 860px) {
  .node-agents__row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .node-agents__facts {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .node-agents__open {
    grid-column: 1 / -1;
    justify-self: start;
  }
}
</style>
