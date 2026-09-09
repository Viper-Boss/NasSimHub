<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, CheckCheck, Clock, Copy, Send } from '@lucide/vue'
import {
  getNKNChatMessages,
  sendNKNChatMessage,
  type NKNChatMessage
} from '../api/client'
import PageContentFrame from '../components/PageContentFrame.vue'

const { t } = useI18n()

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

const activePeer = ref('')
const chatWindow = ref<HTMLElement | null>(null)
const copiedPeer = ref('')
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
    .map(([addr, items]) => ({ addr, items }))
    .sort((a, b) => (b.items.at(-1)?.ts ?? 0) - (a.items.at(-1)?.ts ?? 0))
})

const activeThread = computed(() => {
  if (activePeer.value) {
    const found = grouped.value.find(entry => entry.addr === activePeer.value)
    if (found) return found.items
  }
  return grouped.value[0]?.items ?? []
})

const activeLabel = computed(() => {
  if (activePeer.value) return activePeer.value
  return grouped.value[0]?.addr ?? ''
})

function shortAddress(addr: string): string {
  if (addr.length <= 24) return addr
  return `${addr.slice(0, 14)}…${addr.slice(-8)}`
}

function formatTime(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function tickStatus(item: ChatItem): ChatStatus {
  if (item.dir !== 'out') return ''
  return item.status || 'sent'
}

function scrollToBottom(): void {
  void nextTick(() => {
    const box = chatWindow.value
    if (box) box.scrollTop = box.scrollHeight
  })
}

watch(() => activeThread.value.length, scrollToBottom)
watch(activePeer, scrollToBottom)

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
  const target = state.peer.trim()
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
  activePeer.value = target
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
    optimistic.text += '' // keep bubble; error banner explains the failure
  } finally {
    state.sending = false
    void load()
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

onMounted(() => {
  void load().then(scrollToBottom)
  timer = setInterval(() => { void load() }, 8_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <PageContentFrame>
    <div class="nmobile">
      <header class="nmobile__head">
        <h1>{{ t('homesim.nmobile.title') }}</h1>
        <p>{{ t('homesim.nmobile.subtitle') }}</p>
      </header>

      <!-- Conversation switcher -->
      <div v-if="grouped.length" class="nmobile__threads" role="tablist">
        <button
          v-for="thread in grouped"
          :key="thread.addr"
          class="nmobile__tab"
          :class="{ 'is-active': activeLabel === thread.addr }"
          type="button"
          :title="thread.addr"
          @click="activePeer = activePeer === thread.addr ? '' : thread.addr"
        >
          {{ shortAddress(thread.addr) }}
        </button>
      </div>

      <!-- Chat window -->
      <div class="nmobile__chat">
        <header v-if="activeLabel" class="nmobile__chat-head">
          <span class="nmobile__avatar" aria-hidden="true">{{ (activeLabel[0] ?? '?').toUpperCase() }}</span>
          <div class="nmobile__chat-peer">
            <strong>{{ shortAddress(activeLabel) }}</strong>
            <small>{{ activeLabel }}</small>
          </div>
          <button
            v-if="activeLabel"
            class="nmobile__copy"
            type="button"
            :title="t('homesim.nmobile.copyAddress')"
            @click="copyPeer(activeLabel)"
          >
            <Check v-if="copiedPeer === activeLabel" :size="14" />
            <Copy v-else :size="14" />
          </button>
        </header>

        <div ref="chatWindow" class="nmobile__window">
          <div v-if="state.loaded && grouped.length === 0" class="nmobile__empty">{{ t('homesim.nmobile.empty') }}</div>
          <div
            v-for="(item, index) in activeThread"
            :key="item.localId || `${item.ts}-${index}`"
            class="nmobile__row"
            :class="item.dir === 'out' ? 'is-out' : 'is-in'"
          >
            <span v-if="item.dir === 'in'" class="nmobile__avatar nmobile__avatar--sm" aria-hidden="true">{{ (item.peer[0] ?? '?').toUpperCase() }}</span>
            <div class="nmobile__msg">
              <span class="nmobile__bubble">{{ item.text }}</span>
              <span class="nmobile__meta">
                <time>{{ formatTime(item.ts) }}</time>
                <span v-if="item.dir === 'out'" class="nmobile__ticks" :title="t(`homesim.nmobile.status${tickStatus(item).charAt(0).toUpperCase() + tickStatus(item).slice(1)}`)">
                  <Clock v-if="tickStatus(item) === 'sending'" :size="13" />
                  <Check v-else-if="tickStatus(item) === 'sent'" :size="13" />
                  <CheckCheck v-else class="is-delivered" :size="13" />
                </span>
              </span>
            </div>
          </div>
        </div>

        <!-- Composer -->
        <div class="nmobile__compose">
          <input
            v-model="state.peer"
            class="ui-input nmobile__peer-input"
            type="text"
            :placeholder="t('homesim.nmobile.peerPlaceholder')"
            :aria-label="t('homesim.nmobile.peerPlaceholder')"
            spellcheck="false"
          >
          <div class="nmobile__compose-row">
            <textarea
              v-model="state.draft"
              class="ui-input nmobile__draft"
              rows="2"
              :placeholder="t('homesim.nmobile.messagePlaceholder')"
              :aria-label="t('homesim.nmobile.messagePlaceholder')"
              @keydown.enter.exact.prevent="send"
            />
            <button class="primary-button nmobile__send" type="button" :disabled="state.sending || !state.draft.trim() || !state.peer.trim()" @click="send">
              <Send :size="16" />
            </button>
          </div>
        </div>
      </div>

      <p v-if="state.error" class="field-error" role="alert">{{ state.error }}</p>
    </div>
  </PageContentFrame>
</template>

<style scoped>
.nmobile { display: grid; gap: 14px; }
.nmobile__head h1 { font-size: 20px; }
.nmobile__head p { margin-top: 4px; color: var(--muted); font-size: 13px; }

/* Conversation tabs */
.nmobile__threads {
  display: flex;
  gap: 8px;
  overflow-x: auto;
  padding-bottom: 2px;
}

.nmobile__tab {
  flex-shrink: 0;
  padding: 5px 14px;
  font-size: 12px;
  color: var(--muted);
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: transparent;
  cursor: pointer;
  transition: color var(--motion-fast) var(--ease-standard), border-color var(--motion-fast) var(--ease-standard);
}

.nmobile__tab:hover { color: var(--text); }

.nmobile__tab.is-active {
  color: var(--accent-strong, var(--accent));
  border-color: color-mix(in srgb, var(--accent) 50%, var(--border));
  background: color-mix(in srgb, var(--accent) 12%, transparent);
}

/* Chat card */
.nmobile__chat {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  background: var(--surface-subtle);
  overflow: hidden;
}

.nmobile__chat-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}

.nmobile__chat-peer { display: flex; flex-direction: column; min-width: 0; }
.nmobile__chat-peer strong { font-size: 13px; color: var(--text); }
.nmobile__chat-peer small {
  color: var(--muted);
  font-size: 10.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nmobile__copy {
  margin-left: auto;
  display: inline-flex;
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}

.nmobile__copy:hover { color: var(--text); }

/* Avatars */
.nmobile__avatar {
  flex-shrink: 0;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--accent) 22%, var(--surface));
  color: var(--accent-strong, var(--accent));
  font-size: 13px;
  font-weight: 700;
}

.nmobile__avatar--sm { width: 26px; height: 26px; font-size: 11px; }

/* Message window */
.nmobile__window {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 220px;
  max-height: 460px;
  overflow-y: auto;
  padding: 16px 14px;
}

.nmobile__empty {
  margin: auto;
  padding: 20px;
  text-align: center;
  color: var(--muted);
  font-size: 13px;
}

.nmobile__row { display: flex; gap: 8px; align-items: flex-end; }
.nmobile__row.is-out { flex-direction: row-reverse; }

.nmobile__msg {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-width: min(72%, 420px);
}

.nmobile__row.is-out .nmobile__msg { align-items: flex-end; }
.nmobile__row.is-in .nmobile__msg { align-items: flex-start; }

.nmobile__bubble {
  padding: 8px 12px;
  font-size: 13.5px;
  line-height: 1.45;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  border-radius: var(--radius-control);
}

.nmobile__row.is-in .nmobile__bubble {
  background: var(--surface);
  border: 1px solid var(--border);
  border-top-left-radius: var(--radius-xs);
  color: var(--text);
}

.nmobile__row.is-out .nmobile__bubble {
  background: var(--accent);
  color: #fff;
  border-top-right-radius: var(--radius-xs);
}

.nmobile__meta {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 10.5px;
  color: var(--muted);
}

.nmobile__ticks { display: inline-flex; align-items: center; }
.nmobile__ticks .is-delivered { color: #30a46c; }

/* Composer */
.nmobile__compose {
  display: grid;
  gap: 8px;
  padding: 12px 14px;
  border-top: 1px solid var(--border);
  background: var(--surface);
}

.nmobile__compose-row { display: flex; gap: 8px; align-items: flex-end; }
.nmobile__draft { flex: 1 1 auto; resize: none; min-height: 44px; }

.nmobile__send {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 46px;
  min-height: 44px;
  padding: 0;
}
</style>
