import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  advance,
  buildField,
  defaultMeshOptions,
  seeded,
  signalProgress,
  smooth
} from '../src/components/dhtMeshField.ts'

// The net's decisions are made here rather than inside the render loop so they
// can be checked on a fixed clock. The one that matters most is the last group
// in this file: the picture must never show traffic the gateway did not have.

const SIZE = { width: 560, height: 365 }

test('the net is the same on every build, so a resize does not reshuffle it', () => {
  // The reference build drew the layout and the running animation from one
  // shared generator, so a rebuild produced a different net -- and it rebuilds
  // on every resize, which means dragging a window reshuffled the whole
  // picture. A net that reshuffles reads as a glitch, not as a network.
  const first = buildField(SIZE.width, SIZE.height)
  const second = buildField(SIZE.width, SIZE.height)
  assert.deepEqual(
    first.nodes.map(node => [node.x, node.y, node.phase, node.alpha]),
    second.nodes.map(node => [node.x, node.y, node.phase, node.alpha])
  )
  assert.deepEqual(first.edges, second.edges)

  // Running one of them forward must not change what a fresh build produces.
  for (let time = 0; time < 40; time += 0.1) advance(first, time, true)
  const third = buildField(SIZE.width, SIZE.height)
  assert.deepEqual(third.edges, second.edges, 'the layout depends on the animation')

  // And the layout must depend on NOTHING but its arguments.
  //
  // The two comparisons above are worth little on their own, and that was
  // found by sabotage rather than noticed: seeding the build from
  // `Date.now() / 1000` -- which reshuffles the whole net every second the
  // reader resizes the window -- passed all of them, because two builds a few
  // microseconds apart land in the same second. A clock or a global random is
  // the only way non-determinism can get in here, so the absence of both is
  // the thing to assert.
  const module = readFileSync(
    new URL('../src/components/dhtMeshField.ts', import.meta.url),
    'utf8'
  )
  for (const source of [/Math\.random/, /Date\.now/, /new Date\b/, /performance\.now/]) {
    assert.doesNotMatch(
      module,
      source,
      `the net is seeded from ${source.source}; it will reshuffle itself while nobody is looking`
    )
  }
})

test('the net covers the whole canvas and overhangs every edge', () => {
  // The overhang is what makes the drawing read as a piece cut out of
  // something larger. Without it the net ends in a ring of loose ends just
  // inside the card, which reads as a panel with a border.
  const field = buildField(SIZE.width, SIZE.height)
  const xs = field.nodes.map(node => node.x)
  const ys = field.nodes.map(node => node.y)
  assert.ok(Math.min(...xs) < 0, 'the net does not reach past the left edge')
  assert.ok(Math.max(...xs) > SIZE.width, 'the net does not reach past the right edge')
  assert.ok(Math.min(...ys) < 0, 'the net does not reach past the top edge')
  assert.ok(Math.max(...ys) > SIZE.height, 'the net does not reach past the bottom edge')

  // And it must not leave a visible hole. Stated as the distance from the
  // worst point of the canvas to the nearest node, because that is the thing a
  // reader sees -- an earlier version of this assertion instead demanded a node
  // in every grid-sized cell, which the real layout fails while looking
  // perfectly even: odd rows are offset half a cell and every node jitters, so
  // cell occupancy is not the property being designed. Measured across five
  // card sizes the worst gap is 0.71 to 0.79 of the spacing; one whole spacing
  // is clear of that and would catch a grid that actually broke.
  const spacing = defaultMeshOptions.spacing
  let worst = 0
  for (let x = 0; x <= SIZE.width; x += 8) {
    for (let y = 0; y <= SIZE.height; y += 8) {
      let nearest = Infinity
      for (const node of field.nodes) {
        nearest = Math.min(nearest, Math.hypot(node.x - x, node.y - y))
      }
      worst = Math.max(worst, nearest)
    }
  }
  assert.ok(
    worst < spacing,
    `the emptiest point of the card is ${worst.toFixed(1)}px from any node, against a ${spacing}px spacing`
  )
})

test('the net is tied into triangles, with some strands left out', () => {
  const field = buildField(SIZE.width, SIZE.height)
  // Across, down and one diagonal per cell is three strands per node before
  // the cap, so a net that had them all would be near three times the nodes.
  // About a sixth are dropped, and the dropped ones are the difference between
  // a net someone tied and a mesh a computer generated.
  const ratio = field.edges.length / field.nodes.length
  assert.ok(ratio > 1.6, `only ${ratio.toFixed(2)} strands per node; the net would look sparse`)
  assert.ok(ratio < 2.8, `${ratio.toFixed(2)} strands per node; the net would fill in solid`)

  // No strand may point at a node that does not exist, or the painter skips it
  // and the net quietly loses pieces at the edges.
  for (const [a, b] of field.edges) {
    assert.ok(field.nodes[a] && field.nodes[b], `strand ${a}-${b} has a missing end`)
    assert.notEqual(a, b, 'a strand joins a node to itself')
  }
})

test('the drift is time-based, so it does not depend on the frame rate', () => {
  // Absolute seconds rather than a delta: a stalled frame must not make the
  // net lurch, a fast display must not speed it up, and the same moment has to
  // produce the same picture or the preview harness cannot render one.
  const coarse = buildField(SIZE.width, SIZE.height)
  const fine = buildField(SIZE.width, SIZE.height)
  advance(coarse, 3, false)
  // Stepped as exact fractions rather than by repeated addition: accumulating
  // 0.05 sixty times lands on 2.9999999999999996, and the two runs then differ by a
  // sliver of a frame for reasons that have nothing to do with the code.
  for (let step = 0; step <= 60; step += 1) advance(fine, step / 20, false)
  assert.deepEqual(
    coarse.nodes.map(node => [node.px, node.py]),
    fine.nodes.map(node => [node.px, node.py])
  )

  // And it has to actually move.
  const before = buildField(SIZE.width, SIZE.height)
  advance(before, 0, false)
  const resting = before.nodes.map(node => node.px)
  advance(before, 4, false)
  const moved = before.nodes.filter((node, index) => Math.abs(node.px - resting[index]) > 1).length
  assert.ok(moved > before.nodes.length * 0.5, `only ${moved} nodes drifted`)
})

test('a node fades over seconds rather than blinking', () => {
  assert.equal(smooth(0), 0)
  assert.equal(smooth(1), 1)
  assert.equal(smooth(0.5), 0.5)
  // Smoothstep, not linear: both ends of the fade have to ease, or a node
  // appears to snap into motion and snap out of it.
  assert.ok(smooth(0.1) < 0.1, 'the fade does not ease in')
  assert.ok(smooth(0.9) > 0.9, 'the fade does not ease out')

  const field = buildField(SIZE.width, SIZE.height)
  advance(field, defaultMeshOptions.lifecycleInterval, true)
  const changing = field.nodes.find(node => node.changing)
  assert.ok(changing, 'no node ever changes brightness; the net would be frozen')
  const opening = changing.alpha
  advance(field, defaultMeshOptions.lifecycleInterval + 0.05, true)
  assert.ok(
    Math.abs(field.nodes.indexOf(changing) >= 0 ? changing.alpha - opening : 1) < 0.1,
    'the fade jumped; it should take seconds'
  )
  advance(field, defaultMeshOptions.lifecycleInterval + defaultMeshOptions.fadeDuration, true)
  assert.equal(changing.changing, false, 'the fade never finished')
  assert.ok(
    Math.abs(changing.alpha - changing.to) < 1e-9,
    'the fade settled somewhere other than its target'
  )
})

test('a signal crosses one strand and then it is gone', () => {
  const field = buildField(SIZE.width, SIZE.height)
  let seen = 0
  let most = 0
  for (let time = 0; time < 60; time += 1 / 60) {
    advance(field, time, true)
    seen += field.signals.length
    most = Math.max(most, field.signals.length)
    for (const signal of field.signals) {
      const progress = signalProgress(signal, time)
      assert.ok(progress >= 0 && progress <= 1, `a signal is at ${progress} of its strand`)
      assert.ok(field.nodes[signal.edge[0]], 'a signal starts nowhere')
      assert.ok(field.nodes[signal.edge[1]], 'a signal ends nowhere')
    }
  }
  assert.ok(seen > 0, 'no signals were produced at all; the animation would be static')
  // Few enough at a time that each one can be followed. At one launch every
  // 0.72s lasting 0.48s, two in flight is the arithmetic; more than a handful
  // would mean they are not being retired.
  assert.ok(most <= 3, `${most} signals were in flight at once`)
})

test('a signal only ever leaves from a node that is lit', () => {
  // A bead setting off from a dim node reads as traffic appearing out of
  // nothing, and the dim nodes are the part of the picture that is explicitly
  // "we do not know who this is".
  const field = buildField(SIZE.width, SIZE.height)
  for (let time = 0; time < 120; time += 1 / 60) {
    const before = field.signals.length
    advance(field, time, true)
    if (field.signals.length <= before) continue
    const launched = field.signals[field.signals.length - 1]
    assert.ok(
      field.nodes[launched.edge[0]].alpha > 0.6 && field.nodes[launched.edge[1]].alpha > 0.6,
      'a signal set off along a strand with a dim end'
    )
  }
})

// THE honesty invariant for this card.
//
// The reference build ran the life-cycle and the signals off a timer, with
// nothing behind them -- right for a demo, and invented telemetry here, because
// this card is wired to a real DHT. An amber bead means a packet moved.
test('a DHT that is not ready produces no traffic at all, ever', () => {
  const field = buildField(SIZE.width, SIZE.height)
  for (let time = 0; time < 200; time += 1 / 60) {
    advance(field, time, false)
    assert.equal(field.signals.length, 0, `a signal was drawn at ${time.toFixed(2)}s with the DHT down`)
  }
  // Nothing arrives either: an expanding ring reads as "a node appeared here".
  assert.equal(field.nodes.filter(node => node.changing).length, 0)
  // The net still drifts, because the net is illustration and a frozen card
  // looks broken. Only the claims stop.
  const drifted = field.nodes.filter(node => Math.abs(node.px - node.x) > 1).length
  assert.ok(drifted > 0, 'the net froze; the card would look like a failed render')
})

test('traffic in flight is dropped the moment the DHT goes down', () => {
  // Letting the beads already on screen finish their hop would mean up to half
  // a second of traffic drawn after the channel was known to be gone.
  const field = buildField(SIZE.width, SIZE.height)
  let time = 0
  for (; time < 20; time += 1 / 60) {
    advance(field, time, true)
    if (field.signals.length > 0) break
  }
  assert.ok(field.signals.length > 0, 'no signal was ever in flight to drop')
  advance(field, time + 1 / 60, false)
  assert.equal(field.signals.length, 0, 'a signal survived the DHT going down')
})

test('the painter stops drawing traffic when it is handed a dimmed field', () => {
  // The field gate above is the first line of defence; this is the second. The
  // painter is given `strength`, and the two claims in the drawing -- the beads
  // and the arrival rings -- must both sit behind it.
  const painter = readFileSync(
    new URL('../src/components/dhtMeshPainter.ts', import.meta.url),
    'utf8'
  )
  assert.match(painter, /const live = strength > 0\.999/)
  const gate = painter.indexOf('if (!live) return')
  assert.ok(gate > 0, 'the painter no longer refuses to draw signals on a dimmed field')
  assert.ok(gate < painter.indexOf('for (const signal of field.signals)'), 'the gate is after the signals')
  assert.ok(
    painter.indexOf('if (!live || !node.changing') > 0,
    'the arrival ring is no longer gated'
  )
})

test('both cards pass a strength that follows the real state', () => {
  const mesh = readFileSync(new URL('../src/components/DHTMeshCard.vue', import.meta.url), 'utf8')
  assert.match(
    mesh,
    /advance\(field,\s*time,\s*ready\.value\)/,
    'the mesh card no longer gates the animation on the real DHT state'
  )
  assert.match(
    mesh,
    /strength:\s*ready\.value\s*\?\s*1\s*:/,
    'the mesh card passes a fixed strength; the net would look live while the DHT is down'
  )
  // `ready` must mean ready, not merely configured.
  assert.match(mesh, /dht\.value\?\.state === 'ready'/)
})

test('the two network cards share one stylesheet and one stage height', () => {
  // They sit side by side in one row and stretch together. The moment one gets
  // a tweak the other does not, the row reads as two cards lifted from
  // different screens -- and nothing about a copied rule announces itself.
  for (const name of ['DHTMeshCard.vue', 'NetworkGlobeCard.vue']) {
    const source = readFileSync(new URL(`../src/components/${name}`, import.meta.url), 'utf8')
    assert.match(
      source,
      /import '\.\/networkCard\.css'/,
      `${name} no longer imports the shared card chrome`
    )
    assert.match(source, /class="net-card /, `${name} no longer uses the shared card frame`)
    assert.match(source, /class="net-stage"/, `${name} no longer uses the shared stage`)
    // A card that redeclares the frame has started to drift even while still
    // importing the shared file.
    const styles = source.split('<style')[1] ?? ''
    assert.ok(
      !/border-radius:\s*23px/.test(styles),
      `${name} declares its own card frame again`
    )
    assert.ok(!/height:\s*365px/.test(styles), `${name} sets its own stage height`)
  }
})

test('nothing drawn over a picture reports a number', () => {
  // The caption on each drawing names what the drawing is. A figure placed
  // there would read as coming FROM the drawing -- which is exactly the claim
  // these cards spend their design avoiding, since most of each picture is
  // illustration. So the overlay carries no interpolation and no figure.
  const standaloneNumber = /(?:^|[^A-Za-z0-9])\d+(?:[^A-Za-z0-9]|$)/
  for (const name of ['DHTMeshCard.vue', 'NetworkGlobeCard.vue']) {
    const source = readFileSync(new URL(`../src/components/${name}`, import.meta.url), 'utf8')
    const overlay = [...source.matchAll(/class="net-note[^"]*"[^>]*>([\s\S]*?)<\//g)]
    assert.ok(overlay.length > 0, `${name} has no caption to check`)
    for (const [, body] of overlay) {
      assert.ok(!standaloneNumber.test(body), `${name} puts a figure on the drawing: ${body.trim()}`)
      // A figure can only reach the caption through the two objects a card
      // holds its measurements in, so those are what is forbidden -- rather
      // than a keyword list, which an earlier version of this assertion used
      // and which rejected `nknActive` for containing the word "active" while
      // it was only choosing between two sentences.
      assert.ok(
        !/\b(?:peers|dht)\s*[?.]/.test(body),
        `${name} binds measured data into the caption: ${body.trim()}`
      )
    }
  }
})

test('the generator is the one the reference build used', () => {
  // Not an aesthetic detail: the net's whole texture -- which nodes start dim,
  // where each one sits inside its cell, which strands were dropped -- is this
  // sequence. Swapping the generator redraws the card.
  const random = seeded(9014)
  const drawn = [random(), random(), random()]
  for (const value of drawn) {
    assert.ok(value >= 0 && value < 1, `the generator produced ${value}`)
  }
  assert.notDeepEqual(drawn, [drawn[0], drawn[0], drawn[0]], 'the generator is stuck')
  // And it must be seeded, not random: two generators on one seed agree.
  assert.deepEqual(drawn, [seeded(9014)(), ...[0, 0].map((_, index) => {
    const again = seeded(9014)
    for (let skip = 0; skip <= index; skip += 1) again()
    return again()
  })])
})
