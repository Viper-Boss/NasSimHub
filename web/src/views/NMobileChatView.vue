<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { ArrowLeft, Check, CheckCheck, Clock, Copy, MessageSquarePlus, Search, Send, Trash2, UserRoundPen } from '@lucide/vue'
import {
  gateway,
  getNKNChatMessages,
  sendNKNChatMessage,
  clearNKNChatMessages,
  type NKNChatMessage
} from '../api/client'
import { loadNknPeerNotes, nknPeerNotes, saveNknPeerNote } from '../state/nknPeerNotes'
import type { Contact } from '../api/types'
import PageContentFrame from '../components/PageContentFrame.vue'
import SelectControl from '../components/SelectControl.vue'
import { requestConfirmation } from '../state/confirmation'

const { t } = useI18n()
const route = useRoute()

type ChatStatus = '' | 'sending' | 'sent' | 'delivered'
interface ChatItem extends NKNChatMessage {
  status: ChatStatus
  localId: string
}

const state = reactive({
  messages: [] as ChatItem[],
  peer: '',
  draft: '',
  loading: false,
  sending: false,
  error: '',
  loaded: false
})

// Address -> local name lives in a shared store, so a rename here also shows
// on the contacts page. Both halves are optional: an alias alone is enough, a
// contact link alone shows the contact's name.
const contacts = ref<Contact[]>([])
const contactsByID = computed(() => new Map(contacts.value.map(item => [item.id, item])))
const contactOptions = computed(() => [
  { value: '', label: t('homesim.nmobile.noContact') },
  ...contacts.value.map(item => ({ value: item.id, label: item.display_name }))
])
const noteEditor = reactive({ open: false, address: '', alias: '', contactID: '', saving: false, error: '' })

const activePeer = ref('')
const chatWindow = ref<HTMLElement | null>(null)
const draftInput = ref<HTMLTextAreaElement | null>(null)
const copiedPeer = ref('')
const filter = ref('')
// 'thread' addresses the selected conversation; 'new' exposes the address field
// so a first message can be sent without one existing yet.
const composeMode = ref<'thread' | 'new'>('thread')
// Below the drilldown band only one pane is on screen at a time.
const mobilePane = ref<'list' | 'thread'>('list')
let timer: ReturnType<typeof setInterval> | undefined
let localSeq = 0

const grouped = computed(() => {
  const byPeer = new Map<string, ChatItem[]>()
  for (const message of state.messages) {
    const list = byPeer.get(message.peer) ?? []
    list.push(message)
    byPeer.set(message.peer, list)
  }
  return [...byPeer.entries()]
    .map(([addr, items]) => ({
      addr,
      items: [...items].sort((a, b) => a.ts - b.ts)
    }))
    .sort((a, b) => (b.items.at(-1)?.ts ?? 0) - (a.items.at(-1)?.ts ?? 0))
})

const visibleThreads = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  if (!needle) return grouped.value
  return grouped.value.filter(thread => (
    thread.addr.toLowerCase().includes(needle) ||
    displayName(thread.addr).toLowerCase().includes(needle) ||
    contactName(thread.addr).toLowerCase().includes(needle) ||
    thread.items.some(item => item.text.toLowerCase().includes(needle))
  ))
})

const activeThread = computed(() => {
  // An explicitly selected peer shows its own messages or none. Falling back to
  // the newest conversation here would put someone else's messages under this
  // peer's name — which is exactly what happens when the contacts page links to
  // an address that has no history yet.
  if (activePeer.value) {
    return grouped.value.find(entry => entry.addr === activePeer.value)?.items ?? []
  }
  return composeMode.value === 'new' ? [] : grouped.value[0]?.items ?? []
})

const activeLabel = computed(() => {
  if (activePeer.value) return activePeer.value
  return composeMode.value === 'new' ? '' : grouped.value[0]?.addr ?? ''
})

const canSend = computed(() => {
  if (state.sending || !state.draft.trim()) return false
  return Boolean(composeMode.value === 'new' ? state.peer.trim() : activeLabel.value)
})

function shortAddress(addr: string): string {
  if (addr.length <= 24) return addr
  return `${addr.slice(0, 12)}…${addr.slice(-6)}`
}

// What a conversation is called: the alias wins, then the linked contact's
// name, and only then the raw address. An NKN address says nothing about who
// is on the other end, so the fallback is the last resort, not the default.
function contactName(addr: string): string {
  const id = nknPeerNotes.get(addr)?.contact_id
  return (id && contactsByID.value.get(id)?.display_name) || ''
}

function displayName(addr: string): string {
  return nknPeerNotes.get(addr)?.alias || contactName(addr) || shortAddress(addr)
}

// Shown under the name when the name is not itself the address.
function displaySubtitle(addr: string): string {
  const alias = nknPeerNotes.get(addr)?.alias
  const linked = contactName(addr)
  if (alias && linked) return linked
  return ''
}

function hasNote(addr: string): boolean {
  const note = nknPeerNotes.get(addr)
  return Boolean(note?.alias || note?.contact_id)
}

// A stable hue per address so a conversation keeps the same avatar colour.
function avatarHue(addr: string): number {
  let hash = 0
  for (let index = 0; index < addr.length; index += 1) {
    hash = (hash * 31 + addr.charCodeAt(index)) % 360
  }
  return hash
}

function initial(addr: string): string {
  const named = nknPeerNotes.get(addr)?.alias || contactName(addr)
  if (named) return named.trim()[0]?.toUpperCase() ?? '?'
  return (addr.replace(/^[^a-z0-9]+/i, '')[0] ?? '?').toUpperCase()
}

function formatTime(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function startOfDay(value: Date): number {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime()
}

// Day headings inside the thread, and the compact stamp used in the list.
function dayLabel(ts: number): string {
  const at = new Date(ts * 1000)
  const days = Math.round((startOfDay(new Date()) - startOfDay(at)) / 86_400_000)
  if (days <= 0) return t('homesim.nmobile.today')
  if (days === 1) return t('homesim.nmobile.yesterday')
  return at.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function listStamp(ts: number): string {
  const at = new Date(ts * 1000)
  const days = Math.round((startOfDay(new Date()) - startOfDay(at)) / 86_400_000)
  if (days <= 0) return formatTime(ts)
  if (days === 1) return t('homesim.nmobile.yesterday')
  return at.toLocaleDateString([], { month: 'numeric', day: 'numeric' })
}

function preview(items: ChatItem[]): string {
  const last = items.at(-1)
  if (!last) return ''
  return last.dir === 'out' ? `↗ ${last.text}` : last.text
}

// Consecutive messages from the same side within five minutes read as one turn:
// only the first keeps an avatar, the rest tighten up under it.
function startsTurn(items: ChatItem[], index: number): boolean {
  if (index === 0) return true
  const previous = items[index - 1]
  const current = items[index]
  if (!previous || !current) return true
  return previous.dir !== current.dir || current.ts - previous.ts > 300
}

function startsDay(items: ChatItem[], index: number): boolean {
  const previous = items[index - 1]
  const current = items[index]
  if (!previous || !current) return true
  return startOfDay(new Date(previous.ts * 1000)) !== startOfDay(new Date(current.ts * 1000))
}

function tickStatus(item: ChatItem): ChatStatus {
  if (item.dir !== 'out') return ''
  return item.status || 'sent'
}

function statusTitle(item: ChatItem): string {
  const status = tickStatus(item)
  if (status === 'sending') return t('homesim.nmobile.statusSending')
  if (status === 'delivered') return t('homesim.nmobile.statusDelivered')
  return t('homesim.nmobile.statusSent')
}

function scrollToBottom(): void {
  void nextTick(() => {
    const box = chatWindow.value
    if (box) box.scrollTop = box.scrollHeight
  })
}

function resizeDraft(): void {
  const field = draftInput.value
  if (!field) return
  field.style.height = 'auto'
  field.style.height = `${Math.min(field.scrollHeight, 140)}px`
}

watch(() => activeThread.value.length, scrollToBottom)
watch(activePeer, scrollToBottom)
watch(() => state.draft, () => void nextTick(resizeDraft))

function openThread(addr: string): void {
  activePeer.value = addr
  composeMode.value = 'thread'
  mobilePane.value = 'thread'
  scrollToBottom()
}

function startNewConversation(): void {
  composeMode.value = 'new'
  activePeer.value = ''
  state.peer = ''
  mobilePane.value = 'thread'
  void nextTick(() => draftInput.value?.focus())
}

function mergeIncoming(incoming: NKNChatMessage[]): boolean {
  let added = false
  for (const item of incoming) {
    // 1) same server id -> refresh status (delivery receipts)
    if (item.id) {
      const known = state.messages.find(entry => entry.id === item.id)
      if (known) {
        if (item.status && known.status !== item.status) {
          known.status = item.status
          added = true
        }
        continue
      }
    }
    // 2) optimistic echo: a pending out bubble that the server now confirms
    const pending = state.messages.find(entry => (
      entry.status === 'sending' && entry.dir === 'out' &&
      entry.peer === item.peer && entry.text === item.text
    ))
    if (pending && Math.abs(pending.ts - item.ts) < 600) {
      pending.ts = item.ts
      pending.id = item.id
      pending.status = item.status || 'sent'
      added = true
      continue
    }
    // 3) plain duplicate (ts|dir|peer|text)
    const key = `${item.ts}|${item.dir}|${item.peer}|${item.text}`
    if (state.messages.some(entry => `${entry.ts}|${entry.dir}|${entry.peer}|${entry.text}` === key)) continue
    state.messages.push({ ...item, status: item.status ?? '', localId: `srv-${item.ts}-${state.messages.length}` })
    added = true
  }
  return added
}

async function load(): Promise<void> {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try {
    const latest = state.messages.reduce((max, item) => Math.max(max, item.ts), 0)
    const incoming = await getNKNChatMessages(Math.max(0, latest - 5))
    if (incoming.length) {
      if (mergeIncoming(incoming)) scrollToBottom()
    }
    state.loaded = true
  } catch (reason) {
    state.error = reason instanceof Error ? reason.message : String(reason)
  } finally {
    state.loading = false
  }
}

async function send(): Promise<void> {
  const target = (composeMode.value === 'new' ? state.peer : activeLabel.value).trim()
  const text = state.draft.trim()
  if (!target || !text || state.sending) return
  state.sending = true
  state.error = ''
  localSeq += 1
  const localId = `pending-${localSeq}`
  const optimistic: ChatItem = {
    ts: Math.floor(Date.now() / 1000),
    dir: 'out',
    peer: target,
    text,
    status: 'sending',
    localId
  }
  state.messages.push(optimistic)
  state.draft = ''
  void nextTick(resizeDraft)
  activePeer.value = target
  composeMode.value = 'thread'
  mobilePane.value = 'thread'
  scrollToBottom()
  try {
    const receipt = await sendNKNChatMessage(target, text)
    optimistic.ts = receipt.ts ?? optimistic.ts
    optimistic.id = receipt.id
    optimistic.status = 'sent'
    scrollToBottom()
  } catch (reason) {
    state.error = reason instanceof Error ? reason.message : String(reason)
    optimistic.status = 'sent'
  } finally {
    state.sending = false
    void load()
  }
}

// Contacts are loaded once; they do not change often enough to poll.
async function loadContacts(): Promise<void> {
  try {
    const page = await gateway.listContacts({ limit: 200 })
    contacts.value = page.items
  } catch {
    // Without contacts the alias field still works on its own.
  }
}

function openNoteEditor(addr: string): void {
  const note = nknPeerNotes.get(addr)
  noteEditor.address = addr
  noteEditor.alias = note?.alias ?? ''
  noteEditor.contactID = note?.contact_id ?? ''
  noteEditor.error = ''
  noteEditor.open = true
  if (!contacts.value.length) void loadContacts()
}

async function saveNote(): Promise<void> {
  if (noteEditor.saving) return
  noteEditor.saving = true
  noteEditor.error = ''
  const address = noteEditor.address
  const alias = noteEditor.alias.trim()
  const contactID = noteEditor.contactID
  try {
    await saveNknPeerNote({ address, alias, contact_id: contactID })
    noteEditor.open = false
  } catch (reason) {
    noteEditor.error = reason instanceof Error ? reason.message : String(reason)
  } finally {
    noteEditor.saving = false
  }
}

async function copyPeer(addr: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(addr)
    copiedPeer.value = addr
    window.setTimeout(() => { if (copiedPeer.value === addr) copiedPeer.value = '' }, 2000)
  } catch {
    // clipboard unavailable: ignore
  }
}

const deleting = ref(false)

async function clearConversation(): Promise<void> {
  const addr = activeLabel.value
  if (!addr || deleting.value) return
  const confirmed = await requestConfirmation({
    title: t('homesim.nmobile.deleteConversation'),
    message: t('homesim.nmobile.confirmDelete'),
    confirmLabel: t('common.delete'),
    tone: 'danger'
  })
  if (!confirmed) return
  deleting.value = true
  state.error = ''
  try {
    await clearNKNChatMessages(addr)
    state.messages = state.messages.filter(item => item.peer !== addr)
    if (activePeer.value === addr) activePeer.value = ''
    mobilePane.value = 'list'
  } catch (reason) {
    state.error = reason instanceof Error ? reason.message : String(reason)
  } finally {
    deleting.value = false
  }
}

onMounted(() => {
  // ?peer=<address> arrives from the contacts page, which links straight to a
  // conversation. The thread may hold no messages yet, so this opens the
  // composer addressed to that peer rather than relying on it being in the list.
  const requested = typeof route.query.peer === 'string' ? route.query.peer.trim() : ''
  if (requested) {
    activePeer.value = requested
    mobilePane.value = 'thread'
  }
  void load().then(scrollToBottom)
  void loadNknPeerNotes()
  void loadContacts()
  timer = setInterval(() => { void load() }, 8_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <PageContentFrame>
    <div class="nmo" :data-pane="mobilePane">
      <header class="nmo__head">
        <div>
          <h1>{{ t('homesim.nmobile.title') }}</h1>
          <p>{{ t('homesim.nmobile.subtitle') }}</p>
        </div>
      </header>

      <p v-if="state.error" class="field-error nmo__error" role="alert">{{ state.error }}</p>

      <div class="nmo__panes">
        <!-- Conversation list -->
        <section class="nmo__list" :aria-label="t('homesim.nmobile.conversations')">
          <div class="nmo__list-head">
            <label class="nmo__search">
              <Search :size="15" aria-hidden="true" />
              <input
                id="nmobile-filter"
                v-model="filter"
                type="search"
                :placeholder="t('homesim.nmobile.searchPlaceholder')"
                :aria-label="t('homesim.nmobile.searchPlaceholder')"
                spellcheck="false"
              >
            </label>
            <button
              class="nmo__new"
              type="button"
              :title="t('homesim.nmobile.newConversation')"
              :aria-label="t('homesim.nmobile.newConversation')"
              @click="startNewConversation"
            >
              <MessageSquarePlus :size="17" />
            </button>
          </div>

          <ul class="nmo__threads">
            <li v-for="thread in visibleThreads" :key="thread.addr">
              <button
                class="nmo__thread"
                :class="{ 'is-active': activeLabel === thread.addr }"
                type="button"
                :title="thread.addr"
                @click="openThread(thread.addr)"
              >
                <span
                  class="nmo__avatar"
                  aria-hidden="true"
                  :style="{ '--hue': avatarHue(thread.addr) }"
                >{{ initial(thread.addr) }}</span>
                <span class="nmo__thread-body">
                  <span class="nmo__thread-top">
                    <strong>{{ displayName(thread.addr) }}</strong>
                    <time>{{ listStamp(thread.items.at(-1)?.ts ?? 0) }}</time>
                  </span>
                  <span class="nmo__thread-preview">{{ preview(thread.items) }}</span>
                </span>
              </button>
            </li>
            <li v-if="!state.loading && visibleThreads.length === 0" class="nmo__list-empty">
              {{ filter.trim() ? t('homesim.nmobile.noMatches') : t('homesim.nmobile.empty') }}
            </li>
          </ul>
        </section>

        <!-- Thread -->
        <section class="nmo__thread-pane">
          <header v-if="activeLabel || composeMode === 'new'" class="nmo__bar">
            <button
              class="nmo__back"
              type="button"
              :title="t('homesim.nmobile.backToList')"
              :aria-label="t('homesim.nmobile.backToList')"
              @click="mobilePane = 'list'"
            >
              <ArrowLeft :size="17" />
            </button>
            <template v-if="activeLabel">
              <span
                class="nmo__avatar"
                aria-hidden="true"
                :style="{ '--hue': avatarHue(activeLabel) }"
              >{{ initial(activeLabel) }}</span>
              <span class="nmo__bar-peer">
                <strong>{{ displayName(activeLabel) }}</strong>
                <small :title="activeLabel">{{ displaySubtitle(activeLabel) || activeLabel }}</small>
              </span>
              <button
                class="nmo__bar-action"
                :class="{ 'is-set': hasNote(activeLabel) }"
                type="button"
                :title="t('homesim.nmobile.editNote')"
                :aria-label="t('homesim.nmobile.editNote')"
                @click="openNoteEditor(activeLabel)"
              >
                <UserRoundPen :size="15" />
              </button>
              <button
                class="nmo__bar-action"
                type="button"
                :title="t('homesim.nmobile.copyAddress')"
                :aria-label="t('homesim.nmobile.copyAddress')"
                @click="copyPeer(activeLabel)"
              >
                <Check v-if="copiedPeer === activeLabel" :size="15" />
                <Copy v-else :size="15" />
              </button>
              <button
                class="nmo__bar-action nmo__bar-action--danger"
                type="button"
                :title="t('homesim.nmobile.deleteConversation')"
                :aria-label="t('homesim.nmobile.deleteConversation')"
                :disabled="deleting"
                @click="clearConversation"
              >
                <Trash2 :size="15" />
              </button>
            </template>
            <span v-else class="nmo__bar-peer">
              <strong>{{ t('homesim.nmobile.newConversation') }}</strong>
              <small>{{ t('homesim.nmobile.newConversationHint') }}</small>
            </span>
          </header>

          <!-- Naming panel: an alias, a contact link, or both. -->
          <form v-if="noteEditor.open" class="nmo__note" @submit.prevent="saveNote">
            <div class="nmo__note-fields">
              <label class="nmo__note-field">
                <span>{{ t('homesim.nmobile.aliasLabel') }}</span>
                <input
                  id="nmobile-alias"
                  v-model="noteEditor.alias"
                  class="nmo__control"
                  type="text"
                  maxlength="64"
                  :placeholder="t('homesim.nmobile.aliasPlaceholder')"
                >
              </label>
              <div class="nmo__note-field">
                <span>{{ t('homesim.nmobile.linkContact') }}</span>
                <SelectControl
                  v-model="noteEditor.contactID"
                  :label="t('homesim.nmobile.linkContact')"
                  :options="contactOptions"
                  :placeholder="t('homesim.nmobile.noContact')"
                />
              </div>
            </div>
            <p class="nmo__note-hint">{{ t('homesim.nmobile.noteHint') }}</p>
            <p v-if="noteEditor.error" class="field-error">{{ noteEditor.error }}</p>
            <div class="nmo__note-actions">
              <button class="secondary-button" type="button" @click="noteEditor.open = false">
                {{ t('common.cancel') }}
              </button>
              <button class="primary-button" type="submit" :disabled="noteEditor.saving">
                {{ t('common.save') }}
              </button>
            </div>
          </form>

          <div ref="chatWindow" class="nmo__window">
            <div v-if="!activeLabel && composeMode !== 'new'" class="nmo__blank">
              <span class="nmo__blank-mark" aria-hidden="true">
                <MessageSquarePlus :size="22" />
              </span>
              <strong>{{ t('homesim.nmobile.selectConversation') }}</strong>
              <span>{{ t('homesim.nmobile.selectConversationHint') }}</span>
            </div>

            <template v-for="(item, index) in activeThread" :key="item.localId || `${item.ts}-${index}`">
              <div v-if="startsDay(activeThread, index)" class="nmo__day">
                <span>{{ dayLabel(item.ts) }}</span>
              </div>
              <div
                class="nmo__row"
                :class="[item.dir === 'out' ? 'is-out' : 'is-in', { 'is-turn': startsTurn(activeThread, index) }]"
              >
                <span
                  v-if="item.dir === 'in'"
                  class="nmo__avatar nmo__avatar--sm"
                  :class="{ 'is-hidden': !startsTurn(activeThread, index) }"
                  aria-hidden="true"
                  :style="{ '--hue': avatarHue(item.peer) }"
                >{{ initial(item.peer) }}</span>
                <div class="nmo__msg">
                  <span class="nmo__bubble">{{ item.text }}</span>
                  <span class="nmo__meta">
                    <time>{{ formatTime(item.ts) }}</time>
                    <span
                      v-if="item.dir === 'out'"
                      class="nmo__ticks"
                      :title="statusTitle(item)"
                    >
                      <Clock v-if="tickStatus(item) === 'sending'" :size="13" />
                      <Check v-else-if="tickStatus(item) === 'sent'" :size="13" />
                      <CheckCheck v-else class="is-delivered" :size="13" />
                    </span>
                  </span>
                </div>
              </div>
            </template>
          </div>

          <div v-if="activeLabel || composeMode === 'new'" class="nmo__compose">
            <input
              v-if="composeMode === 'new'"
              id="nmobile-peer"
              v-model="state.peer"
              class="nmo__control nmo__peer"
              type="text"
              :placeholder="t('homesim.nmobile.peerPlaceholder')"
              :aria-label="t('homesim.nmobile.peerPlaceholder')"
              spellcheck="false"
            >
            <div class="nmo__compose-row">
              <textarea
                id="nmobile-draft"
                ref="draftInput"
                v-model="state.draft"
                class="nmo__control nmo__draft"
                rows="1"
                :placeholder="t('homesim.nmobile.messagePlaceholder')"
                :aria-label="t('homesim.nmobile.messagePlaceholder')"
                @keydown.enter.exact.prevent="send"
              />
              <button
                class="primary-button nmo__send"
                type="button"
                :title="t('homesim.nmobile.send')"
                :aria-label="t('homesim.nmobile.send')"
                :disabled="!canSend"
                @click="send"
              >
                <Send :size="16" />
              </button>
            </div>
            <small class="nmo__hint">{{ t('homesim.nmobile.composerHint') }}</small>
          </div>
        </section>
      </div>
    </div>
  </PageContentFrame>
</template>

<style scoped>
.nmo { display: grid; gap: 14px; min-height: 0; }
.nmo__head h1 { font-size: 20px; }
.nmo__head p { margin-top: 4px; max-width: 70ch; color: var(--muted); font-size: 13px; line-height: 1.6; }
.nmo__error { margin: 0; }

/* Two panes on the workspace, one at a time below the drilldown band. */
.nmo__panes {
  display: grid;
  grid-template-columns: minmax(0, 280px) minmax(0, 1fr);
  min-height: 0;
  height: clamp(420px, 62vh, 640px);
  overflow: hidden;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  box-shadow: var(--elevation-card, none);
}

/* ---- conversation list ---- */
.nmo__list {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  border-right: 1px solid var(--border);
}

.nmo__list-head {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 12px;
  border-bottom: 1px solid var(--border);
}

.nmo__search {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
  height: 36px;
  padding: 0 11px;
  color: var(--faint);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}

.nmo__search input {
  min-width: 0;
  flex: 1 1 auto;
  color: var(--text);
  background: none;
  border: 0;
  font: inherit;
  font-size: 13px;
  outline: none;
}

.nmo__new {
  display: grid;
  width: 36px;
  height: 36px;
  flex: none;
  place-items: center;
  color: var(--on-accent);
  background: var(--accent);
  border: 0;
  border-radius: var(--radius-control);
  cursor: pointer;
}

.nmo__threads { display: block; overflow-y: auto; flex: 1 1 auto; margin: 0; padding: 6px; list-style: none; }

.nmo__thread {
  display: flex;
  width: 100%;
  gap: 10px;
  align-items: center;
  padding: 10px;
  color: inherit;
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  cursor: pointer;
  transition: background var(--motion-base) var(--ease-standard);
}

.nmo__thread:hover { background: var(--surface-hover); }

.nmo__thread.is-active {
  background: var(--surface-selected);
  border-color: var(--accent-border);
}

.nmo__thread-body { display: grid; min-width: 0; flex: 1 1 auto; gap: 3px; }
.nmo__thread-top { display: flex; gap: 8px; align-items: baseline; justify-content: space-between; }
.nmo__thread-top strong { overflow: hidden; font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.nmo__thread-top time { flex: none; color: var(--faint); font-size: 10.5px; font-variant-numeric: tabular-nums; }

.nmo__thread-preview {
  overflow: hidden;
  color: var(--muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nmo__list-empty { padding: 26px 16px; color: var(--muted); font-size: 12.5px; line-height: 1.6; text-align: center; }

/* ---- avatars ---- */
.nmo__avatar {
  display: grid;
  width: 36px;
  height: 36px;
  flex: none;
  place-items: center;
  color: hsl(var(--hue, 190) 70% 82%);
  font-size: 14px;
  font-weight: 700;
  background: hsl(var(--hue, 190) 45% 22%);
  border-radius: var(--radius-control);
}

.nmo__avatar--sm { width: 26px; height: 26px; font-size: 11px; border-radius: var(--radius-md); }
.nmo__avatar--sm.is-hidden { visibility: hidden; }

/* ---- thread pane ---- */
/* min-height:0 is what lets the message window scroll instead of stretching the
   pane past the card and clipping the composer under its bottom edge. */
.nmo__thread-pane { display: flex; min-width: 0; min-height: 0; flex-direction: column; }

.nmo__bar {
  display: flex;
  flex: 0 0 auto;
  gap: 10px;
  align-items: center;
  padding: 10px 14px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}

.nmo__bar-peer { display: grid; min-width: 0; flex: 1 1 auto; gap: 2px; }
.nmo__bar-peer strong { font-size: 13.5px; }
.nmo__bar-peer small { overflow: hidden; color: var(--muted); font-size: 10.5px; text-overflow: ellipsis; white-space: nowrap; }

.nmo__back { display: none; }

.nmo__bar-action {
  display: grid;
  width: 32px;
  height: 32px;
  flex: none;
  place-items: center;
  color: var(--muted);
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  cursor: pointer;
}

.nmo__bar-action:hover { color: var(--text); background: var(--surface-hover); }

.nmo__bar-action--danger:hover:not(:disabled) {
  color: var(--danger);
  background: var(--danger-soft);
  border-color: var(--danger-border);
}

.nmo__bar-action:disabled { cursor: wait; opacity: 0.5; }

/* Every text control on this page: same fill, border and radius as the shared
   .field inputs elsewhere in the app. */
.nmo__control {
  width: 100%;
  min-height: 40px;
  padding: 8px 10px;
  color: var(--text);
  font: inherit;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
}

.nmo__control:focus-visible { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }

/* ---- naming panel ---- */
.nmo__note {
  display: grid;
  flex: 0 0 auto;
  gap: 10px;
  padding: 14px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}

.nmo__note-fields { display: grid; gap: 10px; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.nmo__note-field { display: flex; flex-direction: column; gap: 6px; color: var(--muted); font-size: 12px; }
.nmo__note-hint { color: var(--faint); font-size: 11.5px; line-height: 1.6; }
.nmo__note-actions { display: flex; gap: 8px; justify-content: flex-end; }

.nmo__bar-action.is-set { color: var(--accent); border-color: var(--accent-border, var(--border)); }

/* ---- messages ---- */
.nmo__window {
  display: flex;
  min-height: 0;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 3px;
  padding: 16px 18px;
  overflow-y: auto;
}

/* A short thread rests just above the composer instead of floating at the top
   of an empty pane. `margin-block-start: auto` on the first child does that
   without the flex-end alignment bug that makes overflowing content
   unreachable. */
.nmo__window > :first-child { margin-block-start: auto; }

.nmo__blank { display: grid; margin: auto; max-width: 34ch; gap: 8px; justify-items: center; text-align: center; }
.nmo__blank-mark {
  display: grid;
  width: 46px;
  height: 46px;
  place-items: center;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: var(--radius-control);
}
.nmo__blank strong { font-size: 14px; }
.nmo__blank span { color: var(--muted); font-size: 12.5px; line-height: 1.6; }

.nmo__day { display: flex; align-items: center; justify-content: center; margin: 12px 0 8px; }
.nmo__day span {
  padding: 3px 12px;
  color: var(--faint);
  font-size: 11px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
}

.nmo__row { display: flex; gap: 8px; align-items: flex-start; }
.nmo__row.is-turn { margin-top: 10px; }
.nmo__row.is-out { flex-direction: row-reverse; }

.nmo__msg { display: flex; max-width: min(74%, 460px); flex-direction: column; gap: 3px; }
.nmo__row.is-out .nmo__msg { align-items: flex-end; }
.nmo__row.is-in .nmo__msg { align-items: flex-start; }

.nmo__bubble {
  padding: 9px 13px;
  font-size: 13.5px;
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  border-radius: var(--radius-control);
}

.nmo__row.is-in .nmo__bubble {
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
}

.nmo__row.is-in.is-turn .nmo__bubble { border-top-left-radius: var(--radius-xs); }

.nmo__row.is-out .nmo__bubble {
  color: #fff;
  background: linear-gradient(135deg, var(--hs-gradient-start, var(--accent)), var(--hs-gradient-end, var(--accent)));
}

.nmo__row.is-out.is-turn .nmo__bubble { border-top-right-radius: var(--radius-xs); }

.nmo__meta { display: inline-flex; gap: 5px; align-items: center; color: var(--faint); font-size: 10.5px; }
.nmo__meta time { font-variant-numeric: tabular-nums; }
.nmo__ticks { display: inline-flex; align-items: center; }
.nmo__ticks .is-delivered { color: var(--success); }

/* ---- composer ---- */
.nmo__compose {
  display: grid;
  flex: 0 0 auto;
  gap: 8px;
  padding: 12px 14px;
  background: var(--surface);
  border-top: 1px solid var(--border);
}

.nmo__compose-row { display: flex; gap: 8px; align-items: flex-end; }
.nmo__draft { min-height: 42px; max-height: 140px; flex: 1 1 auto; resize: none; }
.nmo__hint { color: var(--faint); font-size: 11px; }

.nmo__send {
  display: inline-flex;
  min-width: 46px;
  min-height: 42px;
  align-items: center;
  justify-content: center;
  padding: 0;
}

/* Drilldown band: one pane at a time, with a back control in the thread bar. */
@media (max-width: 860px) {
  /* Short enough that the composer and its hint clear the bottom navigation
     without the page having to be scrolled to reach them. */
  .nmo__panes { grid-template-columns: minmax(0, 1fr); height: clamp(360px, 58dvh, 560px); }
  .nmo__list { border-right: 0; }
  .nmo[data-pane="thread"] .nmo__list { display: none; }
  .nmo[data-pane="list"] .nmo__thread-pane { display: none; }
  .nmo__back { display: grid; width: 32px; height: 32px; flex: none; place-items: center; color: var(--muted); background: transparent; border: 1px solid var(--border); border-radius: var(--radius-control); cursor: pointer; }
  .nmo__msg { max-width: 82%; }
  .nmo__note-fields { grid-template-columns: minmax(0, 1fr); }
}
</style>
