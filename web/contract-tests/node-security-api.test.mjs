import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  NODE_ARTIFACT_LIMIT_BYTES,
  NODE_MANIFEST_LIMIT_BYTES,
  NodeApiError,
  EMPTY_JOURNAL_LISTING,
  JOURNAL_PAGE_SIZE,
  appendJournalPage,
  getJournalPage,
  getNodeUpdates,
  importNodeManifest,
  journalStatus,
  journalAnswerHonoured,
  journalOlderBound,
  journalQuery,
  readJournalHealth,
  readNodeRelease,
  readNodeSecurity,
  readNodeUpdateDevice,
  readTrustDocument,
  reasonBytes,
  reasonProblem,
  resealJournal,
  resetNodePQPin,
  runNodeUpdateAction,
  runTrustAction,
  trustActionsFor,
  trustCounts
} from '../src/api/nodeSecurity.ts'

const source = await readFile(new URL('../src/api/nodeSecurity.ts', import.meta.url), 'utf8')

/** Source with comments stripped, for the "must not appear" assertions. */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

/** Replaces fetch for one test and records every call made through it. */
async function withFetch(handler, run) {
  const original = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), init })
    return handler(String(url), init, calls.length)
  }
  try {
    return await run(calls)
  } finally {
    globalThis.fetch = original
  }
}

function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' }
  })
}

// These readers are where the rule "no evidence means unknown" is enforced for
// everything the nodes view and the security centre draw. Each test names the
// way a missing or unexpected field could otherwise turn into a pass.

test('a node with no security fields reads as unknown everywhere, never as a pass', () => {
  // What an older gateway sends: none of the four members.
  const security = readNodeSecurity({ node_id: 'NSH-410-TEST01' })
  assert.equal(security.identity.outcome, 'unknown')
  assert.equal(security.identity.pin_state, 'unknown', 'an absent pin state became "none", which is a claim')
  assert.equal(security.identity.available, null, 'an absent "available" became a boolean')
  assert.equal(security.identity.enforced, null)
  assert.equal(security.link_security.known, false)
  assert.equal(security.link_security.post_quantum, 'unknown')
  assert.equal(security.link_security.meets_level, null)
  assert.equal(security.identity_refused, false)
  assert.equal(security.trust.known, false)
  assert.equal(security.trust.state, '')
  assert.equal(security.trust.outgoing_allowed, null, 'no trust record was read as "outgoing allowed" or "refused"')
  assert.equal(security.trust.enforced, null)

  // And not an object at all.
  for (const junk of [null, undefined, 'x', 3, []]) {
    assert.equal(readNodeSecurity(junk).identity.outcome, 'unknown')
  }
})

test('words this build does not know are unknown, not the nearest known word', () => {
  const security = readNodeSecurity({
    identity: { outcome: 'triple', pin_state: 'rotated', available: 'yes', enforced: 1 },
    link_security: { known: true, post_quantum: 'probably', meets_level: 'true' },
    identity_refused: 'no',
    trust: { known: true, state: 'VERIFIED', effective_state: 'TRUSTED ', outgoing_allowed: 'yes', enforced: 'enforce' }
  })
  assert.equal(security.identity.outcome, 'unknown')
  assert.equal(security.identity.pin_state, 'unknown')
  assert.equal(security.identity.available, null)
  assert.equal(security.identity.enforced, null)
  assert.equal(security.link_security.post_quantum, 'unknown')
  assert.equal(security.link_security.meets_level, null, 'a non-boolean "meets_level" was coerced')
  assert.equal(security.identity_refused, false)
  assert.equal(security.trust.state, '')
  assert.equal(security.trust.effective_state, '', 'a state with trailing junk was accepted')
  assert.equal(security.trust.outgoing_allowed, null)
  assert.equal(security.trust.enforced, null, 'a non-boolean "enforced" became true, hiding monitor mode')
})

test('an unobserved session cannot fall short and cannot be post-quantum', () => {
  // The gateway sends meets_level:false while nothing has been observed. Shown
  // as a shortfall, every disconnected device would carry a warning.
  const link = readNodeSecurity({
    link_security: { known: false, meets_level: false, post_quantum: 'yes', requested_level: 'PQ' }
  }).link_security
  assert.equal(link.meets_level, null)
  assert.equal(link.post_quantum, 'unknown', 'a session nobody observed was reported as post-quantum')
  assert.equal(link.requested_level, 'PQ', 'the requested level is policy and must survive')

  const observed = readNodeSecurity({
    link_security: { known: true, meets_level: false, post_quantum: 'no', group: 'X25519', required_group: 'X25519MLKEM768' }
  }).link_security
  assert.equal(observed.meets_level, false)
  assert.equal(observed.post_quantum, 'no')
})

test('a trust record that is not "known" carries no state, time or permission', () => {
  const trust = readNodeSecurity({
    trust: { known: false, state: 'TRUSTED', remaining_seconds: 30, outgoing_allowed: true, enforced: false }
  }).trust
  assert.equal(trust.state, '')
  assert.equal(trust.remaining_seconds, undefined)
  assert.equal(trust.outgoing_allowed, null)
  // The mode is the gateway's, not the record's, and is kept.
  assert.equal(trust.enforced, false)
})

test('timestamps: Go zero time and junk are absent, not a date in year 1', () => {
  const identity = readNodeSecurity({
    identity: { pinned_at: '0001-01-01T00:00:00Z', checked_at: 'yesterday' }
  }).identity
  assert.equal(identity.pinned_at, '')
  assert.equal(identity.checked_at, '')
})

test('a release is verified only on the gateway\'s explicit word, and signatures only then', () => {
  const unverified = readNodeRelease({
    release_id: 'r-test-1', gateway_verification: 'unverified', signatures: 'dual_signed', size: 10
  })
  assert.equal(unverified.gateway_verification, 'unverified')
  assert.equal(unverified.signatures, '', 'a signature outcome was shown without the gateway having verified')

  const odd = readNodeRelease({ release_id: 'r-test-2', gateway_verification: 'VERIFIED' })
  assert.equal(odd.gateway_verification, 'unverified', 'an unrecognised word was read as verified')
  const missing = readNodeRelease({ release_id: 'r-test-3' })
  assert.equal(missing.gateway_verification, 'unverified')
  assert.equal(missing.artifact_ready, false)
  assert.equal(missing.size, undefined, 'a missing size became zero')

  const verified = readNodeRelease({
    release_id: 'r-test-4', gateway_verification: 'verified', signatures: 'dual_signed', artifact_ready: true
  })
  assert.equal(verified.signatures, 'dual_signed')
  assert.equal(readNodeRelease({ release_id: 'r-test-5', gateway_verification: 'verified', signatures: 'triple' }).signatures, '')
  assert.equal(readNodeRelease({ version: '1.0' }), null, 'a release with no id was kept')
})

test('a device that did not answer has no status, whatever came with it', () => {
  // The route promises no last-known state. Should one arrive beside
  // reachable:false it must not reach a panel.
  const device = readNodeUpdateDevice({
    node_id: 'NSH-410-TEST01',
    reachable: false,
    supported: true,
    reason: 'timeout',
    status: { state: 'READY', current_version: '1.2.3' },
    read_at: '2026-01-01T00:00:00Z'
  })
  assert.equal(device.reachable, false)
  assert.equal(device.status, null, 'a last-known status survived for an unreachable device')
  assert.equal(device.supported, false)
  assert.equal(device.read_at, '')
  assert.equal(device.reason, 'timeout')

  const reached = readNodeUpdateDevice({
    reachable: true, supported: true,
    status: { state: 'PENDING_CONFIRM', confirm_deadline: '0001-01-01T00:00:00Z', signatures: {}, keys: {} }
  })
  assert.equal(reached.status.state, 'PENDING_CONFIRM')
  assert.equal(reached.status.confirm_deadline, '')
  assert.equal(reached.status.progress_percent, undefined)
  assert.equal(reached.status.signatures.pq_available, null)
})

test('device update status is fetched every time and never kept', async () => {
  await withFetch(
    (_url, _init, n) =>
      json({
        releases: [], gateway_verifies: false, available: true,
        device: { node_id: 'NSH-410-TEST01', reachable: n === 1, supported: true, status: { state: 'IDLE' } }
      }),
    async calls => {
      const first = await getNodeUpdates('NSH-410-TEST01')
      const second = await getNodeUpdates('NSH-410-TEST01')
      assert.equal(calls.length, 2, 'the second read was served without asking the gateway')
      for (const call of calls) {
        assert.equal(call.init.cache, 'no-store')
        assert.equal(call.url, '/api/v1/nodes/updates?device_id=NSH-410-TEST01')
      }
      assert.equal(first.device.status.state, 'IDLE')
      // The device stopped answering between the two reads: nothing of the
      // first answer may leak into the second.
      assert.equal(second.device.reachable, false)
      assert.equal(second.device.status, null)
    }
  )
  // Nothing at module level could hold an answer between calls.
  const body = code(source)
  assert.doesNotMatch(body, /^(?:const|let|var) \w+ = (?:new Map|new Set|reactive|ref)\b/m, 'the module keeps a store')
  // `stale` is now a device-policy status, not a cache. Reject storage and
  // cache-control shortcuts while allowing the API's real freshness fields.
  assert.doesNotMatch(body, /localStorage|sessionStorage|If-None-Match|stale-while-revalidate/i)
})

test('a signed manifest leaves the browser byte for byte', async () => {
  // Deliberately not canonical JSON: odd spacing, a trailing newline, key order
  // a stringify would not reproduce. The signature covers exactly these bytes.
  const text = '{ "signature":"AAAA",\n  "manifest" : {"version":"1.0.0" ,"release_id":"r-test"}\n}\n'
  const file = new Blob([text], { type: 'application/json' })
  await withFetch(
    () => json({ release: { release_id: 'r-test', gateway_verification: 'unverified' } }),
    async calls => {
      const release = await importNodeManifest(file)
      assert.equal(release.release_id, 'r-test')
      const { url, init } = calls[0]
      assert.equal(url, '/api/v1/nodes/updates/manifest')
      assert.equal(init.method, 'POST')
      assert.equal(init.headers['Content-Type'], 'application/json')
      assert.ok('X-ModemDeck-CSRF' in init.headers, 'the import carries no CSRF header')
      assert.ok(init.body instanceof ArrayBuffer, 'the manifest was not sent as raw bytes')
      assert.equal(new TextDecoder().decode(init.body), text, 'the manifest was re-encoded on the way')
    }
  )
  const importer = source.slice(source.indexOf('export async function importNodeManifest'), source.indexOf('export function importNodeArtifact'))
  assert.doesNotMatch(code(importer), /JSON\.(?:parse|stringify)/, 'the manifest passes through JSON')
  assert.equal(NODE_MANIFEST_LIMIT_BYTES, 32 * 1024)
  assert.equal(NODE_ARTIFACT_LIMIT_BYTES, 32 * 1024 * 1024)
})

test('an apply that was not confirmed is an outcome, and other failures carry the gateway\'s words', async () => {
  await withFetch(
    () =>
      json(
        {
          error: { code: 'update_not_confirmed', message: 'The device did not come back on the new version in time.' },
          device: { node_id: 'NSH-410-TEST01', reachable: true, supported: true, status: { state: 'ROLLING_BACK' } }
        },
        409
      ),
    async calls => {
      const result = await runNodeUpdateAction('apply', 'NSH-410-TEST01', 'r-test')
      assert.equal(result.notConfirmed, true)
      assert.equal(result.device.status.state, 'ROLLING_BACK')
      assert.match(result.message, /did not come back/)
      assert.deepEqual(JSON.parse(calls[0].init.body), { device_id: 'NSH-410-TEST01', release_id: 'r-test' })
      assert.equal(calls.length, 1, 'a step that did not confirm was sent again')
    }
  )
  // The flat error shape every other failure uses.
  await withFetch(
    () => json({ code: 'update_busy', message: 'Another update operation is in progress for this device.' }, 409),
    async calls => {
      await assert.rejects(
        () => runNodeUpdateAction('push', 'NSH-410-TEST01', 'r-test'),
        error =>
          error instanceof NodeApiError &&
          error.code === 'update_busy' &&
          error.message === 'Another update operation is in progress for this device.'
      )
      assert.equal(calls.length, 1, 'a refused step was retried')
    }
  )
  // The gateway unreachable: status 0, never a guessed message.
  const original = globalThis.fetch
  globalThis.fetch = async () => { throw new TypeError('network down') }
  try {
    await assert.rejects(
      () => getNodeUpdates('NSH-410-TEST01'),
      error => error instanceof NodeApiError && error.status === 0 && error.code === 'network'
    )
  } finally {
    globalThis.fetch = original
  }
})

test('the pin reset sends the device ID and nothing a request could assert', async () => {
  await withFetch(
    () => json({ node: { node_id: 'NSH-410-TEST01', identity: { pin_state: 'none' } } }),
    async calls => {
      const node = await resetNodePQPin('NSH-410-TEST01')
      assert.equal(node.node_id, 'NSH-410-TEST01')
      assert.equal(calls[0].url, '/api/v1/nodes/pq-pin/reset')
      assert.equal(calls[0].init.method, 'POST')
      assert.ok('X-ModemDeck-CSRF' in calls[0].init.headers)
      // No key, no fingerprint: the gateway records what the device proves.
      assert.deepEqual(JSON.parse(calls[0].init.body), { device_id: 'NSH-410-TEST01' })
    }
  )
})

test('a trust document with nothing in it claims nothing', () => {
  const empty = readTrustDocument({})
  assert.equal(empty.enforcement, 'unknown', 'an unreported mode was read as enforcing')
  assert.deepEqual(empty.subjects, [])
  assert.equal(empty.journal.ok, null)
  assert.equal(journalStatus(empty.journal), 'unknown')
  for (const state of Object.values(empty.evidence)) assert.equal(state, 'unknown')

  const document = readTrustDocument({
    enforcement: 'ENFORCE',
    subjects: [
      { node_id: 'NSH-410-TEST01', state: 'TRUSTED', effective_state: 'RESTRICTED', allowed: { dial: false, update: true, send_sms: 'no' } },
      { state: 'TRUSTED' },
      { node_id: 'NSH-410-TEST02', state: 'CERTIFIED' }
    ],
    evidence: { device_identity: 'observed', modem_change: 'unsupported', location: 'not_used', node_local: 'fine' }
  })
  assert.equal(document.enforcement, 'unknown')
  assert.equal(document.subjects.length, 2, 'a subject with no device ID was kept')
  const [first, second] = document.subjects
  assert.equal(first.allowed.dial, false)
  assert.equal(first.allowed.update, true)
  assert.equal(first.allowed.send_sms, null, 'a non-boolean permission was coerced')
  assert.equal(first.allowed.receive_call, null, 'an unlisted action was read as allowed or refused')
  assert.equal(first.record_verified, null)
  assert.equal(second.state, '', 'an unknown state was kept as a state')
  assert.equal(document.evidence.modem_change, 'unsupported')
  assert.equal(document.evidence.location, 'not_used')
  assert.equal(document.evidence.node_local, 'unknown', 'an unrecognised evidence word was accepted')
  assert.equal(document.evidence.sim_change, 'unknown')
  // Counted by the state decisions use; a subject with no known state is not counted.
  assert.deepEqual(trustCounts(document), { RESTRICTED: 1 })
})

test('journal health: only a dated report is a finding', () => {
  // Before the first verification the gateway reports a zero value: ok:false
  // and Go's zero time. That is "not checked", not "damaged".
  const never = readJournalHealth({ ok: false, events: 0, head_seq: 0, verified_at: '0001-01-01T00:00:00Z' })
  assert.equal(journalStatus(never), 'unknown', 'a journal nobody checked was reported as damaged')

  const ok = readJournalHealth({ ok: true, events: 12, head_seq: 12, verified_at: '2026-01-02T03:04:05Z' })
  assert.equal(journalStatus(ok), 'ok')
  assert.equal(ok.sealed_from, undefined, 'never resealed became "resealed from 0"')
  assert.equal(ok.first_bad_seq, undefined)

  const damaged = readJournalHealth({
    ok: false, events: 4, head_seq: 9, first_bad_seq: 5, sealed_from: 3,
    problem: 'an event is missing from the journal', verified_at: '2026-01-02T03:04:05Z'
  })
  assert.equal(journalStatus(damaged), 'damaged')
  assert.equal(damaged.first_bad_seq, 5)
  assert.equal(damaged.sealed_from, 3)
  assert.equal(journalStatus(readJournalHealth({ ok: 'true', verified_at: '2026-01-02T03:04:05Z' })), 'unknown')
})

test('administrator actions follow the engine\'s own rules', () => {
  const subject = fields => readTrustDocument({ subjects: [{ node_id: 'NSH-410-TEST01', ...fields }] }).subjects[0]
  assert.deepEqual(trustActionsFor(subject({ state: 'TRUSTED' })), ['restrict', 'quarantine'])
  assert.deepEqual(trustActionsFor(subject({ state: 'OBSERVATION' })), ['restrict', 'quarantine'])
  assert.deepEqual(trustActionsFor(subject({ state: 'RESTRICTED' })), ['quarantine', 'restore'])
  assert.deepEqual(trustActionsFor(subject({ state: 'QUARANTINE' })), ['restore'])
  // A damaged record is restored even though its recorded state looks fine.
  assert.deepEqual(trustActionsFor(subject({ state: 'TRUSTED', record_verified: false })), ['restrict', 'quarantine', 'restore'])
  assert.deepEqual(trustActionsFor(subject({ state: 'SOMETHING_NEW' })), [], 'an unknown state offers every action')
})

test('a reason is required, counted in bytes like the gateway counts it', async () => {
  assert.equal(reasonProblem(''), 'required')
  assert.equal(reasonProblem('   \n '), 'required', 'whitespace passed as a reason')
  assert.equal(reasonProblem('swapped the SIM on purpose'), '')
  assert.equal(reasonProblem('a'.repeat(200)), '')
  assert.equal(reasonProblem('a'.repeat(201)), 'too_long')
  // The gateway measures len(reason) in Go, which is bytes: 66 Chinese
  // characters are 198 bytes and pass, 67 are 201 and do not.
  assert.equal(reasonBytes('换'.repeat(66)), 198)
  assert.equal(reasonProblem('换'.repeat(66)), '')
  assert.equal(reasonProblem('换'.repeat(67)), 'too_long')

  // Without a usable reason nothing is sent at all.
  await withFetch(
    () => json({}),
    async calls => {
      for (const action of ['restrict', 'quarantine', 'restore']) {
        await assert.rejects(
          () => runTrustAction(action, 'NSH-410-TEST01', '  '),
          error => error instanceof NodeApiError && error.code === 'reason_required'
        )
      }
      await assert.rejects(
        () => resealJournal('x'.repeat(201)),
        error => error instanceof NodeApiError && error.code === 'reason_too_long'
      )
      assert.equal(calls.length, 0, 'a request without a reason reached the gateway')

      const document = await runTrustAction('restore', 'NSH-410-TEST01', '  checked the device  ')
      assert.equal(document.enforcement, 'unknown')
      assert.equal(calls[0].url, '/api/v1/security/trust/restore')
      assert.deepEqual(JSON.parse(calls[0].init.body), { device_id: 'NSH-410-TEST01', reason: 'checked the device' })
      assert.ok('X-ModemDeck-CSRF' in calls[0].init.headers)
    }
  )
})

test('journal queries ask newest first, page with before=, and carry the device filter', () => {
  // Opening: the newest page. No sequence number is known yet, so none is sent.
  assert.equal(journalQuery(undefined), 'order=desc&limit=50')
  // Older: everything below the oldest entry on screen.
  assert.equal(journalQuery(71), 'order=desc&limit=50&before=71')
  // The device filter is part of the SAME request, for both kinds of page.
  assert.equal(journalQuery(undefined, 50, 'NSH-410-TEST01'), 'order=desc&limit=50&node_id=NSH-410-TEST01')
  assert.equal(journalQuery(71, 50, 'NSH-410-TEST01'), 'order=desc&limit=50&before=71&node_id=NSH-410-TEST01')
  // The route accepts 1..500 and nothing else; a bound of 0 is no bound.
  assert.equal(journalQuery(undefined, 5_000), 'order=desc&limit=500')
  assert.equal(journalQuery(undefined, 0), 'order=desc&limit=1')
  assert.equal(journalQuery(0), 'order=desc&limit=50')
  // The forward scan is gone: nothing asks with after= any more.
  assert.doesNotMatch(code(source), /after=|findJournalHead|journalWindow|JOURNAL_HEAD_ROUNDS/)
})

test('journal reads say what they ask for and drop entries with no sequence', async () => {
  await withFetch(
    () =>
      json({
        events: [
          { seq: 8, kind: 'SOMETHING_NEW' },
          { kind: 'NO_SEQUENCE' },
          { seq: 7, at: '2026-01-02T03:04:05Z', node_id: 'NSH-410-TEST01', kind: 'SIM_CHANGED', cause: 'a different SIM is in the device', source: 'gateway', result: 'risk raised', prev_hash: 'aa', hash: 'bb' }
        ],
        journal: { ok: true, head_seq: 8, verified_at: '2026-01-02T03:04:05Z' }
      }),
    async calls => {
      const page = await getJournalPage(undefined, 50, 'NSH-410-TEST01')
      assert.equal(calls[0].url, '/api/v1/security/journal?order=desc&limit=50&node_id=NSH-410-TEST01')
      assert.equal(calls[0].init.cache, 'no-store')
      assert.equal(page.events.length, 2)
      assert.equal(page.newestFirst, true)
      // A kind this build has never heard of is kept as sent, for the viewer
      // to show raw.
      assert.equal(page.events[0].kind, 'SOMETHING_NEW')
      assert.equal(page.events[0].at, '')
      // A short page is the end of the journal.
      assert.equal(page.more, false)
    }
  )
})

/** A gateway holding entries 1..total for two devices, alternating. */
function journalGateway(total, { legacy = false } = {}) {
  const all = Array.from({ length: total }, (_, index) => ({
    seq: index + 1,
    kind: 'ACTION_REFUSED',
    node_id: index % 2 === 0 ? 'NSH-410-TEST01' : 'NSH-410-TEST02'
  }))
  return url => {
    const query = new URL(url, 'http://gateway.invalid').searchParams
    const limit = Number(query.get('limit') ?? 100)
    const node = query.get('node_id')
    const mine = node ? all.filter(event => event.node_id === node) : all
    if (legacy) {
      // A build older than order=desc / before=: it reads `after` (absent, so
      // 0) and answers its first page oldest first.
      return json({ events: mine.slice(0, limit) })
    }
    const before = Number(query.get('before') ?? 0)
    const older = before > 0 ? mine.filter(event => event.seq < before) : mine
    return json({ events: older.slice(-limit).reverse() })
  }
}

/** Drives the two calls the viewer makes, the way it makes them. */
async function openJournal(nodeID = '') {
  return appendJournalPage(EMPTY_JOURNAL_LISTING, await getJournalPage(undefined, JOURNAL_PAGE_SIZE, nodeID), undefined)
}

async function olderJournal(listing, nodeID = '') {
  const before = journalOlderBound(listing)
  return appendJournalPage(listing, await getJournalPage(before, JOURNAL_PAGE_SIZE, nodeID), before)
}

test('the viewer opens on the newest page and walks back with before=<oldest shown>', async () => {
  await withFetch(journalGateway(120), async calls => {
    let listing = await openJournal()
    assert.match(calls[0].url, /\?order=desc&limit=50$/)
    assert.deepEqual([listing.events[0].seq, listing.events.at(-1).seq], [120, 71])
    assert.equal(listing.more, true)
    assert.equal(listing.newestFirst, true)

    listing = await olderJournal(listing)
    assert.match(calls[1].url, /\?order=desc&limit=50&before=71$/)
    assert.deepEqual([listing.events[50].seq, listing.events.at(-1).seq], [70, 21])
    assert.equal(listing.more, true)

    listing = await olderJournal(listing)
    assert.match(calls[2].url, /before=21$/)
    assert.equal(listing.events.length, 120)
    assert.equal(listing.events.at(-1).seq, 1)
    // Twenty entries in a page of fifty: the journal's beginning.
    assert.equal(listing.more, false)
    // Strictly newest first across the three pages, nothing shown twice.
    for (let index = 1; index < listing.events.length; index += 1) {
      assert.ok(listing.events[index].seq < listing.events[index - 1].seq)
    }
    assert.equal(calls.length, 3, 'a page cost more than one request')
  })
})

test('a full last page does not promise a page below sequence 1', async () => {
  await withFetch(journalGateway(50), async () => {
    const listing = await openJournal()
    assert.equal(listing.events.length, 50)
    assert.equal(listing.more, false)
  })
})

test('the device filter is sent with every page, not applied afterwards', async () => {
  await withFetch(journalGateway(240), async calls => {
    let listing = await openJournal('NSH-410-TEST02')
    listing = await olderJournal(listing, 'NSH-410-TEST02')
    for (const call of calls) assert.match(call.url, /node_id=NSH-410-TEST02/)
    assert.match(calls[1].url, /before=142&node_id=NSH-410-TEST02$/)
    assert.equal(listing.events.length, 100)
    assert.ok(listing.events.every(event => event.node_id === 'NSH-410-TEST02'))
    assert.equal(listing.events[0].seq, 240)
  })
})

test('a gateway that ignores the newest-first query is detected and never paged', async () => {
  // It answers its OLDEST fifty, ascending. Shown newest first, marked, and
  // with no "older" page: `before` would be ignored too and return the same.
  await withFetch(journalGateway(120, { legacy: true }), async calls => {
    const listing = await openJournal()
    assert.equal(listing.newestFirst, false, 'the oldest page was presented as the newest')
    assert.equal(listing.more, false)
    assert.deepEqual([listing.events[0].seq, listing.events.at(-1).seq], [50, 1])
    assert.equal(calls.length, 1)
  })
  // The same gateway answering an older-page request: its rows are at or
  // beyond the bound, so none is added and the listing is marked.
  const shown = { events: [{ seq: 60 }, { seq: 59 }], more: true, newestFirst: true }
  await withFetch(journalGateway(120, { legacy: true }), async () => {
    const listing = await olderJournal(shown)
    assert.equal(listing.newestFirst, false)
    assert.equal(listing.more, false)
    assert.ok(listing.events.every(event => event.seq <= 60))
    assert.equal(new Set(listing.events.map(event => event.seq)).size, listing.events.length)
  })
  // One entry, or none, reads the same either way and is not flagged.
  assert.equal(journalAnswerHonoured([], undefined), true)
  assert.equal(journalAnswerHonoured([{ seq: 4 }], undefined), true)
  assert.equal(journalAnswerHonoured([{ seq: 4 }, { seq: 3 }], 5), true)
  assert.equal(journalAnswerHonoured([{ seq: 3 }, { seq: 4 }], undefined), false)
  assert.equal(journalAnswerHonoured([{ seq: 5 }], 5), false)
})
