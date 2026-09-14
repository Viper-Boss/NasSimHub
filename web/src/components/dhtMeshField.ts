// The DHT fishnet's geometry and life-cycle.
//
// This lives apart from the component for one reason: the interesting parts --
// which nodes are linked, when a node fades, when a signal is allowed to travel
// -- are decisions, and decisions inside a requestAnimationFrame callback can
// only be checked by watching the screen and hoping. Here they can be run on a
// fixed clock and asserted.
//
// The layout, the drift, the fade timings and the signal rate all come from the
// operator's own reference build of this card. What is added is the module
// boundary, the types, the per-build generator, and the one thing the reference
// left open: this card is wired to a real DHT, so the traffic has to follow it.
//
// WHAT IS REAL AND WHAT IS SCENERY, stated plainly because it would otherwise
// be guesswork, and because most of this picture is scenery:
//
//   the NET -- where the nodes sit, which ones are tied together, how they
//   drift, and which ones are dim -- is illustration. It is a sample of a mesh
//   drawn to fill the card. It is NOT this gateway's routing table: a DHT has
//   no directory, and this gateway does not know who most of the network is.
//   The card's caption says so, and no figure is ever drawn on the picture.
//
//   the SIGNALS -- the amber beads travelling a strand -- are the one mark that
//   means "traffic", and they are gated on the real DHT being ready with peers.
//   With the DHT off, starting, isolated or failed, the net dims and nothing
//   travels. The reference build ran the signals unconditionally off a timer;
//   that is fine in a demo and would be invented telemetry here.

/** A node in the net. Resting position, plus where the drift has put it. */
export interface MeshNode {
  /** Resting position in CSS pixels, in the canvas's own coordinates. */
  x: number
  y: number
  /** Drift offset, so the nodes do not all sway in step. */
  phase: number
  /** 0.08 dim, 1 bright. Chases `to` while `changing`. */
  alpha: number
  from: number
  to: number
  /** Seconds at which the current fade began. */
  start: number
  changing: boolean
  /** Position after the drift, filled in by advance(). */
  px: number
  py: number
}

/** A strand, by node index. */
export type MeshEdge = [number, number]

/** A bead in flight along one strand, from the first index to the second. */
export interface MeshSignal {
  edge: MeshEdge
  /** Seconds at which it set off. */
  start: number
}

export interface MeshField {
  nodes: MeshNode[]
  edges: MeshEdge[]
  signals: MeshSignal[]
  /** Seconds after which another signal may be created. */
  nextSignal: number
  /** Seconds after which another node may change brightness. */
  nextLife: number
  /**
   * How many choices have been drawn so far.
   *
   * The net's own randomness is spent at build time; this is the stream the
   * life-cycle and the signals draw from afterwards. Kept as a count on the
   * field rather than as a closure so the field stays plain data -- which is
   * what lets a test and the preview harness run it forward on a fixed clock
   * and get the same picture every time.
   */
  picks: number
}

export interface MeshOptions {
  /** Grid pitch in CSS pixels. Smaller is denser. */
  spacing: number
  /** How far a node wanders from its resting position, in pixels. */
  drift: number
  driftSpeed: number
  /** Seconds between attempts to launch a signal. */
  signalInterval: number
  /** Seconds a signal takes to cross one strand. */
  signalDuration: number
  /** Seconds between one node changing brightness. */
  lifecycleInterval: number
  /** Seconds a node takes to fade in or out. */
  fadeDuration: number
  seed: number
}

export const defaultMeshOptions: MeshOptions = {
  spacing: 59,
  drift: 9,
  driftSpeed: 0.22,
  signalInterval: 0.72,
  signalDuration: 0.48,
  lifecycleInterval: 4.5,
  fadeDuration: 3.2,
  seed: 9014
}

/**
 * A small deterministic generator.
 *
 * One per build, rather than one shared with the draw loop. The reference build
 * drew the layout and the runtime picks from the same stream, which means the
 * net comes out differently every time the card is rebuilt -- and it is rebuilt
 * on every resize, so dragging a window reshuffles the whole picture. Seeded
 * per build, a given card size always produces the same net.
 */
export function seeded(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0
    return state / 4294967296
  }
}

/** Smoothstep. Eases both ends of a fade so a node does not snap into motion. */
export function smooth(fraction: number): number {
  return fraction * fraction * (3 - 2 * fraction)
}

function clamp(value: number, low: number, high: number): number {
  return Math.max(low, Math.min(high, value))
}

/**
 * buildField lays out the net for a canvas of the given size.
 *
 * The grid runs one cell PAST every edge, and odd rows are offset by half a
 * cell. Both matter: the overhang is what makes the net read as a piece cut out
 * of something larger rather than a panel with a border of loose ends, and the
 * half-cell offset turns square cells into triangles, which is what a net looks
 * like and a lattice does not.
 */
export function buildField(
  width: number,
  height: number,
  options: MeshOptions = defaultMeshOptions
): MeshField {
  const random = seeded(options.seed)
  const { spacing } = options
  const columns = Math.ceil(width / spacing) + 3
  const rows = Math.ceil(height / spacing) + 3
  const nodes: MeshNode[] = []
  for (let row = 0; row < rows; row += 1) {
    for (let column = 0; column < columns; column += 1) {
      const x =
        (column - 1) * spacing + (row % 2) * spacing * 0.5 + (random() - 0.5) * 25
      const y = (row - 1) * spacing + (random() - 0.5) * 20
      nodes.push({
        x,
        y,
        phase: random() * Math.PI * 2,
        // A few nodes start dim. It is what gives the net texture before the
        // first fade has had time to happen.
        alpha: random() < 0.09 ? 0.08 : 1,
        from: 1,
        to: 1,
        start: -100,
        changing: false,
        px: x,
        py: y
      })
    }
  }

  // Strands: across, down, and one diagonal per cell, with about a sixth
  // dropped. The dropped ones are the difference between a net someone tied
  // and a mesh a computer generated.
  const edges: MeshEdge[] = []
  const add = (a: number, b: number): void => {
    if (b < nodes.length && random() > 0.16) edges.push([a, b])
  }
  for (let row = 0; row < rows; row += 1) {
    for (let column = 0; column < columns; column += 1) {
      const index = row * columns + column
      if (column < columns - 1) add(index, index + 1)
      if (row < rows - 1) {
        add(index, index + columns)
        // The diagonal leans the way the half-cell offset went, so it joins the
        // two nodes that actually sit either side of it.
        if (row % 2 && column < columns - 1) add(index, index + columns + 1)
        else if (!(row % 2) && column > 0) add(index, index + columns - 1)
      }
    }
  }

  return {
    nodes,
    edges,
    signals: [],
    nextSignal: 0,
    nextLife: options.lifecycleInterval,
    picks: 0
  }
}

/**
 * advance moves the net on to the given moment.
 *
 * Absolute time in seconds rather than a delta, so a stalled frame does not
 * make the drift lurch and a fast display does not speed it up -- and so the
 * same moment always produces the same picture, which is what makes the preview
 * harness able to render a frame at all.
 *
 * `live` is the honesty gate. False -- the DHT is off, starting, isolated or
 * failed -- freezes the life-cycle, clears the signals in flight and refuses
 * new ones. The net still drifts, because the net is illustration; the traffic
 * does not, because traffic is a claim.
 */
export function advance(
  field: MeshField,
  time: number,
  live: boolean,
  options: MeshOptions = defaultMeshOptions
): void {
  if (live && time >= field.nextLife) {
    const node = field.nodes[Math.floor(draw(field, options) * field.nodes.length)]
    if (node && !node.changing) {
      node.from = node.alpha
      node.to = node.alpha > 0.5 ? 0.08 : 1
      node.start = time
      node.changing = true
    }
    field.nextLife = time + options.lifecycleInterval
  }

  for (const node of field.nodes) {
    if (node.changing) {
      const progress = clamp((time - node.start) / options.fadeDuration, 0, 1)
      node.alpha = node.from + (node.to - node.from) * smooth(progress)
      if (progress === 1) node.changing = false
    }
    // Two sways. The small one is per node and gives the net its shimmer; the
    // large slow one varies with y, so whole bands of the net lean together --
    // that is what reads as the sheet moving rather than the dots vibrating.
    node.px =
      node.x +
      Math.sin(time * options.driftSpeed + node.phase) * options.drift +
      Math.sin(time * 0.09 + node.y * 0.01) * 12
    node.py = node.y + Math.cos(time * options.driftSpeed * 0.8 + node.phase) * options.drift
  }

  for (let index = field.signals.length - 1; index >= 0; index -= 1) {
    if (time - field.signals[index]!.start >= options.signalDuration) {
      field.signals.splice(index, 1)
    }
  }

  if (!live) {
    field.signals.length = 0
    return
  }

  if (time >= field.nextSignal && field.edges.length > 0) {
    const edge = field.edges[Math.floor(draw(field, options) * field.edges.length)]!
    // Both ends bright, or the bead appears to leave from nothing.
    if (field.nodes[edge[0]]!.alpha > 0.6 && field.nodes[edge[1]]!.alpha > 0.6) {
      field.signals.push({
        edge: draw(field, options) > 0.5 ? edge : [edge[1], edge[0]],
        start: time
      })
    }
    field.nextSignal = time + options.signalInterval
  }
}

/** draw takes the next choice from the field's own stream. */
function draw(field: MeshField, options: MeshOptions): number {
  field.picks += 1
  // Re-seeded from the count rather than kept as a running generator, for the
  // same reason `picks` is a number: the field has to stay plain data. The
  // multiply is what keeps consecutive counts from giving neighbouring values,
  // which a linear generator seeded 1, 2, 3 would.
  return seeded((options.seed ^ (field.picks * 2654435761)) >>> 0)()
}

/** signalProgress is how far along its strand a bead has travelled, 0 to 1. */
export function signalProgress(
  signal: MeshSignal,
  time: number,
  options: MeshOptions = defaultMeshOptions
): number {
  return clamp((time - signal.start) / options.signalDuration, 0, 1)
}
