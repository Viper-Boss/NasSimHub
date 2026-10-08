import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import test from 'node:test'

import { createMemoryHistory, createRouter } from 'vue-router'

import {
  hostDeviceMetrics,
  nodeDeviceMetrics,
  nodeHistorySample,
  readFleetNode,
  readFleetNodes,
  withoutCellLocation
} from '../src/api/nodeFleet.ts'
import { i18n, setSystemLanguage } from '../src/i18n/index.ts'
import { deviceGroupLabels } from '../src/i18n/deviceGroupLabels.ts'
import {
  dashboardDevices,
  deviceSource,
  isNodeLine,
  nodeLine
} from '../src/state/dashboardDevices.ts'
import {
  acquireNodeFleet,
  applyFleetFailure,
  applyFleetNodes,
  nodeFleetState,
  releaseNodeFleet,
  resetNodeFleet
} from '../src/state/nodeFleet.ts'
import { loadComponent, render, textOf } from './support/render-sfc.mjs'
import { dashboardNetworkNodes } from '../src/state/dashboardNetworkNodes.ts'

// One card group per device, each bound to that device alone.
//
// The owner's rule: 多模组：完全复用原大疆12卡片组件，同一套模板/布局，数据仅
// 绑定本设备。每个在线设备一个框；拔掉隐藏整框，两个设备各显示一套；NAS全局
// 服务独立。不借用另一台CPU/内存/蜂窝信息。
//
// Everything here is OFFLINE: the documents are what a gateway would send,
// written by hand with obviously fake identifiers, and the components are
// rendered on the server renderer. Nothing below was observed on a device or
// in a browser.

const read = path => readFile(new URL(path, import.meta.url), 'utf8')
const [dashboard, grid, strip, insight, extra, fleetState, fleetApi] = await Promise.all([
  read('../src/views/DashboardView.vue'),
  read('../src/components/DeviceCardGrid.vue'),
  read('../src/components/SystemMetricsStrip.vue'),
  read('../src/components/ModuleInsightCards.vue'),
  read('../src/components/ModuleExtraCards.vue'),
  read('../src/state/nodeFleet.ts'),
  read('../src/api/nodeFleet.ts')
])

/** Source with comments stripped, for the "must not appear" assertions. */
function code(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

const NODE_A = 'NSH-410-TEST01'
const NODE_B = 'NSH-410-TEST02'
const MiB = 1024 * 1024

/** A current agent: every typed field, with figures no other fixture uses. */
function fullNode(overrides = {}) {
  return {
    node_id: NODE_A,
    display_name: 'fixture stick A',
    platform: 'msm8916',
    model: 'UFI003',
    online: true,
    agent_version: '0.0.0-fixture-a',
    line_endpoint_id: `nsh:${NODE_A}:sim1`,
    modem_state: 'ready',
    sim_state: 'ready',
    operator: 'Fixture Mobile',
    registration: 'registered',
    signal_bars: 4,
    resources: {
      observed_at: '2026-01-02T03:04:05Z',
      cpu_percent: 12.5,
      memory_total_bytes: 400 * MiB,
      memory_available_bytes: 100 * MiB,
      uptime_seconds: 3660,
      process_count: 77,
      thread_count: 141,
      boot_id: 'fixture-boot-a',
      cpu: { model: 'Fixture SoC A', core_count: 4, load_1: 0.5, load_5: 0.4, load_15: 0.3, cores: [{ index: 0, current_khz: 998400, max_khz: 1209600 }] },
      thermal_zones: [{ zone: 'thermal_zone0', type: 'cpu0-thermal', celsius: 51.5 }],
      memory: { total_bytes: 400 * MiB, available_bytes: 100 * MiB, cached_bytes: 60 * MiB, buffers_bytes: 8 * MiB },
      storage: [{ role: 'state', path: '/data', total_bytes: 1000 * MiB, free_bytes: 300 * MiB, available_bytes: 250 * MiB, used_bytes: 700 * MiB, fs_type: 'ext4' }],
      interfaces: [
        { name: 'usb0', rx_bytes: 9_000_000, tx_bytes: 9_000_000, rx_bytes_per_second: 7000, tx_bytes_per_second: 7000 },
        { name: 'wwan0', rx_bytes: 4096, tx_bytes: 8192, rx_bytes_per_second: 0, tx_bytes_per_second: 2048 }
      ],
      cell: { band: 'B3', earfcn: 1650, pci: 207, tac: 0x1a2b, cell_id: 0xc0ffee1, observed_at: '2026-01-02T03:04:00Z' },
      metrics: { cellular: { state: 'registered', rat: 'lte', plmn: '00101', rsrp: -91, rsrq: -11, rssi: -63, sinr: 0 } }
    },
    ...overrides
  }
}

/** An agent that predates the typed fields: the flat figures and nothing else. */
function oldNode(overrides = {}) {
  return {
    node_id: NODE_B,
    display_name: 'fixture stick B',
    platform: 'msm8916',
    model: 'UFI003',
    online: true,
    agent_version: '0.0.0-fixture-b',
    line_endpoint_id: `nsh:${NODE_B}:sim1`,
    signal_bars: 0,
    resources: {
      observed_at: '2026-01-02T03:04:06Z',
      cpu_percent: 43,
      memory_total_bytes: 200 * MiB,
      memory_available_bytes: 50 * MiB,
      uptime_seconds: 86400 * 2,
      process_count: 58,
      processes: []
    },
    ...overrides
  }
}

function nodeLineRecord(id, overrides = {}) {
  return {
    id: `nsh:${id}:sim1`, iccid: '', imsi: '', phone_number: '', operator: '',
    home_operator_code: '', home_operator_name: '', home_country_iso: '',
    serving_operator_code: '', serving_operator_name: '', serving_country_iso: '',
    registration_state_known: true, registration_state_code: 1, registration_state: 'home',
    roaming: false, emergency_only: false, device_imei: id, device_name: '', line_label: '',
    model: 'UFI003', radio_desired_enabled: false, radio_desired_enabled_known: false,
    capabilities: {}, ...overrides
  }
}

function hostLineRecord(overrides = {}) {
  return nodeLineRecord('unused', {
    id: 'line-host-fixture', device_imei: '000000000000000', model: 'QDC507', ...overrides
  })
}

function hostSnapshot(overrides = {}) {
  return {
    observed_at: '2026-01-02T03:04:07Z',
    load_raw_1: 0, load_raw_5: 0, load_raw_15: 0, load_percent_1: 0, load_percent_5: 0, load_percent_15: 0,
    cores: 1, memory_total_bytes: 42 * MiB, memory_available_bytes: 10 * MiB, memory_used_percent: 76,
    memory_cached_bytes: 9 * MiB, memory_buffers_bytes: 1 * MiB, flash_path: '/', flash_total_bytes: 72 * MiB,
    flash_free_bytes: 24 * MiB, flash_used_percent: 67, flash_fs_type: 'ubifs', flash_device_model: '',
    flash_device_path: '', modem_temp_celsius: 39, cpu_model_name: 'Fixture host module', cpu_usage_percent: 88,
    cpu_current_mhz: 1401, cpu_max_mhz: 1401, cpu_l1_data_kb: 0, cpu_l1_inst_kb: 0, cpu_l2_kb: 0, cpu_l3_kb: 0,
    cpu_temp_celsius: 64.5, process_count: 145, thread_count: 233, host_uptime_seconds: 7200,
    cellular: {
      state: 'registered', rat: 'LTE', duplex: 'FDD', plmn: '00101', band: '1', earfcn: 100, frequency_mhz: 2120,
      uplink_bandwidth_mhz: 20, downlink_bandwidth_mhz: 20, pci: 301, cell_id_hex: 'FEEDF00', tac_hex: 'B0B0',
      rsrp: -104, rsrq: -14, rssi: -71, sinr: 3
    },
    interfaces: [{ name: 'enxfixture', rx_bytes: 5000, tx_bytes: 6000 }],
    available: true,
    ...overrides
  }
}

const idleHost = { metrics: null, history: [], available: true, error: '' }

/** The two things the dashboard does for each device, in its order. */
function groupsFor({ lines = [], admin = true, host = idleHost } = {}) {
  const devices = dashboardDevices({
    lines,
    admin,
    fleetStatus: nodeFleetState.status,
    nodes: nodeFleetState.nodes
  })
  const hostCount = devices.filter(device => device.kind === 'host').length
  return devices.map(device => ({
    device,
    source: deviceSource(device, {
      admin,
      hostCount,
      host,
      fleetStatus: nodeFleetState.status,
      sources: nodeFleetState.sources
    })
  }))
}

await setSystemLanguage('zh-CN')
const chinese = deviceGroupLabels('zh-CN')
const english = deviceGroupLabels('en-US')
const router = createRouter({
  history: createMemoryHistory(),
  routes: [{ path: '/:pathMatch(.*)*', name: 'settings', component: { render: () => null } }]
})
const Strip = await loadComponent('components/SystemMetricsStrip.vue')
const Grid = await loadComponent('components/DeviceCardGrid.vue')

/** Everything one group draws: its metric tiles and its card field. */
async function drawGroup(group, admin) {
  const tiles = await render(Strip, { source: group.source }, [i18n, router])
  const cards = await render(Grid, { device: group.device, source: group.source, admin }, [i18n, router])
  const html = `${tiles}\n${cards}`
  return { html, text: textOf(html), tiles: textOf(tiles), cards: textOf(cards) }
}

// ---------------------------------------------------------------------------
// Reading the node list
// ---------------------------------------------------------------------------

test('a field the Node did not send is null -- never zero, and a sent zero stays zero', () => {
  const old = readFleetNode(oldNode())
  const resources = old.resources
  assert.equal(resources.cpu_percent, 43)
  // Absent optional fields.
  assert.equal(resources.thread_count, null)
  assert.equal(resources.cpu, null)
  assert.equal(resources.memory, null)
  assert.equal(resources.emmc, null)
  assert.equal(resources.cell, null)
  assert.equal(resources.agent, null)
  assert.equal(resources.interfaces, null, 'an absent interface list was read as an empty one')
  assert.deepEqual(resources.storage, [])
  assert.deepEqual(resources.thermal_zones, [])

  // A present zero is a reading.
  const full = readFleetNode(fullNode()).resources
  const wwan = full.interfaces.find(entry => entry.name === 'wwan0')
  assert.equal(wwan.rx_bytes_per_second, 0)
  assert.equal(wwan.tx_bytes_per_second, 2048)
  assert.equal(readFleetNode(fullNode({ resources: { ...fullNode().resources, cpu_percent: 0 } })).resources.cpu_percent, 0)
  // A rate the Node left out is not a rate of zero.
  const silent = readFleetNode(fullNode({
    resources: { ...fullNode().resources, interfaces: [{ name: 'wwan0', rx_bytes: 1, tx_bytes: 2 }] }
  })).resources.interfaces[0]
  assert.equal(silent.rx_bytes_per_second, null)
  assert.equal(silent.tx_bytes_per_second, null)

  // The older flat fields have no `omitempty`: 0 there means "not sampled".
  const unsampled = readFleetNode(oldNode({
    resources: { observed_at: '2026-01-02T03:04:06Z', cpu_percent: null, memory_total_bytes: 0, memory_available_bytes: 0, uptime_seconds: 0, process_count: 0 }
  })).resources
  assert.equal(unsampled.cpu_percent, null)
  assert.equal(unsampled.memory_total_bytes, null)
  assert.equal(unsampled.uptime_seconds, null)
  assert.equal(unsampled.process_count, null)
})

test('a device is online only on the gateway\'s word, and an offline one carries no telemetry', () => {
  for (const online of [false, undefined, 'true', 1]) {
    const node = readFleetNode(fullNode({ online }))
    assert.equal(node.online, false)
    assert.equal(node.resources, null, 'an offline device kept its last figures')
  }
  assert.equal(readFleetNode({ display_name: 'no id' }), null)
  assert.equal(readFleetNodes({ nodes: 'nope' }), null)
  assert.deepEqual(readFleetNodes({ nodes: [fullNode(), {}, oldNode()] }).map(node => node.node_id), [NODE_A, NODE_B])
})

test('one Node\'s metrics are built from that Node\'s document alone', () => {
  const full = nodeDeviceMetrics(readFleetNode(fullNode()).resources)
  assert.equal(full.cpu_usage_percent, 12.5)
  assert.equal(full.cores, 4)
  assert.equal(full.cpu_model_name, 'Fixture SoC A')
  assert.equal(full.cpu_temp_celsius, 51.5)
  assert.equal(full.memory_used_percent, 75)
  assert.equal(full.memory_cached_bytes, 60 * MiB)
  assert.equal(full.flash_total_bytes, 1000 * MiB)
  assert.equal(full.flash_free_bytes, 250 * MiB, 'the storage tile no longer uses what an unprivileged writer can use')
  assert.equal(full.flash_used_percent, 75)
  assert.equal(full.flash_fs_type, 'ext4')
  assert.equal(Math.round(full.cpu_current_mhz), 998)
  assert.equal(full.thread_count, 141)
  assert.equal(full.host_uptime_seconds, 3660)
  // The cellular interface, by name -- not the busiest one, which is the USB link to the NAS.
  assert.equal(full.data_interface.name, 'wwan0')
  // Serving cell: typed fields, with the signal figures from the older map.
  assert.deepEqual(
    [full.cellular.band, full.cellular.earfcn, full.cellular.pci, full.cellular.tac_hex, full.cellular.cell_id_hex],
    ['B3', 1650, 207, '1A2B', 'C0FFEE1']
  )
  assert.deepEqual([full.cellular.rsrp, full.cellular.rsrq, full.cellular.sinr], [-91, -11, 0])

  const old = nodeDeviceMetrics(readFleetNode(oldNode()).resources)
  assert.equal(old.cpu_usage_percent, 43)
  assert.equal(old.memory_used_percent, 75)
  for (const key of [
    'cores', 'cpu_temp_celsius', 'modem_temp_celsius', 'memory_cached_bytes', 'memory_buffers_bytes',
    'flash_total_bytes', 'flash_free_bytes', 'flash_used_percent', 'cpu_current_mhz', 'thread_count',
    'load_raw_1', 'cpu_model_name'
  ]) {
    assert.equal(old[key], null, `${key} was invented for a Node that did not report it`)
  }
  assert.equal(old.cellular, null)
  assert.equal(old.data_interface, null)
  assert.deepEqual(old.interfaces, [])

  // An agent between the two: the older free-form map is still read.
  const legacy = nodeDeviceMetrics(readFleetNode(oldNode({
    resources: {
      ...oldNode().resources,
      metrics: {
        cores: 4, cpu_temp_celsius: 47, flash_total_bytes: 512 * MiB, flash_free_bytes: 128 * MiB,
        interfaces: [{ name: 'wwan0', rx_bytes: 10, tx_bytes: 20 }],
        cellular: { rat: 'lte', rsrp: null, sinr: null, cell_id_hex: 'ABC', tac_hex: '12' }
      }
    }
  })).resources)
  assert.equal(legacy.cores, 4)
  assert.equal(legacy.cpu_temp_celsius, 47)
  assert.equal(legacy.flash_used_percent, 75)
  assert.equal(legacy.data_interface.name, 'wwan0')
  assert.equal(legacy.cellular.rsrp, null, 'a null signal reading became a number')
  assert.equal(legacy.cellular.cell_id_hex, 'ABC')
})

test('0 B/s is a measurement; no rate is not; a rate is never taken across a reboot', () => {
  const resources = readFleetNode(fullNode()).resources
  const metrics = nodeDeviceMetrics(resources)
  const first = nodeHistorySample(resources, metrics, undefined, 1000)
  assert.equal(first.sample.rxRate, 0)
  assert.equal(first.sample.txRate, 2048)
  assert.equal(first.sample.at, 1000, 'history is stamped with the device clock, which may be years off')

  // An agent that states no rate: nothing on the first sample ...
  const bare = at => readFleetNode(fullNode({
    resources: {
      ...fullNode().resources,
      observed_at: at.observed_at,
      boot_id: at.boot_id ?? 'fixture-boot-a',
      interfaces: [{ name: 'wwan0', rx_bytes: at.rx, tx_bytes: at.tx }]
    }
  })).resources
  const one = bare({ observed_at: '2026-01-02T03:04:05Z', rx: 1000, tx: 500 })
  const start = nodeHistorySample(one, nodeDeviceMetrics(one), undefined)
  assert.equal(start.sample.rxRate, null)
  assert.equal(start.sample.txRate, null)
  // ... then this Node's own counters across two of its own samples.
  const two = bare({ observed_at: '2026-01-02T03:04:15Z', rx: 3000, tx: 500 })
  const next = nodeHistorySample(two, nodeDeviceMetrics(two), start.memory)
  assert.equal(next.sample.rxRate, 200)
  assert.equal(next.sample.txRate, 0, 'a counter that did not move is 0 B/s, not unknown')
  // A counter that went backwards, or a new boot, is a gap.
  const reset = bare({ observed_at: '2026-01-02T03:04:25Z', rx: 10, tx: 600 })
  assert.equal(nodeHistorySample(reset, nodeDeviceMetrics(reset), next.memory).sample.rxRate, null)
  const rebooted = bare({ observed_at: '2026-01-02T03:04:25Z', rx: 9000, tx: 9000, boot_id: 'fixture-boot-b' })
  const after = nodeHistorySample(rebooted, nodeDeviceMetrics(rebooted), next.memory)
  assert.equal(after.sample.rxRate, null)
  assert.equal(after.sample.txRate, null)
})

// ---------------------------------------------------------------------------
// Which devices get a group
// ---------------------------------------------------------------------------

test('two online Nodes are two groups, each holding only its own figures', () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode())])
  const groups = groupsFor({ lines: [nodeLineRecord(NODE_A), nodeLineRecord(NODE_B)] })
  assert.deepEqual(groups.map(group => group.device.key), [`node:${NODE_A}`, `node:${NODE_B}`])
  const [a, b] = groups
  assert.equal(a.source.metrics.cpu_usage_percent, 12.5)
  assert.equal(b.source.metrics.cpu_usage_percent, 43)
  assert.equal(a.source.metrics.cpu_temp_celsius, 51.5)
  // B did not report it. It is unknown -- not A's reading, and not zero.
  assert.equal(b.source.metrics.cpu_temp_celsius, null)
  assert.equal(b.source.metrics.flash_total_bytes, null)
  assert.equal(b.source.metrics.cellular, null)
  assert.notEqual(a.source, b.source)
  assert.equal(a.device.line.id, `nsh:${NODE_A}:sim1`)
  assert.equal(b.device.line.id, `nsh:${NODE_B}:sim1`)
})

test('a device that goes offline loses its whole group, and nothing of it is kept', () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode())])
  assert.equal(groupsFor().length, 2)

  // B is unplugged. Its line may still be in a bootstrap document the page
  // has not refreshed; the node list is what decides.
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode({ online: false }))])
  const after = groupsFor({ lines: [nodeLineRecord(NODE_A), nodeLineRecord(NODE_B)] })
  assert.deepEqual(after.map(group => group.device.nodeID), [NODE_A])
  assert.equal(nodeFleetState.sources[NODE_B], undefined, 'an offline device kept its metrics')

  // It comes back: its history starts again rather than resuming the old line.
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode())])
  assert.equal(nodeFleetState.sources[NODE_B].history.length, 1)

  // A Node the gateway no longer lists at all (unpaired) is gone too.
  applyFleetNodes([readFleetNode(oldNode())])
  assert.deepEqual(groupsFor({ lines: [nodeLineRecord(NODE_A)] }).map(group => group.device.nodeID), [NODE_B])

  // No device at all: no group, which is what selects the page's empty state.
  applyFleetNodes([])
  assert.deepEqual(groupsFor(), [])
  assert.match(dashboard, /<div v-else-if="deviceGroups\.length === 0" class="dashboard-section-state">\s*<Inbox :size="17" \/>\s*\{\{ t\('dashboard\.noLines'\) \}\}/)
})

test('a Node\'s source is looked up by its own ID and has no fallback to another device', async () => {
  resetNodeFleet()
  // A reports resources; B is online and reports none.
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode({ resources: undefined }))])
  const host = { metrics: hostSnapshot(), history: [], available: true, error: '' }
  const groups = groupsFor({ lines: [hostLineRecord()], host })
  assert.deepEqual(groups.map(group => group.device.kind), ['host', 'node', 'node'])
  const b = groups[2]
  assert.equal(b.device.nodeID, NODE_B)
  assert.equal(b.source.metrics, null, 'a Node with no resources was handed some')
  assert.equal(b.source.gap, 'not_reported')
  assert.deepEqual(b.source.history, [])
  // The host's snapshot goes to the host and to nobody else.
  assert.equal(groups[0].source.metrics.cpu_usage_percent, 88)
  assert.equal(groups[1].source.metrics.cpu_usage_percent, 12.5)

  // The only read of the per-Node store is by this device's ID.
  const selector = code(await read('../src/state/dashboardDevices.ts'))
  assert.match(selector, /return context\.sources\[device\.nodeID\] \?\? emptyDeviceSource\('not_reported'\)/)
  assert.equal(selector.match(/context\.sources/g).length, 1)
  assert.equal(selector.match(/context\.host\b/g).length, 1, 'the host snapshot is read on more than the host branch')
})

test('two modules on the NAS: the single host snapshot is given to neither', () => {
  resetNodeFleet()
  const host = { metrics: hostSnapshot(), history: [], available: true, error: '' }
  const groups = groupsFor({
    lines: [hostLineRecord(), hostLineRecord({ id: 'line-host-fixture-2', device_imei: '000000000000001' })],
    host
  })
  assert.equal(groups.length, 2)
  for (const group of groups) {
    assert.equal(group.source.metrics, null)
    assert.equal(group.source.gap, 'ambiguous_host')
  }
  assert.match(chinese.gaps.ambiguous_host, /无法确定/)
})

test('without the node list a Node is known by the line it contributes, and its figures are withheld', () => {
  resetNodeFleet()
  // A member: the list is administrators-only and is never applied.
  const member = groupsFor({ lines: [hostLineRecord(), nodeLineRecord(NODE_A)], admin: false })
  assert.deepEqual(member.map(group => group.device.kind), ['host', 'node'])
  assert.equal(member[1].device.node, null)
  assert.equal(member[1].source.metrics, null)
  assert.equal(member[1].source.gap, 'admin_only')
  // Even if the store somehow held A's metrics, a member is not handed them.
  applyFleetNodes([readFleetNode(fullNode())])
  assert.equal(groupsFor({ lines: [nodeLineRecord(NODE_A)], admin: false })[0].source.metrics, null)

  // An administrator before the first answer: the line stands in, marked as loading.
  resetNodeFleet()
  const early = groupsFor({ lines: [nodeLineRecord(NODE_A)] })
  assert.equal(early.length, 1)
  assert.equal(early[0].source.gap, 'loading')
  // ... and when the list cannot be read at all.
  applyFleetFailure(503, 'fixture outage')
  assert.equal(nodeFleetState.status, 'error')
  assert.equal(groupsFor({ lines: [nodeLineRecord(NODE_A)] })[0].source.gap, 'unavailable')
})

test('a failed poll freezes figures and says so; three in a row, or a refusal, drops them', () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode())])
  applyFleetFailure(0, '')
  assert.equal(nodeFleetState.sources[NODE_A].available, false, 'frozen figures are still presented as live')
  assert.equal(nodeFleetState.sources[NODE_A].metrics.cpu_usage_percent, 12.5)
  applyFleetFailure(0, '')
  applyFleetFailure(0, '')
  assert.deepEqual(nodeFleetState.sources, {})
  assert.deepEqual(nodeFleetState.nodes, [])
  assert.equal(nodeFleetState.status, 'error')

  // One good answer clears the count.
  applyFleetNodes([readFleetNode(fullNode())])
  applyFleetFailure(0, '')
  assert.equal(nodeFleetState.status, 'ready')

  applyFleetFailure(403, '')
  assert.equal(nodeFleetState.status, 'forbidden')
  assert.deepEqual(nodeFleetState.sources, {})
  resetNodeFleet()
})

test('a Node with no ready SIM still gets the same group, from its own view and nothing else', () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode({ sim_state: 'missing', registration: 'unknown', operator: '', signal_bars: 0 }))])
  const [group] = groupsFor()
  assert.equal(group.device.hasLine, false)
  const line = group.device.line
  assert.equal(line.device_imei, NODE_A)
  assert.equal(line.module_only, true)
  // Nothing that identifies a subscriber, and nothing it could be dialled with.
  assert.deepEqual([line.iccid, line.imsi, line.phone_number], ['', '', ''])
  assert.deepEqual(line.capabilities, {})
  // The list sends 0 for "no bars" and for "no reading": not drawn as 0%.
  assert.equal(line.signal_quality, undefined)
  assert.equal(nodeLine(readFleetNode(fullNode())).signal_quality, 80)
  assert.equal(nodeLine(readFleetNode(fullNode())).registration_state, 'home')
  assert.equal(nodeLine(readFleetNode(fullNode({ registration: 'something-new' }))).registration_state_known, false)
  assert.equal(isNodeLine(line), true)
  assert.equal(isNodeLine(hostLineRecord()), false)
})

// ---------------------------------------------------------------------------
// What a reader sees
// ---------------------------------------------------------------------------

test('rendered: each group shows its own values; what B lacks is 未知 even though A has it', async () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode())])
  const [a, b] = groupsFor({ lines: [nodeLineRecord(NODE_A), nodeLineRecord(NODE_B)] })
  const first = await drawGroup(a, true)
  const second = await drawGroup(b, true)

  // A's own.
  for (const figure of ['13%', '51.5°C', 'Fixture SoC A', '4 核', '141', 'wwan0', '1650', '207', '-91', NODE_A]) {
    assert.ok(first.text.includes(figure), `device A no longer shows its own ${figure}`)
  }
  // B's own.
  for (const figure of ['43%', '58', NODE_B]) {
    assert.ok(second.text.includes(figure), `device B no longer shows its own ${figure}`)
  }
  // Nothing of A in B's group, and nothing of B in A's.
  for (const figure of ['13%', '51.5', 'Fixture SoC A', '141', 'wwan0', '1650', '207', '-91', 'C0FFEE1', '1A2B', NODE_A]) {
    assert.ok(!second.html.includes(figure), `device B's group shows device A's ${figure}`)
  }
  for (const figure of ['43%', NODE_B]) {
    assert.ok(!first.html.includes(figure), `device A's group shows device B's ${figure}`)
  }

  // What B did not report reads 未知 -- in the tiles ...
  assert.match(second.tiles, /CPU 温度 未知/)
  assert.match(second.tiles, /存储[\s\S]*利用率 未知 容量 未知 已用 未知 剩余 未知/)
  assert.match(second.tiles, /接收速度 未知 发送速度 未知/)
  assert.match(second.tiles, /缓存占用 未知 缓冲 未知/)
  // ... while what it did report is drawn.
  assert.match(second.tiles, /利用率 43%/)
  assert.match(second.tiles, /容量 200 MB/)
  // An empty rate chart carries no scale of its own invention.
  assert.doesNotMatch(second.tiles, /\d B\/s/)
  // And never as a zero.
  assert.doesNotMatch(second.tiles, /0\.0°C|容量 0 B|剩余 0 B/)
  // In the cards: no data interface and no serving cell, said as such.
  assert.ok(second.cards.includes(chinese.notProvided))
  assert.ok(second.cards.includes(chinese.noDataInterface))
  assert.ok(second.cards.includes(chinese.noCell))
  assert.match(second.cards, /存储 未知/)
})

test('rendered: 0 B/s is drawn as 0 B/s, and an unknown value is given no "ok" tone', async () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode())])
  const [a, b] = groupsFor()
  const first = await drawGroup(a, true)
  assert.match(first.tiles, /接收速度 0 B\/s 发送速度 2\.0 KB\/s/)
  assert.match(first.cards, /下行速率 0 B\/s/)
  assert.match(first.cards, /上行速率 2\.0 KB\/s/)
  // A measured SINR of 0 dB is a reading too.
  assert.match(first.tiles, /信号增益 0\.0 dB/)
  assert.match(first.cards, /SINR\s*0\s*dB/)

  const second = await drawGroup(b, true)
  // B's storage is unknown: its figure must not be coloured as healthy.
  const storageStat = second.html.match(/<span class="tm-ring__label([^"]*)"/)
  assert.ok(storageStat, 'the storage ring label is gone')
  assert.doesNotMatch(storageStat[1], /is-ok/, 'an unknown figure is drawn in the "ok" colour')
  assert.match(strip, /function levelOf\(percent: number \| null\): string \{\s*if \(percent === null\) return ''/)
})

test('rendered: a card with no source for this device stays in the layout and says why', async () => {
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode()), readFleetNode(oldNode({ resources: undefined }))])
  const groups = groupsFor()
  const withData = await drawGroup(groups[0], true)
  const without = await drawGroup(groups[1], true)
  // The same template: the same number of tiles and of cards either way.
  const count = (html, marker) => html.split(marker).length - 1
  const tiles = html => html.match(/class="tm-box(?: tm-box--absent)?"/g).length
  assert.equal(tiles(without.html), 4)
  assert.equal(tiles(withData.html), 4)
  assert.equal(count(without.html, 'class="insight-card"'), count(withData.html, 'class="insight-card"'))
  assert.equal(count(without.html, 'class="insight-card"'), 7)
  assert.equal(tiles(without.html) + count(without.html, 'class="insight-card"') + count(without.html, '<article class="module-card'), 12)
  assert.equal(count(without.html, '<article class="module-card'), 1)
  assert.equal(count(withData.html, '<article class="module-card'), 1)
  // Each empty tile and card names the reason.
  assert.equal(count(without.tiles, chinese.notProvided), 4)
  assert.ok(without.tiles.includes(chinese.gaps.not_reported))
  assert.ok(count(without.cards, chinese.gaps.not_reported) >= 3)
  // No figure of any kind is invented for it.
  assert.doesNotMatch(without.tiles, /\d+%|\d+ (?:B|KB|MB)\b/)

  // A Node offers neither the NAS-side repair actions nor a modem IMEI; the
  // rows stay and say so, under the right label.
  assert.ok(withData.cards.includes(chinese.hostOnlyActions))
  assert.ok(withData.cards.includes(chinese.imeiNotProvided) || withData.html.includes(chinese.imeiNotProvided))
  assert.match(withData.cards, new RegExp(`${chinese.deviceId} ${NODE_A}`))
  assert.doesNotMatch(withData.html, /label="IMEI"|>IMEI</, 'a Node ID is labelled as an IMEI')
})

test('rendered: cell ID and TAC are shown to an administrator and never to a member', async () => {
  resetNodeFleet()
  const host = { metrics: hostSnapshot(), history: [], available: true, error: '' }
  const [asAdmin] = groupsFor({ lines: [hostLineRecord()], host, admin: true })
  const [asMember] = groupsFor({ lines: [hostLineRecord()], host, admin: false })

  const admin = await drawGroup(asAdmin, true)
  assert.match(admin.cards, /Cell ID FEEDF00/)
  assert.match(admin.cards, /TAC B0B0/)
  // Once each, as plain rows: not repeated in a summary line or a tooltip.
  assert.equal(admin.html.split('FEEDF00').length - 1, 1)
  assert.equal(admin.html.split('B0B0').length - 1, 1)
  assert.doesNotMatch(admin.html, /title="[^"]*(?:FEEDF00|B0B0)/)

  // The member's source never carried them ...
  assert.equal(asMember.source.metrics.cellular.cell_id_hex, null)
  assert.equal(asMember.source.metrics.cellular.tac_hex, null)
  assert.equal(asMember.source.metrics.cellular.pci, 301, 'more than the location was removed')
  const member = await drawGroup(asMember, false)
  for (const secret of ['FEEDF00', 'B0B0', 'Cell ID', '<dt>TAC</dt>']) {
    assert.ok(!member.html.includes(secret), `a member's dashboard contains ${secret}`)
  }
  assert.ok(member.cards.includes(chinese.cellAdminOnly))
  // ... and the card does not draw them for a non-administrator even if handed them.
  const leaky = await render(Grid, { device: asAdmin.device, source: asAdmin.source, admin: false }, [i18n, router])
  assert.ok(!leaky.includes('FEEDF00') && !leaky.includes('B0B0'))
  // The rest of the cell is still there for the member.
  assert.match(member.cards, /PCI 301/)

  // A member's Node group: the template, with the reason the figures are withheld.
  const [node] = groupsFor({ lines: [nodeLineRecord(NODE_A)], admin: false })
  const drawn = await drawGroup(node, false)
  assert.equal(drawn.html.match(/class="tm-box tm-box--absent"/g).length, 4)
  assert.ok(drawn.tiles.includes(chinese.gaps.admin_only))
})

test('rendered: the trust row is this device\'s own record, and green only when trusted AND enforced', async () => {
  const trustPill = html => {
    const row = html.match(new RegExp(`${chinese.trustTitle}</span>(?:<!--[^>]*-->)*<span[^>]*class="([^"]*)insight-tag"[^>]*>([^<]*)<`))
    assert.ok(row, 'the device-trust row is gone')
    return { tone: row[1].trim(), text: row[2] }
  }
  resetNodeFleet()
  applyFleetNodes([
    readFleetNode(fullNode({ trust: { known: true, state: 'TRUSTED', effective_state: 'TRUSTED', enforced: true } })),
    readFleetNode(oldNode({ trust: { known: true, state: 'TRUSTED', effective_state: 'RESTRICTED', reason: 'fixture reason', enforced: false } }))
  ])
  const [a, b] = groupsFor()
  const first = trustPill((await drawGroup(a, true)).html)
  const second = trustPill((await drawGroup(b, true)).html)
  assert.deepEqual(first, { tone: 'is-ok', text: '已信任' })
  // The effective state decides, and B's row is B's: restricted, amber.
  assert.deepEqual(second, { tone: 'is-warn', text: '受限' })

  // Monitor mode: the state is named, and never ticked.
  applyFleetNodes([readFleetNode(fullNode({ trust: { known: true, state: 'TRUSTED', effective_state: 'TRUSTED', enforced: false } }))])
  const monitored = await drawGroup(groupsFor()[0], true)
  assert.deepEqual(trustPill(monitored.html), { tone: '', text: '已信任' })
  assert.ok(monitored.html.includes(chinese.trustMonitor))
  // A mode the gateway did not report is not enforcement either.
  applyFleetNodes([readFleetNode(fullNode({ trust: { known: true, state: 'TRUSTED', effective_state: 'TRUSTED' } }))])
  assert.equal(trustPill((await drawGroup(groupsFor()[0], true)).html).tone, '')

  // No record, a record the viewer cannot read, and a device the engine does
  // not assess: three sentences, none of them a pass.
  applyFleetNodes([readFleetNode(fullNode())])
  assert.deepEqual(trustPill((await drawGroup(groupsFor()[0], true)).html), { tone: '', text: chinese.trustNoRecord })
  resetNodeFleet()
  const [member] = groupsFor({ lines: [nodeLineRecord(NODE_A)], admin: false })
  assert.deepEqual(trustPill((await drawGroup(member, false)).html), { tone: '', text: chinese.unknown })
  const [host] = groupsFor({ lines: [hostLineRecord()] })
  assert.deepEqual(trustPill((await drawGroup(host, true)).html), { tone: '', text: chinese.trustNotApplicable })

  // The state names are the ones the nodes view uses.
  const { nodeSecurityCopy } = await import('../src/i18n/nodeSecurityCopy.ts')
  assert.deepEqual(chinese.trustStates, nodeSecurityCopy('zh-CN').trustStates)
  assert.deepEqual(english.trustStates, nodeSecurityCopy('en-US').trustStates)
})

test('the host module keeps its figures, with "not sampled" zeros read as unknown', () => {
  const metrics = hostDeviceMetrics(hostSnapshot({ flash_total_bytes: 0, flash_free_bytes: 0, flash_used_percent: 0, thread_count: 0 }))
  assert.equal(metrics.cpu_usage_percent, 88)
  assert.equal(metrics.flash_total_bytes, null)
  assert.equal(metrics.flash_used_percent, null)
  assert.equal(metrics.thread_count, null)
  assert.equal(metrics.data_interface.name, 'enxfixture')
  assert.equal(metrics.cellular.sinr, 3)
  // No radio reading at all is unknown, not "0 dBm".
  const silent = hostDeviceMetrics(hostSnapshot({
    cellular: { ...hostSnapshot().cellular, rsrp: 0, rsrq: 0, rssi: 0, sinr: 0 }
  }))
  assert.deepEqual([silent.cellular.rsrp, silent.cellular.sinr], [null, null])
  // Removing the location changes nothing else and does not touch the original.
  const redacted = withoutCellLocation(metrics)
  assert.equal(metrics.cellular.cell_id_hex, 'FEEDF00')
  assert.deepEqual(
    { ...redacted.cellular, cell_id_hex: 'FEEDF00', tac_hex: 'B0B0' },
    metrics.cellular
  )
})

// ---------------------------------------------------------------------------
// The page
// ---------------------------------------------------------------------------

test('one template draws every device, and both of a group\'s parts get the same source', () => {
  const template = dashboard.slice(dashboard.indexOf('<template>'), dashboard.lastIndexOf('<style'))
  // One loop over devices, and no second, device-kind-specific copy of it.
  assert.equal(template.match(/v-for="group in deviceGroups"/g).length, 1)
  assert.doesNotMatch(template, /startsWith\('NSH-'\)/, 'the template branches on the kind of device again')
  assert.equal(template.match(/<DeviceCardGrid\b/g).length, 1)
  assert.equal(template.match(/<SystemMetricsStrip\b/g).length, 1)
  assert.match(template, /<SystemMetricsStrip :source="group\.source" \/>/)
  assert.match(template, /<DeviceCardGrid\s+:device="group\.device"\s+:source="group\.source"/)
  // The dashboard no longer places a per-device card itself.
  assert.doesNotMatch(template, /<ModuleCard\b|<ModuleExtraCards\b/)

  // The card field: the three components once each, all on this device's props.
  const field = grid.slice(grid.indexOf('<template>'))
  assert.equal(field.match(/<ModuleCard\b/g).length, 1)
  assert.equal(field.match(/<ModuleInsightCards\b/g).length, 1)
  assert.equal(field.match(/<ModuleExtraCards\b/g).length, 1)
  assert.match(field, /<ModuleInsightCards scope="device" :line="device\.line" :source="source" :admin="admin" \/>/)
  assert.match(field, /^<template>\s*<div class="dashboard-module-grid">/, 'the grid is no longer the root, so the dashboard\'s grid rule stops applying to it')
  // No card component reaches a store of metrics by itself.
  for (const [name, source] of [['the strip', strip], ['the insight cards', insight], ['the card field', grid]]) {
    assert.doesNotMatch(code(source), /state\/systemMetrics|state\/nodeFleet|useDeviceMetrics|fetch\(/, `${name} fetches or reads metrics on its own`)
  }
  assert.match(strip, /defineProps<\{ source: DeviceMetricsSource \}>\(\)/)
})

test('NAS-global cards are drawn exactly once, outside the per-device groups', () => {
  const template = dashboard.slice(dashboard.indexOf('<template>'), dashboard.lastIndexOf('<style'))
  const loopStart = template.indexOf('v-for="group in deviceGroups"')
  const loopEnd = template.indexOf('</section>', loopStart)
  const perDevice = template.slice(loopStart, loopEnd)
  const globalStart = template.indexOf('dashboard-device-group--global')
  assert.ok(loopEnd < globalStart, 'the NAS services section is no longer after the device groups')
  for (const card of [
    '<NetworkGlobeCard />',
    '<DHTMeshCard />',
    '<WireGuardTrafficCard',
    '<ModuleSecurityCard />',
    '<SoftwareUpdateCard />'
  ]) {
    assert.equal(template.split(card).length - 1, 1, `${card} is drawn ${template.split(card).length - 1} times`)
    assert.ok(template.indexOf(card) > globalStart, `${card} is outside the NAS services section`)
    assert.ok(!perDevice.includes(card))
  }
  // All four insight cards remain inside each device group. The nodes card
  // describes NasSimHub, so every module kind reads the shared gateway source.
  assert.doesNotMatch(template, /<ModuleInsightCards scope="network"/)
  const insightTemplate = insight.slice(insight.indexOf('<template>'), insight.lastIndexOf('<style'))
  const nodesCard = insightTemplate.slice(insightTemplate.indexOf('<!-- Nodes and clients'))
  assert.match(nodesCard, /<article class="insight-card">/)
  assert.doesNotMatch(nodesCard, /deviceScope && isNode|noNodeNetwork/)
  assert.match(insight, /acquireDashboardNetworkNodes\(\)/)
  assert.doesNotMatch(insight, /if \(deviceScope\.value && isNode\.value\) return/)

})

test('rendered: the network scope draws the one NAS card and no device card', async () => {
  const Insight = await loadComponent('components/ModuleInsightCards.vue')
  const network = await render(Insight, { scope: 'network' }, [i18n, router])
  assert.equal(network.split('class="insight-card"').length - 1, 1)
  resetNodeFleet()
  applyFleetNodes([readFleetNode(fullNode())])
  const [group] = groupsFor()
  const device = await render(Insight, { scope: 'device', line: group.device.line, source: group.source, admin: true }, [i18n, router])
  assert.equal(device.split('class="insight-card"').length - 1, 4)
  assert.doesNotMatch(device, /此设备尚未上报自己的 NKN/)
  dashboardNetworkNodes.nknEnabled.value = true
  dashboardNetworkNodes.nknAddress.value = 'fixture-nassimhub-address'
  dashboardNetworkNodes.nknNodes.value = { reachable: 3, probed: 4, nodes: [{ address: 'fixture.invalid', rtt_ms: 42 }] }
  dashboardNetworkNodes.dhtStatus.value = { state: 'ready', connected_peers: 2, lan_peers: 1, peer_id: 'fixture-nassimhub-peer' }
  try {
    const nodeWithGateway = await render(Insight, { scope: 'device', line: group.device.line, source: group.source, admin: true }, [i18n, router])
    const gateway = await render(Insight, { scope: 'network' }, [i18n, router])
    for (const html of [nodeWithGateway, gateway]) {
      assert.match(html, /insight-nodes__seeds/)
      assert.match(html, /title="fixture-nassimhub-address"/)
      assert.match(html, /title="fixture.invalid"/)
      assert.match(textOf(html), /3\/4/)
    }
  } finally {
    dashboardNetworkNodes.nknEnabled.value = null
    dashboardNetworkNodes.nknAddress.value = ''
    dashboardNetworkNodes.nknNodes.value = null
    dashboardNetworkNodes.dhtStatus.value = null
  }
})

// ---------------------------------------------------------------------------
// Polling
// ---------------------------------------------------------------------------

test('one /nodes poll feeds every group, and it is cancelled with its last reader', async () => {
  // The route is asked for in exactly one place for the dashboard.
  const sources = await Promise.all(
    (await readdir(new URL('../src/components/', import.meta.url)))
      .filter(name => name.endsWith('.vue'))
      .map(async name => [name, await read(`../src/components/${name}`)])
  )
  for (const [name, source] of [...sources, ['DashboardView.vue', dashboard]]) {
    assert.doesNotMatch(code(source), /['"`]\/api\/v1\/nodes['"`]/, `${name} asks for the node list itself`)
  }
  assert.equal(code(fleetApi).match(/fetch\('\/api\/v1\/nodes'/g).length, 1)
  assert.doesNotMatch(code(fleetState), /fetch\(/)

  resetNodeFleet()
  const original = globalThis.fetch
  const calls = []
  let answer
  globalThis.fetch = (url, init = {}) => {
    calls.push({ url: String(url), init })
    return new Promise((resolve, reject) => {
      answer = resolve
      init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })
  }
  try {
    // Three readers (two device groups and the update centre, say): one request.
    acquireNodeFleet()
    acquireNodeFleet()
    acquireNodeFleet()
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/api/v1/nodes')
    assert.equal(calls[0].init.cache, 'no-store')
    assert.equal(nodeFleetState.status, 'loading')
    answer(new Response(JSON.stringify({ nodes: [fullNode(), oldNode()] }), { status: 200 }))
    await new Promise(resolve => setTimeout(resolve, 0))
    assert.equal(nodeFleetState.status, 'ready')
    assert.deepEqual(Object.keys(nodeFleetState.sources).sort(), [NODE_A, NODE_B])

    releaseNodeFleet()
    releaseNodeFleet()
    assert.equal(nodeFleetState.status, 'ready', 'the poll stopped while a reader remained')

    // A request is out when the last reader leaves: it is aborted, and
    // nothing of the visit is kept for the next one to mistake for current.
    acquireNodeFleet()
    releaseNodeFleet()
    releaseNodeFleet()
    releaseNodeFleet()
    assert.equal(nodeFleetState.status, 'idle')
    assert.deepEqual(nodeFleetState.nodes, [])
    assert.deepEqual(nodeFleetState.sources, {})
  } finally {
    globalThis.fetch = original
    resetNodeFleet()
  }

  // The dashboard holds the poll only for an administrator looking at the
  // overview, and lets go when the page goes away.
  assert.match(dashboard, /watch\(\(\) => isAdmin\.value && overviewSelected\.value, holdNodeFleet\)/)
  assert.match(dashboard, /onBeforeUnmount\(\(\) => \{ holdNodeFleet\(false\) \}\)/)
})

test('per-device requests are scoped to the device and dropped when its group goes away', () => {
  // 24 h statistics: by device for a Node (administrators), never by another line.
  assert.match(extra, /byDevice \? getCommunicationStats\(undefined, nodeID\.value\) : getCommunicationStats\(props\.line\.id\)/)
  assert.match(extra, /const byDevice = nodeID\.value !== '' && sessionState\.role === 'admin'/)
  // Security rows: the gateway's boundaries for THIS device.
  assert.match(extra, /posture\.value = nodeID\.value \? nodePosture\(security, nodeID\.value\) : security/)
  // An answer for a group that has been removed is not stored.
  assert.match(extra, /if \(disposed\) return/)
  assert.match(extra, /onBeforeUnmount\(\(\) => \{\s*disposed = true\s*if \(timer\) clearInterval\(timer\)/)
  // N groups share one posture read rather than making N.
  assert.match(extra, /getSharedSecurityPosture\(\)/)
  // A card that is not drawn asks for nothing.
  assert.match(extra, /if \(props\.mode === 'device'\) return/)
})

test('the group copy exists in both languages and never words a gap as a value', () => {
  assert.deepEqual(Object.keys(chinese).sort(), Object.keys(english).sort())
  assert.deepEqual(Object.keys(chinese.gaps).sort(), Object.keys(english.gaps).sort())
  assert.equal(chinese.unknown, '未知')
  assert.equal(chinese.notProvided, '此设备未提供')
  for (const copy of [chinese, english]) {
    for (const [key, value] of Object.entries(copy)) {
      const text = typeof value === 'function' ? value('x') : typeof value === 'string' ? value : Object.values(value).join(' ')
      assert.ok(text.trim(), `${key} is empty`)
      assert.doesNotMatch(text, /许可证|licen[cs]e/i)
    }
  }
  // Other locales read the English, like the neighbouring label files.
  assert.equal(deviceGroupLabels('ja-JP'), english)
  assert.equal(deviceGroupLabels('zh-TW'), chinese)
})
