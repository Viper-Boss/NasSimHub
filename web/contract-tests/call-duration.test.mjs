import assert from 'node:assert/strict'
import test from 'node:test'
import { formatCallDuration, cumulativeDurationLabel } from '../src/utils/callDuration.ts'
const units = { hour: '时', minute: '分', second: '秒' }
test('call duration preserves seconds and aggregate units', () => {
 assert.equal(formatCallDuration(32, units), '32秒')
 assert.equal(formatCallDuration(1936, units), '32分16秒')
 assert.equal(formatCallDuration(3662, units), '1时1分2秒')
 assert.equal(formatCallDuration(0, units), '0秒')
 assert.equal(formatCallDuration(undefined, units), '')
 assert.equal(formatCallDuration(-1, units), '')
 assert.equal(cumulativeDurationLabel('zh-CN'), '累计时长')
})
