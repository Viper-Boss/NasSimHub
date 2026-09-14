<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Share2 } from '@lucide/vue'
import { meshNote } from '../i18n/networkCardNotes'
import {
  buildField,
  buildSky,
  litTarget,
  reconcileLit,
  seeded,
  step,
  type MeshField,
  type SkyStar
} from './dhtMeshField'
import { paintMesh } from './dhtMeshPainter'
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
// be guesswork. Three layers, and only one of them reports anything:
//
//   the SKY (stars, nebula) is scenery. It carries no network meaning at all,
//   it may twinkle, and it is drawn as tiny ringless points so it cannot be
//   confused with a peer.
//
//   the MESH (dim nodes and the lines between them) is the shape of a DHT: no
//   centre, every node knowing a few others. It is fixed scenery too -- this
//   gateway does not know who those peers are -- so it never claims traffic.
//   Its lines shimmer, slowly and all together, which is ambient rather than
//   directional: nothing appears to travel along them.
//
//   the LIT nodes and the SIGNALS are the only claims. One lit node per peer
//   this gateway has actually verified, and a signal only ever travels between
//   two lit nodes. With the DHT off or isolated nothing is lit and nothing
//   travels -- the sky stays, the traffic does not.
//
// The field does not turn. An earlier version rotated slowly like the globe
// beside it; on the operator's request it now holds still and the life in the
// picture comes from the connections instead of from the camera.

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
const sky: SkyStar[] = buildSky()
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
    // Zero: the field holds still. The rotation machinery stays in the field
    // module because its frame-rate independence and its bounds are tested
    // there, and a future picture may want it -- but this one does not turn.
    spin: 0,
    target: litTarget(routingPeers.value, field.nodes.length),
    animate: !reducedMotion,
    random
  })

  paintMesh(context, field, sky, {
    width,
    height,
    now,
    reducedMotion,
    palette: {
      hue: cssVar('--dht-hue', '#b07cff'),
      dim: cssVar('--mesh-dim', 'rgb(150 188 246 / 72%)'),
      signal: cssVar('--mesh-signal', '#ffc542'),
      deep: cssVar('--sky-deep', '#050916'),
      cloudCool: cssVar('--sky-cloud-cool', 'rgb(38 72 148 / 30%)'),
      cloudWarm: cssVar('--sky-cloud-warm', 'rgb(94 56 148 / 26%)'),
      starCool: cssVar('--star-cool', '226 236 255'),
      starWarm: cssVar('--star-warm', '255 212 168')
    }
  })

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
  /* The sky is painted corner to corner and rounded HERE rather than in the
     canvas: a hairline drawn in canvas cannot meet a pale panel at sub-pixel
     precision, and the seam reads as a bright ring around the picture. The
     browser composites this exactly. */
  border-radius: 12px;
  box-shadow: inset 0 0 0 1px var(--sky-frame);

  /* The palette has three roles and they must stay separable, because two of
     them are claims and one is not: the sky is scenery, violet is a verified
     peer, amber is traffic between two of them. Anything that blurs those
     reads as the card claiming more than it knows.

     The sky is nearly black in both themes, and that is deliberate. A light
     sky with light stars has nowhere to put a star, so the picture would stop
     being a sky and go back to being a diagram. It dissolves to transparent at
     the edges (see the draw code) so a dark disc on a light card reads as a
     view into space rather than a hole punched in the panel. */
  --sky-deep: #050916;
  --sky-cloud-cool: rgb(38 72 148 / 30%);
  --sky-cloud-warm: rgb(94 56 148 / 26%);
  /* Space-separated channels, not a colour: the draw code composes the alpha
     per star, and `rgb(R G B / A%)` needs the channels on their own. */
  --star-cool: 226 236 255;
  --star-warm: 255 212 168;
  /* The mesh is tinted toward the app's blue rather than left neutral: a grey
     mesh reads as a wireframe diagram, a blue one as a lit network. */
  --mesh-dim: rgb(150 188 246 / 72%);
  --mesh-signal: #ffc542;
  --sky-frame: rgb(255 255 255 / 8%);
}

:root[data-theme='light'] .mesh-canvas,
html[data-theme='light'] .mesh-canvas {
  /* Lifted off black and pulled in slightly, so on a light page it reads as an
     inset porthole rather than a void. The stars keep their brightness -- they
     are what the sky is for. */
  --sky-deep: #0a1226;
  --sky-cloud-cool: rgb(48 88 172 / 34%);
  --sky-cloud-warm: rgb(104 64 160 / 28%);
  --mesh-dim: rgb(168 200 250 / 66%);
  --mesh-signal: #ffc861;
  /* A dark edge, not a light one: the same faint white line that reads as a
     highlight on a dark card reads as a bright ring on a pale panel. */
  --sky-frame: rgb(16 28 56 / 24%);
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
