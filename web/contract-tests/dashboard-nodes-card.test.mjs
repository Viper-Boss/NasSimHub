import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { networkCardLabels } from '../src/i18n/networkCardNotes.ts'

const insight = await readFile(
  new URL('../src/components/ModuleInsightCards.vue', import.meta.url),
  'utf8'
)
const networkState = await readFile(new URL('../src/state/dashboardNetworkNodes.ts', import.meta.url), 'utf8')
const chrome = await readFile(
  new URL('../src/components/networkCard.css', import.meta.url),
  'utf8'
)

// The nodes card is the fourth insight card on the dashboard. Everything on it
// is measured -- the seed list is the probe's own result with its own
// round-trip times, and the two identities are what this gateway reports as its
// own -- so the tests here are about it staying that way.

test('the nodes card reads the probe and the DHT, and nothing else', () => {
  for (const call of ['getNKNNetworkNodes', 'getNKNNetworkStatus', 'getDHTSettings']) {
    assert.match(networkState, new RegExp(`\\b${call}\\b`), `the nodes card no longer calls ${call}`)
  }
  // Every figure it renders comes from one of those three refs. A literal in
  // the markup would be a mock-up number that shipped.
  const body = insight.slice(insight.indexOf('insight-nodes'))
  const template = body.slice(body.indexOf('<article'), body.indexOf('</article>') + 10)
  const standaloneNumber = /(?:^|[^A-Za-z0-9_$.-])\d+(?:[^A-Za-z0-9_$%.-]|$)/
  for (const line of template.split('\n')) {
    // Style-ish attributes carry real numbers (a percentage width); the guard
    // is about the text a reader sees.
    if (/:style=|Math\.round|\bslice\(/.test(line)) continue
    assert.ok(
      !standaloneNumber.test(line),
      `a figure is written into the nodes card markup: ${line.trim()}`
    )
  }
})

test('the nodes card says which path is off instead of showing an empty list', () => {
  // An empty seed list on a card titled "nodes and clients" reads as "nobody is
  // there", which is a claim. The card has to name the reason.
  assert.match(insight, /const nodesEmpty = computed/, 'the empty state is gone')
  assert.match(insight, /nodesEmpty[\s\S]{0,200}nknEnabled/, 'the empty state ignores whether NKN is on')
  assert.match(insight, /insight-card__empty[^>]*>\{\{ t\('dashboard\.nodesEmpty'\)/)
  // And it must not probe a channel that is switched off.
  assert.match(
    networkState,
    /enabled \? await getNKNNetworkNodes\(\) : null/,
    'the card probes NKN seed nodes even when NKN is disabled'
  )
})

test('the latency bars share one scale', () => {
  // A bar that fills its own track tells the reader nothing: four nodes at 38,
  // 71, 96 and 154 ms would all draw full. The scale is the slowest node
  // reached, so the bars compare with each other.
  assert.match(insight, /const seedScale = computed/)
  assert.match(insight, /node\.rtt_ms \/ seedScale/, 'the bars no longer share a scale')
  // Unreachable nodes report -1 and must not be listed as if they answered.
  assert.match(insight, /filter\(node => node\.rtt_ms >= 0\)/, 'unreachable nodes are listed as reached')
})

test('a seed address keeps the part that identifies it', () => {
  // Middle-truncating `mainnet-node-0042.nkn.org:30003` gives
  // `mainnet-no…rg:30003` -- the number goes and four rows read as four copies
  // of one node. The first render showed exactly that.
  assert.match(insight, /function shortHost/, 'the host shortener is gone')
  assert.match(insight, /shortHost\(node\.address\)/, 'seed rows no longer use the host shortener')
  // The full value stays reachable on hover, since the shortened form is lossy.
  assert.match(insight, /:title="node\.address"/, 'the full seed address is no longer available')
})

test('every label the nodes card renders exists in all nine locales', () => {
  // These moved into the lazily merged module rather than the locale files
  // because the startup budget had 0.1 KiB of headroom. A key that lives in
  // neither place renders as its own dotted name.
  const used = [...insight.matchAll(/t\('dashboard\.(nodes[A-Za-z]+)'/g)].map(m => m[1])
  assert.ok(used.length >= 6, `only ${used.length} nodes labels are referenced`)
  const locales = ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'vi-VN', 'es-ES', 'de-DE', 'fr-FR', 'pt-BR']
  for (const locale of locales) {
    const labels = networkCardLabels(locale)
    for (const key of used) {
      assert.ok(labels[key], `${locale} is missing dashboard.${key}`)
    }
  }
  // And the reachable count is a template, so the figure is interpolated rather
  // than baked into nine translated strings.
  for (const locale of locales) {
    assert.match(
      networkCardLabels(locale).nodesReachable,
      /\{n\}/,
      `${locale}'s reachable label has no slot for the count`
    )
  }
})

// The canvas fixes, guarded here because both are invisible when they regress.
test('the drawings bleed past their stage, so no canvas edge is ever in view', () => {
  // The operator reported a bright one-pixel line down the inside of the DHT
  // card's right border, at the canvas's last pixel column, present maximised
  // and gone at a smaller window. The cause was not reproduced; what this does
  // is put the canvas's own edge outside the crop.
  const rule = chrome.match(/\.net-stage canvas \{[^}]*\}/)
  assert.ok(rule, 'the stage canvas rule is gone')
  assert.match(rule[0], /position:\s*absolute/)
  assert.match(rule[0], /top:\s*-1px/)
  assert.match(rule[0], /left:\s*-1px/)
  // The size has to be stated: `inset: -1px` alone does not stretch a canvas,
  // because it is a replaced element whose `width: auto` resolves to its
  // attribute. Measured, that left it 49px short on the right.
  assert.match(rule[0], /width:\s*calc\(100% \+ 2px\)/, 'the canvas no longer spans the bleed')
  assert.match(rule[0], /height:\s*calc\(100% \+ 2px\)/, 'the canvas no longer spans the bleed')
  // And the crop has to exist, or the bleed just overflows the card.
  const stage = chrome.match(/\.net-stage \{[^}]*\}/)
  assert.match(stage[0], /overflow:\s*hidden/, 'the stage no longer crops the bleed')
})

test('both canvases resize their backing store on either dimension', async () => {
  // The earlier version compared only the width and set the height inside that
  // branch, so a stage that changed height without changing width kept a stale
  // store and the browser stretched the drawing to fit.
  for (const name of ['DHTMeshCard.vue', 'NetworkGlobeCard.vue']) {
    const source = await readFile(
      new URL(`../src/components/${name}`, import.meta.url),
      'utf8'
    )
    assert.match(
      source,
      /element\.width !== storeWidth \|\| element\.height !== storeHeight/,
      `${name} resizes its backing store on the width alone`
    )
    // And the clear covers the whole store in device space, not the CSS box
    // under the scale transform.
    assert.match(
      source,
      /setTransform\(1, 0, 0, 1, 0, 0\)\s*\n\s*context\.clearRect\(0, 0, element\.width, element\.height\)/,
      `${name} clears the CSS box instead of the whole backing store`
    )
  }
})

test('the seed latency bars all start at the same x', () => {
  // The operator reported the first bar sitting slightly right of the other
  // three. Measured in the browser: every track was 46px wide, but the "72 ms"
  // row's track began 6.67px right of the rest -- exactly the width difference
  // between a two-digit and a three-digit reading.
  //
  // The cause was that each <li> was its own grid, so the three columns were
  // measured per row. A narrower reading made the last column narrower, and the
  // `1fr` address column swallowed the slack, carrying the bar right with it.
  // Bars drawn on one shared scale, for the express purpose of being compared
  // with one another, cannot begin in different places.
  //
  // The fix is structural rather than a number: the columns live on the <ul>
  // and the rows dissolve into it, so all four rows share one set of tracks and
  // the reading column sizes to the widest reading in the list. That is also
  // why this is asserted as "the <ul> owns the columns" rather than as a width
  // -- a hard-coded width would look identical today and break on the first
  // four-digit latency.
  const list = insight.match(/\.insight-nodes__seeds \{[^}]*\}/)
  assert.ok(list, 'the seed list rule is gone')
  assert.match(
    list[0],
    /grid-template-columns:/,
    'the seed list no longer owns the columns, so the rows measure them separately'
  )

  const row = insight.match(/\.insight-nodes__seeds li \{[^}]*\}/)
  assert.ok(row, 'the seed row rule is gone')
  assert.match(
    row[0],
    /display:\s*contents/,
    'a seed row is a grid of its own again, which is what misaligned the bars'
  )
  assert.doesNotMatch(
    row[0],
    /grid-template-columns:/,
    'a seed row defines its own columns, so they are measured per row'
  )

  // And the readings are right-aligned in that shared column, so the digits
  // line up under one another rather than only the "ms".
  const reading = insight.match(/\.insight-nodes__rtt \{[^}]*\}/)
  assert.ok(reading, 'the reading rule is gone')
  assert.match(reading[0], /text-align:\s*right/, 'the readings no longer line up on the right')
  assert.match(reading[0], /tabular-nums/, 'the readings no longer use fixed-width digits')
})
