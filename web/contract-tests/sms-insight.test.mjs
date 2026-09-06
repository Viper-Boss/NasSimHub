import assert from 'node:assert/strict'
import test from 'node:test'
import { analyzeSms, collectCodeSuggestions } from '../src/utils/smsInsight.ts'

const thread = (key = 'a') => ({ key, line_id: key, peer: '10000', last_message_id: 'm1', last_content: '【Example】验证码：123456', last_timestamp: '2026-09-05T10:00:00Z' })
const message = (direction = 'incoming', id = 'm1') => ({ id, content: '验证码：123456', timestamp: '2026-09-05T10:00:00Z', direction })

test('Chinese verification code and signature are extracted as text', () => {
  assert.deepEqual(analyzeSms('【Example】验证码：123456，请勿泄露。'), { category: 'code', source: 'Example', code: '123456' })
})
test('English numeric and alphanumeric tokens are supported', () => {
  assert.equal(analyzeSms('Your verification code is A1B2C3.').code, 'A1B2C3')
  assert.equal(analyzeSms('654321 is your security code.').code, '654321')
})
test('ordinary words and embedded code substrings are not tokens', () => {
  for (const text of ['Your code expires soon', 'code below', 'barcode 123456', 'decode 123456', '验证码：请勿泄露']) assert.equal(analyzeSms(text).code, '', text)
})
test('long IDs are not truncated into verification codes', () => {
  for (const text of ['验证码：123456789012', 'code: ABC123456789', '1234567890 is your OTP']) assert.equal(analyzeSms(text).code, '', text)
})
test('delivery pickup numbers are not labelled verification credentials', () => {
  assert.equal(analyzeSms('菜鸟快递取件码 12-3456').category, 'courier')
  assert.equal(analyzeSms('菜鸟快递取件码 12-3456').code, '')
})
test('promotional bank messages remain promotional', () => {
  assert.equal(analyzeSms('【某银行】限时优惠券，回复TD退订').category, 'promo')
  assert.equal(analyzeSms('【某银行】您的银行卡余额变动').category, 'bank')
})
test('summary-only codes are explicitly uncertain previews', () => {
  const values = collectCodeSuggestions([thread()], {})
  assert.equal(values.length, 1)
  assert.equal(values[0].previewOnly, true)
})
test('loaded outgoing codes suppress matching summary suggestions', () => {
  assert.deepEqual(collectCodeSuggestions([thread()], { a: { data: [message('outgoing')] } }), [])
})
test('loaded inbound messages deduplicate their previews', () => {
  const values = collectCodeSuggestions([thread()], { a: { data: [message(), message()] } })
  assert.equal(values.length, 1)
  assert.equal(values[0].previewOnly, false)
})
test('line filtering prevents mixing SIM code lists', () => {
  const values = collectCodeSuggestions([thread('a'), thread('b')], {}, 'b')
  assert.equal(values.length, 1)
  assert.equal(values[0].threadKey, 'b')
})
test('bad timestamps and empty text are ignored', () => {
  assert.deepEqual(collectCodeSuggestions([{ ...thread(), last_timestamp: 'broken' }], {}), [])
  assert.deepEqual(collectCodeSuggestions([{ ...thread(), last_content: '' }], {}), [])
})
test('the list is bounded and newest-first without modifying source arrays', () => {
  const threads = Array.from({ length: 60 }, (_, i) => ({ ...thread(String(i)), last_timestamp: new Date(Date.UTC(2026, 8, 5, 10, i)).toISOString() }))
  const values = collectCodeSuggestions(threads, {})
  assert.equal(values.length, 50)
  assert.equal(values[0].threadKey, '59')
  assert.equal(threads[0].key, '0')
})
