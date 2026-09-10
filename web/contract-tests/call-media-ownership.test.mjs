import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const callMedia = await readFile(
  new URL('../src/state/callMedia.ts', import.meta.url),
  'utf8'
)
const session = await readFile(
  new URL('../src/state/session.ts', import.meta.url),
  'utf8'
)
const client = await readFile(
  new URL('../src/api/client.ts', import.meta.url),
  'utf8'
)

test('one tab exclusively owns browser call media', () => {
  assert.match(callMedia, /const MEDIA_LOCK_PREFIX = 'modemdeck-call-media:'/)
  assert.match(
    callMedia,
    /navigator\.locks\.request\([\s\S]*?\{ mode: 'exclusive', signal: controller\.signal \}/
  )
  assert.match(callMedia, /globalThis\.crypto\.randomUUID\(\)/)
  assert.match(
    callMedia,
    /gateway\.exchangeCallMedia\([\s\S]*?ownership\.ownerToken[\s\S]*?offerSDP/
  )
  assert.match(
    callMedia,
    /gateway[\s\S]*?\.releaseCallMedia\(callID, ownership\.ownerToken\)/
  )
})

test('browser media renegotiates a broken transport instead of waiting out one window', () => {
  assert.match(callMedia, /const MEDIA_RECOVERY_TIMEOUT_MS = 15_000/)
  // A broken transport is no longer a dead end. Both a transient disconnection
  // and a failed candidate pair schedule a bounded renegotiation that keeps the
  // owner token the browser already holds; the fixed window only stays as the
  // fallback for when no renegotiation is possible at all.
  assert.match(
    callMedia,
    /if \(state === 'disconnected' \|\| state === 'failed'\) \{[\s\S]*?callMediaState\.status = 'recovering'/
  )
  assert.match(callMedia, /const MEDIA_MAX_CONSECUTIVE_RESTARTS = 3/)
  assert.match(callMedia, /const MEDIA_MAX_TOTAL_RESTARTS = 12/)
  assert.match(
    callMedia,
    /if \(iceConfiguration && microphonePipeline\) \{[\s\S]*?scheduleRestart\([\s\S]*?\} else \{\s*beginRecoveryWindow\(callID, token\)/
  )
  assert.match(
    callMedia,
    /async function attemptRestart\([\s\S]*?consecutiveRestarts >= MEDIA_MAX_CONSECUTIVE_RESTARTS[\s\S]*?failConnection\(/
  )
  assert.match(callMedia, /function releasePeer\(\): void \{[\s\S]*?connection\.close\(\)/)
  assert.match(
    callMedia,
    /gateway\.exchangeCallMedia\([\s\S]*?ownership\.ownerToken[\s\S]*?offerSDP/
  )
  assert.match(
    callMedia,
    /if \(generation !== token \|\| currentCallID !== callID \|\| peer !== connection\) return/
  )
  assert.match(callMedia, /state === 'connected'[\s\S]*?clearRecoveryWindow\(\)/)
  assert.match(callMedia, /state === 'closed'[\s\S]*?failConnection\(/)
  assert.doesNotMatch(
    callMedia,
    /state === 'disconnected' \|\| state === 'failed'\) \{\s*failConnection\(/
  )
})

test('session changes release media before credentials are revoked', () => {
  assert.match(
    callMedia,
    /releaseCallMediaForSessionEnd[\s\S]*?shutdownCallMedia\(\)[\s\S]*?ownership\?\.claimed[\s\S]*?await ownership\.cleanup/
  )
  assert.match(
    session,
    /async function terminateSession[\s\S]*?await releaseCallMediaForSessionEnd\(\)[\s\S]*?await operation\(\)[\s\S]*?clearSession\(\)/
  )
  assert.match(
    session,
    /logout[\s\S]*?terminateSession\(\(\) => gateway\.logout\(\)\)/
  )
  assert.match(
    session,
    /changePassword[\s\S]*?terminateSession\(\(\) => gateway\.changePassword\(input\)\)/
  )
  assert.doesNotMatch(client, /callLeaseHolderID|rotateCallLeaseHolder|holder_id/)
})
