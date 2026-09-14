// The DHT fishnet's painting.
//
// Extracted from the component for the same reason the geometry was: a drawing
// decision made inside a requestAnimationFrame callback can only be checked by
// watching the screen and hoping. Out here the same code that ships can be
// pointed at a canvas of known size with a known palette, which is how the
// composition below was reviewed rather than guessed at.
//
// It draws and nothing else: no sizing, no clearing, no clock. The caller owns
// the canvas and has already advanced the field.
//
// Every colour comes in as space-separated RGB channels rather than as a
// finished colour, because almost every mark here composes its own alpha: the
// strand from its dimmer end, the node from its fade, the bead from its bloom.
// A palette of finished `rgba(...)` strings cannot be dimmed without string
// surgery, which is how a drawing ends up with a colour nobody can re-theme.

import { defaultMeshOptions, signalProgress, type MeshField, type MeshOptions } from './dhtMeshField'

/** The roles in the picture. One set is illustration, one is a claim. */
export interface MeshPalette {
  /** The strands between nodes. */
  strand: string
  /** The soft halo around a node. */
  nodeBloom: string
  /** The node itself. */
  nodeCore: string
  /** The ring that expands once as a node appears. */
  ring: string
  /** Traffic: the bead's trail and its halo. The only claim in here. */
  signal: string
  /** The bead's bright centre. */
  signalCore: string
}

export interface PaintOptions {
  width: number
  height: number
  /** Seconds. The same clock the field was advanced on. */
  time: number
  palette: MeshPalette
  /**
   * 1 while the DHT is ready, lower while it is not.
   *
   * The whole net dims rather than disappearing, so the card keeps its shape
   * and the reader can see there is a picture there at all -- what goes away is
   * the traffic and the arrival rings, which are the parts that claim something
   * happened.
   */
  strength: number
  mesh?: MeshOptions
}

export function paintMesh(
  context: CanvasRenderingContext2D,
  field: MeshField,
  options: PaintOptions
): void {
  const { palette, strength, time } = options
  const mesh = options.mesh ?? defaultMeshOptions
  const live = strength > 0.999

  // Strands first, so nodes sit on top of them.
  context.lineWidth = 0.8
  for (const [a, b] of field.edges) {
    const from = field.nodes[a]
    const to = field.nodes[b]
    if (!from || !to) continue
    // A strand is only as bright as its dimmer end, which is what makes a node
    // fading out take its strands with it instead of leaving them hanging.
    const alpha = 0.22 * Math.min(from.alpha, to.alpha) * strength
    context.beginPath()
    context.moveTo(from.px, from.py)
    context.lineTo(to.px, to.py)
    context.strokeStyle = channels(palette.strand, alpha)
    context.stroke()
  }

  for (const node of field.nodes) {
    const alpha = node.alpha * strength
    // Two circles rather than a canvas shadow: a wide faint disc for the bloom
    // and a tight one for the core. Shadows on a few hundred nodes a frame are
    // the one thing in this drawing expensive enough to drop frames on a
    // NAS-class browser, and this reads the same.
    fill(context, node.px, node.py, 5.5, channels(palette.nodeBloom, alpha * 0.055))
    fill(context, node.px, node.py, 1.65, channels(palette.nodeCore, alpha * 0.74))
    // A node arriving gets one ring that expands and fades. It reads as
    // "something appeared here", so it is drawn only on the way IN, and only
    // while the card is live.
    if (!live || !node.changing || node.to !== 1) continue
    const progress = clamp01((time - node.start) / mesh.fadeDuration)
    context.beginPath()
    context.arc(node.px, node.py, 4 + 12 * progress, 0, Math.PI * 2)
    context.strokeStyle = channels(palette.ring, Math.sin(progress * Math.PI) * 0.5)
    context.lineWidth = 1
    context.stroke()
    context.lineWidth = 0.8
  }

  if (!live) return

  // Signals last, over everything, because they are the thing to look at --
  // and the only mark in this picture that claims a packet moved.
  for (const signal of field.signals) {
    const progress = signalProgress(signal, time, mesh)
    const from = field.nodes[signal.edge[0]]
    const to = field.nodes[signal.edge[1]]
    if (!from || !to) continue
    const headX = from.px + (to.px - from.px) * progress
    const headY = from.py + (to.py - from.py) * progress
    // The trail starts a third of the strand back, so the bead reads as moving
    // rather than as a dot that happens to be somewhere.
    const tail = Math.max(0, progress - 0.3)
    const tailX = from.px + (to.px - from.px) * tail
    const tailY = from.py + (to.py - from.py) * tail

    // The end points are nudged apart: a gradient between two identical points
    // is rejected by the canvas and the whole trail silently disappears, which
    // is exactly what happens on the first frame of every signal.
    const gradient = context.createLinearGradient(tailX, tailY, headX + 0.001, headY + 0.001)
    gradient.addColorStop(0, channels(palette.signal, 0))
    gradient.addColorStop(1, channels(palette.signal, 1))
    context.beginPath()
    context.moveTo(tailX, tailY)
    context.lineTo(headX, headY)
    context.strokeStyle = gradient
    context.lineWidth = 1.8
    context.stroke()

    fill(context, headX, headY, 5, channels(palette.signal, 0.075))
    fill(context, headX, headY, 2, channels(palette.signalCore, 1))
  }
}

function channels(rgb: string, alpha: number): string {
  return `rgb(${rgb} / ${(clamp01(alpha) * 100).toFixed(2)}%)`
}

function clamp01(value: number): number {
  return Math.max(0, Math.min(1, value))
}

function fill(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  radius: number,
  colour: string
): void {
  context.beginPath()
  context.arc(x, y, radius, 0, Math.PI * 2)
  context.fillStyle = colour
  context.fill()
}
