import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { MAX_PREFLIGHT_BYTES, parseNasPreflight, preflightSummary } from '../src/utils/nasPreflight.ts'

function report() {
  const real = ['sms_receive', 'sms_send', 'call_in', 'call_out', 'two_way_audio', 'recording']
  return {
    schema: 'nas-comm-preflight/v1', source: 'host-read-only',
    generated_at: '2026-09-04T00:00:00+00:00',
    host: { system: 'Linux', architecture: 'arm64' },
    devices: [{ label: 'Module 1', usb_id: '2ca3:4006', family: 'QDC507 candidate', drivers: ['option'] }],
    checks: [...['linux', 'architecture', 'usb', 'serial', 'qmi', 'audio', 'docker', 'compose', 'data', 'recordings'], ...real].map(id => ({ id, title: id, detail: 'test observation', status: real.includes(id) ? 'not_tested' : 'pass' }))
  }
}
const parse = input => parseNasPreflight(JSON.stringify(input))

test('valid host report does not claim functional readiness', () => {
  const parsed = parse(report())
  assert.equal(parsed.devices.length, 1)
  assert.match(preflightSummary(parsed), /summary\.imported/)
})
test('malformed JSON is actionable', () => assert.throws(() => parseNasPreflight('{'), /errors\.json/))
test('oversized report is rejected before parsing', () => assert.throws(() => parseNasPreflight(' '.repeat(MAX_PREFLIGHT_BYTES + 1)), /errors\.size/))
test('unknown report version is rejected', () => {
  const input = report(); input.schema = 'v99'
  assert.throws(() => parse(input), /errors\.version/)
})
test('unrelated private fields are discarded', () => {
  const input = report(); input.secret = 'private'; input.host.ip = 'private'; input.devices[0].serial = 'private'
  assert.doesNotMatch(JSON.stringify(parse(input)), /private/)
})
test('duplicate or missing checks are rejected', () => {
  const input = report(); input.checks.push(input.checks[0])
  assert.throws(() => parse(input), /errors\.check/)
  input.checks = []
  assert.throws(() => parse(input), /errors\.missing/)
})
test('read-only report cannot assert carrier call success', () => {
  const input = report(); input.checks.find(check => check.id === 'two_way_audio').status = 'pass'
  assert.throws(() => parse(input), /errors\.functional/)
})
test('invalid statuses and USB IDs are rejected', () => {
  const input = report(); input.checks[0].status = 'success'
  assert.throws(() => parse(input), /errors\.check/)
  input.checks[0].status = 'pass'; input.devices[0].usb_id = 'javascript:alert(1)'
  assert.throws(() => parse(input))
})
test('summary prioritizes blocking checks over warnings', () => {
  const input = report(); input.checks[0].status = 'fail'; input.checks[1].status = 'warn'
  assert.match(preflightSummary(parse(input)), /summary\.blocked/)
})
test('dates and array limits are checked', () => {
  const input = report(); input.generated_at = 'invalid'
  assert.throws(() => parse(input), /errors\.date/)
  input.generated_at = '2026-09-04T00:00:00Z'; input.devices = Array(17).fill(input.devices[0])
  assert.throws(() => parse(input), /errors\.count/)
})
test('NAS panel uses escaped local rendering without uploads or persistence', async () => {
  const panel = await readFile(new URL('../src/components/NasPreflightPanel.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(panel, /v-html|fetch\(|localStorage|sessionStorage|gateway\./)
  assert.match(panel, /file\.size > MAX_PREFLIGHT_BYTES/)
  assert.match(panel, /current === generation/)
  assert.match(panel, /role="alert"/)
  assert.match(panel, /nasPreflight\.privacy/)
})
test('new panel extends rather than replaces authenticated diagnostics', async () => {
  const settings = await readFile(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
  assert.match(settings, /<NasPreflightPanel\s*\/>/)
  assert.match(settings, /<DiagnosticsPanel\s*\/>/)
})
