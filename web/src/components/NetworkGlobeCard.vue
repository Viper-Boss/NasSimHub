<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Globe } from '@lucide/vue'
import {
  arcAltitude,
  arcOpacity,
  greatCircle,
  isLand,
  project,
  spherePoints,
  type LatLon
} from './networkGlobeGeometry'
import {
  getDHTSettings,
  getNKNNetworkPeers,
  getNKNNetworkStatus,
  type DHTStatus,
  type NKNNetworkPeers
} from '../api/client'

// The two decentralised control paths, drawn on one globe.
//
// Both are real: NKN beams follow the live peer count, DHT beams follow the
// libp2p routing table. Neither is ever drawn from invented data — a channel
// that is off, isolated or unavailable shows no beams and says which, because
// a globe full of animated traffic that corresponds to nothing is worse than
// no globe at all.

const { t } = useI18n()

const peers = ref<NKNNetworkPeers | null>(null)
const dht = ref<DHTStatus | null>(null)
const nknState = ref<'unknown' | 'disabled' | 'joining' | 'active' | 'error'>('unknown')
const loadError = ref('')
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
    loadError.value = ''
  } catch (reason) {
    loadError.value = reason instanceof Error ? reason.message : String(reason)
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

onMounted(() => {
  void load()
  timer = setInterval(() => { void load() }, 60_000)
  frame = requestAnimationFrame(draw)
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  if (frame) cancelAnimationFrame(frame)
})

const nknActive = computed(() => nknState.value === 'active')
// Only a routing table with peers in it can carry a lookup. "isolated" means
// the host runs but has found nobody, which must not draw traffic.
const dhtActive = computed(() => dht.value?.state === 'ready')

// Beam counts follow the live figures, so the picture says something true: a
// quiet channel draws few beams, a busy one more. Capped so the globe stays
// readable rather than turning into a ball of string.
function beamCount(seen: number, active: boolean): number {
  if (!active) return 0
  return Math.max(1, Math.min(4, Math.ceil(seen / 2)))
}

const nknArcCount = computed(() =>
  beamCount(peers.value?.active_24h ?? peers.value?.count ?? 0, nknActive.value)
)
const dhtArcCount = computed(() =>
  beamCount(dht.value?.routing_peers ?? 0, dhtActive.value)
)

// Node positions are fixed coordinates, not random: the globe has to look the
// same on every repaint, or a poll would make the picture jump for no reason.
// They are illustrative positions on the sphere, not the real location of any
// peer — the gateway never learns where its peers are, and inventing a map pin
// for one would be a lie dressed up as telemetry.
const NKN_NODES: LatLon[] = [
  { lat: 39, lon: 116 }, { lat: 51, lon: 0 }, { lat: 37, lon: -122 },
  { lat: -34, lon: 151 }, { lat: 1, lon: 104 }
]
const DHT_NODES: LatLon[] = [
  { lat: 35, lon: 139 }, { lat: 52, lon: 13 }, { lat: -23, lon: -46 },
  { lat: 19, lon: 73 }, { lat: 55, lon: 37 }
]
// Every link starts from this gateway. Its position is equally illustrative.
const HOME: LatLon = { lat: 31, lon: 121 }

interface Link {
  channel: 'nkn' | 'dht'
  path: LatLon[]
  /** Phase offset so the pulses do not all travel in lockstep. */
  phase: number
}

const links = computed<Link[]>(() => {
  const built: Link[] = []
  for (let index = 0; index < nknArcCount.value; index += 1) {
    built.push({
      channel: 'nkn',
      path: greatCircle(HOME, NKN_NODES[index % NKN_NODES.length]!),
      phase: index / Math.max(1, nknArcCount.value)
    })
  }
  for (let index = 0; index < dhtArcCount.value; index += 1) {
    built.push({
      channel: 'dht',
      path: greatCircle(HOME, DHT_NODES[index % DHT_NODES.length]!),
      phase: 0.5 + index / Math.max(1, dhtArcCount.value)
    })
  }
  return built
})

// Any beam at all means the card has something to show.
const arcs = links

const canvas = ref<HTMLCanvasElement | null>(null)
// Sampled densely enough for the coastlines to read at this size; the sea
// grid stays sparse so the land is what the eye picks out.
const landDots = spherePoints(9000).filter(isLand)
const seaDots = spherePoints(1400)
let frame = 0
let spin = 0
let lastFrameAt = 0

function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined' || !canvas.value) return fallback
  const value = getComputedStyle(canvas.value).getPropertyValue(name).trim()
  return value || fallback
}

function draw(now: number): void {
  const element = canvas.value
  if (!element) return
  const context = element.getContext('2d')
  if (!context) return

  const ratio = Math.min(window.devicePixelRatio || 1, 2)
  const size = element.clientWidth
  if (size <= 0) {
    frame = requestAnimationFrame(draw)
    return
  }
  if (element.width !== Math.round(size * ratio)) {
    element.width = Math.round(size * ratio)
    element.height = Math.round(size * ratio)
  }
  context.setTransform(ratio, 0, 0, ratio, 0, 0)
  context.clearRect(0, 0, size, size)

  const centre = size / 2
  const radius = size * 0.42

  // Rotation is time-based, not per-frame, so a slow frame does not slow the
  // globe down and a fast display does not speed it up.
  const elapsed = lastFrameAt ? Math.min(now - lastFrameAt, 100) : 16
  lastFrameAt = now
  if (!reducedMotion) spin = (spin + elapsed * 0.004) % 360

  const accent = cssVar('--accent', '#5ad1ff')
  const dhtHue = cssVar('--dht-hue', '#b07cff')
  const landColour = cssVar('--globe-land', 'rgba(150,185,225,0.55)')
  const seaColour = cssVar('--globe-sea', 'rgba(120,150,190,0.14)')

  // Rim glow, drawn first so everything else sits on top of it.
  const glow = context.createRadialGradient(centre, centre, radius * 0.72, centre, centre, radius * 1.06)
  glow.addColorStop(0, 'rgba(0,0,0,0)')
  glow.addColorStop(0.82, accent + '22')
  glow.addColorStop(1, 'rgba(0,0,0,0)')
  context.fillStyle = glow
  context.beginPath()
  context.arc(centre, centre, radius * 1.06, 0, Math.PI * 2)
  context.fill()

  for (const point of seaDots) {
    const projected = project(point, spin, centre, centre, radius)
    if (!projected.visible) continue
    context.fillStyle = seaColour
    context.fillRect(projected.x, projected.y, 1, 1)
  }
  for (const point of landDots) {
    const projected = project(point, spin, centre, centre, radius)
    if (!projected.visible) continue
    // Dots near the limb fade out, which is what gives the flat projection its
    // sense of curvature.
    context.globalAlpha = Math.min(1, projected.z * 1.6)
    context.fillStyle = landColour
    context.fillRect(projected.x - 0.6, projected.y - 0.6, 1.7, 1.7)
  }
  context.globalAlpha = 1

  const cycle = (now / 2600) % 1
  for (const link of links.value) {
    const colour = link.channel === 'nkn' ? accent : dhtHue
    drawLink(context, link, colour, spin, centre, radius, cycle)
  }

  frame = requestAnimationFrame(draw)
}

// A link is drawn segment by segment so the part behind the globe simply is not
// drawn, instead of a line cutting across the sphere.
function drawLink(
  context: CanvasRenderingContext2D,
  link: Link,
  colour: string,
  rotation: number,
  centre: number,
  radius: number,
  cycle: number
): void {
  // Links bow above the surface: an arc that hugs the sphere is hard to follow
  // once it crosses the terminator, and the lift is what reads as a hop rather
  // than a scratch on the globe.
  const last = link.path.length - 1
  const projectedPath = link.path.map((point, index) => {
    const flat = project(point, rotation, centre, centre, radius)
    const altitude = arcAltitude(index / Math.max(1, last))
    return {
      x: centre + (flat.x - centre) * altitude,
      y: centre + (flat.y - centre) * altitude,
      alpha: arcOpacity(flat.z)
    }
  })

  // Drawn in short runs rather than as one path, because each run carries its
  // own opacity: the beam has to dissolve as it goes over the horizon, and a
  // single stroke can only have one alpha.
  const drawRun = (from: number, to: number, width: number, glow: number, scale: number): void => {
    context.lineWidth = width
    context.strokeStyle = colour
    context.lineCap = 'round'
    context.shadowColor = colour
    context.shadowBlur = glow
    for (let index = from; index < to; index += 1) {
      const a = projectedPath[index]
      const b = projectedPath[index + 1]
      if (!a || !b) continue
      const alpha = Math.min(a.alpha, b.alpha) * scale
      if (alpha <= 0.02) continue
      context.globalAlpha = alpha
      context.beginPath()
      context.moveTo(a.x, a.y)
      context.lineTo(b.x, b.y)
      context.stroke()
    }
    context.globalAlpha = 1
    context.shadowBlur = 0
  }

  // The resting line, then the travelling pulse over the top of it.
  drawRun(0, last, 1.2, 0, 0.34)
  const head = ((cycle + link.phase) % 1) * last
  const tail = Math.max(0, head - last * 0.16)
  drawRun(Math.floor(tail), Math.min(last, Math.ceil(head)), 2.4, 9, 1)

  const endpoint = projectedPath[last]
  if (endpoint && endpoint.alpha > 0.2) {
    context.globalAlpha = endpoint.alpha
    context.fillStyle = colour
    context.beginPath()
    context.arc(endpoint.x, endpoint.y, 3.1, 0, Math.PI * 2)
    context.fill()
    context.strokeStyle = colour
    context.globalAlpha = endpoint.alpha * 0.4
    context.lineWidth = 1
    context.beginPath()
    context.arc(endpoint.x, endpoint.y, 6.4, 0, Math.PI * 2)
    context.stroke()
    context.globalAlpha = 1
  }
}

const dhtStateLabel = computed(() => {
  const state = dht.value?.state
  switch (state) {
    case 'ready': return t('dashboard.globeDhtReady', { count: dht.value?.routing_peers ?? 0 })
    case 'isolated': return t('dashboard.globeDhtIsolated')
    case 'starting': return t('dashboard.globeDhtStarting')
    case 'error': return t('dashboard.globeDhtError')
    case 'disabled': return t('dashboard.globeDhtOff')
    default: return t('dashboard.globeDhtPending')
  }
})

const stateLabel = computed(() => {
  switch (nknState.value) {
    case 'active': return t('homesim.nknBadge.state_active')
    case 'joining': return t('homesim.nknBadge.state_joining')
    case 'disabled': return t('homesim.nknBadge.state_disabled')
    case 'error': return t('homesim.nknBadge.state_error')
    default: return t('homesim.nknBadge.state_offline')
  }
})

function metric(value: number | null | undefined): string {
  return value === null || value === undefined ? '—' : String(value)
}
</script>

<template>
  <article class="globe-card">
    <header class="globe-card__head">
      <Globe :size="15" />
      <h4>{{ t('dashboard.globeTitle') }}</h4>
    </header>

    <div class="globe-card__stage">
      <canvas ref="canvas" class="globe-canvas" :aria-label="t('dashboard.globeTitle')" role="img" />
      <p v-if="!arcs.length" class="globe-card__idle">{{ stateLabel }}</p>
    </div>

    <ul class="globe-legend">
      <li class="globe-legend__row is-nkn">
        <span class="globe-legend__dot" aria-hidden="true" />
        <span class="globe-legend__name">NKN</span>
        <span class="globe-legend__state">{{ stateLabel }}</span>
      </li>
      <li class="globe-legend__row is-dht" :class="{ 'is-live': dhtActive }">
        <span class="globe-legend__dot" aria-hidden="true" />
        <span class="globe-legend__name">DHT</span>
        <span class="globe-legend__state">{{ dhtStateLabel }}</span>
      </li>
    </ul>

    <dl class="globe-stats">
      <div>
        <dt>{{ t('homesim.nknBadge.daily') }}</dt>
        <dd>{{ metric(peers?.active_24h) }}</dd>
      </div>
      <div>
        <dt>{{ t('homesim.nknBadge.monthly') }}</dt>
        <dd>{{ metric(peers?.active_month) }}</dd>
      </div>
      <div>
        <dt>{{ t('homesim.nknBadge.registered') }}</dt>
        <dd>{{ metric(peers?.active_90d) }}</dd>
      </div>
    </dl>

    <p class="globe-card__note">{{ t('dashboard.globeScopeNote') }}</p>
  </article>
</template>

<style scoped>
.globe-card {
  display: flex;
  width: 100%;
  max-width: 420px;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  box-shadow: var(--elevation-card-sm, none);
}

.globe-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}

.globe-card__head h4 {
  margin: 0;
  color: var(--text);
  font-size: 13px;
}

.globe-card__stage {
  position: relative;
  display: grid;
  place-items: center;
  padding: 4px 0;
}

.globe-canvas {
  display: block;
  width: 100%;
  max-width: 320px;
  aspect-ratio: 1;
  /* Dot and rim colours live here so the canvas can read them as tokens and
     both themes stay consistent with the rest of the app. */
  --globe-land: rgb(150 190 235 / 62%);
  --globe-sea: rgb(120 150 190 / 13%);
}

:root[data-theme='light'] .globe-canvas,
html[data-theme='light'] .globe-canvas {
  --globe-land: rgb(40 70 110 / 55%);
  --globe-sea: rgb(60 90 130 / 12%);
}

.globe-card__idle {
  position: absolute;
  margin: 0;
  color: var(--faint);
  font-size: 11.5px;
}

/* NKN beams. The faint path shows the route; the pulse travels along it. */
.globe-arcs > g { color: var(--accent); }
/* DHT gets its own hue so the two control paths are never confused for one
   another at a glance. */
.globe-arcs > g.is-dht { color: var(--dht-hue, #b07cff); }

.globe-arc {
  stroke: currentcolor;
  stroke-width: 1;
  opacity: 0.22;
}

.globe-arc__pulse {
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-dasharray: 18 250;
  animation: globe-travel 3.4s linear infinite;
}

.globe-node {
  fill: currentcolor;
  animation: globe-blip 3.4s ease-in-out infinite;
}

@keyframes globe-travel {
  from { stroke-dashoffset: 268; }
  to { stroke-dashoffset: 0; }
}

@keyframes globe-blip {
  0%, 55% { opacity: 0.25; r: 2.6; }
  70% { opacity: 1; r: 4.2; }
  100% { opacity: 0.25; r: 2.6; }
}

/* A still picture is the resting state: the beams stay visible, they just
   stop moving for anyone who asked not to see motion. */
@media (prefers-reduced-motion: reduce) {
  .globe-arc__pulse,
  .globe-node {
    animation: none;
  }
  .globe-arc__pulse { stroke-dasharray: none; opacity: 0.6; }
}

.globe-legend {
  display: grid;
  margin: 0;
  padding: 0;
  gap: 6px;
  list-style: none;
}

.globe-legend__row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11.5px;
}

.globe-legend__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentcolor;
}

.globe-legend__name {
  color: var(--text);
  font-weight: 650;
  letter-spacing: 0.03em;
}

.globe-legend__state {
  margin-left: auto;
  color: var(--muted);
}

.globe-legend__row.is-nkn { color: var(--accent); }
/* DHT is not implemented. It is drawn muted so the card shows the honest
   state of the second control path instead of inventing one. */
.globe-legend__row.is-dht { color: var(--faint); }
.globe-legend__row.is-dht.is-live { color: var(--dht-hue, #b07cff); }
.globe-legend__row.is-dht .globe-legend__name { color: var(--muted); }
.globe-legend__row.is-dht.is-live .globe-legend__name { color: var(--text); }

.globe-stats {
  display: grid;
  margin: 0;
  gap: 8px;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: 10px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.globe-stats div { display: grid; gap: 2px; text-align: center; }
.globe-stats dt { color: var(--muted); font-size: 10.5px; }
.globe-stats dd {
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.globe-card__note {
  margin: 0;
  color: var(--faint);
  font-size: 10.5px;
  line-height: 1.6;
}
</style>
