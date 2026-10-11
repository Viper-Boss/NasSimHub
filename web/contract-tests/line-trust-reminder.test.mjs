import test from 'node:test'
import assert from 'node:assert/strict'
import { lineTrustReminder } from '../src/i18n/lineTrustCopy.ts'
import { readTrustDocument } from '../src/api/nodeSecurity.ts'
test('observation, restrictions and quarantine have actionable copy',()=>{
 for(const state of ['OBSERVATION','RESTRICTED','QUARANTINE']) {
  const n={state,enforced:true,outgoing_allowed:false,remaining_seconds:3600}
  assert.match(lineTrustReminder(n,'zh-CN'),/安全中心/)
  assert.match(lineTrustReminder(n,'en'),/Security centre/)
  assert.equal(lineTrustReminder({...n,enforced:false},'zh-CN'),'')
  assert.equal(lineTrustReminder({...n,outgoing_allowed:true},'zh-CN'),'')
 }
 assert.equal(lineTrustReminder(undefined,'zh-CN'),'')
 assert.equal(lineTrustReminder({state:'TRUSTED',enforced:true,outgoing_allowed:false,remaining_seconds:0},'zh-CN'),'')
 assert.match(lineTrustReminder({state:'OBSERVATION',enforced:true,outgoing_allowed:false,remaining_seconds:3601},'zh-CN'),/约剩 2 小时/)
})
test('issuer approval permission is never guessed',()=>{
 assert.equal(readTrustDocument({}).issuer_admin_allowed,false)
 assert.equal(readTrustDocument({issuer_admin_allowed:'true'}).issuer_admin_allowed,false)
 assert.equal(readTrustDocument({issuer_admin_allowed:true}).issuer_admin_allowed,true)
})
