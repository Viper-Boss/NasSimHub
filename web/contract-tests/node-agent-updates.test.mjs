import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { createMemoryHistory, createRouter } from 'vue-router'

import { readFleetNode } from '../src/api/nodeFleet.ts'
import { i18n } from '../src/i18n/index.ts'
import { deviceGroupLabels } from '../src/i18n/deviceGroupLabels.ts'
import {
  applyFleetFailure,
  applyFleetNodes,
  nodeFleetState,
  resetNodeFleet
} from '../src/state/nodeFleet.ts'
import { login, logout, sessionState } from '../src/state/session.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'

// The update centre's entry for the Nodes' own software (Settings -> About).
//
// It lists the paired devices with the agent version each reported and opens
// the per-device update panel. The one thing it must never do is present a
// remembered version as a current one, so most of what follows is about where
// its figures come from and how an offline device is worded.
//
// OFFLINE: gateway documents written by hand with fake identifiers, rendered
// on the server renderer. Not observed on a device or in a browser.

const read = path => readFile(new URL(path, import.meta.url), 'utf8')
const [card, about, panel, updateCard] = await Promise.all([
  read('../src/components/NodeAgentUpdatesCard.vue'),
  read('../src/components/AboutSettingsPanel.vue'),
  read('../src/components/NodeUpdatePanel.vue'),
  read('../src/components/SoftwareUpdateCard.vue')
])

function code(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

/** Signs in through the real session code, against a stubbed gateway. */
async function signIn(role) {
  const original = globalThis.fetch
  globalThis.fetch = async (_path, init = {}) => {
    const method = (init.method || 'GET').toUpperCase()
    if (method === 'DELETE') return new Response(null, { status: 204 })
    const body = {
      authenticated: true, setup_required: false, user_id: `user_${role}`, username: role, role,
      csrf_token: 'fixture-csrf', ios_pairing_enabled: false, allowed_line_ids: [], language: 'zh-CN'
    }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }
  try {
    if (sessionState.status === 'authenticated' && sessionState.role === role) return
    // Signed out first: a role change on a live session reloads the page.
    if (sessionState.status === 'authenticated') await logout()
    await login(role, 'fixture-password')
  } finally {
    globalThis.fetch = original
  }
  assert.equal(sessionState.role, role)
}

const online = {
  node_id: 'NSH-410-TEST01', display_name: 'fixture stick A', online: true,
  agent_version: '9.9.1-fixture', last_seen_at: '2026-01-02T03:04:05Z', pair_state: 'paired'
}
const offline = {
  node_id: 'NSH-410-TEST02', display_name: 'fixture stick B', online: false,
  agent_version: '9.9.0-fixture', last_seen_at: '2026-01-01T00:00:00Z', pair_state: 'paired'
}
const silent = { node_id: 'NSH-410-TEST03', display_name: '', online: false, pair_state: 'paired' }

const router = createRouter({
  history: createMemoryHistory(),
  routes: [{ path: '/', component: { render: () => null } }]
})
const Card = await loadComponent('components/NodeAgentUpdatesCard.vue')
const chinese = deviceGroupLabels('zh-CN')
const english = deviceGroupLabels('en-US')

async function draw() {
  const html = await render(Card, {}, [i18n, router])
  return { html, text: textOf(html) }
}

/** The markup of one device's row. */
function rowOf(html, nodeID) {
  const rows = html.split('<li').slice(1)
  const row = rows.find(entry => entry.includes(nodeID))
  assert.ok(row, `no row for ${nodeID}`)
  return { html: row, text: textOf(`<li${row}`) }
}

test('the list is the live node list: online shows its version, offline says offline', async () => {
  await signIn('admin')
  resetNodeFleet()
  applyFleetNodes([online, offline, silent].map(readFleetNode))
  const { html, text } = await draw()
  assert.ok(text.includes(chinese.agentsTitle))

  const first = rowOf(html, 'NSH-410-TEST01')
  assert.match(first.text, /fixture stick A/)
  assert.match(first.text, new RegExp(`${chinese.agentVersion} 9\\.9\\.1-fixture`))
  assert.ok(first.text.includes(chinese.agentOnline))
  assert.ok(!first.text.includes(chinese.agentVersionLastSeen), 'a live version is labelled as a past one')
  assert.match(first.html, /class="is-online node-agents__badge"/)

  // Offline: "离线", and the version only as what it was at the last connection.
  const second = rowOf(html, 'NSH-410-TEST02')
  assert.ok(second.text.includes('离线'))
  assert.match(second.text, /9\.9\.0-fixture 上次连接时/)
  assert.ok(second.text.includes(chinese.agentLastSeen))
  assert.doesNotMatch(second.html, /is-online/)
  // Its version is not drawn anywhere without that label.
  assert.equal(html.split('9.9.0-fixture').length - 1, 1)

  // Offline and never reported a version: nothing is invented, and no ID is
  // promoted to a name it does not have.
  const third = rowOf(html, 'NSH-410-TEST03')
  assert.ok(third.text.includes(chinese.agentVersionUnknown))
  assert.ok(third.text.includes(chinese.agentOffline))
  assert.ok(third.text.includes(chinese.agentNeverSeen))
  assert.doesNotMatch(third.text, /\d+\.\d+\.\d+/)

  // No claim about an available or pending update: that is the panel's to
  // read from the device.
  assert.doesNotMatch(text, /可更新|有新版本|最新版本|up to date|已是最新/)
})

test('every device has a way into its update panel, and the panel is read from the device', async () => {
  await signIn('admin')
  resetNodeFleet()
  applyFleetNodes([online, offline].map(readFleetNode))
  const { html } = await draw()
  // One button per device, named for the device it opens.
  assert.equal(html.split('node-agents__open').length - 1, 2)
  assert.ok(html.includes(`aria-label="${chinese.agentOpen('fixture stick A')}"`))
  assert.ok(html.includes(`aria-label="${chinese.agentOpen('fixture stick B')}"`))
  // The panel is part A's, loaded on first use, keyed by device so choosing
  // another device re-reads instead of relabelling.
  assert.match(card, /defineAsyncComponent\(\(\) => import\('\.\/NodeUpdatePanel\.vue'\)\)/)
  assert.match(card, /<NodeUpdatePanel\s+v-if="openNode"\s+:key="openNode\.node_id"/)
  assert.match(card, /:device-id="openNode\.node_id"/)
  // Closed until asked for: nothing asks a device on page load.
  assert.ok(!html.includes('node-update'), 'an update panel is open before anyone chose a device')
  assert.match(card, /const openID = ref\(''\)/)
  // The panel itself reads the device when it mounts (part A).
  assert.match(panel, /await getNodeUpdates\(props\.deviceId\)/)
  // A device that is unpaired while its panel is open takes the panel with it.
  assert.match(card, /const openNode = computed\(\(\) => nodes\.value\.find\(node => node\.node_id === openID\.value\)\)/)
})

test('nothing is cached: the card reads the shared live store and holds no version of its own', () => {
  const script = code(card.slice(0, card.indexOf('<template>')))
  assert.match(script, /const nodes = computed\(\(\) => nodeFleetState\.nodes\)/)
  assert.doesNotMatch(script, /localStorage|sessionStorage|indexedDB|fetch\(/)
  // The only refs are which panel is open -- no copy of the list, no version.
  assert.deepEqual([...script.matchAll(/\bref(?:<[^>]*>)?\(/g)].length, 1)
  // It joins the ONE poll of the node list rather than starting another, and
  // leaves it when the page does; the store empties with its last reader.
  assert.match(script, /acquireNodeFleet\(\)/)
  assert.match(script, /onBeforeUnmount\(\(\) => \{\s*if \(held\) releaseNodeFleet\(\)/)
})

test('loading, empty, failed and refused are four different things to read', async () => {
  await signIn('admin')
  resetNodeFleet()
  assert.ok((await draw()).text.includes(chinese.agentsLoading))

  applyFleetNodes([])
  const empty = await draw()
  assert.ok(empty.text.includes(chinese.agentsEmpty))
  assert.ok(!empty.html.includes('<li'))

  resetNodeFleet()
  applyFleetFailure(503, 'fixture outage')
  const failed = await draw()
  assert.ok(failed.text.includes(chinese.agentsUnavailable))
  assert.ok(failed.text.includes(chinese.agentsRetry))
  assert.ok(!failed.text.includes(chinese.agentsEmpty), 'a failed read is reported as "no devices"')
  assert.match(card, /@click="refreshNodeFleet\(\)"/)

  // A list that stops answering takes its rows with it rather than leaving
  // the last versions on screen.
  applyFleetNodes([online].map(readFleetNode))
  applyFleetFailure(0)
  applyFleetFailure(0)
  applyFleetFailure(0)
  const dropped = await draw()
  assert.ok(!dropped.text.includes('9.9.1-fixture'))

  resetNodeFleet()
  applyFleetFailure(403)
  assert.equal(nodeFleetState.status, 'forbidden')
  assert.ok((await draw()).text.includes(chinese.agentsAdminOnly))
  resetNodeFleet()
})

test('a member is shown no Node agent entry at all', async () => {
  await signIn('member')
  resetNodeFleet()
  applyFleetNodes([online].map(readFleetNode))
  const { html, text } = await draw()
  assert.equal(text, '')
  assert.ok(!html.includes('NSH-410-TEST01'))
  // And does not start the administrators-only poll.
  assert.match(card, /onMounted\(\(\) => \{\s*if \(!visible\.value\) return/)
  assert.match(card, /const visible = computed\(\(\) => sessionState\.role === 'admin'\)/)
  resetNodeFleet()
  await signIn('admin')
})

test('the entry lives in the update centre, which the dashboard card already links to', () => {
  const updateIndex = about.indexOf('class="about-card about-update"')
  const agentsIndex = about.indexOf('<NodeAgentUpdatesCard />')
  const legalIndex = about.indexOf('class="about-card about-legal"')
  assert.ok(updateIndex > 0 && agentsIndex > updateIndex && legalIndex > agentsIndex)
  assert.match(about, /defineAsyncComponent\(\(\) => import\('\.\/NodeAgentUpdatesCard\.vue'\)\)/)
  assert.match(updateCard, /:to="\{ name: 'settings', params: \{ section: 'about' \} \}"/)
})

test('the copy says "offline" and "at last connection" in both languages, with no licence wording', () => {
  assert.equal(chinese.agentOffline, '离线')
  assert.equal(chinese.agentVersionLastSeen, '上次连接时')
  assert.equal(english.agentOffline, 'Offline')
  assert.match(english.agentVersionLastSeen, /last connection/)
  assert.match(chinese.agentsSubtitle, /不保存/)
  for (const source of [card, JSON.stringify(chinese), JSON.stringify(english)]) {
    assert.doesNotMatch(source, /许可证|通话许可|licen[cs]e/i)
  }
  // Colours come from the theme tokens only.
  const styles = card.slice(card.indexOf('<style'))
  assert.doesNotMatch(styles, /#[0-9a-fA-F]{3,8}\b|rgba?\(/)
})
