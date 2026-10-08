import { reactive } from 'vue'
import {
  NodeFleetError,
  getFleetNodes,
  nodeDeviceMetrics,
  nodeHistorySample,
  type DeviceMetricsSource,
  type FleetNode,
  type NodeCounterMemory
} from '../api/nodeFleet'

// The ONE poller of GET /api/v1/nodes.
//
// Every Node group on the dashboard, and the update centre's Node list, read
// from here. It used to be one request per card per device -- the metrics
// strip and the insight cards each ran their own ten-second loop against the
// same route -- so two sticks cost four polls for one answer. The route
// returns every Node at once, so one poll feeds them all; consumers are
// counted, the loop stops with the last of them, and the request in flight is
// cancelled rather than left to land in a store nobody reads.
//
// WHAT IS KEPT, AND FOR HOW LONG. The list is replaced by each answer, never
// merged: a Node that is absent or offline in the newest answer loses its
// metrics and its chart history at once, so a group that comes back starts
// empty instead of resuming an hour-old line. When the route itself fails the
// last figures are frozen and marked unavailable (the strip says so) for a few
// polls, then dropped -- a number that stopped moving must not sit on screen
// as if it were current.

export type NodeFleetStatus = 'idle' | 'loading' | 'ready' | 'forbidden' | 'error'

export const nodeFleetState = reactive({
  status: 'idle' as NodeFleetStatus,
  nodes: [] as FleetNode[],
  /** Metrics per ONLINE Node that reported resources, keyed by device ID. */
  sources: {} as Record<string, DeviceMetricsSource>,
  error: ''
})

export const NODE_FLEET_POLL_MS = 10_000
// 90 samples x 10 s covers the strip's 15-minute utilisation window.
const HISTORY = 90
// Consecutive failed polls after which frozen figures are dropped: 30 s.
const FAILURES_BEFORE_DROP = 3

const counters = new Map<string, NodeCounterMemory>()
let timer: ReturnType<typeof setInterval> | undefined
let request: AbortController | undefined
let consumers = 0
let failures = 0

/**
 * Replaces the store with one answer of the route.
 *
 * Exported so the contract tests can drive the store with the documents a
 * gateway would send, without a timer or a network.
 */
export function applyFleetNodes(nodes: FleetNode[], receivedAt = Date.now()): void {
  const sources: Record<string, DeviceMetricsSource> = {}
  const seen = new Set<string>()
  for (const node of nodes) {
    if (!node.online || !node.resources) continue
    seen.add(node.node_id)
    const metrics = nodeDeviceMetrics(node.resources)
    const before = nodeFleetState.sources[node.node_id]
    let history = before?.history ?? []
    // One point per sample the Node took, not per poll: the gateway serves
    // the same document until the Node reports again.
    if (before?.metrics?.observed_at !== metrics.observed_at) {
      const { sample, memory } = nodeHistorySample(
        node.resources,
        metrics,
        counters.get(node.node_id),
        receivedAt
      )
      if (memory) counters.set(node.node_id, memory)
      else counters.delete(node.node_id)
      history = [...history, sample].slice(-HISTORY)
    }
    sources[node.node_id] = { metrics, history, available: true, error: '', gap: '' }
  }
  for (const id of [...counters.keys()]) {
    if (!seen.has(id)) counters.delete(id)
  }
  failures = 0
  nodeFleetState.nodes = nodes
  nodeFleetState.sources = sources
  nodeFleetState.status = 'ready'
  nodeFleetState.error = ''
}

/** Empties the store: nothing is known about any Node. */
export function resetNodeFleet(status: NodeFleetStatus = 'idle', error = ''): void {
  counters.clear()
  failures = 0
  nodeFleetState.nodes = []
  nodeFleetState.sources = {}
  nodeFleetState.status = status
  nodeFleetState.error = error
}

/** Records one failed poll. Exported for the same reason as applyFleetNodes. */
export function applyFleetFailure(status: number, message = ''): void {
  if (status === 401 || status === 403) {
    // Not an outage: this session may not read the list. Asking again every
    // ten seconds would only fill the gateway's log with refusals.
    resetNodeFleet('forbidden')
    stop()
    return
  }
  failures += 1
  const drop = failures >= FAILURES_BEFORE_DROP || nodeFleetState.status !== 'ready'
  if (drop) {
    counters.clear()
    nodeFleetState.nodes = []
    nodeFleetState.sources = {}
  } else {
    // Frozen, and said to be: each strip shows the reason over its last
    // good figures instead of presenting them as live.
    for (const source of Object.values(nodeFleetState.sources)) {
      source.available = false
      source.error = message
    }
  }
  nodeFleetState.status = drop ? 'error' : 'ready'
  nodeFleetState.error = message
}

async function poll(): Promise<void> {
  if (request) return
  const controller = new AbortController()
  request = controller
  if (nodeFleetState.status === 'idle') nodeFleetState.status = 'loading'
  try {
    const nodes = await getFleetNodes(controller.signal)
    if (!controller.signal.aborted) applyFleetNodes(nodes)
  } catch (cause) {
    if (!controller.signal.aborted) {
      applyFleetFailure(
        cause instanceof NodeFleetError ? cause.status : 0,
        cause instanceof Error ? cause.message : ''
      )
    }
  } finally {
    if (request === controller) request = undefined
  }
}

function start(): void {
  if (timer) return
  void poll()
  timer = setInterval(() => { void poll() }, NODE_FLEET_POLL_MS)
}

function stop(): void {
  if (timer) {
    clearInterval(timer)
    timer = undefined
  }
  request?.abort()
  request = undefined
}

/** Starts the poll with the first reader. Call from onMounted. */
export function acquireNodeFleet(): void {
  consumers += 1
  // A refusal ends the loop for the readers that saw it. The first reader of
  // a later visit asks once more, since the session may have changed since.
  if (nodeFleetState.status === 'forbidden' && consumers > 1) return
  start()
}

/** Stops the poll, and cancels the request in flight, with the last reader. */
export function releaseNodeFleet(): void {
  consumers = Math.max(0, consumers - 1)
  if (consumers > 0) return
  stop()
  // Nobody is looking, so nothing here is current any more. The next reader
  // starts from "not known yet" rather than from this visit's last answer.
  if (nodeFleetState.status !== 'forbidden') resetNodeFleet()
}

/** Asks now, outside the cadence -- after an action that changes the list. */
export function refreshNodeFleet(): Promise<void> {
  return poll()
}
