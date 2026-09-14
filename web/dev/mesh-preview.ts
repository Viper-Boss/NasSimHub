// A preview harness for the DHT mesh card's painting.
//
// It imports the SHIPPING painter and geometry, so what it renders is what the
// dashboard renders -- the point of extracting paintMesh was to be able to look
// at the real thing instead of a copy that drifts. Bundle it with esbuild and
// open the sibling HTML file.
//
// Four panels: the two themes, each with the DHT off (what the operator sees
// today, zero verified peers) and with peers (what it looks like once the DHT
// is on and traffic is real).

import { buildField, buildSky, litTarget, reconcileLit, seeded, step } from '../src/components/dhtMeshField'
import { paintMesh, type MeshPalette } from '../src/components/dhtMeshPainter'

const dark: MeshPalette = {
  hue: '#b07cff',
  dim: 'rgb(150 188 246 / 72%)',
  signal: '#ffc542',
  deep: '#050916',
  cloudCool: 'rgb(38 72 148 / 30%)',
  cloudWarm: 'rgb(94 56 148 / 26%)',
  starCool: '226 236 255',
  starWarm: '255 212 168'
}

const light: MeshPalette = {
  ...dark,
  signal: '#ffc861',
  deep: '#0a1226',
  cloudCool: 'rgb(48 88 172 / 34%)',
  cloudWarm: 'rgb(104 64 160 / 28%)',
  dim: 'rgb(168 200 250 / 66%)'
}

interface Panel {
  id: string
  palette: MeshPalette
  peers: number
}

const panels: Panel[] = [
  { id: 'dark-off', palette: dark, peers: 0 },
  { id: 'dark-live', palette: dark, peers: 14 },
  { id: 'light-off', palette: light, peers: 0 },
  { id: 'light-live', palette: light, peers: 14 }
]

for (const panel of panels) {
  const element = document.getElementById(panel.id) as HTMLCanvasElement | null
  if (!element) continue
  const context = element.getContext('2d')
  if (!context) continue

  const field = buildField()
  const sky = buildSky()
  const random = seeded(0xbeef)
  const target = litTarget(panel.peers, field.nodes.length)
  // Seeded the way the card seeds itself on mount, then run forward so signals
  // are in flight and the glow has settled -- a first frame shows neither.
  reconcileLit(field, target, random, field.nodes.length)
  for (let tick = 0; tick < 260; tick += 1) {
    step(field, { elapsed: 16, spin: 0, target, animate: true, random })
  }

  const size = 320
  const ratio = 2
  element.width = size * ratio
  element.height = size * ratio
  context.setTransform(ratio, 0, 0, ratio, 0, 0)
  context.clearRect(0, 0, size, size)
  paintMesh(context, field, sky, {
    width: size,
    height: size,
    now: 4200,
    palette: panel.palette,
    reducedMotion: false
  })
}
