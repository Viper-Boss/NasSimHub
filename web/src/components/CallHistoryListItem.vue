<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  AudioLines,
  Star
} from '@lucide/vue'
import type { CallRecord } from '../api/types'
import type { LineTagLine } from '../utils/lineIdentity'
import { formatRelativeDate } from '../utils/format'
import CommunicationAvatar from './CommunicationAvatar.vue'
import LineTag from './LineTag.vue'
import ListItemAvatarStatus from './ListItemAvatarStatus.vue'
import ListItemStatusRail from './ListItemStatusRail.vue'
import RecordingPlayer from './RecordingPlayer.vue'
import { recordingCatalogState } from '../state/recording'
import { groupedPhone } from '../utils/homesimDisplay'

const props = withDefaults(
  defineProps<{
    call: CallRecord
    name: string
    number: string
    avatar?: string
    contactBound?: boolean
    line: LineTagLine
    lineFallback?: string
    selected?: boolean
    hasRecording?: boolean
  }>(),
  {
    avatar: '',
    contactBound: false,
    lineFallback: '',
    selected: false,
    hasRecording: false
  }
)

const emit = defineEmits<{
  select: [call: CallRecord]
}>()
const { t } = useI18n()
const expandedRecording = ref(false)
const recordings = computed(() => recordingCatalogState.data.filter(item => item.call_id === props.call.id && item.playable && item.download_url))

const directionLabel = computed(() => {
  if (props.call.missed) return t('dashboard.missedCall')
  return props.call.direction === 'incoming'
    ? t('dashboard.incoming')
    : t('dashboard.outgoing')
})
const showNumber = computed(
  () => props.number.trim().toLocaleLowerCase() !== props.name.trim().toLocaleLowerCase()
)
</script>

<template>
  <div class="homesim-call-row">
  <button
    class="list-item call-list-item"
    :class="{
      'is-selected': selected,
      'is-missed': call.missed,
      'is-unread': call.missed && !call.read
    }"
    type="button"
    :aria-label="
      call.missed && !call.read
        ? t('calls.viewUnreadDetails', { name })
        : t('calls.viewDetails', { name })
    "
    @click="emit('select', props.call)"
  >
    <ListItemAvatarStatus
      class="call-list-item__avatar"
      :unread-label="
        call.missed && !call.read
          ? t('calls.unreadMissed')
          : undefined
      "
    >
      <CommunicationAvatar
        channel="call"
        :name="name"
        :address="number"
        :src="avatar"
        :contact-bound="contactBound"
      />
    </ListItemAvatarStatus>
    <span class="list-item__content">
      <span class="call-list-item__identity">
        <strong>{{ name }}</strong>
      </span>
      <span class="call-list-item__meta">
        <LineTag :line="line" :fallback="lineFallback" />
        <small>
          {{ directionLabel }}<template v-if="showNumber"> · {{ groupedPhone(number) }}</template>
        </small>
      </span>
    </span>
    <ListItemStatusRail
      :date="formatRelativeDate(call.started_at)"
      :date-time="call.started_at"
    >
      <template #favorite>
        <Star
          v-if="call.favorite"
          class="call-list-item__favorite"
          :size="15"
          fill="currentColor"
          :aria-label="t('common.favorite')"
        />
      </template>
    </ListItemStatusRail>
  </button>
  <aside class="homesim-call-recording" @click.stop>
    <button v-if="hasRecording" type="button" class="homesim-recording-toggle" :aria-expanded="expandedRecording" @click="expandedRecording = !expandedRecording">
      <AudioLines :size="16" /><span>{{ t('recordings.title') }}</span>
    </button>
    <span v-else class="homesim-no-recording">—</span>
  </aside>
  <div v-if="expandedRecording" class="homesim-call-playback" @click.stop>
    <RecordingPlayer v-for="item in recordings" :key="item.id" :src="item.download_url!" :label="`${name} · ${t('recordings.playback')}`" compact />
    <p v-if="!recordings.length">{{ t('recordings.pending') }}</p>
  </div>
  </div>
</template>

<style scoped>
.homesim-call-row { display: grid; grid-template-columns: minmax(0, 1fr) 88px; border-bottom: 1px solid var(--border); }
.homesim-call-row > .list-item { border-bottom: 0; min-width: 0; }
.homesim-call-recording { display: grid; place-items: center; }
.homesim-recording-toggle { display: flex; align-items: center; gap: 5px; padding: 8px; color: var(--accent); background: var(--accent-soft); border-radius: 12px; font-size: 12px; }
.homesim-no-recording { color: var(--faint); }
.homesim-call-playback { grid-column: 1 / -1; display: grid; gap: 12px; padding: 6px 20px 16px 64px; }
.homesim-call-playback p { color: var(--muted); font-size: 12px; }
.call-list-item__meta {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 6px;
  overflow: hidden;
}

.call-list-item__meta small {
  min-width: 0;
  flex: 1 1 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.call-list-item__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.call-list-item__identity strong {
  min-width: 0;
}

.call-list-item__favorite {
  flex: 0 0 auto;
  color: var(--favorite);
}

.call-list-item__recording {
  display: inline-grid;
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  place-items: center;
  color: var(--accent-strong);
  background: transparent;
}
</style>
