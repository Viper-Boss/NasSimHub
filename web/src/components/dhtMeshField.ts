// The geometry and life-cycle behind the DHT mesh animation.
//
// This lives apart from the component for one reason: the interesting parts --
// which nodes are linked, when a node wakes or goes dark, how a signal travels
// -- are decisions, and decisions inside a requestAnimationFrame callback can
// only be checked by watching the screen and hoping. Here they can be run on a
// fixed clock and asserted.
//
// Nothing here invents network data. The field is scenery whose LIT nodes are
// driven by a real peer count the caller passes in; the dim ones stand for the
// rest of the DHT, which this gateway genuinely has not mapped. That
// distinction is the whole reason the picture is allowed to exist.

/** A node's position in the slab, before projection. */
export interface MeshNode {
  x: number
  y: number
  z: number
  /** Radius on screen at z = 0, before the depth scale. */
  size: number
  /**
   * live is what the node is *becoming*: 1 while it belongs to the lit set, 0
   * while it is scenery. glow chases it, which is what produces the slow
   * breathing fade rather than a node blinking into existence.
   */
  live: number
  glow: number
  /** Phase offset so breathing nodes are not all in step. */
  phase: number
}

export interface MeshLink {
  from: number
  to: number
}

/** A signal in flight along one link. */
export interface MeshSignal {
  link: number
  /** 0 at the from-end, 1 at the to-end. */
  progress: number
  /** Travel time in milliseconds. */
  duration: number
  /** Direction of travel: 1 from→to, -1 to→from. */
  direction: 1 | -1
}

export interface MeshField {
  nodes: MeshNode[]
  links: MeshLink[]
  signals: MeshSignal[]
  /** Accumulated rotation, in radians. */
  rotation: number
  /** Milliseconds until the next online/offline event is considered. */
  nextEvent: number
}

/**
 * A small deterministic generator.
 *
 * The field must look identical on every mount: a mesh that reshuffles itself
 * whenever the component remounts (a route change, a poll-driven re-render)
 * reads as a glitch, not as a network. Math.random cannot give that.
 */
export function seeded(seed: number): () => number {
  let state = seed >>> 0 || 1
  return () => {
    state ^= state << 13
    state >>>= 0
    state ^= state >> 17
    state ^= state << 5
    state >>>= 0
    return state / 0x100000000
  }
}

export interface FieldOptions {
  /** How many nodes in total, lit and unlit together. */
  nodes: number
  /** Two nodes closer than this in the unit slab are linked. */
  linkRadius: number
  /** No node keeps more than this many links, so hubs do not turn into blobs. */
  maxDegree: number
  seed: number
}

export const defaultFieldOptions: FieldOptions = {
  // Density is the whole character of the picture. Too few and it reads as a
  // constellation diagram rather than a network; the count and the radius are
  // tuned together so the typical node has three or four neighbours and the
  // mesh holds together without turning into a solid wash.
  nodes: 130,
  // The radius has to exceed the mean spacing of the cloud or the mesh breaks
  // into islands with gaps between them, which reads as a constellation chart
  // rather than a network. The degree cap, not the radius, is what keeps the
  // dense middle from filling in solid.
  linkRadius: 0.44,
  maxDegree: 5,
  seed: 0x5eed
}

/**
 * buildField lays out the nodes and decides which ones are linked.
 *
 * The slab is wide and shallow (z is compressed) on purpose. A cube of nodes
 * rotating reads as a sphere, which is the neighbouring card's job; a slab
 * reads as a plane seen at an angle, which is what a mesh looks like.
 */
export function buildField(options: FieldOptions = defaultFieldOptions): MeshField {
  const random = seeded(options.seed)
  const nodes: MeshNode[] = []
  for (let index = 0; index < options.nodes; index += 1) {
    nodes.push({
      x: random() * 2 - 1,
      y: (random() * 2 - 1) * 0.92,
      z: (random() * 2 - 1) * 0.42,
      size: 0.9 + random() * 1.3,
      live: 0,
      glow: 0,
      phase: random()
    })
  }

  // Links are computed once, from the resting positions. Recomputing them per
  // frame as the field turns would make edges appear and vanish with the
  // rotation, which looks like the mesh is broken rather than turning.
  const degree = new Array<number>(nodes.length).fill(0)
  const links: MeshLink[] = []
  const candidates: Array<{ from: number; to: number; distance: number }> = []
  for (let a = 0; a < nodes.length; a += 1) {
    for (let b = a + 1; b < nodes.length; b += 1) {
      const first = nodes[a]!
      const second = nodes[b]!
      const distance = Math.hypot(first.x - second.x, first.y - second.y, first.z - second.z)
      if (distance <= options.linkRadius) candidates.push({ from: a, to: b, distance })
    }
  }
  // Shortest first, so when the degree cap bites it is the long straggling
  // edges that get dropped and the local structure that survives.
  candidates.sort((first, second) => first.distance - second.distance)
  for (const candidate of candidates) {
    if (degree[candidate.from]! >= options.maxDegree) continue
    if (degree[candidate.to]! >= options.maxDegree) continue
    degree[candidate.from] = degree[candidate.from]! + 1
    degree[candidate.to] = degree[candidate.to]! + 1
    links.push({ from: candidate.from, to: candidate.to })
  }

  // A node the cap left with no edges at all floats unattached, and a handful
  // of those is what makes a mesh read as scattered dots. Each one gets its
  // nearest neighbour regardless of that neighbour's degree: one edge over the
  // cap is invisible, an orphan is not.
  for (let index = 0; index < nodes.length; index += 1) {
    if (degree[index]! > 0) continue
    let nearest = -1
    let best = Infinity
    for (let other = 0; other < nodes.length; other += 1) {
      if (other === index) continue
      const a = nodes[index]!
      const b = nodes[other]!
      const distance = Math.hypot(a.x - b.x, a.y - b.y, a.z - b.z)
      if (distance < best) {
        best = distance
        nearest = other
      }
    }
    if (nearest >= 0) {
      degree[index] = degree[index]! + 1
      degree[nearest] = degree[nearest]! + 1
      links.push({ from: index, to: nearest })
    }
  }

  // Size follows degree. A well-connected node drawing bigger is the one piece
  // of visual weight here that encodes something true about the graph rather
  // than decorating it -- and it is what stops the field reading as 130
  // identical specks.
  for (let index = 0; index < nodes.length; index += 1) {
    nodes[index]!.size = 0.85 + Math.min(degree[index]!, 6) * 0.28
  }

  return { nodes, links, signals: [], rotation: 0, nextEvent: 2200 }
}

/**
 * A star in the sky the mesh hangs in.
 *
 * Stars carry NO network meaning, and that separation is the point. The mesh --
 * nodes, links, signals -- is the part that reports something, and every claim
 * it makes is bounded by what this gateway has actually verified. The sky is
 * scenery: it exists so the field reads as a patch of space rather than a
 * wireframe on a panel, and it is deliberately built from a different shape of
 * mark (tiny, pure, ringless points) so it cannot be mistaken for a peer.
 */
export interface SkyStar {
  /** Position in the same -1..1 space as the nodes, unprojected. */
  x: number
  y: number
  /** Radius in CSS pixels at the card's drawing size. */
  size: number
  /** 0..1. Most stars are faint; a few carry the image. */
  brightness: number
  /** 0 cool blue-white, 1 warm amber. Real skies are mostly cool. */
  warmth: number
  /** Twinkle offset, so the sky does not pulse in unison. */
  phase: number
  /** Whether to draw the four-point diffraction cross. */
  sparkle: boolean
}

/**
 * buildSky lays out the stars, once and deterministically.
 *
 * Deterministic for the same reason the mesh is: a sky that reshuffles on every
 * remount reads as a glitch. The distribution is uniform rather than clustered
 * because the mesh in front of it already provides the structure -- a clustered
 * sky competes with it.
 *
 * The brightness curve is what makes it look like a photograph instead of
 * confetti. Raising a uniform random to the fourth power leaves most stars
 * nearly invisible and a handful bright, which is roughly how a real field of
 * stars is distributed across a sensor.
 */
export function buildSky(count = 260, seed = 0x57a25): SkyStar[] {
  const random = seeded(seed)
  const stars: SkyStar[] = []

  // Two populations, because one uniform population reads as noise.
  //
  // A real field of stars has a band across it, and the band is what makes the
  // difference between "dots scattered on black" and "a piece of sky". A third
  // of the stars are drawn into a diagonal band; the rest are spread evenly.
  // The band runs corner to corner rather than through the middle, so it does
  // not sit behind the densest part of the mesh and compete with it.
  const banded = Math.round(count / 3)
  for (let index = 0; index < count; index += 1) {
    const inBand = index < banded
    let x = random() * 2 - 1
    let y = random() * 2 - 1
    if (inBand) {
      // y = -x is the band's line; the offset is the sum of two randoms, which
      // clusters toward the middle far more naturally than one random does.
      const along = random() * 2 - 1
      const spread = (random() + random() - 1) * 0.34
      x = along
      y = -along + spread
      if (y < -1 || y > 1) y = spread
    }
    // Raising a uniform random to the fourth power leaves most stars nearly
    // invisible and a handful bright, which is roughly how a real field is
    // distributed across a sensor. Band stars sit a little brighter.
    const brightness = Math.min(1, Math.pow(random(), 4) + (inBand ? 0.1 : 0))
    stars.push({
      x,
      y,
      size: 0.3 + brightness * 0.95,
      brightness,
      // Warm stars are rare, and a handful of amber specks among blue-white
      // ones is what stops the sky reading as a single flat tint.
      warmth: random() < 0.12 ? 0.55 + random() * 0.45 : random() * 0.22,
      phase: random(),
      // Only the brightest few get a cross. Sparkles on everything is the
      // difference between a night sky and a glitter sticker.
      sparkle: brightness > 0.66
    })
  }
  return stars
}

/** litTarget is how many nodes should be lit for a given real peer count. */
export function litTarget(peers: number, total: number): number {
  if (peers <= 0) return 0
  // One lit node per peer, which is the honest mapping, capped so a large
  // routing table does not light the entire field and erase the distinction
  // between "peers we know" and "the rest of the DHT".
  return Math.min(peers, Math.floor(total * 0.55))
}

/**
 * reconcileLit moves nodes into and out of the lit set, a few at a time.
 *
 * It returns the indices it changed. Changing every node at once when the poll
 * arrives would make the whole field flash; the point of the animation is that
 * joining and leaving are things you watch happen.
 */
export function reconcileLit(field: MeshField, target: number, random: () => number, budget = 1): number[] {
  const live: number[] = []
  const dark: number[] = []
  field.nodes.forEach((node, index) => {
    if (node.live > 0.5) live.push(index)
    else dark.push(index)
  })
  const changed: number[] = []
  let remaining = budget
  while (live.length < target && dark.length > 0 && remaining > 0) {
    const pick = Math.floor(random() * dark.length)
    const index = dark.splice(pick, 1)[0]!
    field.nodes[index]!.live = 1
    live.push(index)
    changed.push(index)
    remaining -= 1
  }
  while (live.length > target && live.length > 0 && remaining > 0) {
    const pick = Math.floor(random() * live.length)
    const index = live.splice(pick, 1)[0]!
    field.nodes[index]!.live = 0
    changed.push(index)
    remaining -= 1
  }
  return changed
}

/** How long a node takes to fade fully in or out, in milliseconds. */
export const GLOW_RISE_MS = 2600

export interface StepOptions {
  /** Milliseconds since the previous step. */
  elapsed: number
  /** Radians per second. */
  spin: number
  /** How many nodes should be lit right now. */
  target: number
  /** False freezes rotation, signals and life events but keeps the picture. */
  animate: boolean
  random: () => number
}

/**
 * step advances the field by one frame.
 *
 * Everything here is driven by elapsed milliseconds rather than frame count, so
 * the mesh turns at the same rate on a 60Hz laptop and a 144Hz monitor, and a
 * stalled frame does not make the animation lurch.
 */
export function step(field: MeshField, options: StepOptions): void {
  const { elapsed, animate, random } = options

  // Glow walks toward live at a fixed rate, and ARRIVES. An eased chase
  // (glow += difference * rate) approaches 1 without ever reaching it, which
  // would leave every joined node stuck in the "settling" state, breathing for
  // as long as the page is open instead of going steady once it has joined.
  // The breathing means "joining"; it has to be able to stop.
  const stride = elapsed / GLOW_RISE_MS
  for (const node of field.nodes) {
    const gap = node.live - node.glow
    if (Math.abs(gap) <= stride) node.glow = node.live
    else node.glow += Math.sign(gap) * stride
  }

  if (!animate) return

  field.rotation += (options.spin * elapsed) / 1000

  // Life events are rare on purpose. A mesh where nodes are constantly winking
  // in and out reads as noise; the point is that an arrival is noticeable.
  field.nextEvent -= elapsed
  if (field.nextEvent <= 0) {
    field.nextEvent = 3800 + random() * 4200
    reconcileLit(field, options.target, random, 1)
  }

  // Signals travel only between nodes that are both lit, because a signal is
  // the one thing here that claims traffic happened.
  for (let index = field.signals.length - 1; index >= 0; index -= 1) {
    const signal = field.signals[index]!
    signal.progress += elapsed / signal.duration
    if (signal.progress >= 1) field.signals.splice(index, 1)
  }
  const lit = field.links.filter(link =>
    field.nodes[link.from]!.glow > 0.6 && field.nodes[link.to]!.glow > 0.6
  )
  // Roughly one new signal every 320ms while there is anywhere for one to go,
  // and never more than a handful at once so each one can be followed.
  if (lit.length > 0 && field.signals.length < 5 && random() < elapsed / 320) {
    const chosen = lit[Math.floor(random() * lit.length)]!
    field.signals.push({
      link: field.links.indexOf(chosen),
      progress: 0,
      duration: 420 + random() * 320,
      direction: random() < 0.5 ? 1 : -1
    })
  }
}

export interface Projected {
  x: number
  y: number
  /** 0 at the far edge of the slab, 1 at the near edge. */
  depth: number
  scale: number
}

/**
 * project turns a slab position into canvas coordinates.
 *
 * The perspective is deliberately weak. A strong one would swing the near
 * nodes across the card as it turns, which draws the eye to the motion instead
 * of the mesh; a weak one gives just enough parallax to read as depth.
 */
export function project(
  node: MeshNode,
  rotation: number,
  width: number,
  height: number
): Projected {
  const cos = Math.cos(rotation)
  const sin = Math.sin(rotation)
  const x = node.x * cos - node.z * sin
  const z = node.x * sin + node.z * cos
  // z spans the slab's half-depth, not [-1,1], so normalise against that or
  // the near half of the field never reaches full brightness.
  const depth = Math.min(1, Math.max(0, (z / 1.1 + 1) / 2))
  const scale = 0.8 + depth * 0.36
  // The spread is bounded so that even the nearest node at the widest point of
  // the turn lands inside the canvas. A node clipped by the edge reads as a
  // rendering fault rather than a network that continues past the frame.
  // The spread is bounded well inside the frame, and not only to avoid
  // clipping: the sky has to be visible AROUND the graph. A mesh that reaches
  // the edges reads as a net stretched across the card, where one with a
  // margin of stars around it reads as a constellation sitting in space.
  return {
    x: width / 2 + x * width * 0.38 * scale,
    y: height / 2 + node.y * height * 0.4 * scale,
    depth,
    scale
  }
}

/** pointOnLink is where a signal sits, in projected space. */
export function pointOnLink(
  from: Projected,
  to: Projected,
  signal: MeshSignal
): { x: number; y: number } {
  const fraction = signal.direction === 1 ? signal.progress : 1 - signal.progress
  return {
    x: from.x + (to.x - from.x) * fraction,
    y: from.y + (to.y - from.y) * fraction
  }
}
