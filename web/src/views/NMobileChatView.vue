<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Send } from '@lucide/vue'
import {
  getNKNChatMessages,
  sendNKNChatMessage,
  type NKNChatMessage
} from '../api/client'
import PageContentFrame from '../components/PageContentFrame.vue'

const { t } = useI18n()

const state = reactive({
  messages: [] as NKNChatMessage[],
  peer: '',
  draft: '',
  loading: false,
  sending: false,
  error: '',
  loaded: false
})

const activePeer = ref('')
let timer: ReturnType<typeof setInterval> | undefined

const grouped = computed(() => {
  const byPeer = new Map<string, NKNChatMessage[]>()
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

function formatTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString()
}

async function load(): Promise<void> {
  if (state.loading) return
  state.loading = true
  state.error = ''
  try {
    const latest = state.messages.at(-1)?.ts ?? 0
    const incoming = await getNKNChatMessages(latest)
    if (incoming.length) {
      const known = new Set(state.messages.map(item => `${item.ts}|${item.dir}|${item.peer}|${item.text}`))
      for (const item of incoming) {
        if (!known.has(`${item.ts}|${item.dir}|${item.peer}|${item.text}`)) state.messages.push(item)
      }
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
  try {
    await sendNKNChatMessage(target, text)
    state.draft = ''
    activePeer.value = target
    await load()
  } catch (reason) {
    state.error = reason instanceof Error ? reason.message : String(reason)
  } finally {
    state.sending = false
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => { void load() }, 10_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <PageContentFrame>
    <div class="nmobile">
      <header class="nmobile__head">
        <h1>{{ t('nmobile.title') }}</h1>
        <p>{{ t('nmobile.subtitle') }}</p>
      </header>

      <div class="nmobile__compose">
        <input
          v-model="state.peer"
          class="ui-input"
          type="text"
          :placeholder="t('nmobile.peerPlaceholder')"
          :aria-label="t('nmobile.peerPlaceholder')"
          spellcheck="false"
        >
        <div class="nmobile__compose-row">
          <textarea
            v-model="state.draft"
            class="ui-input nmobile__draft"
            rows="2"
            :placeholder="t('nmobile.messagePlaceholder')"
            :aria-label="t('nmobile.messagePlaceholder')"
            @keydown.enter.exact.prevent="send"
          />
          <button class="primary-action" type="button" :disabled="state.sending || !state.draft.trim() || !state.peer.trim()" @click="send">
            <Send :size="16" />{{ t('nmobile.send') }}
          </button>
        </div>
      </div>

      <p v-if="state.error" class="field-error" role="alert">{{ state.error }}</p>

      <div v-if="state.loaded && grouped.length === 0" class="nmobile__empty">{{ t('nmobile.empty') }}</div>
      <div v-for="thread in grouped" :key="thread.addr" class="nmobile__thread">
        <button class="nmobile__peer" type="button" @click="activePeer = activePeer === thread.addr ? '' : thread.addr">{{ thread.addr }}</button>
        <template v-if="!activePeer || activePeer === thread.addr">
          <div v-for="(item, index) in thread.items" :key="`${item.ts}-${index}`" class="nmobile__msg" :class="`nmobile__msg--${item.dir}`">
            <span class="nmobile__bubble">{{ item.text }}</span>
            <time class="nmobile__time">{{ formatTime(item.ts) }}</time>
          </div>
        </template>
      </div>
    </div>
  </PageContentFrame>
</template>

<style scoped>
.nmobile { display: grid; gap: 14px; }
.nmobile__head h1 { font-size: 20px; }
.nmobile__head p { margin-top: 4px; color: var(--muted); font-size: 13px; }
.nmobile__compose { display: grid; gap: 8px; padding: 14px 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.nmobile__compose-row { display: flex; gap: 8px; align-items: flex-end; }
.nmobile__draft { flex: 1 1 auto; resize: vertical; min-height: 44px; }
.nmobile__empty { padding: 30px; text-align: center; color: var(--muted); font-size: 13px; }
.nmobile__thread { display: grid; gap: 6px; }
.nmobile__peer {
  justify-self: start;
  padding: 2px 10px;
  font-size: 11.5px;
  color: var(--muted);
  border: 1px solid var(--border);
  border-radius: 999px;
  background: transparent;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.nmobile__msg { display: flex; flex-direction: column; gap: 2px; }
.nmobile__msg--in { align-items: flex-start; }
.nmobile__msg--out { align-items: flex-end; }
.nmobile__bubble {
  max-width: 72%;
  padding: 8px 12px;
  font-size: 13.5px;
  border-radius: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.nmobile__msg--out .nmobile__bubble { background: var(--accent-soft, var(--surface-subtle)); }
.nmobile__time { font-size: 10.5px; color: var(--muted); }
</style>
