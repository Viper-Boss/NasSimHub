<script setup lang="ts">
import type { LinkBoundary, SecurityPosture } from '../api/dashboardCards'
import type { SecurityCentreLabels } from '../i18n/securityCentreLabels'

// The link boundaries, one row each.
//
// "Is this post-quantum?" has no single answer: a request crosses the
// browser's connection, possibly a proxy, the gateway's session with each
// device, the call audio and two update channels, and a hybrid handshake on one
// says nothing about the next. So nothing here is summarised. Every boundary is
// its own row and says three things separately: HOW the gateway knows
// (evidence), what was ASKED FOR, and what was OBTAINED. A shortfall is then
// something a reader sees, not something an average hides.
//
// The list is drawn exactly as the gateway sent it, in its order. A boundary
// id this build has no name for is shown by its id; an evidence word or a
// post-quantum word it does not recognise has already been read as "unknown"
// (readBoundary), so a newer gateway cannot make this page draw a yes.
//
// A pure function of its props: the panel that owns it does the one request
// and hands over what came back, or null when nothing did. Nothing is kept
// here, so an old list cannot outlive the answer that replaced it.

const props = defineProps<{
  labels: SecurityCentreLabels
  /** The posture as just read; null when the gateway did not answer. */
  posture: SecurityPosture | null
  /** False until the first read has finished, so "loading" is not "empty". */
  loaded: boolean
}>()

function name(boundary: LinkBoundary): string {
  return props.labels.boundaryNames[boundary.id] ?? boundary.id
}

/**
 * Green only for a "yes" the gateway can stand behind. "No" is not red: a
 * classical link is a fact about the link, not a fault, and colouring it as
 * one would teach a reader to ignore the colour.
 */
function postQuantumTone(boundary: LinkBoundary): string {
  if (boundary.post_quantum !== 'yes') return ''
  return boundary.evidence === 'observed' || boundary.evidence === 'protocol' ? 'is-ok' : ''
}

</script>

<template>
  <section class="boundary-list" :aria-label="labels.boundariesTitle">
    <p class="boundary-list__note">{{ labels.boundariesNote }}</p>
    <p v-if="posture?.transport_scope === 'proxy_hop'" class="boundary-list__scope">
      {{ labels.proxyHopNote }}
    </p>

    <p v-if="!loaded" class="boundary-list__note">{{ labels.loading }}</p>
    <p v-else-if="!posture" class="boundary-list__note">{{ labels.boundariesPending }}</p>
    <p v-else-if="!posture.boundaries || posture.boundaries.length === 0" class="boundary-list__note">
      {{ labels.boundariesEmpty }}
    </p>
    <ul v-else class="boundary-list__rows">
      <li
        v-for="(boundary, index) in posture.boundaries"
        :key="`${boundary.id}-${boundary.node_id ?? ''}-${index}`"
        class="boundary-list__row"
      >
        <div class="boundary-list__head">
          <strong>{{ name(boundary) }}</strong>
          <span class="boundary-list__badge" :title="labels.boundaryEvidenceNotes[boundary.evidence]">
            {{ labels.boundaryEvidence[boundary.evidence] }}
          </span>
        </div>
        <dl>
          <div v-if="boundary.node_id">
            <dt>{{ labels.boundaryDevice }}</dt>
            <dd class="boundary-list__mono">{{ boundary.node_id }}</dd>
          </div>
          <div>
            <dt>{{ labels.protection }}</dt>
            <dd>{{ boundary.protection || labels.notStated }}</dd>
          </div>
          <div>
            <dt>{{ labels.postQuantum }}</dt>
            <dd :class="postQuantumTone(boundary)">{{ labels.tristate[boundary.post_quantum] }}</dd>
          </div>
          <!-- Two rows, always both: what was asked for and what was obtained
               are different facts, and an absent one is said to be absent. -->
          <div>
            <dt>{{ labels.requested }}</dt>
            <dd>{{ boundary.requested || labels.notStated }}</dd>
          </div>
          <div>
            <dt>{{ labels.negotiated }}</dt>
            <dd :class="{ 'is-warn': boundary.meets_requested === false }">
              {{ boundary.negotiated || labels.notStated }}
            </dd>
          </div>
        </dl>
        <p v-if="boundary.meets_requested === false" role="alert" class="boundary-list__warning">
          {{ labels.shortfall }}
        </p>
        <p v-if="boundary.detail" class="boundary-list__detail">{{ boundary.detail }}</p>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.boundary-list {
  display: grid;
  min-width: 0;
  gap: 10px;
}

.boundary-list__note,
.boundary-list__detail,
.boundary-list__scope,
.boundary-list__warning {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.boundary-list__note,
.boundary-list__detail { color: var(--muted); }

.boundary-list__scope,
.boundary-list__warning {
  padding: 8px 10px;
  color: var(--warning-strong);
  background: var(--warning-soft);
  border: 1px solid var(--warning-border);
  border-radius: var(--radius-sm);
}

.boundary-list__rows {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.boundary-list__row {
  display: grid;
  align-content: start;
  min-width: 0;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.boundary-list__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 4px 10px;
}

.boundary-list__head strong {
  min-width: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.boundary-list__badge {
  padding: 2px 9px;
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.5;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.boundary-list dl {
  display: grid;
  gap: 5px;
  margin: 0;
  font-size: 13px;
}

.boundary-list dl > div {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 2px 12px;
  min-width: 0;
}

.boundary-list dt { color: var(--muted); }

.boundary-list dd {
  min-width: 0;
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}

.boundary-list dd.is-ok { color: var(--success); }
.boundary-list dd.is-warn { color: var(--warning-strong); }

.boundary-list__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
