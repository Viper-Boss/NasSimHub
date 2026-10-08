import type { SystemMetricsSnapshot } from '../api/client'
import {
  emptyDeviceSource,
  hostDeviceMetrics,
  withoutCellLocation,
  type DeviceMetricsSample,
  type DeviceMetricsSource,
  type FleetNode
} from '../api/nodeFleet'
import type { LineSummary } from '../api/types'
import type { NodeFleetStatus } from './nodeFleet'

// Which devices the dashboard draws a card group for, and whose figures each
// group is handed.
//
// The owner's rule, verbatim: 多模组：完全复用原大疆12卡片组件，同一套模板/
// 布局，数据仅绑定本设备。每个在线设备一个框；拔掉隐藏整框，两个设备各显示
// 一套；NAS全局服务独立。不借用另一台CPU/内存/蜂窝信息。
//
// So this module is two pure functions and nothing else. dashboardDevices()
// answers "which devices are here right now": one entry per device that is
// present, none for one that is not -- a device that drops out is REMOVED,
// never kept with its last numbers greyed out. deviceSource() answers "what
// may this device's cards read": its own metrics, or an explicit reason there
// are none. There is no path in either by which one device's figures reach
// another's cards, which is the point of keeping them free of any store.

export type DashboardDeviceKind = 'host' | 'node'

export interface DashboardDevice {
  /** Stable for as long as the device is: the v-for key of its group. */
  key: string
  kind: DashboardDeviceKind
  /** The Node's device ID; '' for a module attached to the NAS itself. */
  nodeID: string
  /** The line the cards read. A Node with no ready SIM gets one built from its own view. */
  line: LineSummary
  /** False when `line` was built here and names no line the gateway routes. */
  hasLine: boolean
  /** The Node as the gateway listed it; null for the host and for non-administrators. */
  node: FleetNode | null
}

/** A line contributed by a NasSimHub Node rather than by a modem on the NAS. */
export function isNodeLine(line: LineSummary): boolean {
  return line.id.startsWith('nsh:') || line.device_imei.startsWith('NSH-')
}

function lineOfNode(lines: LineSummary[], node: FleetNode): LineSummary | undefined {
  return lines.find(
    line =>
      (node.line_endpoint_id !== '' && line.id === node.line_endpoint_id) ||
      line.device_imei === node.node_id
  )
}

/** The gateway's own mapping (nassimhubnode.registrationState), for a line built here. */
function registrationOf(node: FleetNode): { known: boolean; state: string } {
  switch (node.registration) {
    case 'registered': return { known: true, state: 'home' }
    case 'roaming': return { known: true, state: 'roaming' }
    case 'searching': return { known: true, state: 'searching' }
    case 'denied': return { known: true, state: 'denied' }
    case 'unregistered': return { known: true, state: 'idle' }
    default: return { known: false, state: '' }
  }
}

/** The gateway's own mapping (nassimhubnode.modemState); '' is "unknown". */
function modemStateOf(node: FleetNode): string {
  if (node.modem_state === 'failed') return 'failed'
  if (node.sim_state === 'pin_locked' || node.sim_state === 'puk_locked') return 'locked'
  if (node.registration === 'registered' || node.registration === 'roaming') return 'registered'
  if (node.registration === 'searching') return 'searching'
  if (node.modem_state === 'ready') return 'enabled'
  return ''
}

/**
 * A line record for a Node that contributes none (no SIM, or a SIM that is
 * not ready), so the same module card can draw it.
 *
 * Built from THIS Node's view and nothing else. It carries no ICCID, IMSI or
 * phone number -- the node list never contains them -- and no capability: a
 * line the gateway does not route cannot dial or send. Signal is left unknown
 * unless the Node reports at least one bar, because the list sends 0 both for
 * "no bars" and for "no reading" and those must not be drawn the same.
 */
export function nodeLine(node: FleetNode): LineSummary {
  const registration = registrationOf(node)
  return {
    id: node.line_endpoint_id || `nsh:${node.node_id}`,
    iccid: '',
    imsi: '',
    phone_number: '',
    operator: node.operator,
    home_operator_code: '',
    home_operator_name: '',
    home_country_iso: '',
    serving_operator_code: '',
    serving_operator_name: node.operator,
    serving_country_iso: '',
    registration_state_known: registration.known,
    registration_state_code: 0,
    registration_state: registration.state,
    roaming: node.registration === 'roaming',
    emergency_only: false,
    device_imei: node.node_id,
    device_name: node.display_name,
    line_label: '',
    line_color: '',
    model: node.model || node.platform,
    firmware: node.agent_version,
    state: modemStateOf(node),
    radio_desired_enabled: false,
    radio_desired_enabled_known: false,
    signal_quality:
      node.signal_bars !== null && node.signal_bars > 0
        ? Math.min(5, Math.round(node.signal_bars)) * 20
        : undefined,
    capabilities: {},
    // Only a card the Node says is absent is drawn as "no card". An unknown
    // or locked card is not the same statement.
    module_only: node.sim_state === 'missing'
  }
}

export interface DashboardDevicesInput {
  /** The module lines that are present: presentModuleLines(). */
  lines: LineSummary[]
  /** Whether the viewer may read the node list at all. */
  admin: boolean
  fleetStatus: NodeFleetStatus
  nodes: FleetNode[]
}

/**
 * One entry per device that is here now.
 *
 * Modules on the NAS come from the present module lines. Nodes come from the
 * gateway's node list when the viewer can read it and it has answered: a Node
 * is listed when the list says `online: true` and at no other time, so a stick
 * that is unplugged loses its group at the next answer even if its line is
 * still in a bootstrap document the page has not refreshed. Without the list
 * (a member, or before the first answer) the only evidence of a Node is the
 * line it contributes, which the gateway publishes only while the Node is
 * online; that line is then the device.
 */
export function dashboardDevices(input: DashboardDevicesInput): DashboardDevice[] {
  const devices: DashboardDevice[] = []
  const nodeLines: LineSummary[] = []
  for (const line of input.lines) {
    if (isNodeLine(line)) {
      nodeLines.push(line)
      continue
    }
    devices.push({ key: `host:${line.id}`, kind: 'host', nodeID: '', line, hasLine: true, node: null })
  }
  if (input.admin && input.fleetStatus === 'ready') {
    for (const node of input.nodes) {
      if (!node.online) continue
      const line = lineOfNode(nodeLines, node)
      devices.push({
        key: `node:${node.node_id}`,
        kind: 'node',
        nodeID: node.node_id,
        line: line ?? nodeLine(node),
        hasLine: line !== undefined,
        node
      })
    }
    return devices
  }
  for (const line of nodeLines) {
    devices.push({
      key: `node:${line.device_imei || line.id}`,
      kind: 'node',
      nodeID: line.device_imei,
      line,
      hasLine: true,
      node: null
    })
  }
  return devices
}

/** The host module's poller state, as state/systemMetrics.ts holds it. */
export interface HostMetricsState {
  metrics: SystemMetricsSnapshot | null
  history: DeviceMetricsSample[]
  available: boolean
  error: string
}

export interface DeviceSourceContext {
  admin: boolean
  /** How many modules are attached to the NAS itself. */
  hostCount: number
  host: HostMetricsState
  fleetStatus: NodeFleetStatus
  /** Per-Node metrics from the one node-list poll, keyed by device ID. */
  sources: Record<string, DeviceMetricsSource>
}

/**
 * The metrics object ONE device's cards are handed.
 *
 * Its own, or an empty one that says why. The two branches never cross: a
 * Node's source is looked up by that Node's ID and by nothing else, and the
 * host snapshot is given only to the host -- and only when there is exactly
 * one module on the NAS, because the gateway serves a single snapshot and
 * with two modules attached it cannot say whose it is.
 */
export function deviceSource(device: DashboardDevice, context: DeviceSourceContext): DeviceMetricsSource {
  if (device.kind === 'host') {
    if (context.hostCount !== 1) return emptyDeviceSource('ambiguous_host')
    const host = context.host
    if (!host.metrics) {
      return emptyDeviceSource(host.available ? 'loading' : 'unavailable', host.error)
    }
    const metrics = hostDeviceMetrics(host.metrics)
    return {
      metrics: context.admin ? metrics : withoutCellLocation(metrics),
      history: host.history,
      available: host.available,
      error: host.error,
      gap: ''
    }
  }
  if (!context.admin) return emptyDeviceSource('admin_only')
  if (context.fleetStatus === 'idle' || context.fleetStatus === 'loading') {
    return emptyDeviceSource('loading')
  }
  if (context.fleetStatus !== 'ready') return emptyDeviceSource('unavailable')
  return context.sources[device.nodeID] ?? emptyDeviceSource('not_reported')
}
