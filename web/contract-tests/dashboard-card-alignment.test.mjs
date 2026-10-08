import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

// The dashboard's card field is a grid, and a grid row offers two bad choices
// when its cards differ in height: size to content and the bottom edges come
// out ragged, or stretch and a void opens under the short cards. Measured on
// the real layout before this was fixed, the worst row had its bottom edges
// 271px apart -- a 300px modem card sitting beside a 560px network card.
//
// The way out is not a CSS trick, it is not putting those two cards in the
// same row: one grid for the short cards, one for the tall ones, stretch
// within each. Measured after: 0px, at every width from 1024 to 3000.
//
// None of that geometry can be asserted without a browser, so these tests
// guard the structure that produces it. Each one names the failure it prevents.

const dashboard = await readFile(
  new URL('../src/views/DashboardView.vue', import.meta.url),
  'utf8'
)
const insightCards = await readFile(
  new URL('../src/components/ModuleInsightCards.vue', import.meta.url),
  'utf8'
)
// The card chrome moved out of ModuleInsightCards.vue when a second component
// started rendering cards into the same row: a scoped style block cannot be
// shared, so the rules that make the cards one object live in this stylesheet
// and both components import it. The height rules below are asserted here.
const insightChrome = await readFile(
  new URL('../src/components/insightCard.css', import.meta.url),
  'utf8'
)
const strip = await readFile(
  new URL('../src/components/SystemMetricsStrip.vue', import.meta.url),
  'utf8'
)
const deviceCardGrid = await readFile(
  new URL('../src/components/DeviceCardGrid.vue', import.meta.url),
  'utf8'
)
const tokens = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')

test('tall network cards live in their own grid, away from the short ones', () => {
  // If these move back into the main grid they will share rows with modem
  // cards again and the 271px ragged row comes back.
  const tallGrid = dashboard.slice(dashboard.indexOf('dashboard-module-grid--tall'))
  assert.match(tallGrid, /<NetworkGlobeCard/, 'the globe left the tall grid')
  assert.match(tallGrid, /<DHTMeshCard/, 'the mesh card left the tall grid')

  // And they must not also appear in the short-card grid.
  const shortGrid = dashboard.slice(
    dashboard.indexOf('<div class="dashboard-module-grid">'),
    dashboard.indexOf('dashboard-module-grid--tall')
  )
  assert.doesNotMatch(shortGrid, /<NetworkGlobeCard/)
  assert.doesNotMatch(shortGrid, /<DHTMeshCard/)
  // The modem and insight cards stay together, because they are the same
  // height class: one grid per device, which is the root of the card field
  // every device group shares.
  assert.match(deviceCardGrid, /^<template>\s*<div class="dashboard-module-grid">/m)
  assert.match(deviceCardGrid, /<ModuleCard/)
  assert.match(deviceCardGrid, /<ModuleInsightCards/)
  assert.doesNotMatch(deviceCardGrid, /<NetworkGlobeCard|<DHTMeshCard/)
  // Nodes and clients keeps its original ninth-card position in each device
  // field; the NAS services section has only its original tall cards.
  assert.doesNotMatch(dashboard, /<ModuleInsightCards scope="network"/)
  const insightTemplate = insightCards.slice(insightCards.indexOf('<template>'), insightCards.lastIndexOf('<style'))
  assert.equal((insightTemplate.match(/<article class="insight-card">/g) ?? []).length, 4)
  assert.ok(insightTemplate.indexOf('dashboard.cellCard') < insightTemplate.indexOf('dashboard.nodesTitle'))
})

test('cards in a row stretch to a shared height', () => {
  // align-items: start is what left the bottoms ragged. It is only safe to
  // stretch because of the split above, so the two rules belong together.
  assert.match(
    dashboard,
    /\.dashboard-module-grid \{[^}]*align-items:\s*stretch/,
    'the module grid no longer stretches its cards to a shared height'
  )
  assert.doesNotMatch(
    dashboard,
    /\.dashboard-module-grid \{[^}]*align-items:\s*start/,
    'align-items: start is back; card bottoms will be ragged again'
  )
})

test('the column count is computed from the minimum, not the maximum', () => {
  // auto-fill counts tracks using the maximum of a definite minmax, so
  // minmax(320px, 420px) answers "one column" for everything from 420px to
  // 851px of grid width -- one card and up to 431px of dead space beside it.
  assert.match(
    tokens,
    /--dashboard-card-track:\s*repeat\(auto-fill,\s*minmax\(min\(\d+px, 100%\),\s*1fr\)\)/,
    'the card track is back to a fixed maximum, which wastes a column of space'
  )
  assert.doesNotMatch(
    tokens,
    /--dashboard-card-track:[^;]*minmax\(\d+px,\s*\d+px\)/,
    'a definite maximum is back in the card track; it packs one column where two fit'
  )
})

test('the metrics strip and the card field share one track', () => {
  // They are one field of cards, one above the other, so their columns have to
  // fall on the same vertical lines. The strip used to declare `repeat(4,
  // 1fr)` of its own and the grid below counted its own columns from the
  // available width, so the two agreed only by accident: measured on the
  // operator's 3441px screen, the strip drew 4 columns of 622px across exactly
  // the width the field below split into 7 of 350px. Only the left edge lined
  // up. Measured again after this change, at nine widths from 1024 to 3441,
  // every strip edge falls on a column edge.
  //
  // One token read by both is the only arrangement that cannot drift, so that
  // is what is asserted rather than two values that happen to match today.
  assert.match(
    dashboard,
    /\.dashboard-module-grid \{[^}]*grid-template-columns: var\(--dashboard-card-track\)/,
    'the card grid no longer reads the shared track'
  )
  assert.match(
    strip,
    /\.tm \{[^}]*grid-template-columns: var\(--dashboard-card-track\)/,
    'the metrics strip no longer reads the shared track'
  )
  // And the strip must not re-declare a column count behind a media query:
  // those ask how wide the WINDOW is, while what decides how many tiles fit is
  // how wide the strip's own container is. On this dashboard the panes either
  // side take roughly 800px out of the middle, so the two numbers are far
  // apart and the breakpoints fought the track.
  const stripStyles = strip.split('<style')[1] ?? ''
  const redeclared = [...stripStyles.matchAll(/\.tm \{[^}]*grid-template-columns:([^;]*);/g)]
    .map(match => match[1].trim())
  assert.deepEqual(
    redeclared,
    ['var(--dashboard-card-track)'],
    'the strip declares its column count more than once; it will disagree with the cards'
  )
})

test('cards fill their track instead of leaving a hole beside them', () => {
  // Each card component caps itself at 420px, which is right on its own and
  // wrong in a grid whose track is already sized to the row: measured holes of
  // 108px at 1024 and 46px at 1440 before this override existed.
  assert.match(
    dashboard,
    /\.dashboard-module-grid[^{]*\{\s*max-width:\s*none/,
    'the per-card 420px cap is no longer lifted inside the grid'
  )
})

test('a stretched card hands the extra height to its content', () => {
  // Stretching without this leaves the figures at the top of the card and
  // dead space under them, which is the void that made stretching look worse
  // than ragged in the first place.
  assert.match(
    insightChrome,
    /\.insight-card__body \{[^}]*flex:\s*1 1 auto/,
    'the insight card body no longer grows with the card'
  )
  assert.match(
    insightChrome,
    /\.insight-card__body \{[^}]*justify-content:\s*space-between/,
    'the insight card body no longer distributes its extra height'
  )
})

test('both card components render into the same row from one stylesheet', () => {
  // Two components, one row. The moment either one styles its own frame the
  // row stops being a row: a different padding or radius on three of seven
  // cards reads as two screens spliced together, and nothing in a scoped style
  // block can prevent it. So the chrome is one file, both import it, and
  // neither redefines the classes it owns.
  const extraCards = readFileSync(
    new URL('../src/components/ModuleExtraCards.vue', import.meta.url),
    'utf8'
  )
  for (const [name, source] of [
    ['ModuleInsightCards.vue', insightCards],
    ['ModuleExtraCards.vue', extraCards]
  ]) {
    assert.match(
      source,
      /import '\.\/insightCard\.css'/,
      `${name} no longer imports the shared card chrome`
    )
    // Nothing in the frame's namespace may be defined in a component. Not a
    // list of five class names -- the whole prefix, so a SIXTH frame class
     // added to one component's scoped block fails here too instead of quietly
    // existing on three cards out of seven. A component's own content keeps its
    // own prefix (.insight-sec__, .insight-stats__, .insight-net__), and using
    // a frame class as an ancestor is fine, hence the anchor on the selector.
    const scoped = source.split('<style')[1] ?? ''
    const frameRule = /(?:^|[\s,>+~])\.insight-cards?(?:__[a-z-]+)?\s*\{/m
    const offender = scoped.match(frameRule)
    assert.ok(
      !offender,
      `${name} defines ${String(offender?.[0]).trim()} in its own style block; ` +
        'the shared stylesheet owns the card frame'
    )
  }

  // `display: contents` is what makes each card a direct grid item. Without it
  // the wrapper takes one cell and stacks its cards inside it, which is three
  // cards in the width of one.
  assert.match(
    insightChrome,
    /\.insight-cards \{[^}]*display:\s*contents/,
    'the card wrapper is no longer transparent to the grid'
  )
})

test('the two network drawings are the same height, from one rule', async () => {
  // Both cards sit in one row and stretch together. If one drawing is a
  // different height from the other, the taller card sets the row height and
  // the shorter one carries the difference as a void.
  //
  // The drawings used to be square canvases capped at a shared maximum; they
  // are now full-bleed panels of a fixed height, so the height is the thing
  // that has to match. It matches by construction: both cards use the same
  // `.net-stage` class from the shared stylesheet, and neither may set its own
  // height. That is stronger than two numbers a test compares -- there is only
  // one number.
  const chrome = await readFile(
    new URL('../src/components/networkCard.css', import.meta.url),
    'utf8'
  )
  const stage = chrome.match(/\.net-stage \{[^}]*\}/)
  assert.ok(stage, 'the shared stage rule is gone')
  assert.match(stage[0], /height:\s*\d+px/, 'the shared stage no longer fixes a height')

  for (const name of ['NetworkGlobeCard.vue', 'DHTMeshCard.vue']) {
    const source = await readFile(
      new URL(`../src/components/${name}`, import.meta.url),
      'utf8'
    )
    assert.match(source, /class="net-stage"/, `${name} no longer uses the shared stage`)
    const styles = source.split('<style')[1] ?? ''
    assert.doesNotMatch(
      styles,
      /height:\s*\d+px/,
      `${name} sets its own drawing height; the row will go ragged`
    )
  }
})
