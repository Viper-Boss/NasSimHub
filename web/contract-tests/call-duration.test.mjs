import assert from 'node:assert/strict'
import test from 'node:test'
import { formatCallDuration, cumulativeDurationLabel, formatRemaining } from '../src/utils/callDuration.ts'
import { formatDuration } from '../src/utils/format.ts'
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

// `duration_seconds` is SECONDS. Each boundary below is one where a formatter
// that mistook the unit, or dropped the smallest one, would print a believable
// wrong answer: 32 seconds as "32 分", 60 seconds as "60 秒", 3599 as an hour.
test('a duration in seconds keeps its unit at every boundary', () => {
 const expected = new Map([
  [0, '0秒'],
  [32, '32秒'],
  [60, '1分0秒'],
  [61, '1分1秒'],
  [3599, '59分59秒'],
  [3600, '1时0分0秒'],
  [86399, '23时59分59秒']
 ])
 for (const [seconds, text] of expected) {
  assert.equal(formatCallDuration(seconds, units), text, `${seconds} seconds`)
 }
 assert.doesNotMatch(formatCallDuration(32, units), /分/, '32 seconds rendered in minutes')
 // Fractions of a second are dropped, not rounded up into the next unit.
 assert.equal(formatCallDuration(59.9, units), '59秒')
 assert.equal(formatCallDuration(Number.NaN, units), '')
 assert.equal(formatCallDuration(Number.POSITIVE_INFINITY, units), '')
})

test('time remaining never names a unit the value does not reach', () => {
 const left = { day: '天', hour: '小时', minute: '分钟', underMinute: '不到1分钟' }
 const expected = new Map([
  [0, '不到1分钟'],
  [32, '不到1分钟'],
  [60, '1分钟'],
  [61, '1分钟'],
  [3599, '59分钟'],
  [3600, '1小时'],
  [86399, '23小时59分钟'],
  [86400, '1天'],
  [90000, '1天1小时'],
  [5 * 86400 + 3600 * 13 + 59, '5天13小时']
 ])
 for (const [seconds, text] of expected) {
  assert.equal(formatRemaining(seconds, left), text, `${seconds} seconds`)
 }
 // 32 seconds is neither "32 minutes" nor "0 hours".
 assert.doesNotMatch(formatRemaining(32, left), /32|0/)
 // Floors, so the larger unit never claims more time than there is.
 assert.equal(formatRemaining(7199, left), '1小时59分钟')
 // Absent or not a duration: empty, so the caller says "unknown" and not zero.
 for (const value of [undefined, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
  assert.equal(formatRemaining(value, left), '')
 }
})

test('call and recording lengths in the lists are seconds, then m:ss, then h:mm:ss', () => {
 // The lists' formatter (call history, recordings, the call timer). Its
 // "seconds" word comes from the locale; the default here is English.
 assert.match(formatDuration(0), /^0 \D+$/)
 assert.match(formatDuration(32), /^32 \D+$/)
 assert.doesNotMatch(formatDuration(32), /min|:/, '32 seconds rendered as minutes')
 assert.equal(formatDuration(60), '1:00')
 assert.equal(formatDuration(61), '1:01')
 assert.equal(formatDuration(3599), '59:59')
 assert.equal(formatDuration(3600), '1:00:00')
 assert.equal(formatDuration(86399), '23:59:59')
 assert.equal(formatDuration(-5), formatDuration(0))
})
