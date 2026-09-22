import type {
  ConfiguredModemDeckGateway,
  GatewayInteractions,
  ListQuery,
  ModemDeckGateway
} from './gateway'
import { emptyIntegrationDraft, integrationDraftProblem, type HomeSIMIntegrationDraft } from './homesim'
import {
  callActionContract,
  callLeaseContract,
  callMediaContract,
  callMediaICEContract,
  callMediaReleaseContract,
  callRecordPath,
  callRecordingContract,
  callRecordingResourcePath,
  communicationContracts,
  createCallActionPayload,
  createCallLeasePayload,
  createCallMediaPayload,
  createCallMediaICEPayload,
  createCallMediaReleasePayload,
  createCallPayload,
  createCallRecordingPayload,
  createMemberPayload,
  createMemberUpdatePayload,
  createDeviceConfigurationPayload,
  createDTMFPayload,
  createGlobalCallSettingsPayload,
  createLineSettingsPayload,
  createSystemSettingsPayload,
  createLineLabelPayload,
  createMessageReadPayload,
  createMessagePayload,
  createNetworkSelectionPayload,
  createProxyPayload,
  createProxyUpdatePayload,
  createRecordingSettingsPayload,
  createTelegramUnitPayload,
  createCloudflareOriginTLSPayload,
  createTLSSettingsPayload,
  externalAccessContract,
  parseActiveCallSnapshotResponse,
  parseCallMediaResponse,
  parseCallMediaICEConfiguration,
  parseCallLeaseStatus,
  parseCallRecordingState,
  parseCallRecordingSnapshotResponse,
  parseCallRecordingsResponse,
  parseCallResponse,
  parseDeviceConfigurationResponse,
  parseCloudflareOriginTLSResponse,
  parseExternalAccessStatusResponse,
  parseGlobalCallSettings,
  parseLineSettingsResponse,
  parseIOSPairingResponse,
  parseIOSTestCallResponse,
  parseIOSDeviceInfo,
  parseSystemSettingsResponse,
  parseLineLabelResponse,
  parseMessageResponse,
  parseMobileNetworkScanResponse,
  parseNetworkSelectionResponse,
  parseNetworkStatusResponse,
  parseProxyCollectionResponse,
  parseProxyDeleteResponse,
  parseProxyMutationResponse,
  parseRecordingEntriesResponse,
  parseRecordingSettingsResponse,
  parseSIMStatusResponse,
  parseTelegramUnitResponse,
  parseTelegramUnitsResponse,
  parseTLSSettingsResponse,
  parseUserResponse,
  parseUsersResponse,
  telegramUnitContract,
  telegramUnitDeletePath,
  diagnosticDeviceConfigurationContract,
  deviceConfigurationContract,
  lineLabelPath,
  iosPairingContract,
  missedCallReadPath,
  networkSelectionContract,
  networkContracts,
  proxyDeletePath,
  proxyResourceContract,
  tlsSettingsContract
} from './contract'
import {
  parseBootstrap,
  parseCalls,
  parseContactResponse,
  parseContacts,
  parseDeviceResponse,
  parseDevices,
  parseLine,
  parseMessages,
  parseRuntimeCapabilities,
  parseThreads
} from './normalize'
import type {
  ActiveCallSnapshot,
  AccountSession,
  AboutInfo,
  ApiErrorBody,
  BootstrapResponse,
  CallFilter,
  CallRecordingSegment,
  CallRecordingState,
  CallSession,
  ChangePasswordInput,
  CommandReceipt,
  ConnectionProfile,
  Contact,
  ContactInput,
  CommunicationCapabilities,
  CommunicationCapabilityName,
  CreateMemberInput,
  CreateDeviceInput,
  CreateProxyInput,
  DeleteConnectionProfileInput,
  Device,
  DeviceConfiguration,
  DiagnosticActiveCall,
  DiagnosticLineSummary,
  DiagnosticLogEntry,
  DiagnosticLogLevel,
  DiagnosticLogPage,
  DiagnosticLogQuery,
  DiagnosticLogStreamHandlers,
  DiagnosticStatus,
  DiagnosticsSnapshot,
  CloudflareOriginTLSStatus,
  GlobalCallSettings,
  IncomingMessageEvent,
  LineLabelResult,
  IOSPairingResult,
  IOSTestCallResult,
  InstallCloudflareOriginTLSInput,
  LoginInput,
  Message,
  MessageEventStreamHandlers,
  MobileNetworkScan,
  NetworkSelectionPolicy,
  NetworkStatus,
  ProxyDeleteResult,
  ProxyInstance,
  ProxyMutation,
  RecordingSettings,
  RenameDeviceInput,
  RuntimeEventStreamHandlers,
  RuntimeState,
  SaveConnectionProfileInput,
  SendMessageInput,
  SessionResponse,
  ProvisionStatus,
  SetupInput,
  SystemLanguage,
  SIMCommandInput,
  SIMAutoUnlock,
  SIMAutoUnlockInput,
  SIMStatus,
  TelegramUnit,
  TelegramUnitInput,
  TLSSettings,
  UpdateCheck,
  UpdateComponent,
  UpdateComponentName,
  UpdateOperation,
  UpdateOperationComponent,
  UpdateOperationComponentState,
  UpdateOperationState,
  UpdateStatus,
  UpdateDeviceConfigurationInput,
  UpdateGlobalCallSettingsInput,
  UpdateLineLabelInput,
  UpdateLineSettingsInput,
  UpdateMemberInput,
  UpdateSystemSettingsInput,
  UpdateNetworkSelectionInput,
  UpdateProxyInput,
  UpdateTLSSettingsInput,
  USSDCommandInput,
  USSDResponse,
  USSDStatus,
  UserAccount
} from './types'
import { ApiError } from './types'
import { translate } from '../i18n'

export const API_ROOT = '/api/v1'
const READ_REQUEST_TIMEOUT_MS = 15_000
const WRITE_REQUEST_TIMEOUT_MS = 60_000
const NETWORK_SCAN_REQUEST_TIMEOUT_MS = 130_000
const CALL_LEASE_REQUEST_TIMEOUT_MS = 4_000
const MESSAGE_EVENT_INACTIVITY_TIMEOUT_MS = 40_000
const RUNTIME_EVENT_INACTIVITY_TIMEOUT_MS = 12_000
const RUNTIME_STATE_COALESCE_MS = 50

const runtimeEnvironment = import.meta.env

export const fixtureMode =
  Boolean(runtimeEnvironment?.DEV) && runtimeEnvironment?.VITE_MODEMDECK_FIXTURE === '1'

export const fixtureCallMediaPreview =
  fixtureMode &&
  typeof window !== 'undefined' &&
  new URLSearchParams(window.location.search).get('callMediaFixture') === '1'

const incomingCallFixture =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('incomingCallFixture')
    : ''
const initialIncomingCallFixture: boolean | 'occupied' =
  incomingCallFixture === 'occupied' ? 'occupied' : incomingCallFixture === '1'
const initialConcurrentCallsFixture =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('multiCallFixture') === '1'
    : false
const outgoingReservationFixture =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('outgoingReservationFixture')
    : ''
const initialOutgoingReservationFixture: 'owned' | 'occupied' | undefined =
  outgoingReservationFixture === 'owned' || outgoingReservationFixture === 'occupied'
    ? outgoingReservationFixture
    : undefined
const softwareUpdateFixture =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('updateFixture')
    : ''
const initialSoftwareUpdateFixture: boolean | 'hardware' =
  softwareUpdateFixture === 'hardware' ? 'hardware' : softwareUpdateFixture === '1'
const initialDiagnosticsFailure =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('diagnosticsFixture') === 'error'
    : false
// ?devicesFixture=none previews the gateway with no module attached. That state
// has its own header treatment and its own empty states, and without a way to
// reach it they can only be checked by unplugging real hardware.
const noDevicesFixture =
  fixtureMode && typeof window !== 'undefined'
    ? new URLSearchParams(window.location.search).get('devicesFixture') === 'none'
    : false

const fixturePreviewOptions =
  fixtureMode && typeof window !== 'undefined'
    ? {
        initialIncomingCall: initialIncomingCallFixture,
        initialConcurrentCalls: initialConcurrentCallsFixture,
        initialOutgoingReservation: initialOutgoingReservationFixture,
        initialSoftwareUpdate: initialSoftwareUpdateFixture,
        initialDiagnosticsFailure,
        noDevices: noDevicesFixture
      }
    : {}

const REAL_INTERACTIONS: GatewayInteractions = {
  contacts: true,
  message: true,
  dial: true,
  telegram: true
}

const FIXTURE_INTERACTIONS: GatewayInteractions = {
  contacts: true,
  message: true,
  dial: true,
  telegram: true
}

let currentCSRFToken = ''
let authenticationRequestGeneration = 0
let authenticationRequestController = new AbortController()
let authenticationRequiredHandler: () => void = () => undefined
const conditionalJSONCache = new Map<string, { etag: string; body: unknown }>()

function requestID(): string {
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = ((bytes[6] || 0) & 0x0f) | 0x40
  bytes[8] = ((bytes[8] || 0) & 0x3f) | 0x80
  const encoded = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  return `${encoded.slice(0, 8)}-${encoded.slice(8, 12)}-${encoded.slice(12, 16)}-${encoded.slice(16, 20)}-${encoded.slice(20)}`
}

export function setClientCSRFToken(token?: string): void {
  currentCSRFToken = token?.trim() || ''
}

export function rotateAuthenticationRequestScope(): void {
  authenticationRequestGeneration += 1
  authenticationRequestController.abort()
  authenticationRequestController = new AbortController()
  conditionalJSONCache.clear()
}

export function setAuthenticationRequiredHandler(handler: () => void): void {
  authenticationRequiredHandler = handler
}

function queryString(query: Record<string, string | undefined>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value) params.set(key, value)
  }
  const encoded = params.toString()
  return encoded ? `?${encoded}` : ''
}

function recordValue(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function stringProperty(source: Record<string, unknown> | undefined, key: string): string | undefined {
  const value = source?.[key]
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function errorDetails(value: unknown): ApiErrorBody | undefined {
  const source = recordValue(value)
  if (!source) return undefined
  return {
    code: stringProperty(source, 'code'),
    message: stringProperty(source, 'message'),
    detail: stringProperty(source, 'detail'),
    field: stringProperty(source, 'field')
  }
}

// A reverse proxy in front of the origin can answer with its own error page
// (for example an nginx "502 Bad Gateway" document) instead of our JSON error
// envelope. Such a body is markup, never something to show a user, so only
// plain-text bodies are returned verbatim. Everything else is reported through
// the localized HTTP status message instead.
const MAX_TEXT_ERROR_LENGTH = 300

function plainTextErrorBody(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const text = value.trim()
  if (!text) return undefined
  if (/^[<{]/.test(text)) return undefined
  return text.length > MAX_TEXT_ERROR_LENGTH
    ? `${text.slice(0, MAX_TEXT_ERROR_LENGTH)}…`
    : text
}

function parseSIMAutoUnlockResponse(value: unknown): SIMAutoUnlock {
  const envelope = (value ?? {}) as { auto_unlock?: Record<string, unknown> }
  const view = (envelope.auto_unlock ?? (value ?? {})) as Record<string, unknown>
  const revision = Number(view.revision)
  if (!Number.isInteger(revision) || revision < 1) throw new Error('SIM auto-unlock response is invalid.')
  return {
    line_id: typeof view.line_id === 'string' ? view.line_id : '',
    enabled: view.enabled === true,
    has_pin: view.has_pin === true,
    updated_at: typeof view.updated_at === 'string' ? view.updated_at : undefined,
    revision
  }
}

function parseSession(value: unknown): SessionResponse {
  const source = recordValue(value)
  if (
    !source ||
    typeof source.authenticated !== 'boolean' ||
    typeof source.setup_required !== 'boolean'
  ) {
    throw new ApiError('NAS猫窝 服务返回了无效的会话状态', 0, 'invalid_response')
  }

  const language = source.language
  if (
    language !== 'auto' &&
    language !== 'zh-CN' &&
    language !== 'zh-TW' &&
    language !== 'en-US' &&
    language !== 'ja-JP' &&
    language !== 'vi-VN' &&
    language !== 'es-ES' &&
    language !== 'de-DE' &&
    language !== 'fr-FR' &&
    language !== 'pt-BR'
  ) {
    throw new ApiError('NasSimHub returned an unsupported system language', 0, 'invalid_response')
  }
  if (typeof source.ios_pairing_enabled !== 'boolean') {
    throw new ApiError(
      'NasSimHub returned invalid iOS pairing access',
      0,
      'invalid_response'
    )
  }
  const session: SessionResponse = {
    authenticated: source.authenticated,
    setup_required: source.setup_required,
    provision_required: source.provision_required === true,
    provision_reason: stringProperty(source, 'provision_reason'),
    user_id: stringProperty(source, 'user_id'),
    username: stringProperty(source, 'username'),
    csrf_token: stringProperty(source, 'csrf_token'),
    ios_pairing_enabled: source.ios_pairing_enabled,
    language: language as SystemLanguage
  }
  const role = stringProperty(source, 'role')
  if (role && role !== 'admin' && role !== 'member') {
    throw new ApiError('NasSimHub returned an unsupported user role', 0, 'invalid_response')
  }
  if (role) session.role = role as 'admin' | 'member'
  const profileContactID = stringProperty(source, 'profile_contact_id')
  if (profileContactID) session.profile_contact_id = profileContactID
  if (Array.isArray(source.allowed_line_ids)) {
    if (!source.allowed_line_ids.every(value => typeof value === 'string' && value.trim())) {
      throw new ApiError('NasSimHub returned invalid line access', 0, 'invalid_response')
    }
    session.allowed_line_ids = source.allowed_line_ids.map(value => String(value).trim())
  }
  if (session.authenticated && (!session.username || !session.csrf_token)) {
    throw new ApiError('NAS猫窝 服务返回了不完整的会话状态', 0, 'invalid_response')
  }
  return session
}

function parseAccountSessions(value: unknown): AccountSession[] {
  const source = recordValue(value)
  if (!source || !Array.isArray(source.sessions)) {
    throw new ApiError(
      'NasSimHub returned an invalid signed-in device list',
      0,
      'invalid_response'
    )
  }
  return source.sessions.map((value, index) => {
    const session = recordValue(value)
    const path = `sessions[${index}]`
    if (!session) {
      throw new ApiError(`${path} is invalid`, 0, 'invalid_response')
    }
    const id = stringProperty(session, 'id')
    const kind = stringProperty(session, 'kind')
    const createdAt = stringProperty(session, 'created_at')
    const pairedAt = stringProperty(session, 'paired_at')
    const lastSeenAt = stringProperty(session, 'last_seen_at')
    if (
      !id ||
      (kind !== 'web' && kind !== 'ios') ||
      !createdAt ||
      !Number.isFinite(Date.parse(createdAt)) ||
      (pairedAt !== undefined && !Number.isFinite(Date.parse(pairedAt))) ||
      (lastSeenAt !== undefined && !Number.isFinite(Date.parse(lastSeenAt))) ||
      typeof session.current !== 'boolean'
    ) {
      throw new ApiError(`${path} is invalid`, 0, 'invalid_response')
    }
    return {
      id,
      kind,
      created_at: createdAt,
      paired_at: pairedAt,
      last_seen_at: lastSeenAt,
      user_agent: stringProperty(session, 'user_agent'),
      access_ip: stringProperty(session, 'access_ip') || undefined,
      access_host: stringProperty(session, 'access_host'),
      device: parseIOSDeviceInfo(session.device, `${path}.device`),
      current: session.current,
      paired: typeof session.paired === 'boolean' ? session.paired : undefined
    }
  })
}

function requiredRecord(value: unknown, path: string): Record<string, unknown> {
  const source = recordValue(value)
  if (!source) throw new ApiError(`${path} 必须是对象`, 0, 'invalid_response')
  return source
}

function stringValue(source: Record<string, unknown>, key: string): string {
  const value = source[key]
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return ''
}

function requiredStringValue(
  source: Record<string, unknown>,
  path: string,
  key: string
): string {
  const value = stringValue(source, key)
  if (!value) throw new ApiError(`${path}.${key} 不能为空`, 0, 'invalid_response')
  return value
}

function requiredBooleanValue(
  source: Record<string, unknown>,
  path: string,
  key: string
): boolean {
  const value = source[key]
  if (typeof value !== 'boolean') {
    throw new ApiError(`${path}.${key} 必须是布尔值`, 0, 'invalid_response')
  }
  return value
}

const UPDATE_STATUSES = new Set<UpdateStatus>([
  'up_to_date',
  'update_available',
  'development',
  'unavailable'
])
const UPDATE_COMPONENT_NAMES = new Set<UpdateComponentName>([
  'api',
  'web',
  'hardware',
  'updater',
  'cloudflared'
])
const UPDATE_OPERATION_STATES = new Set<UpdateOperationState>([
  'running',
  'succeeded',
  'failed'
])
const UPDATE_OPERATION_COMPONENT_STATES = new Set<UpdateOperationComponentState>([
  'pending',
  'pulling',
  'staged',
  'restarting',
  'rolling_back',
  'rolled_back',
  'ready',
  'failed'
])

function parseUpdateOperationComponent(
  value: unknown,
  index: number
): UpdateOperationComponent {
  const path = `update_operation.components[${index}]`
  const source = requiredRecord(value, path)
  const name = requiredStringValue(source, path, 'name') as UpdateComponentName
  if (!UPDATE_COMPONENT_NAMES.has(name)) {
    throw new ApiError(`${path}.name 未知：${name}`, 0, 'invalid_response')
  }
  const state = requiredStringValue(
    source,
    path,
    'state'
  ) as UpdateOperationComponentState
  if (!UPDATE_OPERATION_COMPONENT_STATES.has(state)) {
    throw new ApiError(`${path}.state 未知：${state}`, 0, 'invalid_response')
  }
  return { name, state }
}

function parseUpdateOperation(value: unknown): UpdateOperation {
  const source = requiredRecord(value, 'update_operation')
  const components = source.components
  if (components !== undefined && !Array.isArray(components)) {
    throw new ApiError('update_operation.components 必须是数组', 0, 'invalid_response')
  }
  const state = requiredStringValue(
    source,
    'update_operation',
    'state'
  ) as UpdateOperationState
  if (!UPDATE_OPERATION_STATES.has(state)) {
    throw new ApiError(`update_operation.state 未知：${state}`, 0, 'invalid_response')
  }
  return {
    id: requiredStringValue(source, 'update_operation', 'id'),
    state,
    target_version: requiredStringValue(source, 'update_operation', 'target_version'),
    started_at: requiredStringValue(source, 'update_operation', 'started_at'),
    finished_at: stringValue(source, 'finished_at') || undefined,
    error_code: stringValue(source, 'error_code') || undefined,
    components: components?.map((component, index) =>
      parseUpdateOperationComponent(component, index)
    )
  }
}

function parseUpdateComponent(value: unknown, index: number): UpdateComponent {
  const path = `update_check.components[${index}]`
  const source = requiredRecord(value, path)
  const name = requiredStringValue(source, path, 'name') as UpdateComponentName
  if (!UPDATE_COMPONENT_NAMES.has(name)) {
    throw new ApiError(`${path}.name 未知：${name}`, 0, 'invalid_response')
  }
  return {
    name,
    current_version: stringValue(source, 'current_version') || undefined,
    target_version: stringValue(source, 'target_version') || undefined,
    changed: requiredBooleanValue(source, path, 'changed')
  }
}

function parseAbout(value: unknown): AboutInfo {
  const source = requiredRecord(value, 'about')
  return {
    name: requiredStringValue(source, 'about', 'name'),
    version: requiredStringValue(source, 'about', 'version'),
    repository_url: requiredStringValue(source, 'about', 'repository_url'),
    license_name: requiredStringValue(source, 'about', 'license_name'),
    license_url: requiredStringValue(source, 'about', 'license_url'),
    notices_url: requiredStringValue(source, 'about', 'notices_url')
  }
}

function parseUpdateCheck(value: unknown): UpdateCheck {
  const source = requiredRecord(value, 'update_check')
  const status = requiredStringValue(source, 'update_check', 'status') as UpdateStatus
  if (!UPDATE_STATUSES.has(status)) {
    throw new ApiError(`update_check.status 未知：${status}`, 0, 'invalid_response')
  }
  const components = source.components
  if (components !== undefined && !Array.isArray(components)) {
    throw new ApiError('update_check.components 必须是数组', 0, 'invalid_response')
  }
  return {
    status,
    current_version: requiredStringValue(source, 'update_check', 'current_version'),
    latest_version: stringValue(source, 'latest_version') || undefined,
    release_name: stringValue(source, 'release_name') || undefined,
    release_url: stringValue(source, 'release_url') || undefined,
    release_notes: stringValue(source, 'release_notes') || undefined,
    published_at: stringValue(source, 'published_at') || undefined,
    checked_at: requiredStringValue(source, 'update_check', 'checked_at'),
    error_code: stringValue(source, 'error_code') || undefined,
    apply_available: requiredBooleanValue(source, 'update_check', 'apply_available'),
    components: components?.map((component, index) => parseUpdateComponent(component, index)),
    hardware_confirmation_required: requiredBooleanValue(
      source,
      'update_check',
      'hardware_confirmation_required'
    ),
    operation: source.operation ? parseUpdateOperation(source.operation) : undefined
  }
}

function numberValue(source: Record<string, unknown>, path: string, key: string): number {
  const value = source[key]
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    throw new ApiError(`${path}.${key} 必须是非负数`, 0, 'invalid_response')
  }
  return value
}

const DIAGNOSTIC_CAPABILITIES: Array<[CommunicationCapabilityName, string]> = [
  ['modem', 'modem'],
  ['sim', 'sim'],
  ['voice', 'voice'],
  ['messaging', 'messaging'],
  ['media', 'media'],
  ['dial', 'dial'],
  ['answer', 'answer_call'],
  ['reject', 'reject_call'],
  ['hangup', 'hangup_call'],
  ['dtmf', 'send_dtmf'],
  ['message', 'send_message']
]

function parseDiagnosticCapabilities(value: unknown, path: string): CommunicationCapabilities {
  const source = requiredRecord(value, path)
  const result: CommunicationCapabilities = {}
  for (const [name, wireName] of DIAGNOSTIC_CAPABILITIES) {
    result[name] = requiredBooleanValue(source, path, wireName)
  }
  return result
}

function parseDiagnosticLine(value: unknown, index: number): DiagnosticLineSummary {
  const path = `diagnostics.lines[${index}]`
  const source = requiredRecord(value, path)
  return {
    ...parseLine(source),
    endpoint_id: stringValue(source, 'endpoint_id') || undefined,
    capabilities: parseDiagnosticCapabilities(source.capabilities, `${path}.capabilities`)
  }
}

function parseDiagnosticAvailability(value: unknown, path: string) {
  const source = requiredRecord(value, path)
  return {
    available: requiredBooleanValue(source, path, 'available'),
    error: stringValue(source, 'error') || undefined
  }
}

function parseCommandReceipt(value: unknown): CommandReceipt {
  const response = requiredRecord(value, 'command_response')
  const receipt = requiredRecord(response.receipt, 'command_response.receipt')
  return {
    request_id: requiredStringValue(receipt, 'command_response.receipt', 'request_id'),
    resource_id: requiredStringValue(receipt, 'command_response.receipt', 'resource_id')
  }
}

function parseConnectionProfile(value: unknown, path: string): ConnectionProfile {
  const source = requiredRecord(value, path)
  return {
    profile_id: numberValue(source, path, 'profile_id'),
    profile_name: stringValue(source, 'profile_name'),
    apn: stringValue(source, 'apn'),
    ip_family: stringValue(source, 'ip_family'),
    ip_type: numberValue(source, path, 'ip_type'),
    apn_type: numberValue(source, path, 'apn_type'),
    allowed_auth: numberValue(source, path, 'allowed_auth'),
    user: stringValue(source, 'user') || undefined,
    access_type_preference: numberValue(source, path, 'access_type_preference'),
    roaming_allowance: numberValue(source, path, 'roaming_allowance'),
    profile_source: numberValue(source, path, 'profile_source')
  }
}

function parseConnectionProfiles(value: unknown): ConnectionProfile[] {
  const response = requiredRecord(value, 'profiles_response')
  if (!Array.isArray(response.profiles)) {
    throw new ApiError('profiles_response.profiles 必须是数组', 0, 'invalid_response')
  }
  return response.profiles.map((profile, index) =>
    parseConnectionProfile(profile, `profiles_response.profiles[${index}]`)
  )
}

function parseSavedConnectionProfile(value: unknown): ConnectionProfile {
  const response = requiredRecord(value, 'profile_response')
  return parseConnectionProfile(response.profile, 'profile_response.profile')
}

function parseUSSDStatus(value: unknown): USSDStatus {
  const response = requiredRecord(value, 'ussd_response')
  const source = requiredRecord(response.ussd, 'ussd_response.ussd')
  return {
    line_id: requiredStringValue(source, 'ussd_response.ussd', 'line_id'),
    state: requiredStringValue(source, 'ussd_response.ussd', 'state'),
    state_code: numberValue(source, 'ussd_response.ussd', 'state_code'),
    network_notification: stringValue(source, 'network_notification') || undefined,
    network_request: stringValue(source, 'network_request') || undefined,
    observed_at: requiredStringValue(source, 'ussd_response.ussd', 'observed_at')
  }
}

function parseUSSDResult(value: unknown): USSDResponse {
  const response = requiredRecord(value, 'ussd_command_response')
  const result = requiredRecord(response.result, 'ussd_command_response.result')
  return { response: stringValue(result, 'response') || undefined }
}

function parseDiagnosticAgentCapabilities(value: unknown) {
  const path = 'diagnostics.host_agent.capabilities'
  const source = requiredRecord(value, path)
  return {
    discovery: requiredBooleanValue(source, path, 'discovery'),
    snapshot: requiredBooleanValue(source, path, 'snapshot'),
    device_configuration: requiredBooleanValue(source, path, 'device_configuration'),
    network: requiredBooleanValue(source, path, 'network'),
    proxy: requiredBooleanValue(source, path, 'proxy'),
    dial: requiredBooleanValue(source, path, 'dial'),
    answer_call: requiredBooleanValue(source, path, 'answer_call'),
    reject_call: requiredBooleanValue(source, path, 'reject_call'),
    hangup_call: requiredBooleanValue(source, path, 'hangup_call'),
    send_dtmf: requiredBooleanValue(source, path, 'send_dtmf'),
    send_message: requiredBooleanValue(source, path, 'send_message'),
    sim_management: requiredBooleanValue(source, path, 'sim_management'),
    connection_profiles: requiredBooleanValue(source, path, 'connection_profiles'),
    ussd: requiredBooleanValue(source, path, 'ussd'),
    media:
      source.media === undefined
        ? false
        : requiredBooleanValue(source, path, 'media')
  }
}

function parseDiagnosticCall(value: unknown, index: number): DiagnosticActiveCall {
  const path = `diagnostics.active_calls[${index}]`
  const source = requiredRecord(value, path)
  const audioRate = source.audio_rate
  return {
    id: requiredStringValue(source, path, 'id'),
    line_id: requiredStringValue(source, path, 'line_id'),
    endpoint_line_id: stringValue(source, 'endpoint_line_id') || undefined,
    direction: requiredStringValue(source, path, 'direction'),
    phase: requiredStringValue(source, path, 'phase'),
    bearer: stringValue(source, 'bearer'),
    media_available: requiredBooleanValue(source, path, 'media_available'),
    audio_encoding: stringValue(source, 'audio_encoding') || undefined,
    audio_resolution: stringValue(source, 'audio_resolution') || undefined,
    audio_rate:
      typeof audioRate === 'number' && Number.isFinite(audioRate) && audioRate > 0
        ? audioRate
        : undefined
  }
}

function parseDiagnostics(value: unknown): DiagnosticsSnapshot {
  const source = requiredRecord(value, 'diagnostics')
  const status = requiredStringValue(source, 'diagnostics', 'status')
  if (status !== 'ok' && status !== 'degraded' && status !== 'unavailable') {
    throw new ApiError(`diagnostics.status 未知：${status}`, 0, 'invalid_response')
  }
  const hostAgentSource = requiredRecord(source.host_agent, 'diagnostics.host_agent')
  const lines = source.lines === null ? [] : source.lines
  const activeCalls = source.active_calls === null ? [] : source.active_calls
  if (!Array.isArray(lines) || !Array.isArray(activeCalls)) {
    throw new ApiError('diagnostics 的线路或通话列表无效', 0, 'invalid_response')
  }
  return {
    status: status as DiagnosticStatus,
    observed_at: requiredStringValue(source, 'diagnostics', 'observed_at'),
    database: parseDiagnosticAvailability(source.database, 'diagnostics.database'),
    host_agent: {
      connected: requiredBooleanValue(
        hostAgentSource,
        'diagnostics.host_agent',
        'connected'
      ),
      provider: stringValue(hostAgentSource, 'provider'),
      agent_version: stringValue(hostAgentSource, 'agent_version'),
      runtime_version: stringValue(hostAgentSource, 'runtime_version'),
      boot_epoch: stringValue(hostAgentSource, 'boot_epoch'),
      revision: stringValue(hostAgentSource, 'revision'),
      observed_at: stringValue(hostAgentSource, 'observed_at'),
      last_error: stringValue(hostAgentSource, 'last_error') || undefined,
      capabilities: parseDiagnosticAgentCapabilities(hostAgentSource.capabilities)
    },
    call_runtime: parseDiagnosticAvailability(
      source.call_runtime,
      'diagnostics.call_runtime'
    ),
    lines: lines.map(parseDiagnosticLine),
    active_calls: activeCalls.map(parseDiagnosticCall)
  }
}

function parseDiagnosticLogEntry(value: unknown, path = 'diagnostic_log'): DiagnosticLogEntry {
  const source = requiredRecord(value, path)
  const level = requiredStringValue(source, path, 'level')
  if (level !== 'debug' && level !== 'info' && level !== 'warn' && level !== 'error') {
    throw new ApiError(`${path}.level 未知：${level}`, 0, 'invalid_response')
  }
  const fields = source.fields === undefined ? undefined : requiredRecord(source.fields, `${path}.fields`)
  return {
    id: numberValue(source, path, 'id'),
    timestamp: requiredStringValue(source, path, 'timestamp'),
    level: level as DiagnosticLogLevel,
    source: stringValue(source, 'source') || undefined,
    component: requiredStringValue(source, path, 'component'),
    caller: stringValue(source, 'caller') || undefined,
    message: requiredStringValue(source, path, 'message'),
    fields: fields ? { ...fields } : undefined
  }
}

function parseIncomingMessageEvent(value: unknown): IncomingMessageEvent {
  const source = requiredRecord(value, 'incoming_message_event')
  const observedAt = requiredStringValue(source, 'incoming_message_event', 'observed_at')
  if (Number.isNaN(Date.parse(observedAt))) {
    throw new ApiError(
      'incoming_message_event.observed_at 必须是有效时间',
      0,
      'invalid_response'
    )
  }
  return {
    message_id: requiredStringValue(source, 'incoming_message_event', 'message_id'),
    thread_key: requiredStringValue(source, 'incoming_message_event', 'thread_key'),
    line_id: requiredStringValue(source, 'incoming_message_event', 'line_id'),
    peer: requiredStringValue(source, 'incoming_message_event', 'peer'),
    content: stringValue(source, 'content'),
    timestamp: requiredStringValue(source, 'incoming_message_event', 'timestamp'),
    observed_at: observedAt
  }
}

function parseRuntimeState(value: unknown): RuntimeState {
  const source = requiredRecord(value, 'runtime_state')
  const revision = numberValue(source, 'runtime_state', 'revision')
  const dataRevision = numberValue(source, 'runtime_state', 'data_revision')
  if (!Number.isSafeInteger(revision) || revision < 0) {
    throw new ApiError('runtime_state.revision 必须是非负整数', 0, 'invalid_response')
  }
  if (!Number.isSafeInteger(dataRevision) || dataRevision < 0) {
    throw new ApiError('runtime_state.data_revision 必须是非负整数', 0, 'invalid_response')
  }
  const observedAt = requiredStringValue(source, 'runtime_state', 'observed_at')
  if (Number.isNaN(Date.parse(observedAt))) {
    throw new ApiError('runtime_state.observed_at 必须是有效时间', 0, 'invalid_response')
  }
  const state: RuntimeState = {
    epoch: requiredStringValue(source, 'runtime_state', 'epoch'),
    revision,
    data_revision: dataRevision,
    observed_at: observedAt
  }
  if (source.communication !== undefined) {
    const communication = requiredRecord(
      source.communication,
      'runtime_state.communication'
    )
    if (!Array.isArray(communication.lines)) {
      throw new ApiError(
        'runtime_state.communication.lines 必须是数组',
        0,
        'invalid_response'
      )
    }
    if (!Array.isArray(communication.line_catalog)) {
      throw new ApiError(
        'runtime_state.communication.line_catalog 必须是数组',
        0,
        'invalid_response'
      )
    }
    if (communication.devices !== undefined && !Array.isArray(communication.devices)) {
      throw new ApiError(
        'runtime_state.communication.devices 必须是数组',
        0,
        'invalid_response'
      )
    }
    state.communication = {
      capabilities: parseRuntimeCapabilities(
        communication.capabilities,
        'runtime_state.communication.capabilities'
      ),
      lines: communication.lines.map(parseLine),
      line_catalog: communication.line_catalog.map(parseLine),
      ...(communication.devices !== undefined
        ? { devices: parseDevices({ devices: communication.devices }) }
        : {})
    }
  }
  if (source.network !== undefined) {
    const network = requiredRecord(source.network, 'runtime_state.network')
    if (!Array.isArray(network.proxies)) {
      throw new ApiError(
        'runtime_state.network.proxies 必须是数组',
        0,
        'invalid_response'
      )
    }
    state.network = {
      status: parseNetworkStatusResponse(network.status),
      proxies: parseProxyCollectionResponse({ proxies: network.proxies })
    }
  }
  if (source.calls !== undefined) {
    state.calls = parseActiveCallSnapshotResponse(source.calls)
  }
  if (source.recordings !== undefined) {
    if (!Array.isArray(source.recordings)) {
      throw new ApiError(
        'runtime_state.recordings 必须是数组',
        0,
        'invalid_response'
      )
    }
    state.recordings = source.recordings.map(recording =>
      parseCallRecordingSnapshotResponse(recording)
    )
  }
  return state
}

function mergePendingRuntimeState(
  current: RuntimeState | undefined,
  incoming: RuntimeState
): RuntimeState {
  if (!current || current.epoch !== incoming.epoch) return incoming
  if (incoming.revision < current.revision) return current

  const merged: RuntimeState = { ...incoming }
  const communication = incoming.communication ?? current.communication
  const network = incoming.network ?? current.network
  const calls = incoming.calls ?? current.calls
  const recordings =
    incoming.calls !== undefined
      ? (incoming.recordings ?? [])
      : (incoming.recordings ?? current.recordings)
  if (communication !== undefined) merged.communication = communication
  if (network !== undefined) merged.network = network
  if (calls !== undefined) merged.calls = calls
  if (recordings !== undefined) merged.recordings = recordings
  return merged
}

function parseDiagnosticLogPage(value: unknown): DiagnosticLogPage {
  const source = requiredRecord(value, 'diagnostic_logs')
  if (!Array.isArray(source.entries)) {
    throw new ApiError('diagnostic_logs.entries 必须是数组', 0, 'invalid_response')
  }
  return {
    entries: source.entries.map((entry, index) =>
      parseDiagnosticLogEntry(entry, `diagnostic_logs.entries[${index}]`)
    ),
    oldest_id: numberValue(source, 'diagnostic_logs', 'oldest_id'),
    newest_id: numberValue(source, 'diagnostic_logs', 'newest_id'),
    truncated: requiredBooleanValue(source, 'diagnostic_logs', 'truncated')
  }
}

function diagnosticLogQueryString(query: DiagnosticLogQuery = {}): string {
  return queryString({
    after: query.after === undefined ? undefined : String(query.after),
    limit: query.limit === undefined ? undefined : String(query.limit),
    level: query.level,
    component: query.component?.trim(),
    search: query.search?.trim()
  })
}

async function responseBody(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined

  const text = await response.text()
  if (!text.trim()) return undefined
  if (!response.headers.get('content-type')?.toLocaleLowerCase().includes('application/json')) {
    return text
  }

  try {
    return JSON.parse(text) as unknown
  } catch {
    if (!response.ok) return text
    throw new ApiError('NAS猫窝 服务返回了无效的 JSON', response.status, 'invalid_response')
  }
}

type RequestResult = {
  body: unknown
  response: Response
}

async function requestWithResponse(
  path: string,
  init: RequestInit,
  expectedStatus: number | readonly number[],
  timeoutMilliseconds?: number,
  authenticationRequired = true
): Promise<RequestResult> {
  const method = (init.method || 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS' && currentCSRFToken) {
    headers.set('X-ModemDeck-CSRF', currentCSRFToken)
  }
  const timeoutSignal = AbortSignal.timeout(
    timeoutMilliseconds ??
      (method === 'GET' || method === 'HEAD' || method === 'OPTIONS'
        ? READ_REQUEST_TIMEOUT_MS
        : WRITE_REQUEST_TIMEOUT_MS)
  )
  const requestAuthenticationGeneration = authenticationRequestGeneration
  const authenticationSignal = authenticationRequired
    ? authenticationRequestController.signal
    : undefined
  const requestSignals = [timeoutSignal]
  if (init.signal) requestSignals.push(init.signal)
  if (authenticationSignal) requestSignals.push(authenticationSignal)
  const signal =
    requestSignals.length === 1
      ? requestSignals[0]
      : AbortSignal.any(requestSignals)

  let response: Response
  try {
    response = await fetch(path, { ...init, headers, signal, credentials: 'same-origin' })
  } catch (error) {
    if (init.signal?.aborted || authenticationSignal?.aborted) throw error
    if (timeoutSignal.aborted) {
      throw new ApiError('NAS猫窝 请求超时', 0, 'request_timeout')
    }
    throw new ApiError('无法连接 NAS猫窝 服务')
  }

  let body: unknown
  try {
    body = await responseBody(response)
  } catch (error) {
    if (init.signal?.aborted || authenticationSignal?.aborted) throw error
    if (timeoutSignal.aborted) {
      throw new ApiError('NAS猫窝 请求超时', 0, 'request_timeout')
    }
    if (error instanceof ApiError) throw error
    throw new ApiError('无法读取 NAS猫窝 服务响应', response.status, 'invalid_response')
  }

  const expectedStatuses = Array.isArray(expectedStatus)
    ? expectedStatus
    : [expectedStatus]
  const statusExpected = expectedStatuses.includes(response.status)
  if (!response.ok && !statusExpected) {
    const details = errorDetails(body)
    if (
      response.status === 401 &&
      authenticationRequired &&
      requestAuthenticationGeneration === authenticationRequestGeneration
    ) {
      authenticationRequiredHandler()
    }
    const message =
      details?.message ||
      details?.detail ||
      plainTextErrorBody(body) ||
      translate('runtime.httpStatusFailed', { status: response.status })
    throw new ApiError(message, response.status, details?.code, details?.field)
  }
  if (!statusExpected) {
    throw new ApiError(
      `NAS猫窝 服务返回了意外状态（${response.status}，预期 ${expectedStatuses.join(' 或 ')}）`,
      response.status,
      'unexpected_status'
    )
  }
  return { body, response }
}

async function request(
  path: string,
  init: RequestInit,
  expectedStatus: number,
  timeoutMilliseconds?: number,
  authenticationRequired = true
): Promise<unknown> {
  const result = await requestWithResponse(
    path,
    init,
    expectedStatus,
    timeoutMilliseconds,
    authenticationRequired
  )
  return result.body
}

// Exported for per-feature API modules that belong to a lazily loaded panel.
// Code added to THIS file lands in the startup bundle every visitor downloads,
// so a feature only reachable from one settings page keeps its requests in its
// own module and imports these.
export function get(path: string): Promise<unknown> {
  return request(
    path,
    {
      method: 'GET',
      headers: { Accept: 'application/json' }
    },
    200
  )
}

async function conditionalGet(path: string): Promise<unknown> {
  const cached = conditionalJSONCache.get(path)
  const headers = new Headers({ Accept: 'application/json' })
  if (cached) headers.set('If-None-Match', cached.etag)
  let result = await requestWithResponse(
    path,
    { method: 'GET', headers },
    [200, 304]
  )
  if (result.response.status === 304) {
    if (cached) return cached.body
    result = await requestWithResponse(
      path,
      { method: 'GET', headers: { Accept: 'application/json' } },
      200
    )
  }
  const etag = result.response.headers.get('etag')?.trim() || ''
  if (etag) conditionalJSONCache.set(path, { etag, body: result.body })
  else conditionalJSONCache.delete(path)
  return result.body
}

function getPublic(path: string): Promise<unknown> {
  return request(
    path,
    {
      method: 'GET',
      headers: { Accept: 'application/json' }
    },
    200,
    undefined,
    false
  )
}

/** Lifts the anti-fraud daily distinct-number dial limit (dial pad "unlock"). */
export async function unlockCallGuard(): Promise<void> {
  await writeJSON('/api/v1/calls/guard', 'POST', { action: 'unlock' }, 200)
}

export function writeJSON(
  path: string,
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE',
  input: unknown,
  expectedStatus: number,
  signal?: AbortSignal,
  timeoutMilliseconds?: number
) {
  return request(
    path,
    {
      method,
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(input),
      signal
    },
    expectedStatus,
    timeoutMilliseconds
  )
}

function writePublicJSON(
  path: string,
  method: 'POST',
  input: unknown,
  expectedStatus: number
) {
  return request(
    path,
    {
      method,
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(input)
    },
    expectedStatus,
    undefined,
    false
  )
}

type EventSourceLifecycleHandlers = {
  onOpen: () => void
  onError: (error?: Error) => void
}

function subscribeEventSource(
  path: string,
  handlers: EventSourceLifecycleHandlers,
  bind: (
    source: EventSource,
    restart: (error: Error) => void,
    isActive: () => boolean,
    markActivity: () => void
  ) => void,
  inactivityTimeoutMilliseconds?: number
): () => void {
  let source: EventSource | undefined
  let inactivityTimer: ReturnType<typeof globalThis.setTimeout> | undefined
  let stopped = false

  const clearInactivityTimer = () => {
    if (inactivityTimer === undefined) return
    globalThis.clearTimeout(inactivityTimer)
    inactivityTimer = undefined
  }

  const connect = () => {
    if (stopped) return
    const current = new EventSource(path, { withCredentials: true })
    source = current
    const isActive = () => !stopped && source === current
    const restart = (error: Error) => {
      if (!isActive()) return
      clearInactivityTimer()
      current.close()
      handlers.onError(error)
      connect()
    }
    const markActivity = () => {
      if (!isActive() || !inactivityTimeoutMilliseconds) return
      clearInactivityTimer()
      inactivityTimer = globalThis.setTimeout(() => {
        restart(new Error('事件流长时间没有响应'))
      }, inactivityTimeoutMilliseconds)
    }
    current.onopen = () => {
      if (!isActive()) return
      markActivity()
      handlers.onOpen()
    }
    current.onerror = () => {
      if (isActive()) handlers.onError()
    }
    bind(current, restart, isActive, markActivity)
    markActivity()
  }

  connect()
  return () => {
    if (stopped) return
    stopped = true
    clearInactivityTimer()
    source?.close()
  }
}

const realGateway: ConfiguredModemDeckGateway = {
  interactions: REAL_INTERACTIONS,

  async getAbout(): Promise<AboutInfo> {
    return parseAbout(await get(`${API_ROOT}/about`))
  },

  async checkForUpdates(refresh = false): Promise<UpdateCheck> {
    const query = refresh ? '?refresh=1' : ''
    return parseUpdateCheck(await get(`${API_ROOT}/updates/check${query}`))
  },

  async applySoftwareUpdate(
    version: string,
    confirmHardware: boolean
  ): Promise<UpdateOperation> {
    return parseUpdateOperation(
      await writeJSON(
        `${API_ROOT}/updates/apply`,
        'POST',
        { version, confirm_hardware: confirmHardware },
        202
      )
    )
  },

  async getSoftwareUpdateStatus(): Promise<UpdateOperation> {
    return parseUpdateOperation(await get(`${API_ROOT}/updates/status`))
  },

  async getSession(): Promise<SessionResponse> {
    return parseSession(await getPublic(`${API_ROOT}/session`))
  },

  /**
   * Whether this data directory belongs to this machine.
   *
   * Readable without a session, because the login page has to explain itself
   * before anyone can log in -- and on a cloned gateway nobody can.
   */
  async provisionStatus(): Promise<ProvisionStatus> {
    return (await get(`${API_ROOT}/provision`)) as ProvisionStatus
  },

  /**
   * Requests the factory reset. Records a marker; the wipe happens on the next
   * container start, before the database is opened.
   *
   * The machine ID is typed by the operator rather than sent automatically:
   * this endpoint cannot require a login, so echoing a value only visible on
   * the screen is what separates a deliberate act from a stray request.
   */
  async requestProvisionReset(confirmMachineID: string): Promise<void> {
    await writeJSON(
      `${API_ROOT}/provision/reset`,
      'POST',
      { confirm_machine_id: confirmMachineID },
      202
    )
  },

  async setup(input: SetupInput): Promise<SessionResponse> {
    return parseSession(await writePublicJSON(`${API_ROOT}/setup`, 'POST', input, 201))
  },

  async login(input: LoginInput): Promise<SessionResponse> {
    return parseSession(
      await writePublicJSON(`${API_ROOT}/session`, 'POST', input, 200)
    )
  },

  async changePassword(input: ChangePasswordInput): Promise<void> {
    await writeJSON(`${API_ROOT}/account/password`, 'PUT', input, 204)
  },

  async setAccountContact(contactID: string): Promise<void> {
    await writeJSON(
      `${API_ROOT}/account/contact`,
      'PUT',
      { contact_id: contactID.trim() },
      204
    )
  },

  async listAccountSessions(): Promise<AccountSession[]> {
    return parseAccountSessions(await get(`${API_ROOT}/account/sessions`))
  },

  async logoutAccountSession(id: string): Promise<void> {
    await request(
      `${API_ROOT}/account/sessions/${encodeURIComponent(id)}`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async logoutOtherAccountSessions(): Promise<void> {
    await request(
      `${API_ROOT}/account/sessions/others`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async logout(): Promise<void> {
    await request(
      `${API_ROOT}/session`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async getBootstrap(): Promise<BootstrapResponse> {
    return parseBootstrap(await get(`${API_ROOT}/bootstrap`))
  },

  async listUsers(): Promise<UserAccount[]> {
    return parseUsersResponse(await get(`${API_ROOT}/users`))
  },

  async createMember(input: CreateMemberInput): Promise<UserAccount> {
    return parseUserResponse(
      await writeJSON(`${API_ROOT}/users`, 'POST', createMemberPayload(input), 201)
    )
  },

  async updateMember(id: string, input: UpdateMemberInput): Promise<UserAccount> {
    return parseUserResponse(
      await writeJSON(
        `${API_ROOT}/users/${encodeURIComponent(id)}`,
        'PUT',
        createMemberUpdatePayload(input),
        200
      )
    )
  },

  async setMemberPassword(id: string, password: string): Promise<void> {
    await writeJSON(
      `${API_ROOT}/users/${encodeURIComponent(id)}/password`,
      'PUT',
      { password },
      204
    )
  },

  async revokeUserIOSPairing(id: string): Promise<void> {
    await request(
      `${API_ROOT}/users/${encodeURIComponent(id)}/ios-pairing`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async getSystemSettings() {
    return parseSystemSettingsResponse(await get(`${API_ROOT}/settings/system`))
  },

  async updateSystemSettings(input: UpdateSystemSettingsInput) {
    return parseSystemSettingsResponse(
      await writeJSON(
        `${API_ROOT}/settings/system`,
        'PATCH',
        createSystemSettingsPayload(input),
        200
      )
    )
  },

  async getExternalAccessStatus() {
    return parseExternalAccessStatusResponse(
      await get(externalAccessContract.getStatus.path)
    )
  },

  async refreshExternalAccess() {
    const contract = externalAccessContract.refresh
    return parseExternalAccessStatusResponse(
      await writeJSON(
        contract.path,
        contract.method,
        {},
        contract.successStatus
      )
    )
  },

  async installCloudflareOriginTLS(
    input: InstallCloudflareOriginTLSInput
  ): Promise<CloudflareOriginTLSStatus> {
    const contract = externalAccessContract.installOriginTLS
    return parseCloudflareOriginTLSResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createCloudflareOriginTLSPayload(input),
        contract.successStatus
      )
    )
  },

  async disableCloudflareOriginTLS(): Promise<CloudflareOriginTLSStatus> {
    const contract = externalAccessContract.disableOriginTLS
    return parseCloudflareOriginTLSResponse(
      await writeJSON(contract.path, contract.method, {}, contract.successStatus)
    )
  },

  async getIOSPairing(): Promise<IOSPairingResult> {
    return parseIOSPairingResponse(await get(iosPairingContract.get.path))
  },

  async createIOSPairing(serverURL?: string): Promise<IOSPairingResult> {
    const contract = iosPairingContract.create
    return parseIOSPairingResponse(
      await writeJSON(
        contract.path,
        contract.method,
        serverURL ? { server_url: serverURL } : {},
        contract.successStatus
      )
    )
  },

  async revokeIOSPairing(credentialID: string): Promise<void> {
    const contract = iosPairingContract.revoke
    const path = `${contract.path}?credential_id=${encodeURIComponent(credentialID)}`
    await request(
      path,
      {
        method: contract.method,
        headers: { Accept: 'application/json' }
      },
      contract.successStatus
    )
  },

  async sendIOSTestCall(credentialID?: string): Promise<IOSTestCallResult> {
    const contract = iosPairingContract.testCall
    const path = credentialID
      ? `${contract.path}?credential_id=${encodeURIComponent(credentialID)}`
      : contract.path
    return parseIOSTestCallResponse(
      await writeJSON(
        path,
        contract.method,
        {},
        contract.successStatus
      )
    )
  },

  async getTLSSettings(): Promise<TLSSettings> {
    return parseTLSSettingsResponse(await get(tlsSettingsContract.get.path))
  },

  async updateTLSSettings(input: UpdateTLSSettingsInput): Promise<TLSSettings> {
    const contract = tlsSettingsContract.update
    return parseTLSSettingsResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createTLSSettingsPayload(input),
        contract.successStatus
      )
    )
  },

  async listContacts(query: ListQuery = {}) {
    return parseContacts(
      await conditionalGet(
        `${API_ROOT}/contacts${queryString({
          q: query.q,
          cursor: query.cursor,
          limit: query.limit ? String(query.limit) : undefined
        })}`
      )
    )
  },

  async createContact(input: ContactInput): Promise<Contact> {
    return parseContactResponse(await writeJSON(`${API_ROOT}/contacts`, 'POST', input, 201))
  },

  async updateContact(id: string, input: ContactInput): Promise<Contact> {
    const contactID = encodeURIComponent(id)
    return parseContactResponse(
      await writeJSON(`${API_ROOT}/contacts/${contactID}`, 'PUT', input, 200)
    )
  },

  async deleteContact(id: string, revision?: number): Promise<void> {
    if (!Number.isSafeInteger(revision) || Number(revision) <= 0) {
      throw new ApiError('删除联系人需要有效的 revision', 0, 'invalid_revision', 'revision')
    }
    const contactID = encodeURIComponent(id)
    await request(
      `${API_ROOT}/contacts/${contactID}${queryString({ revision: String(revision) })}`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async deleteContacts(contacts): Promise<void> {
    await writeJSON(
      `${API_ROOT}/contacts/batch`,
      'PATCH',
      {
        action: 'delete',
        contacts: contacts.map(contact => ({
          id: contact.id.trim(),
          revision: contact.revision
        }))
      },
      204
    )
  },

  async listThreads(query: ListQuery = {}) {
    return parseThreads(
      await conditionalGet(
        `${API_ROOT}/messages/threads${queryString({
          q: query.q,
          cursor: query.cursor,
          limit: query.limit ? String(query.limit) : undefined
        })}`
      )
    )
  },

  async listMessages(query) {
    return parseMessages(
      await conditionalGet(
        `${API_ROOT}/messages${queryString({
          line_id: query.line_id,
          peer: query.peer,
          cursor: query.cursor,
          limit: query.limit ? String(query.limit) : undefined
        })}`
      )
    )
  },

  async markThreadRead(query): Promise<void> {
    const contract = communicationContracts.markMessageRead
    await writeJSON(
      contract.path,
      contract.method,
      createMessageReadPayload(query),
      contract.successStatus
    )
  },

  async updateMessageThreads(action, threads): Promise<void> {
    const contract = communicationContracts.updateMessageThreads
    await writeJSON(
      contract.path,
      contract.method,
      {
        action,
        threads: threads.map(createMessageReadPayload)
      },
      contract.successStatus
    )
  },

  async deleteThread(query): Promise<void> {
    const contract = communicationContracts.deleteMessageThread
    await writeJSON(
      contract.path,
      contract.method,
      createMessageReadPayload(query),
      contract.successStatus
    )
  },

  async sendMessage(input: SendMessageInput): Promise<Message> {
    const contract = communicationContracts.sendMessage
    return parseMessageResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createMessagePayload({ ...input, request_id: input.request_id || requestID() }),
        contract.successStatus
      )
    )
  },

  async listCalls(filter: CallFilter = 'all', query: ListQuery = {}) {
    return parseCalls(
      await conditionalGet(
        `${API_ROOT}/calls${queryString({
          kind: filter,
          q: query.q,
          cursor: query.cursor,
          limit: query.limit ? String(query.limit) : undefined
        })}`
      )
    )
  },

  async markMissedCallsRead(): Promise<void> {
    const contract = communicationContracts.markMissedCallsRead
    await writeJSON(contract.path, contract.method, {}, contract.successStatus)
  },

  async markMissedCallRead(id: string): Promise<void> {
    await writeJSON(missedCallReadPath(id), 'PATCH', {}, 204)
  },

  async updateCalls(action, ids): Promise<void> {
    const contract = communicationContracts.updateCalls
    await writeJSON(
      contract.path,
      contract.method,
      {
        action,
        ids: ids.map(id => id.trim())
      },
      contract.successStatus
    )
  },

  async deleteCall(id: string): Promise<void> {
    await request(
      callRecordPath(id),
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async listDevices(): Promise<Device[]> {
    return parseDevices(await get(`${API_ROOT}/devices`))
  },

  async createDevice(input: CreateDeviceInput): Promise<Device> {
    const imei = input.imei.trim()
    if (!imei) throw new Error('IMEI 不能为空')
    return parseDeviceResponse(
      await writeJSON(
        `${API_ROOT}/devices`,
        'POST',
        { imei, name: input.name?.trim() || '' },
        201
      )
    )
  },

  async renameDevice(imei: string, input: RenameDeviceInput): Promise<Device> {
    const normalizedIMEI = imei.trim()
    if (!normalizedIMEI) throw new Error('IMEI 不能为空')
    return parseDeviceResponse(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(normalizedIMEI)}`,
        'PATCH',
        { name: input.name.trim() },
        200
      )
    )
  },

  async deleteDevice(imei: string): Promise<void> {
    const normalizedIMEI = imei.trim()
    if (!normalizedIMEI) throw new Error('IMEI 不能为空')
    await request(
      `${API_ROOT}/devices/${encodeURIComponent(normalizedIMEI)}`,
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async updateLineLabel(lineID: string, input: UpdateLineLabelInput): Promise<LineLabelResult> {
    return parseLineLabelResponse(
      await writeJSON(
        lineLabelPath(lineID),
        'PATCH',
        createLineLabelPayload(input),
        200
      )
    )
  },

  async getNetworkStatus(): Promise<NetworkStatus> {
    return parseNetworkStatusResponse(await get(networkContracts.status.path))
  },

  async getNetworkSelection(lineID: string): Promise<NetworkSelectionPolicy> {
    const contract = networkSelectionContract(lineID).get
    return parseNetworkSelectionResponse(await get(contract.path))
  },

  async updateNetworkSelection(
    lineID: string,
    input: UpdateNetworkSelectionInput
  ): Promise<NetworkSelectionPolicy> {
    const contract = networkSelectionContract(lineID).update
    return parseNetworkSelectionResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createNetworkSelectionPayload(input),
        contract.successStatus
      )
    )
  },

  async scanMobileNetworks(
    lineID: string,
    signal?: AbortSignal
  ): Promise<MobileNetworkScan> {
    const contract = networkSelectionContract(lineID).scan
    return parseMobileNetworkScanResponse(
      await writeJSON(
        contract.path,
        contract.method,
        {},
        contract.successStatus,
        signal,
        NETWORK_SCAN_REQUEST_TIMEOUT_MS
      )
    )
  },

  async listProxies(): Promise<ProxyInstance[]> {
    return parseProxyCollectionResponse(await get(networkContracts.listProxies.path))
  },

  async createProxy(input: CreateProxyInput): Promise<ProxyMutation> {
    const contract = networkContracts.createProxy
    return parseProxyMutationResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createProxyPayload(input),
        contract.successStatus
      )
    )
  },

  async updateProxy(id: string, input: UpdateProxyInput): Promise<ProxyMutation> {
    const contract = proxyResourceContract(id).update
    return parseProxyMutationResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createProxyUpdatePayload(input),
        contract.successStatus
      )
    )
  },

  async deleteProxy(id: string, revision: number): Promise<ProxyDeleteResult> {
    const contract = proxyResourceContract(id).delete
    return parseProxyDeleteResponse(
      await request(
        proxyDeletePath(id, revision),
        {
          method: contract.method,
          headers: { Accept: 'application/json' }
        },
        contract.successStatus
      )
    )
  },

  async getSIMStatus(lineID: string): Promise<SIMStatus> {
    return parseSIMStatusResponse(
      await get(`${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/sim`)
    )
  },

  async commandSIM(lineID: string, input: SIMCommandInput): Promise<CommandReceipt> {
    return parseCommandReceipt(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/sim/commands`,
        'POST',
        { request_id: requestID(), ...input },
        200
      )
    )
  },

  async getSIMAutoUnlock(lineID: string): Promise<SIMAutoUnlock> {
    return parseSIMAutoUnlockResponse(
      await get(`${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/sim/auto-unlock`)
    )
  },

  async saveSIMAutoUnlock(lineID: string, input: SIMAutoUnlockInput): Promise<SIMAutoUnlock> {
    return parseSIMAutoUnlockResponse(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/sim/auto-unlock`,
        'PUT',
        { request_id: requestID(), ...input },
        200
      )
    )
  },

  async listConnectionProfiles(lineID: string): Promise<ConnectionProfile[]> {
    return parseConnectionProfiles(
      await get(`${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/profiles`)
    )
  },

  async saveConnectionProfile(
    lineID: string,
    input: SaveConnectionProfileInput
  ): Promise<ConnectionProfile> {
    return parseSavedConnectionProfile(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/profiles`,
        'PUT',
        { request_id: requestID(), ...input },
        200
      )
    )
  },

  async deleteConnectionProfile(
    lineID: string,
    input: DeleteConnectionProfileInput
  ): Promise<CommandReceipt> {
    return parseCommandReceipt(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/profiles`,
        'DELETE',
        { request_id: requestID(), ...input },
        200
      )
    )
  },

  async getUSSDStatus(lineID: string): Promise<USSDStatus> {
    return parseUSSDStatus(
      await get(`${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/ussd`)
    )
  },

  async commandUSSD(lineID: string, input: USSDCommandInput): Promise<USSDResponse> {
    return parseUSSDResult(
      await writeJSON(
        `${API_ROOT}/devices/${encodeURIComponent(lineID.trim())}/ussd`,
        'POST',
        { request_id: requestID(), ...input },
        200
      )
    )
  },

  async getDiagnostics(): Promise<DiagnosticsSnapshot> {
    return parseDiagnostics(await get(`${API_ROOT}/diagnostics`))
  },

  async getDiagnosticDeviceConfiguration(
    lineID: string
  ): Promise<DeviceConfiguration> {
    const contract = diagnosticDeviceConfigurationContract(lineID).get
    return parseDeviceConfigurationResponse(await get(contract.path))
  },

  async resetDiagnosticUSB(
    lineID: string,
    expectedDeviceRevision: string
  ): Promise<DeviceConfiguration> {
    const contract = diagnosticDeviceConfigurationContract(lineID).resetUSB
    return parseDeviceConfigurationResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createDeviceConfigurationPayload({
          request_id: requestID(),
          operation: 'reset_usb',
          expected_device_revision: expectedDeviceRevision
        }),
        contract.successStatus
      )
    )
  },

  async listDiagnosticLogs(query: DiagnosticLogQuery = {}): Promise<DiagnosticLogPage> {
    return parseDiagnosticLogPage(
      await get(`${API_ROOT}/diagnostics/logs${diagnosticLogQueryString(query)}`)
    )
  },

  subscribeMessageEvents(handlers: MessageEventStreamHandlers): () => void {
    return subscribeEventSource(
      `${API_ROOT}/messages/events`,
      {
        onOpen: () => handlers.onOpen?.(),
        onError: () => undefined
      },
      (source, restart, isActive, markActivity) => {
        source.addEventListener('sms', event => {
          if (!isActive()) return
          markActivity()
          try {
            const message = parseIncomingMessageEvent(JSON.parse(event.data) as unknown)
            handlers.onMessage(message)
          } catch (error) {
            restart(error instanceof Error ? error : new Error('短信事件格式无效'))
          }
        })
        source.addEventListener('heartbeat', event => {
          if (!isActive()) return
          markActivity()
          try {
            const heartbeat = requiredRecord(
              JSON.parse(event.data) as unknown,
              'message_event_heartbeat'
            )
            requiredStringValue(heartbeat, 'message_event_heartbeat', 'at')
          } catch (error) {
            restart(error instanceof Error ? error : new Error('短信事件心跳无效'))
          }
        })
      },
      MESSAGE_EVENT_INACTIVITY_TIMEOUT_MS
    )
  },

  subscribeRuntimeEvents(
    handlers: RuntimeEventStreamHandlers,
    updateOperationID = ''
  ): () => void {
    let pendingState: RuntimeState | undefined
    let stateTimer: ReturnType<typeof globalThis.setTimeout> | undefined
    const clearPendingState = () => {
      if (stateTimer !== undefined) globalThis.clearTimeout(stateTimer)
      stateTimer = undefined
      pendingState = undefined
    }
    const flushPendingState = () => {
      stateTimer = undefined
      const state = pendingState
      pendingState = undefined
      if (state) handlers.onState(state)
    }
    const normalizedUpdateOperationID = updateOperationID.trim()
    const updateQuery = normalizedUpdateOperationID
      ? `?update_operation=${encodeURIComponent(normalizedUpdateOperationID)}`
      : ''
    const closeSource = subscribeEventSource(
      `${API_ROOT}/runtime/events${updateQuery}`,
      {
        onOpen: handlers.onOpen,
        onError: error => {
          clearPendingState()
          handlers.onError(error)
        }
      },
      (source, restart, isActive, markActivity) => {
        source.addEventListener('state', event => {
          if (!isActive()) return
          markActivity()
          try {
            pendingState = mergePendingRuntimeState(
              pendingState,
              parseRuntimeState(JSON.parse(event.data) as unknown)
            )
            if (stateTimer !== undefined) globalThis.clearTimeout(stateTimer)
            stateTimer = globalThis.setTimeout(
              flushPendingState,
              RUNTIME_STATE_COALESCE_MS
            )
          } catch (error) {
            restart(error instanceof Error ? error : new Error('运行时状态格式无效'))
          }
        })
        source.addEventListener('heartbeat', event => {
          if (!isActive()) return
          markActivity()
          try {
            const heartbeat = requiredRecord(
              JSON.parse(event.data) as unknown,
              'runtime_event_heartbeat'
            )
            handlers.onHeartbeat(
              requiredStringValue(heartbeat, 'runtime_event_heartbeat', 'at')
            )
          } catch (error) {
            restart(error instanceof Error ? error : new Error('运行时事件心跳无效'))
          }
        })
        source.addEventListener('update', event => {
          if (!isActive()) return
          markActivity()
          try {
            handlers.onUpdateOperation(
              parseUpdateOperation(JSON.parse(event.data) as unknown)
            )
          } catch (error) {
            restart(error instanceof Error ? error : new Error('更新事件格式无效'))
          }
        })
      },
      RUNTIME_EVENT_INACTIVITY_TIMEOUT_MS
    )
    return () => {
      clearPendingState()
      closeSource()
    }
  },

  subscribeDiagnosticLogs(
    query: DiagnosticLogQuery,
    handlers: DiagnosticLogStreamHandlers
  ): () => void {
    const source = new EventSource(
      `${API_ROOT}/diagnostics/logs/stream${diagnosticLogQueryString(query)}`,
      { withCredentials: true }
    )
    let closed = false
    const close = () => {
      if (closed) return
      closed = true
      source.close()
    }
    source.onopen = () => {
      if (!closed) handlers.onOpen()
    }
    source.addEventListener('log', event => {
      if (closed) return
      try {
        handlers.onEntry(parseDiagnosticLogEntry(JSON.parse(event.data) as unknown))
      } catch (error) {
        close()
        handlers.onError(error instanceof Error ? error : new Error('实时日志格式无效'))
      }
    })
    source.addEventListener('reset', event => {
      if (closed) return
      try {
        const reset = requiredRecord(JSON.parse(event.data) as unknown, 'diagnostic_log_reset')
        handlers.onReset(
          numberValue(reset, 'diagnostic_log_reset', 'oldest_id'),
          numberValue(reset, 'diagnostic_log_reset', 'newest_id')
        )
      } catch (error) {
        close()
        handlers.onError(error instanceof Error ? error : new Error('日志重置事件格式无效'))
      }
    })
    source.onerror = () => {
      if (closed) return
      close()
      handlers.onError()
    }
    return close
  },

  async downloadDiagnosticLogs(query: DiagnosticLogQuery = {}): Promise<Blob> {
    const body = await request(
      `${API_ROOT}/diagnostics/logs/download${diagnosticLogQueryString(query)}`,
      {
        method: 'GET',
        headers: { Accept: 'application/x-ndjson' }
      },
      200
    )
    if (body !== undefined && typeof body !== 'string') {
      throw new ApiError('日志下载响应格式无效', 0, 'invalid_response')
    }
    return new Blob([body || ''], { type: 'application/x-ndjson' })
  },

  async getGlobalCallSettings(): Promise<GlobalCallSettings> {
    return parseGlobalCallSettings(await get(communicationContracts.getCallSettings.path))
  },

  async updateGlobalCallSettings(
    input: UpdateGlobalCallSettingsInput
  ): Promise<GlobalCallSettings> {
    const contract = communicationContracts.updateCallSettings
    return parseGlobalCallSettings(
      await writeJSON(
        contract.path,
        contract.method,
        createGlobalCallSettingsPayload(input),
        contract.successStatus
      )
    )
  },

  async updateLineSettings(input: UpdateLineSettingsInput) {
    return parseLineSettingsResponse(
      await writeJSON(
        `${API_ROOT}/settings/lines`,
        'PATCH',
        createLineSettingsPayload(input),
        200
      )
    )
  },

  async getDeviceConfiguration(lineID: string): Promise<DeviceConfiguration> {
    const contract = deviceConfigurationContract(lineID).get
    return parseDeviceConfigurationResponse(await get(contract.path))
  },

  async updateDeviceConfiguration(
    lineID: string,
    input: UpdateDeviceConfigurationInput
  ): Promise<DeviceConfiguration> {
    const contract = deviceConfigurationContract(lineID).update
    return parseDeviceConfigurationResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createDeviceConfigurationPayload(input),
        contract.successStatus
      )
    )
  },

  async getActiveCallSnapshot(): Promise<ActiveCallSnapshot> {
    return parseActiveCallSnapshotResponse(
      await get(communicationContracts.activeCalls.path)
    )
  },

  async startCall(
    lineKey: string,
    number: string,
    recordingEnabled?: boolean
  ): Promise<CallSession> {
    const contract = communicationContracts.startCall
    return parseCallResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createCallPayload(
          lineKey,
          number,
          requestID(),
          recordingEnabled
        ),
        contract.successStatus
      )
    )
  },

  async callAction(id, action, recordingEnabled): Promise<void> {
    const contract = callActionContract(id, action)
    await writeJSON(
      contract.path,
      contract.method,
      createCallActionPayload(
        action,
        requestID(),
        recordingEnabled
      ),
      contract.successStatus
    )
  },

  async sendDTMF(id: string, digit: string): Promise<void> {
    const contract = callActionContract(id, 'dtmf')
    await writeJSON(
      contract.path,
      contract.method,
      createDTMFPayload(digit, requestID()),
      contract.successStatus
    )
  },

  async renewCallLease(id: string) {
    const contract = callLeaseContract(id)
    return parseCallLeaseStatus(
      await writeJSON(
        contract.path,
        contract.method,
        createCallLeasePayload(),
        contract.successStatus,
        undefined,
        CALL_LEASE_REQUEST_TIMEOUT_MS
      )
    )
  },

  async getCallMediaICEConfiguration(id: string, signal?: AbortSignal) {
    const contract = callMediaICEContract(id)
    return parseCallMediaICEConfiguration(
      await writeJSON(
        contract.path,
        contract.method,
        createCallMediaICEPayload(),
        contract.successStatus,
        signal
      )
    )
  },

  async exchangeCallMedia(
    id: string,
    ownerToken: string,
    offerSDP: string,
    signal?: AbortSignal
  ): Promise<string> {
    const contract = callMediaContract(id)
    return parseCallMediaResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createCallMediaPayload(ownerToken, offerSDP),
        contract.successStatus,
        signal
      )
    )
  },

  async releaseCallMedia(id: string, ownerToken: string): Promise<void> {
    const contract = callMediaReleaseContract(id)
      await writeJSON(
        contract.path,
        contract.method,
        createCallMediaReleasePayload(ownerToken),
        contract.successStatus,
        undefined,
        CALL_LEASE_REQUEST_TIMEOUT_MS
      )
  },

  async getRecordingSettings(): Promise<RecordingSettings> {
    const contract = communicationContracts.getRecordingSettings
    return parseRecordingSettingsResponse(await get(contract.path))
  },

  async listRecordings(query: ListQuery = {}) {
    const contract = communicationContracts.listRecordings
    return parseRecordingEntriesResponse(
      await conditionalGet(
        `${contract.path}${queryString({
          q: query.q,
          cursor: query.cursor,
          limit: query.limit ? String(query.limit) : undefined
        })}`
      )
    )
  },

  async updateRecordingSettings(settings: RecordingSettings): Promise<RecordingSettings> {
    const contract = communicationContracts.updateRecordingSettings
    return parseRecordingSettingsResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createRecordingSettingsPayload(settings),
        contract.successStatus
      )
    )
  },

  async setCallRecording(id: string, enabled: boolean): Promise<CallRecordingState> {
    const contract = callRecordingContract(id).update
    return parseCallRecordingState(
      await writeJSON(
        contract.path,
        contract.method,
        createCallRecordingPayload(enabled),
        contract.successStatus
      )
    )
  },

  async getCallRecording(id: string) {
    const contract = callRecordingContract(id).list
    return parseCallRecordingSnapshotResponse(await get(contract.path))
  },

  async listCallRecordings(id: string): Promise<CallRecordingSegment[]> {
    const contract = callRecordingContract(id).list
    return parseCallRecordingsResponse(await get(contract.path))
  },

  async deleteRecording(callID: string, recordingID: string): Promise<void> {
    await request(
      callRecordingResourcePath(callID, recordingID),
      {
        method: 'DELETE',
        headers: { Accept: 'application/json' }
      },
      204
    )
  },

  async updateRecordings(action, recordings): Promise<void> {
    const contract = communicationContracts.updateRecordings
    await writeJSON(
      contract.path,
      contract.method,
      {
        action,
        recordings: recordings.map(recording => ({
          call_id: recording.call_id.trim(),
          id: recording.id.trim()
        }))
      },
      contract.successStatus
    )
  },

  async listTelegramUnits(): Promise<TelegramUnit[]> {
    return parseTelegramUnitsResponse(await get(communicationContracts.listTelegram.path))
  },

  async createTelegramUnit(input: TelegramUnitInput): Promise<TelegramUnit> {
    const contract = communicationContracts.createTelegram
    return parseTelegramUnitResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createTelegramUnitPayload(input),
        contract.successStatus
      )
    )
  },

  async updateTelegramUnit(id: string, input: TelegramUnitInput): Promise<TelegramUnit> {
    const contract = telegramUnitContract(id).update
    return parseTelegramUnitResponse(
      await writeJSON(
        contract.path,
        contract.method,
        createTelegramUnitPayload(input),
        contract.successStatus
      )
    )
  },

  async deleteTelegramUnit(id: string, revision: number): Promise<void> {
    const contract = telegramUnitContract(id).delete
    await request(
      telegramUnitDeletePath(id, revision),
      {
        method: contract.method,
        headers: { Accept: 'application/json' }
      },
      contract.successStatus
    )
  }
}

function configureFixture(gateway: ModemDeckGateway): ConfiguredModemDeckGateway {
  const session: SessionResponse = {
    authenticated: true,
    setup_required: false,
    user_id: 'user_admin',
    username: 'fixture',
    role: 'admin',
    ios_pairing_enabled: true,
    allowed_line_ids: [],
    language: 'auto'
  }
  let accountSessions: AccountSession[] = [
    {
      id: 'fixture-current',
      kind: 'web',
      created_at: '2026-08-01T10:00:00Z',
      last_seen_at: '2026-08-03T04:00:00Z',
      user_agent:
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/138.0.0.0 Safari/537.36',
      access_ip: '192.0.2.22',
      access_host: '192.168.50.111:7577',
      current: true
    },
    {
      id: 'fixture-cloudflare',
      kind: 'web',
      created_at: '2026-07-31T02:30:00Z',
      last_seen_at: '2026-08-01T10:03:00Z',
      user_agent:
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 Version/18.5 Mobile/15E148 Safari/604.1',
      access_ip: '203.0.113.42',
      access_host: 'call.b1ank.page',
      current: false
    },
    {
      id: 'ios-pairing',
      kind: 'ios',
      created_at: '2026-07-20T06:15:00Z',
      paired_at: '2026-07-20T06:16:00Z',
      last_seen_at: '2026-08-03T05:10:00Z',
      device: {
        device_name: "Test iPhone",
        device_model: 'iPhone',
        device_model_identifier: 'iPhone18,2',
        os_name: 'iOS',
        os_version: '26.0',
        app_version: '0.1.0',
        app_build: '1'
      },
      current: false,
      paired: true
    }
  ]
  return {
    ...gateway,
    interactions: FIXTURE_INTERACTIONS,
    async getSession() {
      return session
    },
    // The demo is never a clone: it has no data directory and no hardware to
    // compare one against.
    async provisionStatus() {
      return {
        status: 'ready' as const,
        cloned: false,
        reason: '',
        machine_id: 'fixture-machine'
      }
    },
    async requestProvisionReset() {
      throw new Error('演示模式不会重置数据目录。')
    },
    async setup() {
      return session
    },
    async login() {
      return session
    },
    async changePassword() {
      return undefined
    },
    async setAccountContact() {
      return undefined
    },
    async listAccountSessions() {
      return accountSessions.map(item => ({ ...item }))
    },
    async logoutAccountSession(id: string) {
      accountSessions = accountSessions.filter(item => item.id !== id || item.current)
    },
    async logoutOtherAccountSessions() {
      accountSessions = accountSessions.filter(item => item.current)
    },
    async logout() {
      return undefined
    }
  }
}

const fixtureGateway = fixtureMode
  ? (await import('./fixture')).createFixtureGateway(fixturePreviewOptions)
  : undefined

export const gateway: ConfiguredModemDeckGateway = fixtureGateway
  ? configureFixture(fixtureGateway)
  : realGateway

export async function backupContactsToNAS(): Promise<{ filename: string; count: number; created_at: string; directory: string }> {
  if (fixtureMode) throw new Error('当前为演示模式，未连接飞牛服务器，没有生成 NAS 备份。')
  const result = await writeJSON(`${API_ROOT}/contacts/backup`, 'POST', {}, 201) as Record<string, unknown>
  if (typeof result?.filename !== 'string' || typeof result.count !== 'number' || typeof result.created_at !== 'string') throw new Error('备份响应无效，请检查服务器备份目录后再决定是否重试。')
  return {
    filename: result.filename,
    count: result.count,
    created_at: result.created_at,
    directory: typeof result.directory === 'string' ? result.directory : ''
  }
}



export type CallFilterPolicy = {
  revision: number
  mode: 'off' | 'blacklist' | 'whitelist'
  whitelist: string[]
  blacklist: string[]
  min_reports: number
}

export async function getCallFilter(): Promise<{ filter: CallFilterPolicy; state: string; runtime_ready: boolean }> {
  if (fixtureMode) throw new Error('当前为演示模式，来电过滤未连接 NAS。')
  const result = await get(`${API_ROOT}/settings/homesim/call-filter`) as { filter?: CallFilterPolicy; state?: string; runtime_ready?: boolean }
  if (!result?.filter || !Number.isInteger(result.filter.revision)) throw new Error('来电过滤响应无效。')
  return { filter: result.filter, state: result.state || '', runtime_ready: result.runtime_ready === true }
}

export async function saveCallFilter(filter: CallFilterPolicy): Promise<{ filter: CallFilterPolicy; state: string; runtime_ready: boolean }> {
  if (fixtureMode) throw new Error('当前为演示模式，来电过滤未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/call-filter`, 'PUT', filter, 200) as { filter?: CallFilterPolicy; state?: string; runtime_ready?: boolean }
  if (!result?.filter || !Number.isInteger(result.filter.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return { filter: result.filter, state: result.state || '', runtime_ready: result.runtime_ready === true }
}

export type ForwarderChannelConfig = { webhook_url?: string; secret?: string; credential_ref?: string; nkn_topic?: string; nkn_addresses?: string }

export type ForwarderChannel = {
  id: string
  name: string
  type: 'webhook' | 'dingtalk' | 'feishu' | 'wecom' | 'serverchan' | 'pushplus' | 'nkn'
  enabled: boolean
  events: string[]
  config: ForwarderChannelConfig
}

export type ForwarderSettings = { revision: number; forwarders: ForwarderChannel[] }

export type NKNSettingsResponse = {
  settings: { revision: number; enabled: boolean }
  status: { state: 'connecting' | 'connected' | 'disabled' | 'error'; address?: string; error?: string; updated_at?: string }
  capabilities: { sms_forwarding: boolean; http_tunnel: boolean; voice_transport: boolean }
}
export type WireGuardSettings = { mtproto?: {enabled:boolean; bind_address:string; port:number; secret:string}; revision: number; enabled: boolean; address: string; listen_port: number; advertised_endpoint: string; peers: { name: string; public_key: string; endpoint: string; allowed_ips: string[]; nkn_address: string }[] }
// Live per-peer figures from the gateway's interface. Only the public key
// travels; nothing here can carry a secret.
export type WireGuardPeerStatus = {
  name?: string
  public_key: string
  endpoint?: string
  last_handshake_unix?: number
  rx_bytes: number
  tx_bytes: number
  current: boolean
}
export type WireGuardResponse = { settings: WireGuardSettings; status: { state: string; mtproto_state?: string; public_key?: string; error?: string; revision: number; peers?: WireGuardPeerStatus[] }; runtime_ready: boolean }
export async function getWireGuardSettings(): Promise<WireGuardResponse> {
  if (fixtureMode) {
    return {
      settings: { revision: 1, enabled: true, address: '10.77.0.1/24', listen_port: 51820, advertised_endpoint: '', peers: [{ name: '\u5f02\u5730 NAS', public_key: 'demoPeerPublicKey0000000000000000000000000000=', endpoint: '203.0.113.9:51820', allowed_ips: ['10.77.0.2/32'], nkn_address: '' }] },
      status: {
        state: 'up',
        revision: 1,
        public_key: 'demoGatewayPublicKey00000000000000000000000=',
        peers: [{ name: '\u5f02\u5730 NAS', public_key: 'demoPeerPublicKey0000000000000000000000000000=', endpoint: '203.0.113.9:51820', last_handshake_unix: Math.floor(Date.now() / 1000) - 42, rx_bytes: 5_242_880, tx_bytes: 1_048_576, current: true }]
      },
      runtime_ready: true
    }
  }
  return await get(`${API_ROOT}/settings/homesim/wireguard`) as WireGuardResponse
}
export async function saveWireGuardSettings(settings: WireGuardSettings): Promise<WireGuardResponse> {
  if (fixtureMode) throw new Error('演示模式无法启动 VPN。')
  return await writeJSON(`${API_ROOT}/settings/homesim/wireguard`, 'PUT', settings, 200) as WireGuardResponse
}

export async function createMobileEnrollment(serverURL: string): Promise<{ payload: { expires_at: string; [key: string]: unknown } }> {
  if (fixtureMode) throw new Error('请连接真实服务器后生成配对码。')
  return await writeJSON(`${API_ROOT}/mobile/enrollment`, 'POST', { server_url: serverURL }, 201) as { payload: { expires_at: string; [key: string]: unknown } }
}

export async function getNKNSettings(): Promise<NKNSettingsResponse> {
  if (fixtureMode) return { settings: { revision: 1, enabled: true }, status: { state: 'connecting' }, capabilities: { sms_forwarding: true, http_tunnel: false, voice_transport: false } }
  return await get(`${API_ROOT}/settings/homesim/nkn`) as NKNSettingsResponse
}

export async function saveNKNSettings(settings: NKNSettingsResponse['settings']): Promise<NKNSettingsResponse> {
  if (fixtureMode) throw new Error('演示模式无法修改 NAS 网络配置。')
  return await writeJSON(`${API_ROOT}/settings/homesim/nkn`, 'PUT', settings, 200) as NKNSettingsResponse
}

export async function getForwarderSettings(): Promise<{ settings: ForwarderSettings; max_channels: number; runtime_ready: boolean }> {
  if (fixtureMode) throw new Error('当前为演示模式，转发渠道未连接 NAS。')
  const result = await get(`${API_ROOT}/settings/homesim/forwarders`) as { settings?: ForwarderSettings; max_channels?: number; runtime_ready?: boolean }
  if (!result?.settings || !Array.isArray(result.settings.forwarders) || !Number.isInteger(result.settings.revision)) throw new Error('转发渠道响应无效。')
  return {
    settings: result.settings,
    max_channels: typeof result.max_channels === 'number' ? result.max_channels : 8,
    runtime_ready: result.runtime_ready === true
  }
}

export async function saveForwarderSettings(next: ForwarderSettings): Promise<{ settings: ForwarderSettings; runtime_ready: boolean }> {
  if (fixtureMode) throw new Error('当前为演示模式，转发渠道未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/forwarders`, 'PUT', next, 200) as { settings?: ForwarderSettings; runtime_ready?: boolean }
  if (!result?.settings || !Number.isInteger(result.settings.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return { settings: result.settings, runtime_ready: result.runtime_ready === true }
}

export async function testForwarderChannel(channel: ForwarderChannel): Promise<void> {
  if (fixtureMode) throw new Error('当前为演示模式，无法发送测试消息。')
  await writeJSON(`${API_ROOT}/settings/homesim/forwarders/test`, 'POST', channel, 200)
}

export interface NKNNodeSettings {
  enabled: boolean
  beneficiary_addr: string
  seed_overrides?: string
  revision: number
}

export interface NKNNodeRuntime {
  state: string
  wallet_address?: string
  started_at?: string
  sync_state?: string
  wallet_balance?: string
  relay_count?: number
  error?: string
}

export interface NKNNodeStatus {
  settings: NKNNodeSettings
  runtime: NKNNodeRuntime
  wallet_exists: boolean
  binary_present: boolean
}

function normalizeNKNNodeStatus(result: Partial<NKNNodeStatus> | null): NKNNodeStatus {
  if (!result?.settings || !Number.isInteger(result.settings.revision) || !result.runtime) throw new Error('NKN 节点状态响应无效。')
  return { settings: result.settings, runtime: result.runtime, wallet_exists: result.wallet_exists === true, binary_present: result.binary_present === true }
}

export async function getNKNNodeStatus(): Promise<NKNNodeStatus> {
  if (fixtureMode) throw new Error('当前为演示模式，NKN 节点未连接 NAS。')
  return normalizeNKNNodeStatus(await get(`${API_ROOT}/settings/nkn-node`) as Partial<NKNNodeStatus> | null)
}

export async function getNKNNodeLogs(): Promise<string[]> {
  if (fixtureMode) throw new Error('当前为演示模式，NKN 节点未连接 NAS。')
  const result = await get(`${API_ROOT}/settings/nkn-node/logs`) as { lines?: string[] } | null
  return Array.isArray(result?.lines) ? result!.lines! : []
}

export async function updateNKNNode(next: { enabled: boolean; beneficiary_addr: string; seed_overrides?: string; expected_revision: number }): Promise<NKNNodeStatus> {
  if (fixtureMode) throw new Error('当前为演示模式，NKN 节点未保存到 NAS。')
  return normalizeNKNNodeStatus(await writeJSON(`${API_ROOT}/settings/nkn-node`, 'PUT', next, 200) as Partial<NKNNodeStatus> | null)
}

// Self-custody wallet lifecycle for the embedded NKN node: create with an
// administrator-chosen password, import an existing keystore (wallet.json
// contents), or irreversibly wipe both key files from the NAS.
export async function walletNKNNodeAction(input: { action: 'create' | 'import' | 'delete'; password?: string; keystore?: string; confirm?: boolean }): Promise<NKNNodeStatus> {
  if (fixtureMode) throw new Error('当前为演示模式，NKN 节点未连接 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/nkn-node/wallet`, 'POST', input, 200) as { wallet_address?: string; status?: Partial<NKNNodeStatus> } | null
  return normalizeNKNNodeStatus(result?.status ?? null)
}

// Streams the password-encrypted keystore (wallet.json) for offline backup.
export async function downloadNKNNodeWalletBackup(): Promise<void> {
  const response = await fetch(`${API_ROOT}/settings/nkn-node/wallet/backup`)
  if (!response.ok) throw new Error(response.status === 404 ? 'NAS 上还没有节点钱包。' : `备份下载失败（HTTP ${response.status}）。`)
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = 'nkn-node-wallet.json'
  anchor.click()
  URL.revokeObjectURL(url)
}

export interface NKNNetworkSettings {
  enabled: boolean
  revision: number
}

export interface NKNNetworkRuntime {
  state: string // disabled | joining | active | error
  my_address?: string
  last_renew?: string
  last_error?: string
}

export interface NKNNetworkStatus {
  settings: NKNNetworkSettings
  runtime: NKNNetworkRuntime
  topic: string
}

export interface SystemMetricsInterfaceCounters {
  name: string
  rx_bytes: number
  tx_bytes: number
}

export interface SystemMetricsCellular {
  state: string
  rat: string
  duplex: string
  plmn: string
  band: string
  earfcn: number
  frequency_mhz: number
  uplink_bandwidth_mhz: number
  downlink_bandwidth_mhz: number
  pci: number
  cell_id_hex: string
  tac_hex: string
  rsrp: number
  rsrq: number
  rssi: number
  sinr: number
}

export interface SystemMetricsSnapshot {
  observed_at: string
  load_raw_1: number
  load_raw_5: number
  load_raw_15: number
  load_percent_1: number
  load_percent_5: number
  load_percent_15: number
  cores: number
  memory_total_bytes: number
  memory_available_bytes: number
  memory_used_percent: number
  memory_cached_bytes: number
  memory_buffers_bytes: number
  flash_path: string
  flash_total_bytes: number
  flash_free_bytes: number
  flash_used_percent: number
  flash_fs_type: string
  flash_device_model: string
  flash_device_path: string
  modem_temp_celsius: number | null
  cpu_model_name: string
  cpu_usage_percent: number
  cpu_current_mhz: number
  cpu_max_mhz: number
  cpu_l1_data_kb: number
  cpu_l1_inst_kb: number
  cpu_l2_kb: number
  cpu_l3_kb: number
  cpu_temp_celsius: number | null
  process_count: number
  thread_count: number
  host_uptime_seconds: number
  cellular: SystemMetricsCellular | null
  module_channel?: string
  interfaces: SystemMetricsInterfaceCounters[]
  available: boolean
  error?: string
}

// Preview mode used to throw, which left the dashboard's throughput chart and
// signal meters unrenderable — so they could not be reviewed without a NAS.
// The counters below advance on every poll so the chart draws a real line.
let fixtureMetricsTick = 0

function fixtureSystemMetrics(): SystemMetricsSnapshot {
  fixtureMetricsTick += 1
  const wave = (phase: number, scale: number) =>
    Math.max(0, Math.round((Math.sin((fixtureMetricsTick + phase) / 2.2) + 1.1) * scale))
  return {
    observed_at: new Date().toISOString(),
    load_raw_1: 0.34, load_raw_5: 0.51, load_raw_15: 0.42,
    load_percent_1: 2, load_percent_5: 34, load_percent_15: 27,
    cores: 4,
    memory_total_bytes: 42 * 1024 * 1024, memory_available_bytes: 9.8 * 1024 * 1024,
    memory_used_percent: 77, memory_cached_bytes: 9.5 * 1024 * 1024,
    memory_buffers_bytes: 1.2 * 1024 * 1024,
    memory_spec: '', flash_path: '/', flash_total_bytes: 72.9 * 1024 * 1024,
    flash_free_bytes: 23.6 * 1024 * 1024, flash_used_percent: 68,
    flash_fs_type: 'ext4', flash_device_model: '', flash_device_path: '',
    modem_temp_celsius: 41, cpu_model_name: 'Fixture CPU', cpu_usage_percent: 2,
    cpu_current_mhz: 998, cpu_max_mhz: 1400,
    cpu_l1_data_kb: 32, cpu_l1_inst_kb: 32, cpu_l2_kb: 512, cpu_l3_kb: 0,
    cpu_temp_celsius: 45, process_count: 145, thread_count: 230,
    host_uptime_seconds: 112_800,
    cellular: {
      state: 'registered', rat: 'LTE', duplex: 'FDD', plmn: '46001', band: 'B3',
      earfcn: 1650, frequency_mhz: 1850, uplink_bandwidth_mhz: 20,
      downlink_bandwidth_mhz: 20, pci: 207, cell_id_hex: 'C2FCC1F', tac_hex: 'E624',
      rsrp: -94, rsrq: -11, rssi: -63, sinr: 5
    },
    interfaces: [{
      name: 'enxa272bccda103',
      rx_bytes: 5_400 + fixtureMetricsTick * wave(0, 900),
      tx_bytes: 21_800 + fixtureMetricsTick * wave(3, 400)
    }],
    available: true
  } as SystemMetricsSnapshot
}

export async function getSystemMetrics(): Promise<SystemMetricsSnapshot> {
  if (fixtureMode) return fixtureSystemMetrics()
  const result = await get(`${API_ROOT}/system-metrics`) as Partial<SystemMetricsSnapshot> | null
  if (!result || !Array.isArray(result.interfaces)) throw new Error('系统指标响应无效。')
  return {
    observed_at: typeof result.observed_at === 'string' ? result.observed_at : '',
    load_raw_1: Number(result.load_raw_1 ?? 0),
    load_raw_5: Number(result.load_raw_5 ?? 0),
    load_raw_15: Number(result.load_raw_15 ?? 0),
    load_percent_1: Number(result.load_percent_1 ?? 0),
    load_percent_5: Number(result.load_percent_5 ?? 0),
    load_percent_15: Number(result.load_percent_15 ?? 0),
    cores: Number(result.cores ?? 0),
    memory_total_bytes: Number(result.memory_total_bytes ?? 0),
    memory_available_bytes: Number(result.memory_available_bytes ?? 0),
    memory_used_percent: Number(result.memory_used_percent ?? 0),
    memory_cached_bytes: Number(result.memory_cached_bytes ?? 0),
    memory_buffers_bytes: Number(result.memory_buffers_bytes ?? 0),
    flash_path: typeof result.flash_path === 'string' ? result.flash_path : '',
    flash_total_bytes: Number(result.flash_total_bytes ?? 0),
    flash_free_bytes: Number(result.flash_free_bytes ?? 0),
    flash_used_percent: Number(result.flash_used_percent ?? 0),
    flash_fs_type: typeof result.flash_fs_type === 'string' ? result.flash_fs_type : '',
    flash_device_model: typeof result.flash_device_model === 'string' ? result.flash_device_model : '',
    flash_device_path: typeof result.flash_device_path === 'string' ? result.flash_device_path : '',
    modem_temp_celsius: typeof result.modem_temp_celsius === 'number' ? result.modem_temp_celsius : null,
    cpu_model_name: typeof result.cpu_model_name === 'string' ? result.cpu_model_name : '',
    cpu_usage_percent: Number(result.cpu_usage_percent ?? 0),
    cpu_current_mhz: Number(result.cpu_current_mhz ?? 0),
    cpu_max_mhz: Number(result.cpu_max_mhz ?? 0),
    cpu_l1_data_kb: Number(result.cpu_l1_data_kb ?? 0),
    cpu_l1_inst_kb: Number(result.cpu_l1_inst_kb ?? 0),
    cpu_l2_kb: Number(result.cpu_l2_kb ?? 0),
    cpu_l3_kb: Number(result.cpu_l3_kb ?? 0),
    cpu_temp_celsius: typeof result.cpu_temp_celsius === 'number' ? result.cpu_temp_celsius : null,
    process_count: Number(result.process_count ?? 0),
    thread_count: Number(result.thread_count ?? 0),
    host_uptime_seconds: Number(result.host_uptime_seconds ?? 0),
    cellular: result.cellular && typeof result.cellular === 'object' ? result.cellular as SystemMetricsCellular : null,
    module_channel: typeof result.module_channel === 'string' ? result.module_channel : undefined,
    interfaces: result.interfaces,
    available: result.available === true,
    error: typeof result.error === 'string' ? result.error : undefined
  }
}

export interface VowifiIkeProbe {
  attempted: boolean
  address?: string
  port_500: boolean
  port_4500: boolean
  reachable: boolean
  wait_millis?: number
  error?: string
}

export interface VowifiPathProbe {
  available: boolean
  answer_code?: string
  addresses?: string[]
  dns_servers?: string[]
  interface?: string
  error?: string
  ike: VowifiIkeProbe
}

export interface VowifiEgressProbe {
  available: boolean
  public_ip?: string
  isp?: string
  error?: string
}

export interface VowifiDiagnosticsSnapshot {
  observed_at: string
  plmn?: string
  epdg_fqdn?: string
  operator_name?: string
  data_connected: boolean
  cellular_path: VowifiPathProbe
  broadband_path: VowifiPathProbe
  egress: VowifiEgressProbe
  available: boolean
  error?: string
}

export async function getVowifiDiagnostics(): Promise<VowifiDiagnosticsSnapshot> {
  if (fixtureMode) throw new Error('当前为演示模式，VoWiFi 诊断未连接 NAS。')
  const result = await get(`${API_ROOT}/vowifi-diagnostics`) as Partial<VowifiDiagnosticsSnapshot> | null
  if (!result) throw new Error('VoWiFi 诊断响应无效。')
  const path = (value: unknown): VowifiPathProbe => {
    const source = (value ?? {}) as Partial<VowifiPathProbe>
    const ike = (source.ike ?? {}) as Partial<VowifiIkeProbe>
    return {
      available: source.available === true,
      answer_code: typeof source.answer_code === 'string' ? source.answer_code : undefined,
      addresses: Array.isArray(source.addresses) ? source.addresses : [],
      dns_servers: Array.isArray(source.dns_servers) ? source.dns_servers : [],
      interface: typeof source.interface === 'string' ? source.interface : undefined,
      error: typeof source.error === 'string' ? source.error : undefined,
      ike: {
        attempted: ike.attempted === true,
        address: typeof ike.address === 'string' ? ike.address : undefined,
        port_500: ike.port_500 === true,
        port_4500: ike.port_4500 === true,
        reachable: ike.reachable === true,
        wait_millis: typeof ike.wait_millis === 'number' ? ike.wait_millis : undefined,
        error: typeof ike.error === 'string' ? ike.error : undefined
      }
    }
  }
  const egress = (result.egress ?? {}) as Partial<VowifiEgressProbe>
  return {
    observed_at: typeof result.observed_at === 'string' ? result.observed_at : '',
    plmn: typeof result.plmn === 'string' ? result.plmn : undefined,
    epdg_fqdn: typeof result.epdg_fqdn === 'string' ? result.epdg_fqdn : undefined,
    operator_name: typeof result.operator_name === 'string' ? result.operator_name : undefined,
    data_connected: result.data_connected === true,
    cellular_path: path(result.cellular_path),
    broadband_path: path(result.broadband_path),
    egress: {
      available: egress.available === true,
      public_ip: typeof egress.public_ip === 'string' ? egress.public_ip : undefined,
      isp: typeof egress.isp === 'string' ? egress.isp : undefined,
      error: typeof egress.error === 'string' ? egress.error : undefined
    },
    available: result.available === true,
    error: typeof result.error === 'string' ? result.error : undefined
  }
}

export interface NKNNetworkPeer {
  address: string
  meta?: string
  is_self?: boolean
}

export interface NKNNetworkPeers {
  coverage_started_at?: string
  statistics_scope?: string
  // Each is the subscriber count of one on-chain topic whose subscription
  // duration is the window. null means the chain query failed -- never zero,
  // which is a real answer meaning nobody is out there.
  active_24h: number | null
  active_month: number | null
  active_90d: number | null
  topic: string
  window: string
  count: number
  peers: NKNNetworkPeer[]
  fetched_at: string
}

export async function getNKNNetworkStatus(): Promise<NKNNetworkStatus> {
  if (fixtureMode) {
    return {
      settings: { revision: 1, enabled: true },
      runtime: { state: 'active' },
      topic: 'nassimhub.demo'
    } as NKNNetworkStatus
  }
  const result = await get(`${API_ROOT}/settings/nkn-network`) as Partial<NKNNetworkStatus> | null
  if (!result?.settings || !Number.isInteger(result.settings.revision) || !result.runtime) throw new Error('网络目录状态响应无效。')
  return { settings: result.settings, runtime: result.runtime, topic: typeof result.topic === 'string' ? result.topic : '' }
}

export async function updateNKNNetwork(next: { enabled: boolean; expected_revision: number }): Promise<NKNNetworkStatus> {
  if (fixtureMode) throw new Error('当前为演示模式，网络目录未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/nkn-network`, 'PUT', next, 200) as Partial<NKNNetworkStatus> | null
  if (!result?.settings || !Number.isInteger(result.settings.revision) || !result.runtime) throw new Error('网络目录保存响应无效。')
  return { settings: result.settings, runtime: result.runtime, topic: typeof result.topic === 'string' ? result.topic : '' }
}

export async function getNKNNetworkPeers(): Promise<NKNNetworkPeers> {
  if (fixtureMode) {
    return {
      active_24h: 7, active_month: 23, active_90d: 41,
      topic: 'nassimhub.demo', window: '24h', count: 7, peers: [],
      fetched_at: new Date().toISOString(),
      statistics_scope: 'local_observed_identities'
    }
  }
  const result = await get(`${API_ROOT}/settings/nkn-network/peers`) as Partial<NKNNetworkPeers> | null
  if (!result || !Array.isArray(result.peers)) throw new Error('网络目录列表响应无效。')
  const metric = (value: unknown): number | null => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : null
  return {
    active_24h: metric(result.active_24h),
    coverage_started_at: result.coverage_started_at,
    statistics_scope: result.statistics_scope,
    active_month: metric(result.active_month),
    active_90d: metric(result.active_90d),
    topic: typeof result.topic === 'string' ? result.topic : '',
    window: typeof result.window === 'string' ? result.window : '',
    count: typeof result.count === 'number' ? result.count : result.peers.length,
    peers: result.peers,
    fetched_at: typeof result.fetched_at === 'string' ? result.fetched_at : ''
  }
}

// The DHT is the second control path beside NKN, and like NKN it runs by
// default: a spare path that ships switched off is not a spare. It is confined
// to this project's own protocol prefix and relays nothing for anyone, which is
// what makes running by default defensible.
export interface DHTSettings {
  revision: number
  enabled: boolean
  listen_port: number
  bootstrap_peers: string[]
  // Connection allowlist. Empty accepts any peer speaking this project's own
  // protocol; one entry makes the gate strict.
  authorized_peers: string[]
  // mDNS. On unless switched off: it is the only bootstrap source needing no
  // configuration, so two instances on one network find each other with
  // nobody exchanging an address.
  disable_lan_discovery: boolean
}

export interface DHTStatus {
  state: 'disabled' | 'starting' | 'isolated' | 'ready' | 'error' | 'unavailable'
  peer_id?: string
  addresses?: string[]
  connected_peers: number
  routing_peers: number
  bootstrap_configured: boolean
  lan_discovery: boolean
  lan_peers: number
  error?: string
  updated_at?: string
}

export interface DHTResponse {
  settings: DHTSettings
  status: DHTStatus
}

export async function getDHTSettings(): Promise<DHTResponse> {
  if (fixtureMode) {
    return {
      settings: { revision: 1, enabled: true, listen_port: 4001, bootstrap_peers: [], authorized_peers: [], disable_lan_discovery: false },
      // Demo mode shows a joined node so the card can be reviewed; the real
      // default is off, and a real node with no peers reports "isolated"
      // rather than this.
      status: {
        state: 'ready',
        peer_id: '12D3KooWDemoPeerIdentityForPreviewOnly00000000',
        connected_peers: 4,
        routing_peers: 6,
        bootstrap_configured: true,
        lan_discovery: true,
        lan_peers: 1
      }
    }
  }
  const result = await get(`${API_ROOT}/settings/homesim/dht`) as DHTResponse | null
  if (!result?.settings || !result.status) throw new Error('DHT 状态响应无效。')
  return result
}

export async function saveDHTSettings(settings: DHTSettings): Promise<DHTResponse> {
  if (fixtureMode) throw new Error('当前为演示模式，DHT 配置未保存到 NAS。')
  return await writeJSON(`${API_ROOT}/settings/homesim/dht`, 'PUT', settings, 200) as DHTResponse
}

export interface NKNChatMessage {
  ts: number
  dir: 'in' | 'out'
  peer: string
  text: string
  id?: string
  status?: '' | 'sending' | 'sent' | 'delivered' // 'sending' is client-side only
}

// Preview mode used to throw here, so the chat page was the one screen that
// showed an error instead of itself. It now serves a small in-memory thread set
// so the layout can be reviewed without a NAS.
const fixtureChatPeers = [
  'demo.' + '9f3c'.repeat(16),
  'lab.' + 'a71e'.repeat(16)
]
const fixtureChatMessages: NKNChatMessage[] = fixtureMode
  ? (() => {
      const now = Math.floor(Date.now() / 1000)
      const first = fixtureChatPeers[0] as string
      const second = fixtureChatPeers[1] as string
      return [
        { ts: now - 90_000, dir: 'in', peer: first, text: '在吗？我这边把 NKN 地址换了。', id: 'demo-1', status: 'delivered' },
        { ts: now - 89_400, dir: 'out', peer: first, text: '收到，新地址已经存到通讯录了。', id: 'demo-2', status: 'delivered' },
        { ts: now - 4_800, dir: 'in', peer: first, text: '验证码到了吗？', id: 'demo-3', status: 'delivered' },
        { ts: now - 4_600, dir: 'out', peer: first, text: '到了，682931，五分钟内有效。', id: 'demo-4', status: 'delivered' },
        { ts: now - 4_580, dir: 'out', peer: first, text: '你先用，我这边不动这张卡。', id: 'demo-5', status: 'sent' },
        { ts: now - 1_200, dir: 'in', peer: second, text: 'Gateway is back online after the reboot.', id: 'demo-6', status: 'delivered' }
      ] as NKNChatMessage[]
    })()
  : []

export async function getNKNChatMessages(after = 0): Promise<NKNChatMessage[]> {
  if (fixtureMode) return fixtureChatMessages.filter(item => item.ts > after)
  const result = await get(`${API_ROOT}/nkn/chat/messages?after=${encodeURIComponent(String(after))}`) as { messages?: unknown } | null
  if (!result || !Array.isArray(result.messages)) throw new Error('NKN 聊天记录响应无效。')
  return result.messages.filter((item): item is NKNChatMessage => (
    !!item && typeof item === 'object' &&
    typeof (item as NKNChatMessage).ts === 'number' &&
    typeof (item as NKNChatMessage).text === 'string' &&
    typeof (item as NKNChatMessage).peer === 'string'
  ))
}

export async function sendNKNChatMessage(to: string, text: string): Promise<{ id?: string; ts?: number }> {
  if (fixtureMode) {
    const echo: NKNChatMessage = { ts: Math.floor(Date.now() / 1000), dir: 'out', peer: to, text, id: `demo-${fixtureChatMessages.length + 1}`, status: 'sent' }
    fixtureChatMessages.push(echo)
    return { id: echo.id, ts: echo.ts }
  }
  const result = await writeJSON(`${API_ROOT}/nkn/chat/send`, 'POST', { to, text }, 200) as { ok?: boolean; id?: string; ts?: number } | null
  return { id: result?.id, ts: result?.ts }
}

// A peer note gives an NKN address a readable name: a free-text alias, a link
// to a contact record, or both. Stored on the NAS so every browser sees the
// same names.
export interface NKNChatPeerNote {
  address: string
  alias?: string
  contact_id?: string
  updated_at?: number
}

const fixtureChatPeerNotes: NKNChatPeerNote[] = fixtureMode
  ? [{ address: fixtureChatPeers[0] as string, alias: '老王', updated_at: Math.floor(Date.now() / 1000) }]
  : []

export async function getNKNChatPeerNotes(): Promise<NKNChatPeerNote[]> {
  if (fixtureMode) return fixtureChatPeerNotes.map(item => ({ ...item }))
  const result = await get(`${API_ROOT}/nkn/chat/peers`) as { peers?: unknown } | null
  if (!result || !Array.isArray(result.peers)) return []
  return result.peers.filter((item): item is NKNChatPeerNote => (
    !!item && typeof item === 'object' && typeof (item as NKNChatPeerNote).address === 'string'
  ))
}

export async function saveNKNChatPeerNote(note: NKNChatPeerNote): Promise<void> {
  const payload = { address: note.address, alias: note.alias ?? '', contact_id: note.contact_id ?? '' }
  if (fixtureMode) {
    const index = fixtureChatPeerNotes.findIndex(item => item.address === note.address)
    if (index >= 0) fixtureChatPeerNotes.splice(index, 1)
    if (payload.alias || payload.contact_id) {
      fixtureChatPeerNotes.push({ ...payload, contact_id: payload.contact_id || undefined, updated_at: Math.floor(Date.now() / 1000) })
    }
    return
  }
  await writeJSON(`${API_ROOT}/nkn/chat/peers`, 'POST', payload, 200)
}

export async function clearNKNChatMessages(peer = ''): Promise<void> {
  if (fixtureMode) {
    const kept = fixtureChatMessages.filter(item => peer !== '' && item.peer !== peer)
    fixtureChatMessages.splice(0, fixtureChatMessages.length, ...kept)
    return
  }
  await writeJSON(`${API_ROOT}/nkn/chat/clear`, 'POST', { peer }, 200)
}

/**
 * Force a full QDC507 ADB re-authorization (QADBKEY challenge/response plus a
 * controlled module reboot). The agent runs the whole flow synchronously and
 * it can take 2-3 minutes, so this call uses a timeout far past the default
 * write timeout — never wire it through the shared device-update helpers.
 */
export async function repairQDC507VoiceUSB(lineID: string): Promise<void> {
  if (fixtureMode) throw new Error('当前为演示模式，修复未连接 NAS。')
  await writeJSON(
    `/api/v1/devices/${encodeURIComponent(lineID)}/configuration`,
    'PATCH',
    { operation: 'repair_voice_usb' },
    200,
    undefined,
    200_000
  )
}

export interface NKNNetworkNodeReport {
  address: string
  rtt_ms: number
  connections?: number
}

export interface NKNNetworkNodes {
  nodes: NKNNetworkNodeReport[]
  reachable: number
  probed: number
}

export async function getNKNNetworkNodes(): Promise<NKNNetworkNodes> {
  if (fixtureMode) throw new Error('当前为演示模式，网络目录未连接 NAS。')
  const result = await get(`${API_ROOT}/settings/nkn-network/nodes`) as Partial<NKNNetworkNodes> | null
  if (!result || !Array.isArray(result.nodes)) throw new Error('NKN 节点探测响应无效。')
  const nodes = result.nodes
    .filter(node => typeof node?.address === 'string' && node.address)
    .map(node => ({
      address: node.address,
      rtt_ms: typeof node.rtt_ms === 'number' ? node.rtt_ms : -1,
      connections: typeof node.connections === 'number' && node.connections >= 0 ? node.connections : undefined
    }))
  return {
    nodes,
    reachable: typeof result.reachable === 'number' ? result.reachable : nodes.filter(node => node.rtt_ms >= 0).length,
    probed: typeof result.probed === 'number' ? result.probed : nodes.length
  }
}

export async function getHomeSIMIntegrationDraft(): Promise<HomeSIMIntegrationDraft> {
  if (fixtureMode) return emptyIntegrationDraft()
  const result = await get(`${API_ROOT}/settings/homesim`) as { draft: HomeSIMIntegrationDraft }
  if (!result?.draft?.ai || !result.draft.spam || !result.draft.identity || !Number.isInteger(result.draft.revision)) throw new Error('接入配置响应无效。')
  return result.draft
}

export async function saveHomeSIMIntegrationDraft(draft: HomeSIMIntegrationDraft): Promise<HomeSIMIntegrationDraft> {
  if (fixtureMode) throw new Error('当前为演示模式，接入草稿未保存到 NAS。')
  const problem = integrationDraftProblem(draft)
  if (problem) throw new Error(problem)
  const result = await writeJSON(`${API_ROOT}/settings/homesim`, 'PUT', draft, 200) as { draft: HomeSIMIntegrationDraft }
  if (!result?.draft?.ai || !Number.isInteger(result.draft.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return result.draft
}

export type HomeSIMBarkSettings = {
  revision: number
  server_url: string
  credential_ref: string
  group: string
  sound: string
}

export async function getHomeSIMBarkSettings(): Promise<HomeSIMBarkSettings> {
  if (fixtureMode) {
    return { revision: 1, server_url: 'https://api.day.app', credential_ref: 'HOMESIM_BARK_KEY', group: 'HomeSIM', sound: '' }
  }
  const result = await get(`${API_ROOT}/settings/homesim/bark`) as { settings: HomeSIMBarkSettings }
  if (!result?.settings || !Number.isInteger(result.settings.revision) || typeof result.settings.server_url !== 'string') {
    throw new Error('Bark 设置响应无效。')
  }
  return result.settings
}

export async function saveHomeSIMBarkSettings(settings: HomeSIMBarkSettings): Promise<HomeSIMBarkSettings> {
  if (fixtureMode) throw new Error('当前为演示模式，Bark 设置未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/bark`, 'PUT', settings, 200) as { settings: HomeSIMBarkSettings }
  if (!result?.settings || !Number.isInteger(result.settings.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return result.settings
}

export async function sendHomeSIMBarkTestPush(): Promise<{ sent: boolean; status: number }> {
  if (fixtureMode) throw new Error('当前为演示模式，未连接服务器，没有发送测试推送。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/bark/test`, 'POST', {}, 200) as { sent: boolean; status: number }
  if (!result?.sent) throw new Error('测试推送响应无效，请检查服务器日志。')
  return result
}

export type HomeSIMPhoneGeo = { province: string; city: string; carrier: string }

// Offline region lookup on the server (phone.dat). Returns null when the
// server has no record for the prefix or the request fails; callers fall
// back to the built-in prefix table.
export async function fetchHomeSIMPhoneGeo(number: string): Promise<HomeSIMPhoneGeo | null> {
  if (fixtureMode) return null
  try {
    const result = await get(`${API_ROOT}/homesim/phone-geo?number=${encodeURIComponent(number)}`) as {
      province?: string
      city?: string
      carrier?: string
    }
    if (!result || typeof result.province !== 'string' || !result.province) return null
    return {
      province: result.province,
      city: typeof result.city === 'string' ? result.city : '',
      carrier: typeof result.carrier === 'string' ? result.carrier : ''
    }
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// F50 (ZTE 5G MiFi) — the second supported device type. The gateway proxies
// the stock ZTE web backend; the F50 exposes SMS + traffic + signal but no
// voice calls.

export type HomeSIMF50Settings = {
  revision: number
  base_url: string
  username: string
  /** Write-only: the F50 admin password. Never returned by the API. */
  password?: string
}

export type HomeSIMF50SettingsResult = {
  settings: HomeSIMF50Settings
  passwordSet: boolean
}

export type HomeSIMF50Status = {
  NetworkType: string
  Provider: string
  Signalbar: string
  RSRP: string
  RSRQ: string
  SNR: string
  Bands: string
  LTEBand: string
  NRBand: string
  RealtimeRxThrpt: string
  RealtimeTxThrpt: string
  MonthlyRxBytes: string
  MonthlyTxBytes: string
  TotalRxBytes: string
  TotalTxBytes: string
  BillingDay: string
  ClearDate: string
  DataLimitSize: string
  DataLimitUnit: string
  Temperature: string
  CPUTemp: string
  ICTemp: string
  CPUUtility: string
  MemUtility: string
  WifiStaNum: string
  SMSUnread: string
  FirmwareWA: string
  FirmwareCR: string
}

export type HomeSIMF50Message = {
  ID: string
  Number: string
  Content: string
  Date: string
  Tag: string
}

export async function getHomeSIMF50Settings(): Promise<HomeSIMF50SettingsResult> {
  if (fixtureMode) {
    return { settings: { revision: 1, base_url: 'http://192.168.0.1', username: 'admin' }, passwordSet: true }
  }
  const result = await get(`${API_ROOT}/settings/homesim/f50`) as { settings: HomeSIMF50Settings, password_set?: boolean }
  if (!result?.settings || !Number.isInteger(result.settings.revision) || typeof result.settings.base_url !== 'string') {
    throw new Error('F50 设置响应无效。')
  }
  return { settings: result.settings, passwordSet: !!result.password_set }
}

export async function saveHomeSIMF50Settings(settings: HomeSIMF50Settings): Promise<HomeSIMF50SettingsResult> {
  if (fixtureMode) throw new Error('当前为演示模式，F50 设置未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/f50`, 'PUT', settings, 200) as { settings: HomeSIMF50Settings, password_set?: boolean }
  if (!result?.settings || !Number.isInteger(result.settings.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return { settings: result.settings, passwordSet: !!result.password_set }
}

const f50MockStatus = (): HomeSIMF50Status => ({
  NetworkType: '5G SA',
  Provider: 'CHINA MOBILE',
  Signalbar: '4',
  RSRP: '-87',
  RSRQ: '-10',
  SNR: '13',
  Bands: 'n78',
  LTEBand: 'B3',
  NRBand: 'n78',
  RealtimeRxThrpt: '12.5KB/s',
  RealtimeTxThrpt: '3.2KB/s',
  MonthlyRxBytes: '19964354560',
  MonthlyTxBytes: '2040109466',
  TotalRxBytes: '88096413696',
  TotalTxBytes: '9012345678',
  BillingDay: '1',
  ClearDate: '',
  DataLimitSize: '100',
  DataLimitUnit: 'GB',
  Temperature: '41',
  CPUTemp: '44',
  ICTemp: '43',
  CPUUtility: '23',
  MemUtility: '57',
  WifiStaNum: '2',
  SMSUnread: '1',
  FirmwareWA: 'F50 2.3.2',
  FirmwareCR: ''
})

const f50MockMessages = (): HomeSIMF50Message[] => [
  { ID: '7', Number: '+8613901898834', Content: '【建设银行】您尾号8834的储蓄卡账户完成一笔快捷支付交易，验证码 739264，有效期5分钟。', Date: '26;09;06;14;20;00', Tag: '0' },
  { ID: '6', Number: '+8613801662210', Content: '这个周末回家吃饭吗？', Date: '26;09;06;14;00;00', Tag: '1' }
]

export async function getF50Status(): Promise<HomeSIMF50Status> {
  if (fixtureMode) return f50MockStatus()
  const result = await get(`${API_ROOT}/homesim/f50/status`) as { status: HomeSIMF50Status }
  if (!result?.status || typeof result.status.NetworkType !== 'string') throw new Error('F50 状态响应无效。')
  return result.status
}

export async function getF50SMSList(page = 0): Promise<HomeSIMF50Message[]> {
  if (fixtureMode) return f50MockMessages()
  const result = await get(`${API_ROOT}/homesim/f50/sms?page=${encodeURIComponent(String(page))}`) as { messages: HomeSIMF50Message[] }
  if (!result || !Array.isArray(result.messages)) throw new Error('F50 短信响应无效。')
  return result.messages
}

export async function sendF50SMS(number: string, content: string): Promise<void> {
  if (fixtureMode) throw new Error('当前为演示模式，未连接 F50 设备，没有真实发送短信。')
  await writeJSON(`${API_ROOT}/homesim/f50/sms/send`, 'POST', { number, content }, 200)
}

export type HomeSIMNetworkSwitches = {
  revision: number
  cellular_share: boolean
  home_sim_only: boolean
}

export async function getHomeSIMNetworkSwitches(): Promise<HomeSIMNetworkSwitches> {
  if (fixtureMode) return { revision: 1, cellular_share: false, home_sim_only: false }
  const result = await get(`${API_ROOT}/settings/homesim/network-switches`) as { switches: HomeSIMNetworkSwitches }
  if (!result?.switches || !Number.isInteger(result.switches.revision)) throw new Error('供网开关响应无效。')
  return result.switches
}

export async function saveHomeSIMNetworkSwitches(switches: HomeSIMNetworkSwitches): Promise<HomeSIMNetworkSwitches> {
  if (fixtureMode) throw new Error('当前为演示模式，供网开关未保存到 NAS。')
  const result = await writeJSON(`${API_ROOT}/settings/homesim/network-switches`, 'PUT', switches, 200) as { switches: HomeSIMNetworkSwitches }
  if (!result?.switches || !Number.isInteger(result.switches.revision)) throw new Error('保存响应无效，请重新加载确认结果。')
  return result.switches
}

export async function markF50SMSRead(ids: string[]): Promise<void> {
  if (fixtureMode) return
  await writeJSON(`${API_ROOT}/homesim/f50/sms/read`, 'POST', { ids }, 200)
}
