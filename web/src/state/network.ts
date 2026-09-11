import { reactive } from 'vue'
import { gateway } from '../api/client'
import type {
  NetworkStatus,
  ProxyApplyStatus,
  ProxyInstance,
  ProxyMode,
  ResourceStatus
} from '../api/types'
import { ApiError } from '../api/types'
import { translate } from '../i18n'
import { isIPAddress, isLoopbackAddress } from '../utils/ipAddress'
import { proxyCredentialError } from '../utils/proxyCredentials'

export type ProxyDraft = {
  line_id: string
  mode: ProxyMode
  listen_address: string
  listen_port: number
  username: string
  password: string
  // 'line' leaves through a modem data bearer; 'wireguard' leaves through a
  // tunnel interface, which is how a LAN device reaches the far side of a
  // NAS-to-NAS link. Resolvers are typed as one comma-separated field.
  egress: 'line' | 'wireguard'
  egress_interface: string
  egress_dns: string
}

function splitEgressDNS(value: string): string[] {
  return value
    .split(',')
    .map(entry => entry.trim())
    .filter(Boolean)
}

export const networkState = reactive<{
  status: ResourceStatus
  snapshot: NetworkStatus | null
  proxies: ProxyInstance[]
  error: string
  busyID: string
  notice: string
}>({
  status: 'idle',
  snapshot: null,
  proxies: [],
  error: '',
  busyID: '',
  notice: ''
})

let loadGeneration = 0
let loadRequest: Promise<void> | undefined

function failureMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.status === 403) {
    return translate('runtime.networkForbidden')
  }
  return error instanceof Error ? error.message : fallback
}

function proxyName(draft: ProxyDraft, lineLabel: string): string {
  const protocol = draft.mode === 'http' ? 'HTTP' : 'SOCKS5'
  // A tunnel proxy has no line, so naming it after one would be a lie.
  const source = draft.egress === 'wireguard'
    ? (draft.egress_interface.trim() || 'WireGuard')
    : (lineLabel.trim() || translate('lines.line'))
  return translate('runtime.proxyName', {
    line: source,
    protocol,
    port: draft.listen_port
  }).slice(0, 100)
}

function validateDraft(draft: ProxyDraft, existing?: ProxyInstance): string {
  const username = draft.username.trim()
  const password = draft.password
  if (draft.egress === 'wireguard') {
    if (!draft.egress_interface.trim()) return translate('proxy.enterEgressInterface')
    const servers = splitEgressDNS(draft.egress_dns)
    // Name lookups must leave through the tunnel too, or the LAN device's
    // browsing is visible on the path its traffic deliberately avoids.
    if (servers.length === 0) return translate('proxy.enterEgressDNS')
    if (servers.length > 4) return translate('proxy.tooManyEgressDNS')
    if (servers.some(server => !isIPAddress(server))) return translate('proxy.invalidEgressDNS')
  } else if (!draft.line_id.trim()) {
    return translate('proxy.selectLine')
  }
  if (draft.mode !== 'http' && draft.mode !== 'socks5') {
    return translate('runtime.selectProxyProtocol')
  }
  if (!draft.listen_address.trim()) return translate('proxy.enterListenAddress')
  if (!isIPAddress(draft.listen_address)) return translate('proxy.invalidListenAddress')
  if (
    !Number.isSafeInteger(draft.listen_port) ||
    draft.listen_port < 1024 ||
    draft.listen_port > 65535
  ) {
    return translate('proxy.invalidPort')
  }
  const credentialError = proxyCredentialError(
    draft.mode,
    username,
    password,
    existing?.has_password,
    key => translate(key)
  )
  if (credentialError) return credentialError
  if (!isLoopbackAddress(draft.listen_address) && !username) {
    return translate('runtime.localCredentialsRequired')
  }
  return ''
}

function applyNotice(applied: boolean, status: ProxyApplyStatus): string {
  if (applied) return ''
  switch (status) {
    case 'agent_unavailable':
      return translate('runtime.proxySavedAgentUnavailable')
    case 'agent_rejected':
      return translate('runtime.proxySavedAgentRejected')
    case 'runtime_unavailable':
      return translate('runtime.proxySavedRuntimeUnavailable')
    default:
      return translate('runtime.proxySavedPending')
  }
}

export function loadNetwork(force = false, silent = false): Promise<void> {
  if (!force && networkState.status === 'ready') return Promise.resolve()
  if (loadRequest) return loadRequest

  const token = ++loadGeneration
  if (!silent || !networkState.snapshot) networkState.status = 'loading'
  networkState.error = ''
  let request: Promise<void>
  request = Promise.all([gateway.getNetworkStatus(), gateway.listProxies()])
    .then(([snapshot, proxies]) => {
      if (token !== loadGeneration) return
      acceptNetworkSnapshot(snapshot, proxies)
      networkState.notice = ''
    })
    .catch(error => {
      if (token !== loadGeneration) return
      networkState.status =
        error instanceof ApiError && error.status === 403 ? 'forbidden' : 'error'
      networkState.error = failureMessage(error, translate('runtime.trafficLoadFailed'))
    })
    .finally(() => {
      if (loadRequest === request) loadRequest = undefined
    })
  loadRequest = request
  return request
}

export function acceptNetworkSnapshot(
  snapshot: NetworkStatus,
  proxies?: ProxyInstance[],
  supersedePendingLoad = false
): void {
  if (supersedePendingLoad) loadGeneration += 1
  const current = networkState.snapshot
  if (
    current?.boot_epoch === snapshot.boot_epoch &&
    current.observed_at &&
    snapshot.observed_at &&
    Date.parse(snapshot.observed_at) < Date.parse(current.observed_at)
  ) {
    return
  }
  networkState.snapshot = snapshot
  if (proxies) networkState.proxies = proxies
  networkState.status = 'ready'
  networkState.error = ''
}

export function resetNetworkState(): void {
  loadGeneration += 1
  loadRequest = undefined
  networkState.status = 'idle'
  networkState.snapshot = null
  networkState.proxies = []
  networkState.error = ''
  networkState.busyID = ''
  networkState.notice = ''
}

async function handleMutationFailure(error: unknown, fallback: string): Promise<void> {
  const message = failureMessage(error, fallback)
  if (error instanceof ApiError && error.status === 409) {
    await loadNetwork(true, true)
    networkState.error = translate('runtime.proxyConflict')
    return
  }
  networkState.error = message
}

export async function saveProxy(
  draft: ProxyDraft,
  lineLabel: string,
  existing?: ProxyInstance
): Promise<boolean> {
  if (networkState.busyID) return false
  const validationError = validateDraft(draft, existing)
  if (validationError) {
    networkState.error = validationError
    return false
  }

  const username = draft.username.trim()
  const password = draft.password
  const authEnabled = Boolean(username)
  const tunnel = draft.egress === 'wireguard'
  const common = {
    name: proxyName(draft, lineLabel),
    // A tunnel proxy carries no line: it is not tied to a SIM, and sending one
    // would make it fail whenever that SIM went away.
    line_id: tunnel ? '' : draft.line_id.trim(),
    enabled: existing?.enabled ?? true,
    mode: draft.mode,
    listen_address: draft.listen_address.trim(),
    listen_port: draft.listen_port,
    auth_enabled: authEnabled,
    username: authEnabled ? username : '',
    egress: draft.egress,
    egress_interface: tunnel ? draft.egress_interface.trim() : '',
    egress_dns: tunnel ? splitEgressDNS(draft.egress_dns) : []
  }

  networkState.busyID = existing?.id || 'new'
  networkState.error = ''
  networkState.notice = ''
  try {
    const result = existing
      ? await gateway.updateProxy(existing.id, {
          ...common,
          revision: existing.revision,
          ...(password ? { password } : {})
        })
      : await gateway.createProxy({
          ...common,
          password: authEnabled ? password : ''
        })
    const index = networkState.proxies.findIndex(proxy => proxy.id === result.proxy.id)
    if (index >= 0) networkState.proxies[index] = result.proxy
    else networkState.proxies.push(result.proxy)
    await loadNetwork(true, true)
    networkState.notice = applyNotice(result.applied, result.status)
    return true
  } catch (error) {
    await handleMutationFailure(error, translate('runtime.proxySaveFailed'))
    return false
  } finally {
    networkState.busyID = ''
  }
}

export async function setProxyEnabled(
  proxy: ProxyInstance,
  enabled: boolean
): Promise<boolean> {
  if (networkState.busyID) return false
  networkState.busyID = proxy.id
  networkState.error = ''
  networkState.notice = ''
  try {
    const result = await gateway.updateProxy(proxy.id, {
      revision: proxy.revision,
      name: proxy.name,
      line_id: proxy.line_id,
      enabled,
      mode: proxy.mode,
      listen_address: proxy.listen_address,
      listen_port: proxy.listen_port,
      auth_enabled: proxy.auth_enabled,
      username: proxy.username
    })
    const index = networkState.proxies.findIndex(item => item.id === proxy.id)
    if (index >= 0) networkState.proxies[index] = result.proxy
    await loadNetwork(true, true)
    networkState.notice = applyNotice(result.applied, result.status)
    return true
  } catch (error) {
    await handleMutationFailure(error, translate('runtime.proxyToggleFailed'))
    return false
  } finally {
    networkState.busyID = ''
  }
}

export async function removeProxy(proxy: ProxyInstance): Promise<boolean> {
  if (networkState.busyID) return false
  networkState.busyID = proxy.id
  networkState.error = ''
  networkState.notice = ''
  try {
    const result = await gateway.deleteProxy(proxy.id, proxy.revision)
    networkState.proxies = networkState.proxies.filter(item => item.id !== proxy.id)
    await loadNetwork(true, true)
    networkState.notice = applyNotice(result.applied, result.status)
    return true
  } catch (error) {
    await handleMutationFailure(error, translate('runtime.proxyDeleteFailed'))
    return false
  } finally {
    networkState.busyID = ''
  }
}
