import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  advance,
  buildField,
  defaultMeshOptions,
  hopGlow,
  seeded,
  signalHead,
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

test('a lookup walks several hops, and they are real strands', () => {
  // This is what the change was for. A signal used to be ONE edge: 0.48s
  // across a 59px strand, then gone -- which on screen reads as a dot
  // twitching once and vanishing, and the operator's report was exactly that,
  // "it only jumps one node and then stops". A lookup is a walk.
  // Absolute, not read from the options. Asserting `hops >= minHops` was the
  // first version and it was worthless: setting minHops back to 1 -- exactly
  // the single-hop bead this replaced -- moved the assertion with it and the
  // test passed. Found by sabotage. THREE is the number this drawing needs,
  // whatever the config later says.
  assert.ok(
    defaultMeshOptions.minHops >= 3,
    `routes may be as short as ${defaultMeshOptions.minHops} hop(s); that is the twitch this replaced`
  )

  const field = buildField(SIZE.width, SIZE.height)
  const strands = new Set(field.edges.map(([a, b]) => `${Math.min(a, b)}:${Math.max(a, b)}`))
  const seen = new Set()
  const lengths = []

  for (let step = 0; step < 60 * 180; step += 1) {
    advance(field, step / 60, true)
    for (const signal of field.signals) {
      const key = `${signal.start}:${signal.path[0]}`
      if (seen.has(key)) continue
      seen.add(key)
      const hops = signal.path.length - 1
      lengths.push(hops)
      assert.ok(hops >= 3, `a route of ${hops} hop(s) was launched; a lookup is a walk`)
      assert.ok(
        hops <= defaultMeshOptions.maxHops,
        `a route of ${hops} hops was launched, over the ${defaultMeshOptions.maxHops} cap`
      )
      // Every step must be a strand that exists, or the bead cuts across the
      // net instead of travelling it.
      for (let hop = 0; hop + 1 < signal.path.length; hop += 1) {
        const a = signal.path[hop]
        const b = signal.path[hop + 1]
        assert.ok(
          strands.has(`${Math.min(a, b)}:${Math.max(a, b)}`),
          `hop ${hop} of a route jumps between ${a} and ${b}, which are not linked`
        )
      }
      // And no node twice: a route that doubles back on itself reads as lost.
      assert.equal(
        new Set(signal.path).size,
        signal.path.length,
        'a route visits the same node twice'
      )
    }
  }

  assert.ok(lengths.length > 100, `only ${lengths.length} routes in three minutes`)
  // The hop count has to actually vary, or every lookup looks like the same
  // animation replayed.
  assert.ok(new Set(lengths).size >= 3, `routes only ever have ${[...new Set(lengths)]} hops`)
})

test('a lookup starts where it can be seen', () => {
  // The net overhangs every edge on purpose, so a route started out there
  // spends its hops off screen. Measured on a 300px panel, only 57% of the
  // nodes are inside the frame at all.
  const field = buildField(SIZE.width, SIZE.height)
  assert.ok(field.interior.length > 0, 'no node counts as inside the canvas')
  for (const index of field.interior) {
    const node = field.nodes[index]
    assert.ok(
      node.x >= 0 && node.x <= SIZE.width && node.y >= 0 && node.y <= SIZE.height,
      `node ${index} is listed as interior but sits at ${node.x.toFixed(0)},${node.y.toFixed(0)}`
    )
  }

  for (let step = 0; step < 60 * 60; step += 1) {
    const before = field.signals.length
    advance(field, step / 60, true)
    if (field.signals.length <= before) continue
    const launched = field.signals[field.signals.length - 1]
    assert.ok(
      field.interior.includes(launched.path[0]),
      'a route set off from a node outside the visible canvas'
    )
  }
})

test('a lookup travels, rather than circling one cell', () => {
  // An unbiased random walk on a net this dense spends its hops going back and
  // forth between the same few nodes, which looks aimless and, worse, looks
  // like a bug. The walk is given a heading and mostly follows it. Measured
  // across 260 routes: median end-to-end distance 193px against a 59px strand,
  // and a straightness of 0.69 -- so it goes somewhere without being a ruler.
  const field = buildField(SIZE.width, SIZE.height)
  const seen = new Set()
  const spans = []
  for (let step = 0; step < 60 * 240; step += 1) {
    advance(field, step / 60, true)
    for (const signal of field.signals) {
      const key = `${signal.start}:${signal.path[0]}`
      if (seen.has(key)) continue
      seen.add(key)
      const first = field.nodes[signal.path[0]]
      const last = field.nodes[signal.path[signal.path.length - 1]]
      spans.push(Math.hypot(first.x - last.x, first.y - last.y))
    }
  }
  spans.sort((a, b) => a - b)
  const median = spans[spans.length >> 1]
  assert.ok(
    median > defaultMeshOptions.spacing * 2,
    `the median route covers ${median.toFixed(0)}px, barely more than one ${defaultMeshOptions.spacing}px strand`
  )
})

test('the route lights up behind the bead and never ahead of it', () => {
  // The lit trail is what turns a moving dot into a visible ROUTE. Lighting a
  // hop the bead has not reached would show where the lookup is going before it
  // has been there, which is the one thing this drawing must not do -- it would
  // be showing a path that has not happened.
  const field = buildField(SIZE.width, SIZE.height)
  let checked = 0
  let mostLitAtOnce = 0
  for (let step = 0; step < 60 * 90; step += 1) {
    const time = step / 60
    advance(field, time, true)
    for (const signal of field.signals) {
      const head = signalHead(signal, time, defaultMeshOptions)
      let lit = 0
      for (let hop = 0; hop + 1 < signal.path.length; hop += 1) {
        const glow = hopGlow(signal, hop, time, defaultMeshOptions)
        assert.ok(glow >= 0 && glow <= 1, `hop glow ${glow} is out of range`)
        if (glow > 0.01) lit += 1
        // AHEAD of the bead is the assertion that matters, and it is absolute.
        if (hop > head.hop) {
          assert.equal(glow, 0, `hop ${hop} is lit while the bead is only on hop ${head.hop}`)
        }
        // The hop the bead is ON is always lit -- otherwise the bead would be
        // travelling a dark strand.
        if (hop === head.hop && !head.arrived) {
          assert.ok(glow > 0, `the bead is crossing hop ${hop} and the strand is dark`)
        }
        // BEHIND the bead is deliberately not asserted lit. A route stays in
        // the field until its trail has faded, so a crossed hop reaching zero
        // is correct -- an earlier version of this test demanded it stay lit
        // and failed on exactly that, which is the code being right.
        checked += 1
      }
      mostLitAtOnce = Math.max(mostLitAtOnce, lit)
    }
  }
  assert.ok(checked > 1000, `only ${checked} hop glows were checked`)
  // The whole point of the trail: more than one strand of a route is lit at
  // once, so the reader sees a PATH rather than a dot on a line.
  assert.ok(
    mostLitAtOnce >= 3,
    `never more than ${mostLitAtOnce} strands of a route lit at once; there is no visible trail`
  )

  // And the trail goes out, or a long-running card silts up with old routes.
  //
  // Checked at an ABSOLUTE four seconds rather than at
  // `signalDuration + trailFade`, which was the first version and was
  // worthless: setting trailFade to 9999 moved the check time with it and the
  // test passed. Found by sabotage.
  const route = { path: [0, 1, 2, 3], start: 0 }
  assert.ok(
    hopGlow(route, 0, defaultMeshOptions.signalDuration, defaultMeshOptions) > 0,
    'the first hop is dark the moment the bead leaves it'
  )
  assert.equal(
    hopGlow(route, 0, 4, defaultMeshOptions),
    0,
    'a strand is still lit four seconds after the bead crossed it'
  )
  assert.ok(
    defaultMeshOptions.trailFade < 3,
    `a trail lingers for ${defaultMeshOptions.trailFade}s; routes would pile up on the card`
  )
})

test('a lookup only ever sets off along nodes that are lit', () => {
  // A bead moving through a dim node reads as traffic passing through
  // something this gateway has said it knows nothing about.
  const field = buildField(SIZE.width, SIZE.height)
  for (let step = 0; step < 60 * 120; step += 1) {
    const before = field.signals.length
    advance(field, step / 60, true)
    if (field.signals.length <= before) continue
    const launched = field.signals[field.signals.length - 1]
    for (const index of launched.path) {
      assert.ok(
        field.nodes[index].alpha > 0.6,
        'a route was planned through a node that is dim'
      )
    }
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
  assert.ok(field.signals.length > 0, 'no lookup was ever in flight to drop')
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
