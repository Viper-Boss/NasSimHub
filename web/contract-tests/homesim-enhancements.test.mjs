import assert from 'node:assert/strict'
import test from 'node:test'
import { groupedPhone, messageTime } from '../src/utils/homesimDisplay.ts'
import { simCommandProblem } from '../src/utils/simSafety.ts'
import { smsSignature, signatureGroups, collectCodeSuggestions } from '../src/utils/smsInsight.ts'
import { emptyIntegrationDraft, integrationDraftProblem } from '../src/api/homesim.ts'

test('display formatting preserves destination, service and international numbers', () => {
  assert.equal(groupedPhone('+8613800160010'), '+86 138 0016 0010')
  assert.equal(groupedPhone('13400000000'), '134 0000 0000')
  assert.equal(groupedPhone('95533'), '95533')
  assert.equal(groupedPhone('+12025550101'), '+12025550101')
  assert.equal(messageTime('bad-date'), '—')
  assert.match(messageTime('2026-09-05T15:13:00Z'), /2026/)
})

test('signatures group aliases without matching body mentions or changing threads', () => {
  assert.equal(smsSignature('请联系建设银行'), '')
  assert.equal(smsSignature('【中国建设银行】账户通知'), '建设银行')
  assert.equal(smsSignature('【中信银行信用卡】验证码 123456'), '中信银行')
  const threads = [{key:'a',line_id:'sim1',last_content:'【建设银行】通知',unread_count:1}, {key:'b',line_id:'sim2',last_content:'【建行】通知',unread_count:0,marked_unread:true}]
  assert.deepEqual(signatureGroups(threads), [{source:'建设银行',keys:['a','b'],unread:2}])
  assert.equal(threads[1].line_id, 'sim2')
})

test('full code library excludes unknown previews and retains original message', () => {
  const thread = { key:'t',peer:'1061',line_id:'sim',last_message_id:'last',last_content:'验证码 111111',last_timestamp:'2026-09-05T01:00:00Z' }
  assert.deepEqual(collectCodeSuggestions([thread], {}, '', 1000, false), [])
  const messages = { t: {data:[{id:'m',direction:'incoming',content:'【银行】验证码 123456',timestamp:'2026-09-05T01:00:00Z'}]} }
  const result = collectCodeSuggestions([thread], messages, '', 1000, false)
  assert.equal(result.length, 1)
  assert.equal(result[0].peer, '1061')
  assert.equal(result[0].content, '【银行】验证码 123456')
})

const sim = { present:true, unlock_required:'sim-pin', unlock_retries:{'sim-pin':3,'sim-puk':10} }
test('PIN validation blocks malformed input, absent cards and exhausted retries', () => {
  assert.equal(simCommandProblem(sim,{operation:'send_pin',pin:'1234'}), '')
  assert.ok(simCommandProblem(sim,{operation:'send_pin',pin:'123'}))
  assert.ok(simCommandProblem(null,{operation:'send_pin',pin:'1234'}))
  assert.ok(simCommandProblem({...sim,unlock_retries:{'sim-pin':0}},{operation:'send_pin',pin:'1234'}))
})
test('PUK submission requires a PUK-locked card and repeated new PIN', () => {
  const input = {operation:'send_puk',puk:'12345678',new_pin:'5678'}
  assert.ok(simCommandProblem(sim,input,'5678'))
  assert.ok(simCommandProblem({...sim,unlock_required:'sim-puk'},input,'5679'))
  assert.equal(simCommandProblem({...sim,unlock_required:'sim-puk'},input,'5678'), '')
})
test('PIN protection changes require an unlocked card', () => {
  assert.ok(simCommandProblem(sim,{operation:'enable_pin',pin:'1234',enabled:true}))
  assert.equal(simCommandProblem({...sim,unlock_required:'none'},{operation:'enable_pin',pin:'1234',enabled:true}), '')
})
test('default adapter draft is valid but does not contain runtime enable switches', () => {
  const draft = emptyIntegrationDraft()
  assert.equal(integrationDraftProblem(draft), '')
  assert.equal('enabled' in draft.ai, false)
})
test('adapter configuration rejects embedded credentials and insecure transport', () => {
  for (const endpoint of ['http://example.com','https://name:secret@example.com','wss://example.com?key=secret','wss://example.com#secret']) {
    const draft = emptyIntegrationDraft(); draft.ai.endpoint = endpoint
    assert.ok(integrationDraftProblem(draft), endpoint)
  }
  const draft = emptyIntegrationDraft(); draft.ai.credential_ref = 'sk-secret'
  assert.ok(integrationDraftProblem(draft))
})
test('adapter bounds and black/white conflicts are validated', () => {
  const draft = emptyIntegrationDraft(); draft.spam.blacklist = ['+8613800160010']; draft.spam.whitelist = ['+8613800160010']
  assert.ok(integrationDraftProblem(draft))
  draft.spam.whitelist = []; draft.ai.answer_after_seconds = 0
  assert.ok(integrationDraftProblem(draft))
})
