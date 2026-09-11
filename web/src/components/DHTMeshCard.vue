<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Share2 } from '@lucide/vue'
import { meshNote } from '../i18n/networkCardNotes'
import {
  buildField,
  litTarget,
  pointOnLink,
  project,
  reconcileLit,
  seeded,
  step,
  type MeshField,
  type Projected
} from './dhtMeshField'
import { getDHTSettings, type DHTStatus } from '../api/client'

// The DHT's own picture, beside the globe rather than sharing it.
//
// The two are different shapes of network and deserve different shapes of
// drawing. NKN is a published address on a chain, so a globe with beams from
// here to there is the right picture. A DHT has no centre: what this machine
// has is a routing table of peers that know other peers, and the honest
// picture of that is a mesh with no middle.
//
// What is real and what is scenery, stated plainly because it would otherwise
// be guesswork: the LIT nodes count the routing table -- one lit node per peer
// this gateway has actually verified -- and signals only ever travel between
// two lit nodes. The dim nodes are the rest of the DHT, which this gateway has
// not mapped and makes no claim about. When the DHT is off or isolated nothing
// is lit and nothing moves.

const i18n = useI18n()
const { t } = i18n

// The note is a paragraph in nine languages that renders in one card. It loads
// with this card rather than with the app, and merges synchronously so the
// first paint has it instead of briefly showing a raw key name.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, { dashboard: { meshNote: meshNote(locale) } })
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
const routingPeers = computed(() => (ready.value ? dht.value?.routing_peers ?? 0 : 0))

const canvas = ref<HTMLCanvasElement | null>(null)
const field: MeshField = buildField()
const random = seeded(0xbeef)
let frame = 0
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
  const width = element.clientWidth
  const height = element.clientHeight
  if (width <= 0 || height <= 0) {
    frame = requestAnimationFrame(draw)
    return
  }
  if (element.width !== Math.round(width * ratio)) {
    element.width = Math.round(width * ratio)
    element.height = Math.round(height * ratio)
  }
  context.setTransform(ratio, 0, 0, ratio, 0, 0)
  context.clearRect(0, 0, width, height)

  const elapsed = lastFrameAt ? Math.min(now - lastFrameAt, 100) : 16
  lastFrameAt = now

  step(field, {
    elapsed,
    // Slow enough that you notice it has moved rather than watch it move.
    spin: 0.085,
    target: litTarget(routingPeers.value, field.nodes.length),
    animate: !reducedMotion,
    random
  })

  const hue = cssVar('--dht-hue', '#b07cff')
  const dim = cssVar('--mesh-dim', 'rgb(150 170 210 / 26%)')
  const signalColour = cssVar('--mesh-signal', '#ffc542')
  const wash = cssVar('--mesh-wash', 'rgb(90 140 220 / 12%)')

  // A soft pool of light behind the field. Without it the mesh floats on a
  // flat panel; with it the far nodes sit in something and the depth cue the
  // projection provides actually lands.
  const pool = context.createRadialGradient(
    width / 2, height / 2, 0,
    width / 2, height / 2, Math.max(width, height) * 0.62
  )
  pool.addColorStop(0, wash)
  pool.addColorStop(1, 'rgba(0,0,0,0)')
  context.fillStyle = pool
  context.fillRect(0, 0, width, height)

  const projected: Projected[] = field.nodes.map(node =>
    project(node, field.rotation, width, height)
  )

  // Edges first, so nodes sit on top of them.
  for (const link of field.links) {
    const from = projected[link.from]!
    const to = projected[link.to]!
    const a = field.nodes[link.from]!
    const b = field.nodes[link.to]!
    const strength = Math.min(a.glow, b.glow)
    const depth = (from.depth + to.depth) / 2
    // Depth fades the far edges out, which is what keeps a mesh of this
    // density from reading as a flat tangle.
    context.globalAlpha = (0.3 + depth * 0.4) * (0.5 + strength * 0.5)
    context.strokeStyle = strength > 0.5 ? hue : dim
    context.lineWidth = 0.6 + depth * 0.5
    context.beginPath()
    context.moveTo(from.x, from.y)
    context.lineTo(to.x, to.y)
    context.stroke()
  }
  context.globalAlpha = 1

  // Nodes, far ones first so near ones overlap them correctly.
  const order = field.nodes
    .map((_, index) => index)
    .sort((first, second) => projected[first]!.depth - projected[second]!.depth)
  for (const index of order) {
    const node = field.nodes[index]!
    const point = projected[index]!
    const radius = node.size * point.scale
    // A node that is on, but still climbing to full brightness, breathes. Once
    // settled it holds steady -- the breathing is what says "joining", so it
    // has to stop when the joining is done.
    const settling = node.glow > 0.02 && node.glow < 0.985
    const breath = settling
      ? 0.55 + 0.45 * Math.sin((now / 620) + node.phase * Math.PI * 2)
      : 1
    const lit = node.glow * breath

    if (lit > 0.05) {
      context.globalAlpha = Math.min(1, lit * (0.45 + point.depth * 0.55))
      context.fillStyle = hue
      context.shadowColor = hue
      context.shadowBlur = 10 * lit
      context.beginPath()
      context.arc(point.x, point.y, radius * (1 + lit * 0.35), 0, Math.PI * 2)
      context.fill()
      context.shadowBlur = 0
      // The halo is what makes an arrival read from across the room.
      context.globalAlpha = Math.min(1, lit * 0.3)
      context.strokeStyle = hue
      context.lineWidth = 1
      context.beginPath()
      context.arc(point.x, point.y, radius * (2.4 + lit * 1.6), 0, Math.PI * 2)
      context.stroke()
    }
    // The unlit remainder is always drawn, including under a lit node, so a
    // node fading out lands back on the scenery rather than disappearing.
    // Two circles rather than a canvas shadow: a wide faint disc for the bloom
    // and a tight one for the core. Shadows on 130 nodes a frame are the one
    // thing in this drawing expensive enough to drop frames on a NAS-class
    // browser, and this reads the same.
    const base = (1 - node.glow) * (0.45 + point.depth * 0.55)
    context.fillStyle = dim
    context.globalAlpha = base * 0.22
    context.beginPath()
    context.arc(point.x, point.y, radius * 2.6, 0, Math.PI * 2)
    context.fill()
    context.globalAlpha = base
    context.beginPath()
    context.arc(point.x, point.y, radius * 0.95, 0, Math.PI * 2)
    context.fill()
  }
  context.globalAlpha = 1

  // Signals last, over everything, because they are the thing to look at.
  for (const signal of field.signals) {
    const link = field.links[signal.link]
    if (!link) continue
    const from = projected[link.from]!
    const to = projected[link.to]!
    const head = pointOnLink(from, to, signal)
    const trail = pointOnLink(from, to, {
      ...signal,
      progress: Math.max(0, signal.progress - 0.34)
    })
    // Bright at the start of the hop, fading as it lands, so the eye is caught
    // by the departure rather than a bead sliding along a wire.
    const fade = 1 - Math.pow(signal.progress, 2.2)

    const gradient = context.createLinearGradient(trail.x, trail.y, head.x, head.y)
    gradient.addColorStop(0, signalColour + '00')
    gradient.addColorStop(1, signalColour)
    context.globalAlpha = fade
    context.strokeStyle = gradient
    context.lineWidth = 1.9
    context.lineCap = 'round'
    context.beginPath()
    context.moveTo(trail.x, trail.y)
    context.lineTo(head.x, head.y)
    context.stroke()

    context.fillStyle = signalColour
    context.shadowColor = signalColour
    context.shadowBlur = 12
    context.beginPath()
    context.arc(head.x, head.y, 2.2, 0, Math.PI * 2)
    context.fill()
    context.shadowBlur = 0
  }
  context.globalAlpha = 1

  frame = requestAnimationFrame(draw)
}

onMounted(() => {
  void load().then(() => {
    // Seed the opening picture with the peers already known, rather than
    // watching them trickle in one at a time on every page load. Arrivals
    // after this are real changes.
    reconcileLit(field, litTarget(routingPeers.value, field.nodes.length), random, field.nodes.length)
  })
  timer = setInterval(() => { void load() }, 60_000)
  frame = requestAnimationFrame(draw)
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  if (frame) cancelAnimationFrame(frame)
})

// The state names come from the DHT settings panel's own keys rather than a
// parallel set. One state, one translation: a second copy is a second thing to
// keep in step, and the first time they drift the two screens disagree about
// what the same DHT is doing.
const stateLabel = computed(() => {
  switch (dht.value?.state) {
    case 'ready': return t('dht.stateReady')
    case 'isolated': return t('dht.stateIsolated')
    case 'starting': return t('dht.stateStarting')
    case 'error': return t('dht.stateError')
    case 'disabled': return t('dht.stateDisabled')
    case 'unavailable': return t('dht.stateUnavailable')
    default: return t('dht.statePending')
  }
})

function metric(value: number | null | undefined): string {
  return value === null || value === undefined ? '—' : String(value)
}
</script>

<template>
  <article class="mesh-card">
    <header class="mesh-card__head">
      <Share2 :size="15" />
      <h4>{{ t('dashboard.meshTitle') }}</h4>
      <span class="mesh-card__state" :class="{ 'is-live': ready }">{{ stateLabel }}</span>
    </header>

    <div class="mesh-card__stage">
      <canvas ref="canvas" class="mesh-canvas" :aria-label="t('dashboard.meshTitle')" role="img" />
    </div>

    <dl class="mesh-stats">
      <div>
        <dt>{{ t('dashboard.meshRouting') }}</dt>
        <dd>{{ metric(dht?.routing_peers) }}</dd>
      </div>
      <div>
        <dt>{{ t('dashboard.meshConnected') }}</dt>
        <dd>{{ metric(dht?.connected_peers) }}</dd>
      </div>
      <div>
        <dt>{{ t('dashboard.meshLan') }}</dt>
        <dd>{{ metric(dht?.lan_peers) }}</dd>
      </div>
    </dl>

    <p class="mesh-card__note">{{ t('dashboard.meshNote') }}</p>
  </article>
</template>

<style scoped>
.mesh-card {
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

.mesh-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}

.mesh-card__head h4 {
  margin: 0;
  color: var(--text);
  font-size: 13px;
}

.mesh-card__state {
  margin-left: auto;
  color: var(--faint);
  font-size: 11px;
}

.mesh-card__state.is-live { color: var(--dht-hue, #b07cff); }

/* Same as the globe beside it: the picture absorbs the row's extra height so
   the two cards stay visually matched. */
.mesh-card__stage {
  position: relative;
  display: grid;
  flex: 1 1 auto;
  place-items: center;
  padding: 4px 0;
}

.mesh-canvas {
  display: block;
  width: 100%;
  aspect-ratio: 1;
  /* Capped to match the globe's drawing beside it. Without this the square
     grows with the card, and on a wide single-column row the mesh card ends up
     far taller than the globe -- which then stretches to match and carries the
     difference as dead space. */
  max-width: 320px;
  /* The scenery is tinted toward the app's blue rather than left neutral: a
     grey mesh reads as a wireframe diagram, a blue one as a lit network. */
  --mesh-dim: rgb(140 180 240 / 70%);
  --mesh-wash: rgb(70 130 220 / 15%);
  --mesh-signal: #ffc542;
}

:root[data-theme='light'] .mesh-canvas,
html[data-theme='light'] .mesh-canvas {
  --mesh-dim: rgb(60 100 160 / 45%);
  --mesh-wash: rgb(70 130 220 / 8%);
  --mesh-signal: #b26a00;
}

.mesh-stats {
  display: grid;
  margin: 0;
  gap: 8px;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: 10px;
  background: var(--surface-subtle);
  border-radius: var(--radius-md);
}

.mesh-stats div { display: grid; gap: 2px; text-align: center; }
.mesh-stats dt { color: var(--muted); font-size: 10.5px; }
.mesh-stats dd {
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.mesh-card__note {
  margin: 0;
  color: var(--faint);
  font-size: 10.5px;
  line-height: 1.6;
}
</style>
