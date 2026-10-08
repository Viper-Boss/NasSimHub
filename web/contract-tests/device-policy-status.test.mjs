import assert from 'node:assert/strict'
import test from 'node:test'
import { readDevicePolicyStatus, devicePolicyOutcome, readNodeSecurity, readTrustDocument } from '../src/api/nodeSecurity.ts'
import { devicePolicyCopy } from '../src/i18n/devicePolicyCopy.ts'

const applied = { known: true, supported: true, installed: true, enforcing: true, stale: false, damaged: false, matches_gateway: true, freshness: 'fresh' }
test('NAS trust does not prove device enforcement; offline answers discard stale device flags', () => {
  const security = readNodeSecurity({ trust: { known: true, state: 'TRUSTED', outgoing_allowed: true } })
  assert.equal(devicePolicyOutcome(security.trust.device), 'unknown')
  assert.equal(devicePolicyOutcome(readDevicePolicyStatus({ ...applied, known: false })), 'unknown')
  assert.equal(readDevicePolicyStatus({ ...applied, known: false }).enforcing, null)
})
test('only a complete device answer earns the applied state', () => {
  assert.equal(devicePolicyOutcome(readDevicePolicyStatus(applied)), 'enforced')
  for (const key of ['known', 'supported', 'installed', 'enforcing', 'stale', 'damaged', 'matches_gateway']) {
    const input = { ...applied }; delete input[key]
    assert.equal(devicePolicyOutcome(readDevicePolicyStatus(input)), 'unknown', key)
    assert.notEqual(devicePolicyOutcome(readDevicePolicyStatus({ ...applied, [key]: 'true' })), 'enforced', key)
  }
})
test('restart, expiry, damaged policy, missing delivery and monitor are distinct', () => {
  for (const [patch, expected] of [
    [{ supported: false }, 'unsupported'], [{ installed: false }, 'missing'],
    [{ enforcing: false }, 'monitor'], [{ damaged: true }, 'damaged'],
    [{ matches_gateway: false }, 'mismatch'],
    [{ stale: true, freshness: 'stale_restart' }, 'restart'],
    [{ stale: true, freshness: 'stale_expired' }, 'expired'],
    [{ stale: true, freshness: 'new-server-state' }, 'stale']
  ]) assert.equal(devicePolicyOutcome(readDevicePolicyStatus({ ...applied, ...patch })), expected)
  assert.equal(devicePolicyOutcome(readDevicePolicyStatus({ ...applied, installed: false, damaged: true })), 'damaged')
})
test('each subject retains its own evidence; no fallback to the global aggregate', () => {
  const result = readTrustDocument({ evidence: { modem_change: 'observed' }, subjects: [
    { node_id: 'one', state: 'TRUSTED', device: applied, evidence: { modem_change: 'observed', node_local: 'observed' } },
    { node_id: 'two', state: 'TRUSTED', device: { known: true, supported: false }, evidence: { modem_change: 'unsupported' } }
  ] })
  assert.equal(devicePolicyOutcome(result.subjects[0].device), 'enforced')
  assert.equal(devicePolicyOutcome(result.subjects[1].device), 'unsupported')
  assert.equal(result.subjects[1].evidence.modem_change, 'unsupported')
  assert.equal(result.subjects[1].evidence.node_local, 'unknown')
})
test('unreported time and unsafe generations are not manufactured', () => {
  const read = readDevicePolicyStatus({ ...applied, generation: 2 ** 64, checked_at: '0001-01-01T00:00:00Z' })
  assert.equal(read.generation, undefined)
  assert.equal(read.checked_at, '')
})
test('all nine locales have complete device status and evidence text', () => {
  for (const locale of ['zh-CN','zh-TW','en-US','de-DE','es-ES','fr-FR','ja-JP','pt-BR','vi-VN']) {
    const copy = devicePolicyCopy(locale)
    assert.equal(Object.keys(copy.outcomes).length, 10)
    assert.equal(Object.keys(copy.evidence).length, 4)
    for (const text of [copy.title, copy.checked, copy.delivery, ...Object.values(copy.outcomes), ...Object.values(copy.evidence)]) assert.ok(text?.trim(), locale)
    if (locale !== 'en-US') assert.notEqual(copy.title, devicePolicyCopy('en-US').title, locale)
  }
})
