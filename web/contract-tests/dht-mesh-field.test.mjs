import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  GLOW_RISE_MS,
  buildField,
  buildSky,
  defaultFieldOptions,
  litTarget,
  project,
  reconcileLit,
  seeded,
  step
} from '../src/components/dhtMeshField.ts'

// The mesh animation's decisions are made here rather than inside the render
// loop so they can be checked on a fixed clock. The one that matters most is
// the last test in this file: the picture must never show traffic the gateway
// did not actually have.

test('the field is the same on every build, so a remount does not reshuffle it', () => {
  const first = buildField()
  const second = buildField()
  assert.deepEqual(
    first.nodes.map(node => [node.x, node.y, node.z]),
    second.nodes.map(node => [node.x, node.y, node.z])
  )
  assert.deepEqual(first.links, second.links)
})

test('no node is left floating without an edge', () => {
  const field = buildField()
  const degree = new Array(field.nodes.length).fill(0)
  for (const link of field.links) {
    degree[link.from] += 1
    degree[link.to] += 1
  }
  const orphans = degree.filter(count => count === 0).length
  assert.equal(orphans, 0, `${orphans} nodes have no links; the mesh would read as scattered dots`)
})

test('node size follows how well connected the node is', () => {
  const field = buildField()
  const degree = new Array(field.nodes.length).fill(0)
  for (const link of field.links) {
    degree[link.from] += 1
    degree[link.to] += 1
  }
  let mostLinked = 0
  let leastLinked = 0
  degree.forEach((count, index) => {
    if (count > degree[mostLinked]) mostLinked = index
    if (count < degree[leastLinked]) leastLinked = index
  })
  assert.ok(
    field.nodes[mostLinked].size > field.nodes[leastLinked].size,
    'the busiest node should draw larger than the quietest one'
  )
})

test('the lit set counts real peers and never takes over the field', () => {
  const total = defaultFieldOptions.nodes
  assert.equal(litTarget(0, total), 0)
  assert.equal(litTarget(6, total), 6)
  // A large routing table must still leave dim nodes, because the dim ones are
  // what says "there is more of this network than we can see".
  assert.ok(litTarget(10_000, total) < total)
})

test('nodes join and leave a few at a time rather than all at once', () => {
  const field = buildField()
  const random = seeded(7)
  const changed = reconcileLit(field, 20, random, 1)
  assert.equal(changed.length, 1)
  assert.equal(field.nodes.filter(node => node.live > 0.5).length, 1)
})

test('a node fades in over a couple of seconds instead of appearing', () => {
  const field = buildField()
  const random = seeded(11)
  reconcileLit(field, 1, random, 1)
  const index = field.nodes.findIndex(node => node.live > 0.5)
  assert.ok(index >= 0)

  // One frame in, the node is on its way but nowhere near lit.
  step(field, { elapsed: 16, spin: 0, target: 1, animate: true, random })
  const earlyGlow = field.nodes[index].glow
  assert.ok(earlyGlow > 0, 'the node should have started to appear')
  assert.ok(earlyGlow < 0.1, `the node jumped to ${earlyGlow}; it should still be dim one frame in`)

  // Run out the fade and it settles at full brightness.
  for (let elapsed = 0; elapsed < GLOW_RISE_MS * 4; elapsed += 16) {
    step(field, { elapsed: 16, spin: 0, target: 1, animate: true, random })
  }
  assert.ok(field.nodes[index].glow > 0.99)
})

test('the mesh turns at the same rate whatever the frame rate', () => {
  const slow = buildField()
  const fast = buildField()
  const random = seeded(3)
  step(slow, { elapsed: 100, spin: 0.5, target: 0, animate: true, random })
  for (let frame = 0; frame < 10; frame += 1) {
    step(fast, { elapsed: 10, spin: 0.5, target: 0, animate: true, random })
  }
  assert.ok(
    Math.abs(slow.rotation - fast.rotation) < 1e-9,
    `one 100ms frame turned ${slow.rotation}, ten 10ms frames turned ${fast.rotation}`
  )
})

test('nothing moves when motion is turned off', () => {
  const field = buildField()
  const random = seeded(5)
  reconcileLit(field, 20, random, 20)
  for (let frame = 0; frame < 400; frame += 1) {
    step(field, { elapsed: 16, spin: 0.5, target: 20, animate: false, random })
  }
  assert.equal(field.rotation, 0)
  assert.equal(field.signals.length, 0)
  // The picture still resolves: glow settles so the mesh is drawn lit, just
  // still. A reduced-motion reader gets the same information, without motion.
  assert.ok(field.nodes.filter(node => node.glow > 0.9).length >= 20)
})

test('no peers means no traffic is ever drawn', () => {
  // This is the honesty invariant. A mesh animating hops while the DHT is off
  // or isolated would be inventing telemetry, which is worse than drawing
  // nothing at all.
  const field = buildField()
  const random = seeded(13)
  for (let frame = 0; frame < 4000; frame += 1) {
    step(field, { elapsed: 16, spin: 0.2, target: litTarget(0, field.nodes.length), animate: true, random })
  }
  assert.equal(field.signals.length, 0)
  assert.equal(field.nodes.filter(node => node.glow > 0.01).length, 0)
})

test('a signal only ever travels between two lit nodes', () => {
  const field = buildField()
  const random = seeded(17)
  reconcileLit(field, 40, random, 40)
  let seen = 0
  for (let frame = 0; frame < 3000; frame += 1) {
    step(field, { elapsed: 16, spin: 0.2, target: 40, animate: true, random })
    for (const signal of field.signals) {
      const link = field.links[signal.link]
      assert.ok(link, 'a signal points at a link that exists')
      assert.ok(
        field.nodes[link.from].glow > 0.5 && field.nodes[link.to].glow > 0.5,
        'a signal was drawn on a link whose endpoints are not both lit'
      )
      seen += 1
    }
  }
  assert.ok(seen > 0, 'no signals were produced at all; the animation would be static')
})

test('projection keeps every node inside the canvas', () => {
  const field = buildField()
  for (let turn = 0; turn < Math.PI * 2; turn += 0.2) {
    for (const node of field.nodes) {
      const point = project(node, turn, 320, 320)
      assert.ok(point.x >= 0 && point.x <= 320, `x ${point.x} escaped the canvas`)
      assert.ok(point.y >= 0 && point.y <= 320, `y ${point.y} escaped the canvas`)
      assert.ok(point.depth >= 0 && point.depth <= 1)
    }
  }
})

// The sky is scenery, and it has to look like a sky rather than confetti.
test('the sky is deterministic, mostly faint, and banded', () => {
  const first = buildSky()
  const second = buildSky()
  assert.deepEqual(first, second, 'the sky reshuffles between mounts')

  // The brightness curve is what makes it read as a photograph rather than an
  // even spray of dots. The property that matters is where the MIDDLE of the
  // field sits, not how long the bright tail is: raising a uniform random to
  // the fourth power puts the median at 0.5^4, so half the stars are nearly
  // invisible and the ones you notice are a minority riding on top of them.
  //
  // Asserted as a median after an initial version checked the size of the
  // bright tail against a number picked from intuition -- which the real curve
  // failed while looking exactly right on screen. The tail is wider than it
  // feels; the median is the thing being designed.
  const brightness = first.map(star => star.brightness).sort((a, b) => a - b)
  const median = brightness[Math.floor(brightness.length / 2)]
  assert.ok(
    median < 0.15,
    `the median star brightness is ${median.toFixed(3)}; the field is not mostly faint`
  )
  const bright = first.filter(star => star.brightness > 0.5).length
  assert.ok(
    bright > 0 && bright < first.length * 0.3,
    `${bright} of ${first.length} stars are bright; there should be a minority of them`
  )
  // Sparkles: the rule is structural, not a count. A diffraction cross is
  // glare, so it may only sit on a star bright enough to produce glare --
  // that is the invariant, and it holds however the density is later tuned.
  // The exact proportion is a tuning choice, so it is bounded loosely rather
  // than pinned to a number picked from intuition (two earlier assertions in
  // this test were exactly that, and both failed against a curve that looked
  // right on screen).
  const sparkles = first.filter(star => star.sparkle)
  assert.ok(sparkles.length > 0, 'nothing sparkles, so the sky has no glare in it')
  assert.ok(
    sparkles.length < first.length * 0.2,
    `${sparkles.length} of ${first.length} stars sparkle; a cross on everything is glitter`
  )
  for (const star of sparkles) {
    assert.ok(
      star.brightness > 0.5,
      `a star at brightness ${star.brightness.toFixed(3)} draws a glare cross`
    )
  }

  // Every star inside the frame: one drawn outside is invisible work, and a
  // star half off the edge reads as a rendering fault.
  for (const star of first) {
    assert.ok(
      star.x >= -1 && star.x <= 1 && star.y >= -1 && star.y <= 1,
      `star at ${star.x},${star.y} is outside the field`
    )
  }

  // The band. A third of the stars are drawn toward the y = -x diagonal, and
  // that is what separates "a piece of sky" from "dots on black".
  //
  // The window and the threshold are measured against a uniform field rather
  // than reasoned about, because the first version of this assertion was
  // reasoned about and was worthless: x + y for a uniform square is triangular
  // rather than flat, so about 31% of a uniform field already lands inside
  // |x + y| < 0.35 -- roughly double what I had assumed -- and the test passed
  // with the band generator switched off entirely. Found by switching it off.
  //
  // Measured in the tighter window: 40% banded against 18% uniform. Thirty is
  // clear of the uniform case and leaves the density free to be retuned.
  const near = first.filter(star => Math.abs(star.y + star.x) < 0.2).length
  const share = (near / first.length) * 100
  assert.ok(
    share > 30,
    `${share.toFixed(1)}% of stars sit in the band window; a uniform field gives about 18%`
  )
})

// The card must not turn, and must draw through the extracted painter.
//
// Both are decisions that a later edit could undo without anything failing:
// the rotation machinery is still in this module and still tested, so putting a
// spin back would work perfectly and silently reverse what the operator asked
// for; and a painter nobody calls is the failure this project has been caught
// by before.
test('the mesh card holds still and paints through the painter', () => {
  const card = readFileSync(new URL('../src/components/DHTMeshCard.vue', import.meta.url), 'utf8')
  // `0\b` matched the 0 in `0.085`, because a dot is a word boundary -- the
  // first version of this assertion passed with the rotation put straight back.
  // The value has to be the whole value.
  assert.match(
    card,
    /spin:\s*0\s*,/,
    'the field is spinning again; it was asked to hold still'
  )
  assert.match(card, /paintMesh\(/, 'the card no longer draws through the painter')
  assert.match(card, /buildSky\(/, 'the card no longer builds a sky')
  // The painting itself must live in the painter, or the preview harness is
  // reviewing something the card does not use.
  assert.ok(
    !/createRadialGradient/.test(card),
    'the card is painting inline again, so the painter is no longer what ships'
  )
})
