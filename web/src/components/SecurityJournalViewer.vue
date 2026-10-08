<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  EMPTY_JOURNAL_LISTING,
  JOURNAL_PAGE_SIZE,
  NodeApiError,
  appendJournalPage,
  getJournalPage,
  journalOlderBound
} from '../api/nodeSecurity'
import type { JournalEvent, JournalListing } from '../api/nodeSecurity'
import type { SecurityCentreLabels } from '../i18n/securityCentreLabels'
import SelectControl from './SelectControl.vue'

// The security journal, newest entries first, one page at a time.
//
// WHAT A READER NEEDS FROM AN ENTRY is what happened, to which device, why,
// who said so and what came of it. The hashes that chain the entries together
// are how the gateway checks the journal, not something a person reads, so
// they sit in a collapsed detail and are never the content of a row.
//
// THE GATEWAY PAGES NEWEST FIRST. Opening asks for `order=desc`; "load older"
// asks for `before=<the oldest sequence number shown>`; the device filter is
// part of the same request, so a filtered page is a page of that device's
// entries and not a page of everything with most rows thrown away. Older pages
// are fetched only when asked for.
//
// A gateway older than that query ignores it and answers its OLDEST page. The
// reader detects that from the answer (getJournalPage -> newestFirst) and the
// viewer then says the list may be incomplete and offers no further page,
// rather than presenting old entries as the latest ones.
//
// A kind this build has no label for is shown as the gateway sent it. Dropping
// it, or filing it under a nearby label, would hide exactly the entry a newer
// gateway thought worth adding.

const props = defineProps<{
  labels: SecurityCentreLabels
  locale: string
  /** Entries before this were resealed over and are not vouched for. */
  sealedFrom: number | undefined
  /** Device IDs offered in the filter. */
  devices: string[]
}>()

// The entries on screen, whether an older page can exist, and whether every
// answer so far was the newest-first one that was asked for.
const listing = ref<JournalListing>(EMPTY_JOURNAL_LISTING)
const events = computed(() => listing.value.events)
const more = computed(() => listing.value.more)
const newestFirst = computed(() => listing.value.newestFirst)
const loading = ref(false)
const error = ref('')
const filter = ref('')
const started = ref(false)
// Bumped by every fresh start, so an older page that answers after the filter
// changed is dropped instead of being appended to another device's list.
let generation = 0

function errorText(cause: unknown): string {
  if (cause instanceof NodeApiError) {
    if (cause.code === 'network') return props.labels.offline
    if (cause.status === 403) return props.labels.adminOnly
    if (cause.code === 'invalid_response') return props.labels.invalidResponse
    if (cause.message) return cause.message
    return `${props.labels.failed} (${cause.status})`
  }
  return props.labels.failed
}

async function readPage(before: number | undefined): Promise<void> {
  const mine = generation
  loading.value = true
  error.value = ''
  try {
    const page = await getJournalPage(before, JOURNAL_PAGE_SIZE, filter.value)
    if (mine !== generation) return
    listing.value = appendJournalPage(listing.value, page, before)
  } catch (cause) {
    if (mine === generation) error.value = errorText(cause)
  } finally {
    if (mine === generation) loading.value = false
  }
}

async function loadOlder(): Promise<void> {
  if (loading.value || !more.value) return
  await readPage(journalOlderBound(listing.value))
}

/** Start again from the newest entry. Nothing shown before survives. */
async function loadNewest(): Promise<void> {
  generation += 1
  listing.value = EMPTY_JOURNAL_LISTING
  started.value = true
  await readPage(undefined)
}

function kindLabel(kind: string): string {
  return props.labels.eventKinds[kind] ?? (kind || props.labels.unknown)
}

function formatTime(value: string): string {
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? props.labels.unknown : new Date(parsed).toLocaleString(props.locale)
}

function unverified(event: JournalEvent): boolean {
  return props.sealedFrom !== undefined && event.seq < props.sealedFrom
}

const filterOptions = computed(() => [
  { value: '', label: props.labels.viewerFilterAll },
  ...props.devices.map(device => ({ value: device, label: device }))
])

const empty = computed(() => started.value && !loading.value && !error.value && events.value.length === 0)

watch(filter, () => {
  void loadNewest()
})

onMounted(() => {
  void loadNewest()
})

defineExpose({ reload: loadNewest })
</script>

<template>
  <section class="journal-viewer" :aria-label="labels.viewerTitle">
    <div class="journal-viewer__bar">
      <div class="journal-viewer__filter">
        <SelectControl
          v-model="filter"
          :options="filterOptions"
          :label="labels.viewerFilter"
          :disabled="loading"
          compact
        />
      </div>
      <button type="button" class="secondary-button" :disabled="loading" @click="loadNewest">
        {{ loading ? labels.loading : labels.viewerNewest }}
      </button>
    </div>

    <p v-if="!newestFirst" class="journal-viewer__note journal-viewer__incomplete" role="status">{{ labels.viewerMayBeIncomplete }}</p>
    <p v-if="loading && !events.length" class="journal-viewer__note" role="status">{{ labels.loading }}</p>
    <p v-if="error" role="alert" class="journal-viewer__error">{{ error }}</p>
    <p v-else-if="empty" class="journal-viewer__note">{{ labels.viewerEmpty }}</p>

    <ol v-if="events.length" class="journal-viewer__list">
      <li v-for="event in events" :key="event.seq" class="journal-viewer__entry">
        <div class="journal-viewer__head">
          <strong>{{ kindLabel(event.kind) }}</strong>
          <span>{{ formatTime(event.at) }}</span>
        </div>
        <p v-if="unverified(event)" class="journal-viewer__unverified">{{ labels.viewerUnverified }}</p>
        <dl>
          <div>
            <dt>{{ labels.eventDevice }}</dt>
            <dd class="journal-viewer__mono">{{ event.node_id || labels.eventGateway }}</dd>
          </div>
          <div><dt>{{ labels.eventCause }}</dt><dd>{{ event.cause || labels.none }}</dd></div>
          <div><dt>{{ labels.eventSource }}</dt><dd>{{ event.source || labels.unknown }}</dd></div>
          <div><dt>{{ labels.eventResult }}</dt><dd>{{ event.result || labels.none }}</dd></div>
        </dl>
        <details class="journal-viewer__chain">
          <summary class="ui-disclosure-summary">{{ labels.eventDetails }}</summary>
          <dl>
            <div><dt>{{ labels.eventSeq }}</dt><dd>#{{ event.seq }}</dd></div>
            <div><dt>{{ labels.eventHash }}</dt><dd class="journal-viewer__mono">{{ event.hash || labels.none }}</dd></div>
            <div><dt>{{ labels.eventPrevHash }}</dt><dd class="journal-viewer__mono">{{ event.prev_hash || labels.none }}</dd></div>
          </dl>
        </details>
      </li>
    </ol>

    <!-- Kept when an older page failed, so that page can be asked for again. -->
    <div v-if="started" class="journal-viewer__foot">
      <button v-if="more" type="button" class="secondary-button" :disabled="loading" @click="loadOlder">
        {{ loading ? labels.loading : labels.viewerOlder }}
      </button>
      <!-- "This is the beginning" is a claim about the journal, so it is made
           only when the gateway answered the question that was asked. -->
      <span v-else-if="events.length && newestFirst" class="journal-viewer__note">{{ labels.viewerNoMore }}</span>
    </div>
  </section>
</template>

<style scoped>
.journal-viewer {
  display: grid;
  min-width: 0;
  gap: 12px;
}

.journal-viewer__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 10px;
}

.journal-viewer__filter {
  flex: 1 1 200px;
  min-width: 0;
  max-width: 360px;
}

.journal-viewer__note,
.journal-viewer__error,
.journal-viewer__unverified {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.journal-viewer__note { color: var(--muted); }
.journal-viewer__error { color: var(--danger-strong); }
.journal-viewer__unverified { color: var(--warning-strong); font-size: 12px; }
.journal-viewer__incomplete { color: var(--warning-strong); }

.journal-viewer__list {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.journal-viewer__entry {
  display: grid;
  min-width: 0;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.journal-viewer__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 2px 12px;
}

.journal-viewer__head strong {
  min-width: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.journal-viewer__head span {
  color: var(--muted);
  font-size: 12px;
}

.journal-viewer dl {
  display: grid;
  gap: 5px;
  margin: 0;
  font-size: 13px;
}

/* Label over value once the pair no longer fits on a line: a cause is a
   sentence, and at 360px it must wrap under its label, not beside it. */
.journal-viewer dl > div {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 12px;
  min-width: 0;
}

.journal-viewer dt {
  flex: 0 0 72px;
  color: var(--muted);
}

.journal-viewer dd {
  flex: 1 1 180px;
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.journal-viewer__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.journal-viewer__chain { font-size: 12px; }
.journal-viewer__chain summary { color: var(--muted); cursor: pointer; }
.journal-viewer__chain dl { margin-top: 6px; }

.journal-viewer__foot {
  display: flex;
  justify-content: center;
}
</style>
