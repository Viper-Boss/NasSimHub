// A preview harness for the DHT route animation.
//
// It imports the SHIPPING field and painter, and samples the same field at
// successive moments -- which is the only way to check from stills that a
// lookup walks its route rather than twitching once. Four frames a third of a
// second apart is roughly one hop per frame.

import { advance, buildField, signalHead, type MeshField } from '../src/components/dhtMeshField'
import { paintMesh } from '../src/components/dhtMeshPainter'

const palette = {
  strand: '124 169 217',
  nodeBloom: '120 191 239',
  nodeCore: '161 215 249',
  ring: '143 225 255',
  signal: '255 214 124',
  signalCore: '255 228 167'
}

const WIDTH = 300
const HEIGHT = 300
const STEP = 1 / 120

// Run one field forward until a route with plenty of hops is in flight, then
// keep sampling THAT field so the four frames are one continuous animation.
function findRoute(): { field: MeshField; from: number } {
  const field = buildField(WIDTH, HEIGHT)
  for (let tick = 0; tick < 120 * 120; tick += 1) {
    const time = tick * STEP
    advance(field, time, true)
    const long = field.signals.find(signal => signal.path.length >= 5)
    if (long && Math.abs(time - long.start) < STEP * 1.5) return { field, from: time }
  }
  return { field, from: 0 }
}

const { from } = findRoute()
const row = document.getElementById('row')!

/**
 * A field advanced from zero to one moment, stepped identically every time.
 *
 * Built fresh per frame rather than advancing one shared field onwards, and
 * that is not fussiness: advancing one field cumulatively meant each frame had
 * seen MORE history than the last, so by the fourth frame the route being
 * tracked was a different lookup entirely -- the hop counter counted up
 * convincingly while the picture jumped across the card. Deterministic stepping
 * from zero makes the four frames one animation.
 */
function at(time: number): MeshField {
  const field = buildField(WIDTH, HEIGHT)
  for (let tick = 0; tick * STEP <= time; tick += 1) advance(field, tick * STEP, true)
  return field
}

for (let frame = 0; frame < 4; frame += 1) {
  const time = from + frame * 0.33
  const cell = document.createElement('div')
  cell.className = 'cell'
  const box = document.createElement('div')
  box.className = 'frame'
  const canvas = document.createElement('canvas')
  box.appendChild(canvas)
  const label = document.createElement('span')
  label.className = 'lbl'
  cell.append(box, label)
  row.appendChild(cell)

  const ratio = 2
  canvas.width = WIDTH * ratio
  canvas.height = HEIGHT * ratio
  const context = canvas.getContext('2d')!
  context.setTransform(ratio, 0, 0, ratio, 0, 0)

  const field = at(time)
  paintMesh(context, field, {
    width: WIDTH,
    height: HEIGHT,
    time,
    strength: 1,
    palette
  })

  // Tracked by the moment it set off, so the label describes the SAME lookup
  // in every frame.
  const tracked = field.signals.find(signal => Math.abs(signal.start - from) < 0.02)
  const head = tracked ? signalHead(tracked, time) : null
  label.textContent = `t +${(frame * 0.33).toFixed(2)}s` +
    (tracked && head
      ? ` · hop ${head.hop + 1}/${tracked.path.length - 1}${head.arrived ? ' (landed)' : ''}`
      : ' · route finished')
}
