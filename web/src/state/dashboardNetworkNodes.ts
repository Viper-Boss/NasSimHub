import { ref } from 'vue'
import { getDHTSettings, getNKNNetworkNodes, getNKNNetworkStatus, type DHTStatus, type NKNNetworkNodes } from '../api/client'

// All modem groups display NasSimHub's nodes and clients. Keep one gateway
// probe shared by the cards, independent of each modem's own resource source.
const nknNodes = ref<NKNNetworkNodes | null>(null)
const nknAddress = ref('')
const nknEnabled = ref<boolean | null>(null)
const dhtStatus = ref<DHTStatus | null>(null)
export const dashboardNetworkNodes = { nknNodes, nknAddress, nknEnabled, dhtStatus }

let readers = 0
let generation = 0
let timer: ReturnType<typeof setInterval> | undefined
let refreshing = false

async function refreshNodes(epoch: number): Promise<void> {
  if (refreshing) return
  refreshing = true
  let enabled: boolean | null = null
  let address = ''
  let nodes: NKNNetworkNodes | null = null
  let status: DHTStatus | null = null
  try {
    try {
      const network = await getNKNNetworkStatus()
      enabled = network.settings.enabled
      address = network.runtime.my_address ?? ''
      nodes = enabled ? await getNKNNetworkNodes() : null
    } catch { /* unavailable facts remain unknown */ }
    try { status = (await getDHTSettings()).status } catch { /* unknown */ }
    if (readers === 0 || epoch !== generation) return
    nknEnabled.value = enabled
    nknAddress.value = address
    nknNodes.value = nodes
    dhtStatus.value = status
  } finally {
    refreshing = false
    // A new reader may have arrived while the previous generation completed.
    if (readers > 0 && epoch !== generation) void refreshNodes(generation)
  }
}

export function acquireDashboardNetworkNodes(): void {
  if (++readers !== 1) return
  generation++
  void refreshNodes(generation)
  timer = setInterval(() => { void refreshNodes(generation) }, 60_000)
}

export function releaseDashboardNetworkNodes(): void {
  if (readers === 0 || --readers !== 0) return
  generation++
  if (timer) clearInterval(timer)
  timer = undefined
  nknNodes.value = null
  nknAddress.value = ''
  nknEnabled.value = null
  dhtStatus.value = null
}
