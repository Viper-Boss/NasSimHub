<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { KeyRound, Trash2, X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { messageResources } from '../state/workspace'
import type { MessageThread, Message } from '../api/types'
import { gateway } from '../api/client'
import { collectCodeSuggestions } from '../utils/smsInsight'
import { formatRelativeDate } from '../utils/format'
import { groupedPhone, messageTime } from '../utils/homesimDisplay'
import { requestConfirmation } from '../state/confirmation'
import OverlayDialog from './OverlayDialog.vue'
import SmsCodeButton from './SmsCodeButton.vue'
const props = defineProps<{ threads: MessageThread[]; lineFilter?: string }>()
const emit = defineEmits<{ select: [threadKey: string] }>()
const { t } = useI18n()
const expanded = ref(false)
const scannedThreads = ref<MessageThread[]>([])
const scanned = ref<Record<string, { data: Message[] }>>({})
const scanning = ref(false)
const scanError = ref('')
const scanComplete = ref(false)
const scannedCount = ref(0)
let generation = 0
const dismissed = ref(new Set<string>())
const codes = computed(() => collectCodeSuggestions(props.threads, messageResources, props.lineFilter).filter(item => !dismissed.value.has(item.key)))
const library = computed(() => scannedThreads.value.length
  ? collectCodeSuggestions(scannedThreads.value, scanned.value, props.lineFilter, Number.MAX_SAFE_INTEGER, false).filter(item => !dismissed.value.has(item.key))
  : codes.value)
const recent = computed(() => (scanComplete.value ? library.value : codes.value).slice(0, 3))
function cancelScan(): void { generation++; scanning.value = false }
watch(expanded, open => { if (!open) cancelScan() })
watch(() => props.lineFilter, () => { cancelScan(); scannedThreads.value = []; scanned.value = {}; scanComplete.value = false; scannedCount.value = 0 })
onBeforeUnmount(cancelScan)
async function scanAll(): Promise<void> {
  if (scanning.value) return
  const run = ++generation
  scanning.value = true; scanComplete.value = false; scanError.value = ''; scannedCount.value = 0
  scannedThreads.value = []; scanned.value = {}
  try {
    let cursor: string | undefined
    const seenCursors = new Set<string>()
    do {
      const page = await gateway.listThreads({ limit: 100, cursor })
      if (run !== generation) return
      for (const thread of page.items) {
        if (props.lineFilter && props.lineFilter !== 'all' && thread.line_id !== props.lineFilter) continue
        const messages: Message[] = []
        let messageCursor: string | undefined
        const seenMessages = new Set<string>()
        do {
          const result = await gateway.listMessages({ line_id: thread.line_id, peer: thread.peer, limit: 100, cursor: messageCursor })
          if (run !== generation) return
          messages.push(...result.items)
          scannedCount.value += result.items.length
          // Bound memory use without silently calling partial results complete.
          if (scannedCount.value > 100000) throw new Error(t('homesim.scanLimit'))
          messageCursor = result.meta?.has_more ? result.meta.next_cursor : undefined
          if (result.meta?.has_more && (!messageCursor || seenMessages.has(messageCursor))) throw new Error(t('homesim.invalidCursor'))
          if (messageCursor) seenMessages.add(messageCursor)
        } while (messageCursor)
        scanned.value[thread.key] = { data: messages }
        scannedThreads.value.push(thread)
      }
      cursor = page.meta?.has_more ? page.meta.next_cursor : undefined
      if (page.meta?.has_more && (!cursor || seenCursors.has(cursor))) throw new Error(t('homesim.invalidCursor'))
      if (cursor) seenCursors.add(cursor)
    } while (cursor)
    scanComplete.value = true
  } catch (error) {
    if (run === generation) scanError.value = error instanceof Error ? error.message : t('homesim.scanFailed')
  } finally { if (run === generation) scanning.value = false }
}
function select(key: string): void { expanded.value = false; emit('select', key) }
function openLibrary(): void { expanded.value = true; void scanAll() }

const clearing = ref(false)
// Clear only the current view. A code-bearing thread can also contain ordinary SMS.
async function clearCodes(): Promise<void> {
  if (clearing.value) return
  const source = scanComplete.value ? library.value : codes.value
  if (!source.length) return
  const confirmed = await requestConfirmation({
    title: t('homesim.clearCodesTitle'),
    message: t('homesim.clearCodesMessage', { count: new Set(source.map(item => item.threadKey)).size }),
    confirmLabel: t('homesim.clearCodesConfirm'),
    tone: 'danger'
  })
  if (!confirmed) return
  clearing.value = true
  try {
    cancelScan()
    dismissed.value = new Set([...dismissed.value, ...source.map(item => item.key)])
  } finally {
    clearing.value = false
  }
}
</script>

<template>
  <section class="sms-code-shelf" :aria-label="t('homesim.codes')">
    <header>
      <strong><KeyRound :size="16" aria-hidden="true" />{{ t('homesim.codes') }}</strong>
      <span class="sms-code-shelf__header-actions">
        <button
          v-if="recent.length"
          type="button"
          class="text-button sms-code-shelf__clear"
          :disabled="clearing"
          @click="clearCodes"
        ><Trash2 :size="14" aria-hidden="true" />{{ t('homesim.clearCodes') }}</button>
        <button type="button" class="text-button" @click="openLibrary">{{ t('homesim.allCodes', { count: library.length }) }}</button>
      </span>
    </header>
    <p class="sms-code-shelf__scope">{{ t('homesim.previewScope') }}</p>
    <div class="sms-code-shelf__list">
      <div v-for="item in recent" :key="item.key" class="sms-code-shelf__item">
        <button type="button" class="sms-code-shelf__identity" @click="select(item.threadKey)">
          <span class="sms-code-shelf__meta">
            <small v-if="item.source">{{ item.source }}</small>
            <time :datetime="item.timestamp">{{ formatRelativeDate(item.timestamp) }}</time>
          </span>
          <code>{{ item.code }}</code>
        </button>
        <SmsCodeButton :code="item.code" />
      </div>
      <p v-if="!recent.length" class="sms-code-shelf__empty">{{ t('homesim.noCodes') }}</p>
    </div>
  </section>
  <OverlayDialog :open="expanded" size="large" labelledby="homesim-codes-title" @close="expanded = false">
    <section class="sms-code-library">
      <header><h2 id="homesim-codes-title">{{ t('homesim.codes') }}</h2><button type="button" class="icon-button" :aria-label="t('common.close')" @click="expanded = false"><X :size="20" /></button></header>
      <div class="sms-code-library__toolbar">
        <p>{{ t('homesim.codeScope') }}</p>
        <button
          v-if="library.length"
          type="button"
          class="text-button sms-code-shelf__clear"
          :disabled="clearing"
          @click="clearCodes"
        ><Trash2 :size="14" aria-hidden="true" />{{ t('homesim.clearCodes') }}</button>
      </div>
      <div class="sms-code-library__actions">
        <button type="button" class="secondary-button" :disabled="scanning" @click="scanAll">{{ t(scanning ? 'homesim.scanningCodes' : 'homesim.scanCodes') }}</button>
        <button v-if="scanning" type="button" class="text-button" @click="cancelScan(); scanError = t('homesim.scanCancelled')">{{ t('common.cancel') }}</button>
      </div>
      <p role="status">{{ scannedCount }} {{ t('homesim.scannedMessages') }} · {{ scanComplete ? t('homesim.scanDone') : t('homesim.codeScope') }}</p>
      <p v-if="scanError" class="field-error" role="alert">{{ scanError }}</p>
      <div v-for="item in library" :key="item.key" class="sms-code-shelf__item sms-code-shelf__item--full">
        <button type="button" class="sms-code-shelf__identity" @click="select(item.threadKey)">
          <span class="sms-code-shelf__meta">
            <small>{{ item.source }} · {{ groupedPhone(item.peer) }}</small>
            <time :datetime="item.timestamp">{{ messageTime(item.timestamp) }}</time>
          </span>
          <code>{{ item.code }}</code>
        </button>
        <SmsCodeButton :code="item.code" />
        <p class="sms-code-message">{{ item.content }}</p>
      </div>
    </section>
  </OverlayDialog>
</template>

<style scoped>
.sms-code-shelf { flex: 0 0 auto; padding: 14px; border-bottom: 1px solid var(--border); background: var(--surface-subtle); }
.sms-code-shelf header, .sms-code-library header { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.sms-code-shelf header strong { display: flex; align-items: center; gap: 8px; font-size: .875rem; }
.sms-code-shelf header strong svg { color: var(--accent); }
.sms-code-shelf__scope, .sms-code-library > p { color: var(--muted); font-size: .75rem; line-height: 1.5; margin: 8px 0; }
.sms-code-shelf__list { max-height: min(280px, 32dvh); overflow-y: auto; }
.sms-code-shelf__item { display: flex; align-items: center; gap: 8px; padding: 8px 10px; margin-top: 6px; background: var(--surface); border: 1px solid var(--border); border-radius: 13px; }
.sms-code-shelf__identity { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 3px; color: var(--text); background: transparent; text-align: left; }
.sms-code-shelf__meta { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; min-width: 0; }
.sms-code-shelf__meta small { flex: 0 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sms-code-shelf__meta time { flex: 0 0 auto; }
.sms-code-shelf__identity code { color: var(--accent); font-size: 1.25rem; letter-spacing: .07em; user-select: text; }
.sms-code-shelf__identity time { font-size: .75rem; color: var(--muted); }
.sms-code-shelf__empty { color: var(--muted); font-size: .875rem; padding: 10px 0; }
.sms-code-shelf__header-actions { display: inline-flex; flex: 0 0 auto; align-items: center; gap: 10px; }
.sms-code-shelf__clear { display: inline-flex; align-items: center; gap: 4px; color: var(--danger); }
.sms-code-library__toolbar { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; }
.sms-code-library__toolbar p { margin: 0; }
.sms-code-library__actions { display: flex; align-items: center; gap: 10px; margin: 8px 0; }
.sms-code-shelf__item--full { flex-wrap: wrap; }
.sms-code-message { flex-basis: 100%; padding-top: 8px; border-top: 1px solid var(--border); color: var(--muted); font-size: 14px; line-height: 1.7; overflow-wrap: anywhere; }
.sms-code-library { padding: 24px; width: min(560px, 100%); max-height: 80dvh; overflow-y: auto; }
.sms-code-library h2 { font-size: 1.25rem; }
@media (max-height: 700px) { .sms-code-shelf__list { max-height: 115px; } }
</style>
