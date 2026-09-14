// The painting, extracted from the component for the same reason the geometry
// was: a drawing decision made inside a requestAnimationFrame callback can only
// be checked by watching the screen and hoping. Out here the same code that
// ships can be pointed at a canvas of known size with a known palette, which is
// how the composition below was reviewed rather than guessed at.
//
// It draws and nothing else: no sizing, no clearing, no clock. The caller owns
// the canvas and has already advanced the field.

import type { MeshField, Projected, SkyStar } from './dhtMeshField'
import { pointOnLink, project } from './dhtMeshField'

/** The three roles in the picture, resolved from CSS by the caller. */
export interface MeshPalette {
  /** A verified peer, and a line between two of them. */
  hue: string
  /** The unmapped mesh: dim nodes and the lines between them. */
  dim: string
  /** Traffic between two verified peers. */
  signal: string
  /** The sky's near-black ground. */
  deep: string
  cloudCool: string
  cloudWarm: string
  /** Space-separated RGB channels; the alpha is composed per star. */
  starCool: string
  starWarm: string
}

export interface PaintOptions {
  width: number
  height: number
  /** Milliseconds, for twinkle and shimmer phase only. */
  now: number
  palette: MeshPalette
  /** True freezes the twinkle and the shimmer. */
  reducedMotion: boolean
}

export function paintMesh(
  context: CanvasRenderingContext2D,
  field: MeshField,
  sky: SkyStar[],
  options: PaintOptions
): void {
  const { width, height, now, palette, reducedMotion } = options
  const hue = palette.hue
  const dim = palette.dim
  const signalColour = palette.signal
  const deep = palette.deep
  const cloudCool = palette.cloudCool
  const cloudWarm = palette.cloudWarm
  const starCool = palette.starCool
  const starWarm = palette.starWarm

  // The sky fills the canvas, and the FRAME is the element's own.
  //
  // Three attempts, worth recording because two of them looked like bugs. A
  // deep radial fading to transparent dissolved into the card nicely in dark
  // theme and produced a blown-out white halo in light theme. Clipping to a
  // rounded path and stroking it fixed the halo but left a bright seam where
  // the stroke, the untouched canvas corners and the panel behind them met --
  // a hairline drawn in canvas cannot line up with a pale panel at sub-pixel
  // precision.
  //
  // So the canvas paints an opaque sky corner to corner and the rounding and
  // the hairline are CSS on the element (border-radius plus an inset shadow),
  // which the browser composites exactly. Both were found by rendering the two
  // themes and looking, which is what the painter was pulled out of the
  // component for.
  context.fillStyle = deep
  context.fillRect(0, 0, width, height)

  // Two nebula clouds, off-centre and overlapping. Off-centre because a cloud
  // centred behind the mesh just looks like a vignette; overlapping because a
  // single blob reads as a lens flare and two reading through each other read
  // as depth.
  const sizeRatio = Math.max(width, height)
  for (const cloud of [
    { x: 0.3, y: 0.26, radius: 0.58, colour: cloudCool },
    { x: 0.72, y: 0.74, radius: 0.5, colour: cloudWarm }
  ]) {
    const gradient = context.createRadialGradient(
      width * cloud.x, height * cloud.y, 0,
      width * cloud.x, height * cloud.y, sizeRatio * cloud.radius
    )
    gradient.addColorStop(0, cloud.colour)
    gradient.addColorStop(1, 'rgba(0,0,0,0)')
    context.fillStyle = gradient
    context.fillRect(0, 0, width, height)
  }

  // Stars. Drawn before the mesh so the graph sits in front of the sky, and
  // kept to plain filled points -- the mesh nodes are the things with haloes,
  // and that is the whole visual difference between scenery and a peer.
  for (const star of sky) {
    const twinkle = reducedMotion
      ? 1
      : 0.72 + 0.28 * Math.sin(now / 1900 + star.phase * Math.PI * 2)
    const alpha = Math.min(1, (0.16 + star.brightness * 0.84) * twinkle)
    const x = width / 2 + star.x * width * 0.49
    const y = height / 2 + star.y * height * 0.49
    const tint = star.warmth > 0.5 ? starWarm : starCool
    context.fillStyle = `rgb(${tint} / ${Math.round(alpha * 100)}%)`
    context.beginPath()
    context.arc(x, y, star.size, 0, Math.PI * 2)
    context.fill()
    if (!star.sparkle) continue
    // The diffraction cross on the brightest few. Thin, and only as long as the
    // star is bright, so it reads as glare rather than as a drawn asterisk.
    const arm = star.size * (3.4 + star.brightness * 2.6)
    context.strokeStyle = `rgb(${tint} / ${Math.round(alpha * 42)}%)`
    context.lineWidth = 0.6
    context.beginPath()
    context.moveTo(x - arm, y)
    context.lineTo(x + arm, y)
    context.moveTo(x, y - arm)
    context.lineTo(x, y + arm)
    context.stroke()
  }

  const projected: Projected[] = field.nodes.map(node =>
    project(node, field.rotation, width, height)
  )

  // Edges first, so nodes sit on top of them.
  for (let index = 0; index < field.links.length; index += 1) {
    const link = field.links[index]!
    const from = projected[link.from]!
    const to = projected[link.to]!
    const a = field.nodes[link.from]!
    const b = field.nodes[link.to]!
    const strength = Math.min(a.glow, b.glow)
    const depth = (from.depth + to.depth) / 2
    // The shimmer, which is the life in the picture now that the field does not
    // turn. It is AMBIENT on purpose: every line breathes on its own slow
    // phase, and nothing appears to move along any of them. A travelling bead
    // would read as a packet, and a packet is a claim -- that mark is reserved
    // for the signals below, which only exist between two verified peers.
    //
    // The phase comes from the endpoint indices rather than stored state, so it
    // is stable across frames and remounts without another field to keep.
    const phase = ((link.from * 7 + link.to * 13) % 100) / 100
    const shimmer = reducedMotion
      ? 1
      : 0.74 + 0.26 * Math.sin(now / 2600 + phase * Math.PI * 2)
    // Depth fades the far edges out, which is what keeps a mesh of this
    // density from reading as a flat tangle.
    context.globalAlpha = (0.3 + depth * 0.4) * (0.5 + strength * 0.5) * shimmer
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
}
