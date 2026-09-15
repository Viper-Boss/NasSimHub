<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { meshCanvasLabel, meshNote, networkCardLabels } from '../i18n/networkCardNotes'
import { advance, buildField, type MeshField } from './dhtMeshField'
import { paintMesh } from './dhtMeshPainter'
import './networkCard.css'
import { getDHTSettings, type DHTStatus } from '../api/client'

// The DHT side of the pair: a fishnet filling the card, drifting slowly.
//
// The drawing, the copy and the layout are the operator's reference build of
// this card. Two things are this app's rather than the reference's:
//
//   the FIGURES are the real routing table, live links and LAN peers, and the
//   badge and both rows are the real DHT state. The reference had fixed demo
//   numbers and a button that toggled them.
//
//   the SIGNALS -- the amber beads -- only travel while the DHT is genuinely
//   ready. The reference ran them off a timer regardless, which is right for a
//   demo and would be invented telemetry here. With the DHT off, joining,
//   isolated or failed the net dims and nothing travels.
//
// What the picture does NOT claim, which matters because almost all of it is
// illustration: the net is a sample of a mesh drawn to fill the card. Where the
// nodes sit, which ones are tied together, how they drift and which ones are
// dim are all decorative. It is not this gateway's routing table -- a DHT has
// no directory, and this gateway does not know who most of the network is. The
// footnote says exactly that, and no figure is ever drawn on the picture.

const i18n = useI18n()
const { t } = i18n

// The note is a paragraph in nine languages that renders in one card. It loads
// with this card rather than with the app, and merges synchronously so the
// first paint has it instead of briefly showing a raw key name.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, {
      dashboard: {
        ...networkCardLabels(locale),
        meshNote: meshNote(locale),
        meshCanvasLabel: meshCanvasLabel(locale)
      }
    })
  },
  { immediate: true, flush: 'sync' }
)

const dht = ref<DHTStatus | null>(null)
let timer: ReturnType<typeof setInterval> | undefined

async function load(): Promise<void> {
  try {
    dht.value = (await getDHTSettings()).status
  } catch {
    dht.value = null
  }
}

const reducedMotion = typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches

const ready = computed(() => dht.value?.state === 'ready')

const canvas = ref<HTMLCanvasElement | null>(null)
let field: MeshField | null = null
let builtFor = ''
let frame = 0
let lastFrameAt = 0
let time = 0

function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined' || !canvas.value) return fallback
  const value = getComputedStyle(canvas.value).getPropertyValue(name).trim()
  return value || fallback
}

function render(): void {
  const element = canvas.value
  if (!element) return
  const context = element.getContext('2d')
  if (!context) return
  const width = element.clientWidth
  const height = element.clientHeight
  if (width <= 0 || height <= 0) return

  // The backing store is sized from BOTH dimensions, and each is compared on
  // its own. The earlier version only compared the width and set the height
  // inside that branch, so a stage that changed height without changing width
  // kept a stale store and the browser stretched the drawing to fit -- which is
  // exactly what the narrow-card breakpoint does to this stage (365px to
  // 300px).
  const ratio = Math.min(window.devicePixelRatio || 1, 2)
  const storeWidth = Math.round(width * ratio)
  const storeHeight = Math.round(height * ratio)
  if (element.width !== storeWidth || element.height !== storeHeight) {
    element.width = storeWidth
    element.height = storeHeight
  }

  // Cleared in DEVICE space, over the whole store, rather than over the CSS box
  // under the scale transform. `clientWidth` is an integer while the element's
  // box is fractional, so a CSS-space clear can leave a sliver of the store
  // untouched -- and anything left there is composited again every frame.
  context.setTransform(1, 0, 0, 1, 0, 0)
  context.clearRect(0, 0, element.width, element.height)
  context.setTransform(ratio, 0, 0, ratio, 0, 0)

  // Rebuilt only when the size actually changed. Rebuilding on every resize
  // callback reshuffles the net while the reader is dragging the window, and a
  // net that reshuffles reads as a glitch rather than as a network.
  const size = `${Math.round(width)}x${Math.round(height)}`
  if (!field || builtFor !== size) {
    field = buildField(width, height)
    builtFor = size
  }

  advance(field, time, ready.value)

  paintMesh(context, field, {
    width,
    height,
    time,
    strength: ready.value ? 1 : 0.22,
    palette: {
      strand: cssVar('--mesh-strand', '124 169 217'),
      nodeBloom: cssVar('--mesh-node-bloom', '120 191 239'),
      nodeCore: cssVar('--mesh-node-core', '161 215 249'),
      ring: cssVar('--mesh-ring', '143 225 255'),
      signal: cssVar('--mesh-signal', '255 214 124'),
      signalCore: cssVar('--mesh-signal-core', '255 228 167')
    }
  })
}

function tick(now: number): void {
  // Seconds, clamped: a tab that was in the background for a minute must not
  // jump the net a minute forward when it comes back.
  if (lastFrameAt) time += Math.min((now - lastFrameAt) / 1000, 0.05)
  lastFrameAt = now
  render()
  frame = requestAnimationFrame(tick)
}

let observer: ResizeObserver | undefined

onMounted(() => {
  void load()
  timer = setInterval(() => { void load() }, 60_000)
  if (typeof ResizeObserver === 'function' && canvas.value) {
    observer = new ResizeObserver(() => render())
    observer.observe(canvas.value)
  }
  // A reader who asked for no motion gets the picture, still. It carries the
  // same information -- the information is in the figures and the rows.
  if (reducedMotion) render()
  else frame = requestAnimationFrame(tick)
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  if (frame) cancelAnimationFrame(frame)
  observer?.disconnect()
})

watch(ready, () => { if (reducedMotion) render() })

// The state names come from the DHT settings panel's own keys rather than a
// parallel set. One state, one translation: a second copy is a second thing to
// keep in step, and the first time they drift the two screens disagree about
// what the same DHT is doing.
const badgeLabel = computed(() => {
  switch (dht.value?.state) {
    case 'ready': return t('dashboard.meshBadgeHealthy')
    case 'isolated': return t('dht.stateIsolated')
    case 'starting': return t('dht.stateStarting')
    case 'error': return t('dht.stateError')
    case 'disabled': return t('dashboard.meshBadgeOff')
    case 'unavailable': return t('dht.stateUnavailable')
    default: return t('dht.statePending')
  }
})

const discoveryLabel = computed(() =>
  ready.value ? t('dashboard.meshDiscoveryOn') : t('dashboard.meshDiscoveryOff')
)

const secondPathLabel = computed(() => {
  switch (dht.value?.state) {
    case 'ready': return t('dashboard.meshSecondOn')
    case 'disabled': return t('dashboard.globeDhtOff')
    default: return badgeLabel.value
  }
})

function metric(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—'
  return value.toLocaleString(i18n.locale.value)
}
</script>

<template>
  <article class="net-card mesh-card" :data-state="ready ? 'online' : 'offline'">
    <header class="net-card__head">
      <div>
        <div class="net-card__title">
          <svg
            class="net-card__icon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.4"
            aria-hidden="true"
          >
            <circle cx="5" cy="12" r="2.5" />
            <circle cx="18" cy="5" r="2.5" />
            <circle cx="18" cy="19" r="2.5" />
            <path d="m7 11 9-5M7 13l9 5M18 8v8" />
          </svg>
          <h4>{{ t('dashboard.meshTitle') }}</h4>
        </div>
        <p class="net-card__eyebrow">SELF-ORGANIZING MESH</p>
      </div>
      <span class="net-card__badge">{{ badgeLabel }}</span>
    </header>

    <div class="net-stage">
      <canvas ref="canvas" :aria-label="t('dashboard.meshCanvasLabel')" role="img" />
      <p class="net-note">{{ t('dashboard.meshVisualNote') }}</p>
      <p v-if="ready" class="net-note net-note--right net-note--signal">
        {{ t('dashboard.meshSignalNote') }}
      </p>
    </div>

    <div class="net-card__body">
      <div class="net-row" :class="{ 'is-off': !ready }">
        <strong>
          <i class="net-row__dot" aria-hidden="true" />{{ t('dashboard.meshDiscoveryName') }}
        </strong>
        <span>{{ discoveryLabel }}</span>
      </div>
      <div class="net-row" :class="{ 'is-off': !ready }">
        <strong>
          <i class="net-row__dot is-green" aria-hidden="true" />{{ t('dashboard.meshSecondName') }}
        </strong>
        <span>{{ secondPathLabel }}</span>
      </div>

      <dl class="net-stats">
        <div class="net-stat">
          <dt>{{ t('dashboard.meshRouting') }}</dt>
          <dd>{{ metric(dht?.routing_peers) }}</dd>
        </div>
        <div class="net-stat">
          <dt>{{ t('dashboard.meshConnected') }}</dt>
          <dd>{{ metric(dht?.connected_peers) }}</dd>
        </div>
        <div class="net-stat">
          <dt>{{ t('dashboard.meshLan') }}</dt>
          <dd>{{ metric(dht?.lan_peers) }}</dd>
        </div>
      </dl>

      <p class="net-card__note">{{ t('dashboard.meshNote') }}</p>
    </div>
  </article>
</template>

<style scoped>
/* The card's frame, head, stage, rows, tiles and note all come from
   networkCard.css, which the globe beside it imports too. What is left here is
   only the net's own colours, read by the draw code as tokens.

   Two roles, and they must stay separable because one is a claim and one is
   not: blue is the net, which is illustration, and amber is traffic between
   peers, which is measured. Anything that blurs those reads as the card
   claiming more than it knows.

   Space-separated channels, not colours: the draw code composes the alpha per
   strand and per node, and `rgb(R G B / A%)` needs the channels on their own. */

.mesh-card canvas {
  --mesh-strand: 124 169 217;
  --mesh-node-bloom: 120 191 239;
  --mesh-node-core: 161 215 249;
  --mesh-ring: 143 225 255;
  --mesh-signal: 255 214 124;
  --mesh-signal-core: 255 228 167;
}
</style>
