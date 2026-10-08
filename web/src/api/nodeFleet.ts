// The live list of paired NasSimHub Nodes, and one device's telemetry as the
// dashboard cards read it.
//
// Kept out of client.ts for the reason nodeSecurity.ts is: code there lands in
// the startup bundle, and this is reached only from the dashboard's device
// groups and the update centre's Node list.
//
// THE RULE EVERYTHING HERE EXISTS TO KEEP: a card shows ITS OWN device's
// figures or says it does not know. So every reader answers `null` for a field
// the Node did not send, and nothing is defaulted -- not to zero, and never to
// another device's value. `null` and `0` are different answers all the way to
// the screen: a rate of 0 B/s is a measurement, an absent rate is not.
//
// The Go structs these mirror: nassimhubnode.NodeView (fleet.go) and
// proto.SystemResources (third_party/nassimhub-node/proto/resources.go). Their
// `json` tags are the contract.

import type {
  SystemMetricsCellular,
  SystemMetricsInterfaceCounters,
  SystemMetricsSnapshot
} from './client'

// ---------------------------------------------------------------------------
// Field readers
// ---------------------------------------------------------------------------

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** A finite number, or null. `0` is a number. */
function numeric(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** A finite number that is not negative, or null. */
function amount(value: unknown): number | null {
  const parsed = numeric(value)
  return parsed !== null && parsed >= 0 ? parsed : null
}

/**
 * A quantity that cannot be zero on a running device -- total memory, uptime,
 * the process count. The older flat fields carry no `omitempty`, so a Node
 * that did not sample them sends 0; that 0 is "not sampled", not a reading.
 */
function positive(value: unknown): number | null {
  const parsed = numeric(value)
  return parsed !== null && parsed > 0 ? parsed : null
}

function flag(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null
}

/** An RFC 3339 instant, or '' when absent, unparseable or Go's zero time. */
function instant(value: unknown): string {
  const raw = text(value)
  if (!raw) return ''
  const parsed = Date.parse(raw)
  if (Number.isNaN(parsed) || new Date(parsed).getUTCFullYear() <= 1) return ''
  return raw
}

function list(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function present<T>(entry: T | null): entry is T {
  return entry !== null
}

// ---------------------------------------------------------------------------
// proto.SystemResources
// ---------------------------------------------------------------------------

export interface NodeCPUCore {
  index: number
  online: boolean | null
  current_khz: number | null
  min_khz: number | null
  max_khz: number | null
  percent: number | null
}

export interface NodeCPU {
  model: string
  core_count: number | null
  cores: NodeCPUCore[]
  load_1: number | null
  load_5: number | null
  load_15: number | null
}

export interface NodeThermalZone {
  zone: string
  type: string
  celsius: number
}

export interface NodeMemory {
  total_bytes: number | null
  available_bytes: number | null
  free_bytes: number | null
  buffers_bytes: number | null
  cached_bytes: number | null
  swap_total_bytes: number | null
  swap_free_bytes: number | null
}

export interface NodeStorage {
  role: string
  path: string
  total_bytes: number | null
  free_bytes: number | null
  available_bytes: number | null
  used_bytes: number | null
  fs_type: string
}

export interface NodeEMMC {
  device: string
  life_time_est_a: number | null
  life_time_est_b: number | null
  pre_eol: number | null
}

export interface NodeInterface {
  name: string
  rx_bytes: number | null
  tx_bytes: number | null
  /** Null when the Node could not state a rate for this interval. 0 is a rate. */
  rx_bytes_per_second: number | null
  tx_bytes_per_second: number | null
}

export interface NodeAgentProcess {
  pid: number | null
  rss_bytes: number | null
  cpu_percent: number | null
}

/**
 * The serving cell. TAC and cell ID locate the device, which is why this is
 * read only from the administrators' node list and why deviceMetricsFor()
 * drops both for anyone else.
 */
export interface NodeCell {
  band: string
  earfcn: number | null
  pci: number | null
  tac: number | null
  cell_id: number | null
  observed_at: string
}

export interface NodeProcess {
  pid: number
  name: string
  rss_bytes: number | null
}

export interface NodeResources {
  observed_at: string
  cpu_percent: number | null
  memory_total_bytes: number | null
  memory_available_bytes: number | null
  uptime_seconds: number | null
  process_count: number | null
  process_scan_truncated: boolean
  processes: NodeProcess[]
  thread_count: number | null
  cpu: NodeCPU | null
  thermal_zones: NodeThermalZone[]
  memory: NodeMemory | null
  storage: NodeStorage[]
  emmc: NodeEMMC | null
  /** Null when the typed list is absent (an older agent); [] is "none found". */
  interfaces: NodeInterface[] | null
  agent: NodeAgentProcess | null
  /** Changes on every boot: counters restarted. Does not identify the device. */
  boot_id: string
  cell: NodeCell | null
  /** The older free-form map, kept for agents that predate the typed fields. */
  metrics: Record<string, unknown>
}

function readCore(value: unknown): NodeCPUCore | null {
  const source = record(value)
  const index = amount(source?.index)
  if (!source || index === null) return null
  return {
    index,
    online: flag(source.online),
    current_khz: amount(source.current_khz),
    min_khz: amount(source.min_khz),
    max_khz: amount(source.max_khz),
    percent: amount(source.percent)
  }
}

function readCPU(value: unknown): NodeCPU | null {
  const source = record(value)
  if (!source) return null
  return {
    model: text(source.model),
    core_count: positive(source.core_count),
    cores: list(source.cores).map(readCore).filter(present),
    load_1: amount(source.load_1),
    load_5: amount(source.load_5),
    load_15: amount(source.load_15)
  }
}

function readThermalZone(value: unknown): NodeThermalZone | null {
  const source = record(value)
  const celsius = numeric(source?.celsius)
  if (!source || celsius === null) return null
  return { zone: text(source.zone), type: text(source.type), celsius }
}

function readMemory(value: unknown): NodeMemory | null {
  const source = record(value)
  if (!source) return null
  return {
    total_bytes: positive(source.total_bytes),
    available_bytes: amount(source.available_bytes),
    free_bytes: amount(source.free_bytes),
    buffers_bytes: amount(source.buffers_bytes),
    cached_bytes: amount(source.cached_bytes),
    swap_total_bytes: amount(source.swap_total_bytes),
    swap_free_bytes: amount(source.swap_free_bytes)
  }
}

function readStorage(value: unknown): NodeStorage | null {
  const source = record(value)
  if (!source) return null
  return {
    role: text(source.role),
    path: text(source.path),
    total_bytes: positive(source.total_bytes),
    free_bytes: amount(source.free_bytes),
    available_bytes: amount(source.available_bytes),
    used_bytes: amount(source.used_bytes),
    fs_type: text(source.fs_type)
  }
}

function readEMMC(value: unknown): NodeEMMC | null {
  const source = record(value)
  if (!source) return null
  return {
    device: text(source.device),
    life_time_est_a: amount(source.life_time_est_a),
    life_time_est_b: amount(source.life_time_est_b),
    pre_eol: amount(source.pre_eol)
  }
}

function readInterface(value: unknown): NodeInterface | null {
  const source = record(value)
  const name = text(source?.name)
  if (!source || !name) return null
  return {
    name,
    rx_bytes: amount(source.rx_bytes),
    tx_bytes: amount(source.tx_bytes),
    rx_bytes_per_second: amount(source.rx_bytes_per_second),
    tx_bytes_per_second: amount(source.tx_bytes_per_second)
  }
}

function readAgent(value: unknown): NodeAgentProcess | null {
  const source = record(value)
  if (!source) return null
  return {
    pid: positive(source.pid),
    rss_bytes: amount(source.rss_bytes),
    cpu_percent: amount(source.cpu_percent)
  }
}

function readCell(value: unknown): NodeCell | null {
  const source = record(value)
  if (!source) return null
  return {
    band: text(source.band),
    earfcn: amount(source.earfcn),
    pci: amount(source.pci),
    tac: amount(source.tac),
    cell_id: amount(source.cell_id),
    observed_at: instant(source.observed_at)
  }
}

function readProcess(value: unknown): NodeProcess | null {
  const source = record(value)
  const pid = positive(source?.pid)
  if (!source || pid === null) return null
  return { pid, name: text(source.name), rss_bytes: amount(source.rss_bytes) }
}

/** `resources`, or null when the Node sent none (an older agent, or offline). */
export function readNodeResources(value: unknown): NodeResources | null {
  const source = record(value)
  if (!source) return null
  return {
    observed_at: instant(source.observed_at),
    cpu_percent: amount(source.cpu_percent),
    memory_total_bytes: positive(source.memory_total_bytes),
    memory_available_bytes: amount(source.memory_available_bytes),
    uptime_seconds: positive(source.uptime_seconds),
    process_count: positive(source.process_count),
    process_scan_truncated: source.process_scan_truncated === true,
    processes: list(source.processes).map(readProcess).filter(present),
    thread_count: positive(source.thread_count),
    cpu: readCPU(source.cpu),
    thermal_zones: list(source.thermal_zones).map(readThermalZone).filter(present),
    memory: readMemory(source.memory),
    storage: list(source.storage).map(readStorage).filter(present),
    emmc: readEMMC(source.emmc),
    interfaces: Array.isArray(source.interfaces)
      ? source.interfaces.map(readInterface).filter(present)
      : null,
    agent: readAgent(source.agent),
    boot_id: text(source.boot_id),
    cell: readCell(source.cell),
    metrics: record(source.metrics) ?? {}
  }
}

// ---------------------------------------------------------------------------
// nassimhubnode.NodeView
// ---------------------------------------------------------------------------

export type NodeTristate = 'yes' | 'no' | 'unknown'

export interface FleetNode {
  node_id: string
  display_name: string
  platform: string
  model: string
  pair_state: string
  online: boolean
  revoked: boolean
  /** Null when the gateway did not say; never read as compatible. */
  compatible: boolean | null
  transport: string
  /**
   * The agent version the Node reported on its last connection. For a device
   * that is offline this is a fact about the past and must be labelled as one.
   */
  agent_version: string
  last_seen_at: string
  modem_state: string
  sim_state: string
  operator: string
  registration: string
  /**
   * 0..5. The gateway sends 0 both for "no bars" and for "no reading", so 0
   * is never drawn as a measurement from this field alone; see nodeLine().
   */
  signal_bars: number | null
  sms_capable: boolean | null
  volte: NodeTristate
  voice_control: boolean | null
  voice_audio: boolean | null
  /** The ID of the line this Node contributes while it holds a ready SIM. */
  line_endpoint_id: string
  resources: NodeResources | null
  /** The entry as received, for readNodeSecurity(); never drawn directly. */
  raw: unknown
}

/** One entry of `nodes[]`, or null for an entry with no device ID. */
export function readFleetNode(value: unknown): FleetNode | null {
  const source = record(value)
  const nodeID = text(source?.node_id)
  if (!source || !nodeID) return null
  // A device is online only on the gateway's explicit word.
  const online = source.online === true
  const volte = source.volte
  return {
    node_id: nodeID,
    display_name: text(source.display_name),
    platform: text(source.platform),
    model: text(source.model),
    pair_state: text(source.pair_state),
    online,
    revoked: source.revoked === true,
    compatible: flag(source.compatible),
    transport: text(source.transport),
    agent_version: text(source.agent_version),
    last_seen_at: instant(source.last_seen_at),
    modem_state: text(source.modem_state),
    sim_state: text(source.sim_state),
    operator: text(source.operator),
    registration: text(source.registration),
    signal_bars: amount(source.signal_bars),
    sms_capable: flag(source.sms_capable),
    volte: volte === 'yes' || volte === 'no' ? volte : 'unknown',
    voice_control: flag(source.voice_control),
    voice_audio: flag(source.voice_audio),
    line_endpoint_id: text(source.line_endpoint_id),
    // Telemetry from a session that is no longer up describes nothing. The
    // gateway already withholds it; dropped here too so no card can ever draw
    // an offline device's last numbers as current.
    resources: online ? readNodeResources(source.resources) : null,
    raw: value
  }
}

export function readFleetNodes(body: unknown): FleetNode[] | null {
  const nodes = record(body)?.nodes
  if (!Array.isArray(nodes)) return null
  return nodes.map(readFleetNode).filter(present)
}

/** A failed read of the node list. `status` 0: the request never arrived. */
export class NodeFleetError extends Error {
  constructor(readonly status: number, message = '') {
    super(message)
  }
}

/**
 * GET /api/v1/nodes (administrators). One live read, never cached; `signal`
 * cancels it when the last reader goes away.
 */
export async function getFleetNodes(signal?: AbortSignal): Promise<FleetNode[]> {
  let response: Response
  try {
    response = await fetch('/api/v1/nodes', {
      credentials: 'same-origin',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
      signal
    })
  } catch (cause) {
    if (signal?.aborted) throw cause
    throw new NodeFleetError(0)
  }
  const parsed: unknown = await response.json().catch(() => null)
  if (!response.ok) throw new NodeFleetError(response.status, text(record(parsed)?.message))
  const nodes = readFleetNodes(parsed)
  if (!nodes) throw new NodeFleetError(response.status, 'invalid_response')
  return nodes
}

// ---------------------------------------------------------------------------
// One device's metrics, in the shape the dashboard cards read
// ---------------------------------------------------------------------------

type Nullable<T> = { [K in keyof T]: T[K] | null }

/** The serving cell as a card reads it: every field may be unknown. */
export type DeviceCellular = Nullable<SystemMetricsCellular>

export interface DeviceInterface {
  name: string
  rx_bytes: number | null
  tx_bytes: number | null
}

type MetricsScalars = Omit<
  SystemMetricsSnapshot,
  'observed_at' | 'cellular' | 'interfaces' | 'available' | 'error' | 'module_channel'
>

/**
 * SystemMetricsSnapshot with every reading allowed to be unknown.
 *
 * The host module's snapshot is assignable to this as it stands; a Node's is
 * built by nodeDeviceMetrics(). The cards read this type and nothing else, so
 * the same template draws both without knowing which it has.
 */
export interface DeviceMetrics extends Nullable<MetricsScalars> {
  observed_at: string
  cellular: DeviceCellular | null
  interfaces: DeviceInterface[]
  /** The interface the data card describes; null when the device has none. */
  data_interface: DeviceInterface | null
  module_channel?: string
  available: boolean
  error?: string
}

/** One point of a device's history. Null is a gap, never a zero. */
export interface DeviceMetricsSample {
  cpuUsage: number | null
  cpuTemp: number | null
  modemTemp: number | null
  mem: number | null
  flash: number | null
  rxRate: number | null
  txRate?: number | null
  at?: number
}

/**
 * Why a device has no metrics to draw. Each is a different sentence on the
 * card, because "the device did not report" and "your role may not read it"
 * are different facts.
 */
export type DeviceMetricsGap =
  | ''
  | 'loading'
  | 'not_reported'
  | 'admin_only'
  | 'unavailable'
  | 'ambiguous_host'

/** The device-scoped object every card of one group is handed. */
export interface DeviceMetricsSource {
  metrics: DeviceMetrics | null
  history: DeviceMetricsSample[]
  available: boolean
  error: string
  gap: DeviceMetricsGap
}

export function emptyDeviceSource(gap: DeviceMetricsGap, error = ''): DeviceMetricsSource {
  return { metrics: null, history: [], available: gap !== 'unavailable', error, gap }
}

function hex(value: number | null): string | null {
  return value === null ? null : Math.floor(value).toString(16).toUpperCase()
}

function legacyNumber(metrics: Record<string, unknown>, key: string): number | null {
  return numeric(metrics[key])
}

function legacyText(source: Record<string, unknown> | undefined, key: string): string | null {
  const value = text(source?.[key])
  return value || null
}

/**
 * The hottest zone whose kernel type names `word`. The agent's own rule
 * (systemstats.legacyMetrics): a zone is the CPU's or the modem's only when
 * the kernel says so, never by position.
 */
function zoneCelsius(zones: NodeThermalZone[], word: string): number | null {
  let hottest: number | null = null
  for (const zone of zones) {
    if (!zone.type.toLowerCase().includes(word)) continue
    if (hottest === null || zone.celsius > hottest) hottest = zone.celsius
  }
  return hottest
}

/** The filesystem the storage tile describes: the agent's state volume. */
function stateStorage(storage: NodeStorage[]): NodeStorage | null {
  return storage.find(entry => entry.role === 'state') ?? null
}

/**
 * A Node's cellular data interface. Named by what it is on the device (the
 * modem's wwan/rmnet netdev), never chosen by traffic volume: on a stick the
 * busiest interface is usually the USB or Wi-Fi link to the NAS.
 */
export function nodeDataInterface(interfaces: NodeInterface[]): NodeInterface | null {
  return (
    interfaces.find(entry => entry.name.startsWith('wwan')) ??
    interfaces.find(entry => entry.name.startsWith('rmnet')) ??
    null
  )
}

function nodeInterfaces(resources: NodeResources): NodeInterface[] {
  if (resources.interfaces) return resources.interfaces
  return list(resources.metrics.interfaces).map(readInterface).filter(present)
}

function nodeCellular(resources: NodeResources): DeviceCellular | null {
  const legacy = record(resources.metrics.cellular)
  const cell = resources.cell
  if (!legacy && !cell) return null
  return {
    state: legacyText(legacy, 'state'),
    rat: legacyText(legacy, 'rat'),
    duplex: legacyText(legacy, 'duplex'),
    plmn: legacyText(legacy, 'plmn'),
    band: cell?.band || legacyText(legacy, 'band'),
    earfcn: cell?.earfcn ?? amount(legacy?.earfcn),
    frequency_mhz: positive(legacy?.frequency_mhz),
    uplink_bandwidth_mhz: positive(legacy?.uplink_bandwidth_mhz),
    downlink_bandwidth_mhz: positive(legacy?.downlink_bandwidth_mhz),
    pci: cell?.pci ?? amount(legacy?.pci),
    cell_id_hex: hex(cell?.cell_id ?? null) ?? legacyText(legacy, 'cell_id_hex'),
    tac_hex: hex(cell?.tac ?? null) ?? legacyText(legacy, 'tac_hex'),
    rsrp: numeric(legacy?.rsrp),
    rsrq: numeric(legacy?.rsrq),
    rssi: numeric(legacy?.rssi),
    sinr: numeric(legacy?.sinr)
  }
}

/**
 * One Node's resources as the cards read them. Every figure is this Node's
 * own or null; the typed fields win and the older map fills in for agents
 * that predate them.
 */
export function nodeDeviceMetrics(resources: NodeResources): DeviceMetrics {
  const legacy = resources.metrics
  const total = resources.memory?.total_bytes ?? resources.memory_total_bytes
  const available =
    resources.memory?.available_bytes ??
    // The flat pair is one reading: "available" without its total is not one.
    (resources.memory_total_bytes !== null ? resources.memory_available_bytes : null)
  const state = stateStorage(resources.storage)
  const flashTotal = state?.total_bytes ?? positive(legacy.flash_total_bytes)
  const flashFree = state ? state.available_bytes : amount(legacy.flash_free_bytes)
  const clocked = resources.cpu?.cores.find(core => core.current_khz !== null)
  const interfaces = nodeInterfaces(resources)
  return {
    observed_at: resources.observed_at,
    load_raw_1: resources.cpu?.load_1 ?? amount(legacy.load_raw_1),
    load_raw_5: resources.cpu?.load_5 ?? amount(legacy.load_raw_5),
    load_raw_15: resources.cpu?.load_15 ?? amount(legacy.load_raw_15),
    load_percent_1: null,
    load_percent_5: null,
    load_percent_15: null,
    cores: resources.cpu?.core_count ?? positive(legacy.cores),
    memory_total_bytes: total,
    memory_available_bytes: available,
    memory_used_percent:
      total !== null && available !== null
        ? Math.max(0, Math.min(100, (100 * (total - available)) / total))
        : null,
    memory_cached_bytes: resources.memory?.cached_bytes ?? amount(legacy.memory_cached_bytes),
    memory_buffers_bytes: resources.memory?.buffers_bytes ?? amount(legacy.memory_buffers_bytes),
    flash_path: state?.path || legacyText(legacy, 'flash_path'),
    flash_total_bytes: flashTotal,
    flash_free_bytes: flashFree,
    flash_used_percent:
      flashTotal !== null && flashFree !== null
        ? Math.max(0, Math.min(100, (100 * (flashTotal - flashFree)) / flashTotal))
        : null,
    flash_fs_type: state?.fs_type || legacyText(legacy, 'flash_fs_type'),
    flash_device_model: legacyText(legacy, 'flash_device_model'),
    flash_device_path: resources.emmc?.device || legacyText(legacy, 'flash_device_path'),
    modem_temp_celsius:
      zoneCelsius(resources.thermal_zones, 'modem') ?? legacyNumber(legacy, 'modem_temp_celsius'),
    // Empty when the kernel names no processor. Never the board's model name:
    // that is a guess about a different thing.
    cpu_model_name: resources.cpu?.model || legacyText(legacy, 'cpu_model_name'),
    cpu_usage_percent: resources.cpu_percent,
    cpu_current_mhz:
      clocked?.current_khz != null ? clocked.current_khz / 1000 : positive(legacy.cpu_current_mhz),
    cpu_max_mhz: clocked?.max_khz != null ? clocked.max_khz / 1000 : positive(legacy.cpu_max_mhz),
    cpu_l1_data_kb: null,
    cpu_l1_inst_kb: null,
    cpu_l2_kb: null,
    cpu_l3_kb: null,
    cpu_temp_celsius:
      zoneCelsius(resources.thermal_zones, 'cpu') ?? legacyNumber(legacy, 'cpu_temp_celsius'),
    process_count: resources.process_count,
    thread_count: resources.thread_count ?? positive(legacy.thread_count),
    host_uptime_seconds: resources.uptime_seconds,
    cellular: nodeCellular(resources),
    interfaces,
    data_interface: nodeDataInterface(interfaces),
    module_channel: 'ready',
    available: true
  }
}

/** What nodeHistorySample() needs to remember between two polls of one Node. */
export interface NodeCounterMemory {
  boot_id: string
  observed_at: string
  name: string
  rx_bytes: number | null
  tx_bytes: number | null
}

function counterRate(
  now: number | null,
  before: number | null,
  seconds: number
): number | null {
  if (now === null || before === null || seconds <= 0 || now < before) return null
  return (now - before) / seconds
}

/**
 * One history point for a Node, and what to remember for the next.
 *
 * The rate is the Node's own figure when it states one (0 included). Only
 * when it states none -- an agent older than the rate fields -- is it worked
 * out from this Node's counters across two of ITS samples, and then only when
 * that is sound: same interface, same boot, time moved forward, the counter
 * did not go backwards. Anything else is a gap, not a zero. (The interval is
 * taken between two of the Node's own timestamps, so a wrong clock on the
 * device does not distort it as long as it runs at the right speed.)
 */
export function nodeHistorySample(
  resources: NodeResources,
  metrics: DeviceMetrics,
  previous: NodeCounterMemory | undefined,
  receivedAt = Date.now()
): { sample: DeviceMetricsSample; memory: NodeCounterMemory | undefined } {
  const chosen = nodeDataInterface(nodeInterfaces(resources))
  const at = Date.parse(resources.observed_at)
  let rxRate = chosen?.rx_bytes_per_second ?? null
  let txRate = chosen?.tx_bytes_per_second ?? null
  if (chosen && previous && previous.name === chosen.name && previous.boot_id === resources.boot_id) {
    const seconds = (at - Date.parse(previous.observed_at)) / 1000
    if (rxRate === null) rxRate = counterRate(chosen.rx_bytes, previous.rx_bytes, seconds)
    if (txRate === null) txRate = counterRate(chosen.tx_bytes, previous.tx_bytes, seconds)
  }
  return {
    sample: {
      cpuUsage: metrics.cpu_usage_percent,
      cpuTemp: metrics.cpu_temp_celsius,
      modemTemp: metrics.modem_temp_celsius,
      mem: metrics.memory_used_percent,
      flash: metrics.flash_used_percent,
      rxRate,
      txRate,
      // When THIS BROWSER received it, like the host's samples -- not the
      // Node's own clock. The strip's 1/5/15-minute windows are counted back
      // from now, and a stick with no battery-backed clock can be years off
      // until it has synchronised; its samples would all fall outside them.
      at: receivedAt
    },
    memory: chosen
      ? {
          boot_id: resources.boot_id,
          observed_at: resources.observed_at,
          name: chosen.name,
          rx_bytes: chosen.rx_bytes,
          tx_bytes: chosen.tx_bytes
        }
      : undefined
  }
}

function zeroIsUnsampled(value: number): number | null {
  return value > 0 ? value : null
}

/** The host module's data interface, by the rule the dashboard always used. */
function hostDataInterface(
  interfaces: SystemMetricsInterfaceCounters[]
): SystemMetricsInterfaceCounters | null {
  const usb = interfaces.find(item => item.name.startsWith('enx') || item.name.startsWith('usb'))
  if (usb) return usb
  return interfaces.reduce<SystemMetricsInterfaceCounters | null>(
    (best, item) => (!best || item.rx_bytes > best.rx_bytes ? item : best),
    null
  )
}

/**
 * The host module's snapshot as the cards read it.
 *
 * getSystemMetrics() coerces a field the gateway omitted to 0, so here the
 * quantities that cannot be 0 on a running module are turned back into
 * "unknown". That is what the cards did one formatter at a time before they
 * were shared (`formatBytes(0)` drew a dash); doing it once, here, keeps the
 * host's cards as they were while letting the formatters treat 0 as 0.
 */
export function hostDeviceMetrics(snapshot: SystemMetricsSnapshot): DeviceMetrics {
  const cell = snapshot.cellular
  // The module reports its radio figures together. All zero is "no reading";
  // a 0 dB SINR beside a real RSRP is a measurement and is kept.
  const measured = cell ? cell.rsrp !== 0 || cell.rsrq !== 0 || cell.rssi !== 0 : false
  return {
    ...snapshot,
    cores: zeroIsUnsampled(snapshot.cores),
    memory_total_bytes: zeroIsUnsampled(snapshot.memory_total_bytes),
    memory_available_bytes: snapshot.memory_total_bytes > 0 ? snapshot.memory_available_bytes : null,
    memory_used_percent: snapshot.memory_total_bytes > 0 ? snapshot.memory_used_percent : null,
    memory_cached_bytes: snapshot.memory_total_bytes > 0 ? snapshot.memory_cached_bytes : null,
    memory_buffers_bytes: snapshot.memory_total_bytes > 0 ? snapshot.memory_buffers_bytes : null,
    flash_total_bytes: zeroIsUnsampled(snapshot.flash_total_bytes),
    flash_free_bytes: snapshot.flash_total_bytes > 0 ? snapshot.flash_free_bytes : null,
    flash_used_percent: snapshot.flash_total_bytes > 0 ? snapshot.flash_used_percent : null,
    cpu_current_mhz: zeroIsUnsampled(snapshot.cpu_current_mhz),
    cpu_max_mhz: zeroIsUnsampled(snapshot.cpu_max_mhz),
    process_count: zeroIsUnsampled(snapshot.process_count),
    thread_count: zeroIsUnsampled(snapshot.thread_count),
    host_uptime_seconds: zeroIsUnsampled(snapshot.host_uptime_seconds),
    cellular: cell
      ? {
          ...cell,
          band: cell.band || null,
          earfcn: zeroIsUnsampled(cell.earfcn),
          frequency_mhz: zeroIsUnsampled(cell.frequency_mhz),
          uplink_bandwidth_mhz: zeroIsUnsampled(cell.uplink_bandwidth_mhz),
          downlink_bandwidth_mhz: zeroIsUnsampled(cell.downlink_bandwidth_mhz),
          cell_id_hex: cell.cell_id_hex || null,
          tac_hex: cell.tac_hex || null,
          rsrp: measured ? cell.rsrp : null,
          rsrq: measured ? cell.rsrq : null,
          rssi: measured ? cell.rssi : null,
          sinr: measured ? cell.sinr : null
        }
      : null,
    data_interface: hostDataInterface(snapshot.interfaces)
  }
}

/**
 * The same metrics without what locates the device.
 *
 * Cell ID and TAC place a device on a map. They are shown to administrators
 * and to nobody else, and the removal happens HERE -- before any card is
 * handed the object -- so no card, tooltip or copied text downstream can leak
 * what it was never given.
 */
export function withoutCellLocation(metrics: DeviceMetrics): DeviceMetrics {
  if (!metrics.cellular) return metrics
  return { ...metrics, cellular: { ...metrics.cellular, cell_id_hex: null, tac_hex: null } }
}
