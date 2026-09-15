<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { globeCanvasLabel, globeScopeNote, networkCardLabels } from '../i18n/networkCardNotes'
import { buildGlobe, type SpherePoint } from './networkGlobeGeometry'
import { paintGlobe } from './networkGlobePainter'
import './networkCard.css'
import {
  getDHTSettings,
  getNKNNetworkPeers,
  getNKNNetworkStatus,
  type DHTStatus,
  type NKNNetworkPeers
} from '../api/client'

// The NKN side of the pair: a slowly turning globe of dots.
//
// The drawing, the copy and the layout are the operator's reference build of
// this card. Two things are this app's rather than the reference's, and both
// are because this card is wired to a live gateway where the reference had
// fixed demo numbers:
//
//   the FIGURES are the real NKN topic counts, and the two status rows and the
//   badge are the real channel states. Nothing on this card is invented.
//
//   the MARKERS -- the three pulsing rings riding the sphere -- are drawn only
//   while NKN is actually active, and the whole globe dims when it is not. A
//   marker pulsing on a disconnected globe reads as activity, and that is a
//   claim. The continents are decorative either way: this gateway never learns
//   where a peer is, so no dot on this globe is anybody's location.

const i18n = useI18n()
const { t } = i18n

// The scope note is a paragraph in nine languages shown in one card. It loads
// with the dashboard rather than with the app; see networkCardNotes.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, {
      dashboard: {
        ...networkCardLabels(locale),
        globeScopeNote: globeScopeNote(locale),
        globeCanvasLabel: globeCanvasLabel(locale)
      }
    })
  },
  { immediate: true, flush: 'sync' }
)

const peers = ref<NKNNetworkPeers | null>(null)
const dht = ref<DHTStatus | null>(null)
const nknState = ref<'unknown' | 'disabled' | 'joining' | 'active' | 'error'>('unknown')
let timer: ReturnType<typeof setInterval> | undefined

async function load(): Promise<void> {
  try {
    const status = await getNKNNetworkStatus()
    if (!status.settings.enabled) {
      nknState.value = 'disabled'
    } else {
      const runtime = status.runtime.state
      nknState.value = runtime === 'active' ? 'active' : runtime === 'error' ? 'error' : 'joining'
    }
  } catch {
    nknState.value = 'unknown'
  }
  try {
    peers.value = await getNKNNetworkPeers()
  } catch {
    peers.value = null
  }
  try {
    dht.value = (await getDHTSettings()).status
  } catch {
    dht.value = null
  }
}

const reducedMotion = typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches

const nknActive = computed(() => nknState.value === 'active')
// Only a routing table with peers in it can carry a lookup. "isolated" means
// the host runs but has found nobody.
const dhtReady = computed(() => dht.value?.state === 'ready')

const canvas = ref<HTMLCanvasElement | null>(null)
// Sampled once. The land test is a point against a dozen polygons, and doing
// it per frame for five thousand points is the one thing here expensive enough
// to drop frames on a NAS-class browser.
const points: SpherePoint[] = buildGlobe()
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

  paintGlobe(context, points, {
    width,
    height,
    time,
    // Dimmed while the channel is down, and the markers go with it.
    strength: nknActive.value ? 1 : 0.42,
    palette: {
      halo: cssVar('--globe-halo', '#5dbfe5'),
      dot: cssVar('--globe-dot', '133 208 245'),
      marker: cssVar('--globe-marker', '#a4e9ff'),
      bodyNear: cssVar('--globe-body-near', 'rgb(32 73 101 / 13%)'),
      bodyFar: cssVar('--globe-body-far', 'rgb(9 23 40 / 20%)'),
      rim: cssVar('--globe-rim', 'rgb(120 205 239 / 13%)')
    }
  })
}

function tick(now: number): void {
  // Seconds, clamped: a tab that was in the background for a minute must not
  // jump the globe a minute forward when it comes back.
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

// Repaint when the state changes, so a still picture still follows the channel.
watch(nknActive, () => { if (reducedMotion) render() })

const stateLabel = computed(() => {
  switch (nknState.value) {
    case 'active': return t('dashboard.globeBadgeOn')
    case 'joining': return t('homesim.nknBadge.state_joining')
    case 'disabled': return t('homesim.nknBadge.state_disabled')
    case 'error': return t('homesim.nknBadge.state_error')
    default: return t('dashboard.globeBadgeOff')
  }
})

const pathLabel = computed(() =>
  nknActive.value ? t('dashboard.globePathOk') : t('dashboard.globePathWaiting')
)

// The second row reports the DHT, and it is the real state rather than a fixed
// "available" line: off, joining, isolated and failed all look different to an
// operator, and they are the states this row exists to show.
const dhtStateLabel = computed(() => {
  switch (dht.value?.state) {
    case 'ready': return t('dashboard.globeSecondReady')
    case 'isolated': return t('dashboard.globeDhtIsolated')
    case 'starting': return t('dashboard.globeDhtStarting')
    case 'error': return t('dashboard.globeDhtError')
    case 'disabled': return t('dashboard.globeDhtOff')
    default: return t('dashboard.globeDhtPending')
  }
})

function metric(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—'
  return value.toLocaleString(i18n.locale.value)
}
</script>

<template>
  <article class="net-card globe-card" :data-state="nknActive ? 'online' : 'offline'">
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
            <circle cx="12" cy="12" r="9" />
            <ellipse cx="12" cy="12" rx="4" ry="9" />
            <path d="M3 12h18M5 6.5h14M5 17.5h14" />
          </svg>
          <h4>{{ t('dashboard.globeTitle') }}</h4>
        </div>
        <p class="net-card__eyebrow">GLOBAL CONNECTIVITY</p>
      </div>
      <span class="net-card__badge">{{ stateLabel }}</span>
    </header>

    <div class="net-stage">
      <canvas ref="canvas" :aria-label="t('dashboard.globeCanvasLabel')" role="img" />
      <!-- The caption follows the channel. The reference build had it fixed at
           "worldwide · always on", which is a claim, and it sat on the card
           unchanged while NKN was disconnected. -->
      <p class="net-note">
        {{ nknActive ? t('dashboard.globeVisualNote') : t('dashboard.globeVisualNoteOff') }}
      </p>
    </div>

    <div class="net-card__body">
      <div class="net-row" :class="{ 'is-off': !nknActive }">
        <strong><i class="net-row__dot" aria-hidden="true" />{{ t('dashboard.globePathName') }}</strong>
        <span>{{ pathLabel }}</span>
      </div>
      <div class="net-row" :class="{ 'is-off': !dhtReady }">
        <strong>
          <i class="net-row__dot is-green" aria-hidden="true" />{{ t('dashboard.globeSecondName') }}
        </strong>
        <span>{{ dhtStateLabel }}</span>
      </div>

      <dl class="net-stats">
        <div class="net-stat">
          <dt>{{ t('dashboard.globeActive24h') }}</dt>
          <dd>{{ metric(peers?.active_24h) }}</dd>
        </div>
        <div class="net-stat">
          <dt>{{ t('dashboard.globeActive30d') }}</dt>
          <dd>{{ metric(peers?.active_month) }}</dd>
        </div>
        <div class="net-stat">
          <dt>{{ t('dashboard.globeActive90d') }}</dt>
          <dd>{{ metric(peers?.active_90d) }}</dd>
        </div>
      </dl>

      <p class="net-card__note">{{ t('dashboard.globeScopeNote') }}</p>
    </div>
  </article>
</template>

<style scoped>
/* The card's frame, head, stage, rows, tiles and note all come from
   networkCard.css, which the DHT card beside it imports too. What is left here
   is only the globe's own colours, read by the draw code as tokens so both
   themes stay consistent with the rest of the app. */

.globe-card canvas {
  --globe-halo: #5dbfe5;
  /* Space-separated channels, not a colour: the draw code composes the alpha
     per dot, and `rgb(R G B / A%)` needs the channels on their own. */
  --globe-dot: 133 208 245;
  --globe-marker: #a4e9ff;
  --globe-body-near: rgb(32 73 101 / 13%);
  --globe-body-far: rgb(9 23 40 / 20%);
  --globe-rim: rgb(120 205 239 / 13%);
}
</style>
