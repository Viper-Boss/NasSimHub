import { API_ROOT, fixtureMode, get, writeJSON } from './client'

export interface ForwardSecret {
  env?: string
  value?: string
  set?: boolean
}
export interface ForwardChannel {
  id: string
  name: string
  type: string
  enabled: boolean
  params: Record<string, string>
  secrets: Record<string, ForwardSecret>
}
export interface ForwardCondition {
  op: string
  field?: string
  value?: string
  children?: ForwardCondition[]
}
export interface ForwardQuiet {
  enabled: boolean
  start: string
  end: string
  timezone: string
  days: number[]
}
export interface ForwardRule {
  id: string
  name: string
  enabled: boolean
  events: string[]
  line_ids: string[]
  condition: ForwardCondition
  channel_ids: string[]
  logic: string
  template_id: string
  stop: boolean
  quiet: ForwardQuiet
}
export interface ForwardTemplate {
  id: string
  name: string
  title: string
  body: string
}
export interface ForwardConfig {
  revision: number
  owner_id?: string
  enabled: boolean
  channels: ForwardChannel[]
  rules: ForwardRule[]
  templates: ForwardTemplate[]
}
export interface ForwardKind {
  type: string
  params: string[]
  secrets: string[]
}
export interface ForwardDelivery {
  id: number
  event_id: string
  line_id: string
  rule_id: string
  channel_id: string
  status: string
  attempts: number
  error_class: string
  next_at: number
}
export const forwardKinds: ForwardKind[] = [
  { type: 'dingtalk', params: [], secrets: ['url', 'signing_key'] },
  {
    type: 'dingtalk_app',
    params: ['agent_id', 'to'],
    secrets: ['app_key', 'app_secret']
  },
  {
    type: 'email',
    params: ['host', 'port', 'username', 'from', 'to', 'tls'],
    secrets: ['password']
  },
  {
    type: 'bark',
    params: ['server', 'group', 'sound', 'encryption'],
    secrets: ['key', 'encryption_key']
  },
  { type: 'webhook', params: [], secrets: ['url', 'headers'] },
  { type: 'wecom', params: [], secrets: ['url'] },
  {
    type: 'wecom_app',
    params: ['agent_id', 'to'],
    secrets: ['corp_id', 'corp_secret']
  },
  { type: 'serverchan', params: [], secrets: ['key'] },
  { type: 'telegram', params: ['chat_id'], secrets: ['token'] },
  { type: 'sms', params: ['line_id', 'to'], secrets: [] },
  { type: 'feishu', params: [], secrets: ['url', 'signing_key'] },
  {
    type: 'feishu_app',
    params: ['to', 'receive_id_type'],
    secrets: ['app_id', 'app_secret']
  },
  { type: 'pushplus', params: ['topic'], secrets: ['token'] },
  { type: 'gotify', params: ['server', 'priority'], secrets: ['token'] },
  {
    type: 'socket',
    params: [
      'protocol',
      'host',
      'port',
      'tls',
      'topic',
      'client_id',
      'username'
    ],
    secrets: ['password']
  }
]
export function defaultForwardConfig(): ForwardConfig {
  return {
    revision: 1,
    enabled: false,
    channels: [],
    rules: [],
    templates: [
      {
        id: 'default',
        name: 'Default',
        title: 'NasSimHub · {{.Event}} · {{.Peer}}',
        body: '{{.Body}}\n{{.LineID}} · {{.Time}}'
      }
    ]
  }
}
function parseConfig(value: unknown): ForwardConfig {
  const c = value as ForwardConfig | undefined
  if (
    !c ||
    !Number.isSafeInteger(c.revision) ||
    c.revision < 1 ||
    typeof c.enabled !== 'boolean' ||
    !Array.isArray(c.channels) ||
    !Array.isArray(c.rules) ||
    !Array.isArray(c.templates)
  )
    throw new Error('Invalid forwarding configuration response.')
  for (const ch of c.channels) {
    if (!ch || typeof ch.id !== 'string' || !ch.params || !ch.secrets)
      throw new Error('Invalid forwarding channel response.')
  }
  return c
}
const root = `${API_ROOT}/settings/forwarding`
function requireLive(): void {
  if (fixtureMode)
    throw new Error(
      'Demo mode: no settings were saved and no notification was sent.'
    )
}
export async function getForwarding(): Promise<{
  config: ForwardConfig
  catalog: ForwardKind[]
}> {
  if (fixtureMode) {
    const c = defaultForwardConfig()
    c.channels.push({
      id: 'demo',
      name: 'Demo webhook',
      type: 'webhook',
      enabled: false,
      params: {},
      secrets: { url: { env: 'FORWARDING_WEBHOOK_URL' } }
    })
    c.rules.push({
      id: 'demo_rule',
      name: 'Demo SMS',
      enabled: true,
      events: ['incoming_sms'],
      line_ids: [],
      condition: { op: 'always' },
      channel_ids: ['demo'],
      logic: 'all',
      template_id: 'default',
      stop: false,
      quiet: {
        enabled: false,
        start: '22:00',
        end: '08:00',
        timezone: 'Asia/Shanghai',
        days: []
      }
    })
    return { config: c, catalog: structuredClone(forwardKinds) }
  }
  const result = (await get(root)) as {
    config?: unknown
    catalog?: ForwardKind[]
  }
  if (!Array.isArray(result.catalog))
    throw new Error('Invalid forwarding channel catalog.')
  return { config: parseConfig(result.config), catalog: result.catalog }
}
export async function saveForwarding(
  config: ForwardConfig
): Promise<ForwardConfig> {
  requireLive()
  const result = (await writeJSON(root, 'PUT', config, 200)) as {
    config: unknown
  }
  return parseConfig(result.config)
}
export async function migrateForwarding(
  revision: number
): Promise<ForwardConfig> {
  requireLive()
  const result = (await writeJSON(
    `${root}/migrate`,
    'POST',
    { revision },
    200
  )) as { config: unknown }
  return parseConfig(result.config)
}
export async function testForwarding(channelID: string): Promise<void> {
  requireLive()
  await writeJSON(`${root}/test`, 'POST', { channel_id: channelID }, 200)
}
export async function retryForwarding(id: number): Promise<void> {
  requireLive()
  await writeJSON(`${root}/retry`, 'POST', { id }, 200)
}
export async function forwardDeliveries(
  before = 0
): Promise<{ deliveries: ForwardDelivery[]; next_before: number }> {
  if (fixtureMode) return { deliveries: [], next_before: 0 }
  const result = (await get(
    `${root}/deliveries${before ? `?before=${before}` : ''}`
  )) as { deliveries: ForwardDelivery[]; next_before: number }
  if (!Array.isArray(result.deliveries))
    throw new Error('Invalid forwarding deliveries response.')
  return result
}
export async function previewForwarding(
  config: ForwardConfig,
  ruleID: string,
  body: string
): Promise<{ matched: boolean; rendered: { title: string; body: string } }> {
  const event = {
    type: 'incoming_sms',
    line_id: 'synthetic-line',
    peer: '10086',
    body,
    occurred_at: new Date().toISOString(),
    resource_id: 'preview'
  }
  if (fixtureMode)
    throw new Error(
      'Demo mode: the NAS rule engine was not called; preview is not evaluated.'
    )
  return (await writeJSON(
    `${root}/preview`,
    'POST',
    { config, rule_id: ruleID, event },
    200
  )) as { matched: boolean; rendered: { title: string; body: string } }
}
