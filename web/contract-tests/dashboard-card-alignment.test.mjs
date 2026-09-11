import assert from 'node:assert/strict'
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
  // The modem and insight cards stay where they are, together, because they
  // are the same height class.
  assert.match(shortGrid, /<ModuleCard/)
  assert.match(shortGrid, /<ModuleInsightCards/)
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
    dashboard,
    /repeat\(auto-fill,\s*minmax\(min\(320px, 100%\),\s*1fr\)\)/,
    'the grid is back to a fixed maximum track, which wastes a column of space'
  )
  assert.doesNotMatch(
    dashboard,
    /repeat\(auto-fill,\s*minmax\(320px,\s*420px\)\)/,
    'minmax(320px, 420px) is back; it packs one column where two fit'
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
    insightCards,
    /\.insight-card__body \{[^}]*flex:\s*1 1 auto/,
    'the insight card body no longer grows with the card'
  )
  assert.match(
    insightCards,
    /\.insight-card__body \{[^}]*justify-content:\s*space-between/,
    'the insight card body no longer distributes its extra height'
  )
})

test('the two network drawings are capped to the same size', async () => {
  // Both cards sit in one row and stretch together. If one drawing grows with
  // the card and the other is capped, the taller card sets the row height and
  // the shorter one carries the difference as a void.
  const globe = await readFile(
    new URL('../src/components/NetworkGlobeCard.vue', import.meta.url),
    'utf8'
  )
  const mesh = await readFile(
    new URL('../src/components/DHTMeshCard.vue', import.meta.url),
    'utf8'
  )
  const cap = source => {
    const match = source.match(/max-width:\s*(\d+)px/)
    return match ? Number(match[1]) : null
  }
  assert.equal(
    cap(globe.slice(globe.indexOf('.globe-canvas'))),
    cap(mesh.slice(mesh.indexOf('.mesh-canvas'))),
    'the globe and the mesh are drawn at different maximum sizes'
  )
})
