import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { defaultForwardConfig, forwardKinds, getForwarding, saveForwarding, previewForwarding } from '../src/api/forwarding.ts'
import { setClientCSRFToken } from '../src/api/client.ts'
import { visibleSettingsSectionIDs } from '../src/components/settings/settingsNavigation.ts'

test('unified forwarding is admin-only and legacy choices are hidden', () => {
  assert.ok(visibleSettingsSectionIDs({ isAdmin: true, canPairIOS: true }).includes('forwarding'))
  assert.ok(!visibleSettingsSectionIDs({ isAdmin: false, canPairIOS: true }).includes('forwarding'))
  for (const alias of ['bark', 'sms-forward']) assert.ok(!visibleSettingsSectionIDs({ isAdmin: true, canPairIOS: true }).includes(alias))
})
test('fixture channel catalog agrees with the actual Go producer', async () => {
  const source = await readFile(new URL('../../internal/forwarding/model.go', import.meta.url), 'utf8')
  const block = source.split('var Catalog = []ChannelKind{')[1].split('\n}')[0]
  const names = [...block.matchAll(/^\s*\{"([^"]+)"/gm)].map(match => match[1])
  assert.equal(names.length, 15)
  assert.deepEqual(forwardKinds.map(kind => kind.type), names)
  assert.ok(!names.includes('nkn'))
})
test('forwarding reads reject malformed server configuration', async t => {
  const old = globalThis.fetch
  t.after(() => { globalThis.fetch = old })
  globalThis.fetch = async () => new Response(JSON.stringify({ config: { revision: 1, enabled: false, channels: null }, catalog: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  await assert.rejects(getForwarding(), /Invalid forwarding configuration/)
})
test('saving uses the original CSRF client and only the unified resource', async t => {
  const old = globalThis.fetch
  t.after(() => { globalThis.fetch = old; setClientCSRFToken('') })
  setClientCSRFToken('synthetic-csrf')
  let sent
  globalThis.fetch = async (path, init) => {
    sent = { path, init }
    return new Response(JSON.stringify({ config: { ...defaultForwardConfig(), revision: 2 } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }
  const result = await saveForwarding(defaultForwardConfig())
  assert.equal(result.revision, 2)
  assert.equal(sent.path, '/api/v1/settings/forwarding')
  assert.equal(sent.init.method, 'PUT')
  assert.equal(new Headers(sent.init.headers).get('X-ModemDeck-CSRF'), 'synthetic-csrf')
  assert.equal(JSON.parse(sent.init.body).enabled, false)
})
test('preview explicitly posts synthetic input and performs no send request', async t => {
  const old = globalThis.fetch
  t.after(() => { globalThis.fetch = old })
  const calls = []
  globalThis.fetch = async (path, init) => {
    calls.push({ path, body: JSON.parse(init.body) })
    return new Response(JSON.stringify({ matched: false, rendered: { title: 'synthetic', body: 'synthetic' }, sends: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }
  const result = await previewForwarding(defaultForwardConfig(), 'rule', 'synthetic')
  assert.equal(result.matched, false)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].path, '/api/v1/settings/forwarding/preview')
  assert.equal(calls[0].body.event.resource_id, 'preview')
  assert.equal(calls[0].body.event.line_id, 'synthetic-line')
})
