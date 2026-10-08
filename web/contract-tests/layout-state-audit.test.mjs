import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

// The layout and state audit of the pages the r127 device work touched: the
// dashboard's device groups, the nodes view, Settings -> Security and the
// update centre's Node list.
//
// These are SOURCE checks -- what can be decided by reading CSS and markup. A
// rule that passes here says the stylesheet contains no fixed width wider than
// a 360px screen and that long identifiers are allowed to wrap; whether the
// result looks right is a question for a browser, and nobody has looked.

const read = path => readFile(new URL(path, import.meta.url), 'utf8')

const audited = [
  'components/DeviceCardGrid.vue',
  'components/NodeAgentUpdatesCard.vue',
  'components/LinkBoundaryList.vue',
  'components/NodeSecurityBlock.vue',
  'components/NodeUpdatePanel.vue',
  'components/SecurityCentrePanel.vue',
  'components/SecurityJournalViewer.vue',
  'components/SystemMetricsStrip.vue',
  'components/ModuleInsightCards.vue',
  'components/ModuleExtraCards.vue',
  'views/NasSimHubNodesView.vue'
]
const sources = Object.fromEntries(
  await Promise.all(audited.map(async path => [path, await read(`../src/${path}`)]))
)
const [dashboard, insightCss] = await Promise.all([
  read('../src/views/DashboardView.vue'),
  read('../src/components/insightCard.css')
])

/** The stylesheet of a component, comments removed. */
function styles(source) {
  return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)]
    .map(match => match[1])
    .join('\n')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
}

test('new and touched panels take every colour from the shared tokens', () => {
  // The components added by this work, and the nodes view whose literal
  // colours were replaced. (The dashboard's older cards keep a few literals
  // of their own, e.g. chart strokes; they are not part of this change.)
  for (const path of [
    'components/DeviceCardGrid.vue',
    'components/NodeAgentUpdatesCard.vue',
    'components/LinkBoundaryList.vue',
    'components/NodeSecurityBlock.vue',
    'components/NodeUpdatePanel.vue',
    'components/SecurityCentrePanel.vue',
    'components/SecurityJournalViewer.vue',
    'views/NasSimHubNodesView.vue'
  ]) {
    const css = styles(sources[path])
    assert.doesNotMatch(css, /#[0-9a-fA-F]{3,8}\b/, `${path} has a literal hex colour`)
    assert.doesNotMatch(css, /\b(?:rgb|rgba|hsl|hsla)\(/, `${path} has a literal colour function`)
  }
  const absent = insightCss.slice(insightCss.indexOf('.insight-card__absent {'), insightCss.indexOf('.insight-line {'))
  assert.doesNotMatch(absent.replace(/\/\*[\s\S]*?\*\//g, ' '), /#[0-9a-fA-F]{3,8}\b|\brgba?\(/)
})

test('nothing in these panels is wider than a 360px screen by construction', () => {
  for (const [path, source] of Object.entries(sources)) {
    const css = styles(source)
    // A grid track with a fixed minimum must be capped by the container.
    for (const track of css.matchAll(/minmax\(\s*(\d+)px/g)) {
      assert.ok(Number(track[1]) < 200, `${path}: minmax(${track[1]}px, ...) is not capped with min(100%, ...)`)
    }
    // No fixed width or min-width beyond the narrowest supported screen.
    for (const rule of css.matchAll(/(?:^|[\s;{])(?:min-)?width:\s*(\d+)px/g)) {
      assert.ok(Number(rule[1]) <= 320, `${path}: a fixed width of ${rule[1]}px`)
    }
  }
  // The dashboard's card fields share one track, capped the same way.
  assert.match(dashboard, /\.dashboard-module-grid \{\s*display: grid;\s*grid-template-columns: var\(--dashboard-card-track\);/)
  assert.match(sources['components/SystemMetricsStrip.vue'], /\.tm \{[\s\S]{0,80}grid-template-columns: var\(--dashboard-card-track\);/)
  assert.match(sources['views/NasSimHubNodesView.vue'], /\.node-inventory__list \{[^}]*minmax\(min\(100%, 300px\), 1fr\)/)
})

test('long device IDs, fingerprints and sentences are allowed to wrap', () => {
  // The group heading's tag is a device ID.
  assert.match(dashboard, /\.dashboard-device-group__head \{[^}]*flex-wrap: wrap/)
  assert.match(dashboard, /\.dashboard-device-group__head > span \{[^}]*overflow-wrap: anywhere/)
  // A label/value line's value can be a device ID or an operator's name.
  assert.match(insightCss, /\.insight-line__value \{[^}]*min-width: 0;[^}]*overflow-wrap: anywhere/)
  // "Not provided by this device", and the reason under it.
  assert.match(insightCss, /\.insight-card__absent \{[^}]*overflow-wrap: anywhere/)
  assert.match(sources['components/SystemMetricsStrip.vue'], /\.tm-box__absent \{[^}]*overflow-wrap: anywhere/)
  // Update centre rows: name, ID and facts each wrap; the button stacks under
  // them on a narrow card instead of being pushed off it.
  const agents = sources['components/NodeAgentUpdatesCard.vue']
  for (const selector of ['.node-agents__identity strong', '.node-agents__id', '.node-agents__facts dd', '.node-agents__state']) {
    assert.match(agents, new RegExp(`${selector.replace(/\./g, '\\.')} \\{[^}]*overflow-wrap: anywhere`), selector)
  }
  assert.match(agents, /@media \(max-width: 860px\) \{[\s\S]*\.node-agents__open \{\s*grid-column: 1 \/ -1;/)
  // Nodes view: a card headed by a bare device ID, its facts and its table.
  const nodes = sources['views/NasSimHubNodesView.vue']
  assert.match(nodes, /\.node-inventory__card-head h2 \{[^}]*overflow-wrap: anywhere/)
  assert.match(nodes, /\ndd \{[^}]*overflow-wrap: anywhere/)
  assert.match(nodes, /\.node-inventory__processes th, \.node-inventory__processes td \{[^}]*overflow-wrap: anywhere/)
  assert.match(nodes, /\.node-inventory__actions \{[^}]*flex-wrap: wrap/)
})

test('the nodes view is an administrator\'s page: a member gets a sentence, no controls and no poll', () => {
  const nodes = sources['views/NasSimHubNodesView.vue']
  assert.match(nodes, /const admin = computed\(\(\) => sessionState\.role === 'admin'\)/)
  assert.match(nodes, /<div v-if="!admin" class="node-inventory">\s*<p class="node-inventory__notice" role="status">\{\{ securityCopy\.updates\.forbidden \}\}<\/p>\s*<\/div>\s*<div v-else class="node-inventory">/)
  assert.match(nodes, /onMounted\(\(\) => \{\s*if \(!admin\.value\) return\s*void refresh\(\)/)
  // Loading, failure and "no devices" are three different things to read.
  assert.match(nodes, /\{\{ loading \? copy\.loading : copy\.refresh \}\}/)
  assert.match(nodes, /<p v-if="error" role="alert" class="node-inventory__error">/)
  assert.match(nodes, /<p v-else-if="!loading && nodes\.length === 0" class="node-inventory__empty">/)
})

test('the nodes view shows a resource the Node did not sample as unknown, not as zero', () => {
  const nodes = sources['views/NasSimHubNodesView.vue']
  assert.match(nodes, /node\.resources\.memory_total_bytes > 0 \? `[^`]+` : copy\.unknown/)
  assert.match(nodes, /node\.resources\.uptime_seconds > 0 \? uptime\(node\.resources\.uptime_seconds\) : copy\.unknown/)
  assert.match(nodes, /node\.resources\.process_count > 0 \? node\.resources\.process_count : copy\.unknown/)
})
