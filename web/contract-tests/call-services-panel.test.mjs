import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { readdirSync } from 'node:fs'
import test from 'node:test'

import { callServicesMessages } from '../src/i18n/callServicesMessages.ts'
import {
  SETTINGS_SECTION_ORDER,
  visibleSettingsSectionIDs
} from '../src/components/settings/settingsNavigation.ts'

// Call forwarding and call waiting are the only settings in this app that the
// gateway does not own: they are held by the operator against the subscriber.
// Every test here guards a consequence of that, or a bug found while building
// the panel.

const panel = await readFile(
  new URL('../src/components/settings/CallServicesPanel.vue', import.meta.url),
  'utf8'
)
const client = await readFile(new URL('../src/api/callServices.ts', import.meta.url), 'utf8')
const settingsView = await readFile(
  new URL('../src/views/SettingsView.vue', import.meta.url),
  'utf8'
)

test('the panel shows what the network reported back, not what was sent', () => {
  // A network can accept a registration and hold a normalised number, a
  // rounded delay, or nothing at all if the subscription lacks the service.
  // An optimistic update would show a forwarding rule that does not exist,
  // and forwarding is the one setting where believing a wrong answer means
  // missing every call.
  assert.match(
    panel,
    /response\.value = next[\s\S]{0,200}seedDrafts\(next\.call_services\)/,
    'the panel no longer reseeds itself from the network read-back after a change'
  )
  // Nothing may write the requested value straight into the displayed state.
  assert.doesNotMatch(
    panel,
    /waiting_active\s*=\s*enabled/,
    'call waiting is being displayed from the request instead of the read-back'
  )
})

test('nothing about these services is cached or given a revision', async () => {
  // A stored copy goes stale the moment somebody dials *21# from a handset
  // with the same SIM. The absence of a revision is the design, not an
  // oversight, so it is asserted.
  // Matched against the code, not the prose: the module's own comment says
  // there is no revision, and a bare /revision/ search found that sentence.
  const code = client.replace(/\/\/.*$/gm, '')
  assert.doesNotMatch(
    code,
    /revision/,
    'a revision appeared on call services; they are not ours to version'
  )
  // And the module must stay out of the startup bundle: client.ts is
  // downloaded by every visitor, this page is opened by almost none.
  const shell = await readFile(new URL('../src/api/client.ts', import.meta.url), 'utf8')
  assert.doesNotMatch(
    shell,
    /getCallServices|setCallForwarding/,
    'the call-services requests are back in client.ts, which every visitor downloads'
  )
  assert.match(
    panel,
    /no polling|There is also no polling/,
    'the no-polling rationale left the panel; a timer here hammers the operator network'
  )
  assert.doesNotMatch(panel, /setInterval/, 'the panel polls; every read is a round trip to the HLR')
})

test('the line selector is seeded with a real line', () => {
  // An empty model matches no option, so the control rendered a blank label
  // and an aria-label ending in a bare colon. Found by reading the DOM, not
  // the markup.
  assert.match(
    panel,
    /if \(!selectedLineKey\.value && available\.length\)/,
    'the line selector is no longer seeded, so it renders blank'
  )
})

test('switching line clears the previous answer', () => {
  // Another line is another subscriber's services. Leaving the old answer on
  // screen while the new one loads shows one line's forwarding under another
  // line's name.
  assert.match(
    panel,
    /watch\(activeLineKey,[\s\S]{0,160}response\.value = null/,
    'the previous line answer is not cleared when the line changes'
  )
})

test('the panel is admin-only', () => {
  // Forwarding changes the subscriber's own network service and can divert
  // every incoming call. That is not something a non-admin user of a shared
  // gateway should be able to set.
  const forUser = visibleSettingsSectionIDs({ isAdmin: false, canPairIOS: false })
  assert.ok(
    !forUser.includes('call-services'),
    'a non-admin can reach the forwarding panel'
  )
  const forAdmin = visibleSettingsSectionIDs({ isAdmin: true, canPairIOS: false })
  assert.ok(forAdmin.includes('call-services'), 'an admin cannot reach the forwarding panel')
  assert.ok(
    SETTINGS_SECTION_ORDER.includes('call-services'),
    'the section is missing from the order, so it never renders in the nav'
  )
})

test('the panel is registered everywhere SettingsView needs it', () => {
  // Four separate edits are required and missing any one produces a menu entry
  // that opens a blank page, or a page with no menu entry.
  assert.match(settingsView, /loadCallServicesPanel/, 'no lazy loader')
  assert.match(settingsView, /id: 'call-services'/, 'no nav catalog entry')
  assert.match(settingsView, /case 'call-services':\s*\n\s*return loadCallServicesPanel/, 'no preload case')
  assert.match(
    settingsView,
    /selectedSection === 'call-services'[\s\S]{0,140}<CallServicesPanel \/>/,
    'no render branch'
  )
})

test('every shipped locale gets the nav label, and the panel copy is not in the startup bundle', async () => {
  const locales = readdirSync(new URL('../src/i18n/locales', import.meta.url))
    .filter(name => name.endsWith('.ts'))
  assert.ok(locales.length >= 9)
  for (const file of locales) {
    const source = await readFile(new URL(`../src/i18n/locales/${file}`, import.meta.url), 'utf8')
    assert.match(
      source,
      /"?callServicesDescription"?:/,
      `${file} has no nav label for the call services panel`
    )
    // The prose belongs in callServicesMessages, loaded with the panel. A key
    // here would be downloaded by every visitor to show a page most never open.
    assert.doesNotMatch(
      source,
      /"?unreachableHint"?:/,
      `${file} carries the panel's prose in the startup locale`
    )
  }
})

test('both languages of the panel copy carry the same keys', () => {
  // A key present in English and missing in Chinese falls back silently, which
  // reads as a translation nobody did.
  assert.deepEqual(
    Object.keys(callServicesMessages('zh-CN')).sort(),
    Object.keys(callServicesMessages('en-US')).sort()
  )
  // A Chinese variant must not fall all the way back to English.
  assert.equal(callServicesMessages('zh-TW').title, callServicesMessages('zh-CN').title)
})

test('every key the panel renders resolves in both languages', () => {
  const used = new Set()
  for (const match of panel.matchAll(/t\(\s*['`]callServices\.([A-Za-z0-9_]+)['`]/g)) {
    used.add(match[1])
  }
  // Two are built from the reason name at render time rather than written out.
  for (const reason of ['unconditional', 'busy', 'no_reply', 'unreachable']) {
    used.add(reason)
    used.add(`${reason}Hint`)
  }
  assert.ok(used.size > 10, `found only ${used.size} keys; the pattern has drifted`)
  for (const language of ['en-US', 'zh-CN']) {
    const available = callServicesMessages(language)
    for (const key of used) {
      assert.ok(
        typeof available[key] === 'string' && available[key].length > 0,
        `callServices.${key} is missing from ${language}; it would render as its own key name`
      )
    }
  }
})

test('the call-services URL matches the route the backend actually parses', async () => {
  // THIS IS THE BUG THIS TEST EXISTS FOR, and it shipped: the builder said
  // `/api/v1/lines/{id}/call-services` while the backend's parser --
  // lineServiceResource in internal/httpapi/line_services.go -- only accepts
  // the prefix `/api/v1/devices/`. Every request 404'd before reaching a
  // handler, so call forwarding had never once worked from a browser.
  //
  // It hid for so long because the UI renders a 404 here as the same
  // "unavailable" notice it shows for an agent too old to offer the
  // capability. That is a legitimate state, so nothing looked broken.
  assert.match(
    client,
    /\$\{API_ROOT\}\/devices\/\$\{encodeURIComponent\(lineID\)\}\/call-services/,
    'the call-services path is not under /devices/, so every request 404s'
  )
  assert.doesNotMatch(
    client,
    /\$\{API_ROOT\}\/lines\//,
    'the call-services path is back under /lines/, which the backend does not route'
  )

  // And it is checked against the backend rather than against a string this
  // test made up, so moving the route on either side fails here.
  const parser = await readFile(
    new URL('../../internal/httpapi/line_services.go', import.meta.url),
    'utf8'
  )
  assert.match(
    parser,
    /const prefix = "\/api\/v1\/devices\/"/,
    'the backend prefix moved; the frontend builder has to move with it'
  )
  assert.match(parser, /"call-services"/, 'call-services is no longer a routed resource')
})

test('call services resolve the agent line id instead of passing the API one', async () => {
  // The second half of the same failure. The API addresses lines by an ID
  // derived from the SIM so a line survives being moved between ports; the
  // agent addresses them by one derived from the hardware it can see.
  // store.LineSummary.EndpointID is the translation, and every other agent
  // call applies it. Call services were wired straight to the agent client, so
  // they did not -- the agent refused all five supplementary queries as "line
  // was not found" and reported that as the line not being registered on a
  // network, on a line that was registered and answering AT commands.
  const [main, service] = await Promise.all([
    readFile(new URL('../../cmd/modemdeck/main.go', import.meta.url), 'utf8'),
    readFile(
      new URL('../../internal/communication/call_services.go', import.meta.url),
      'utf8'
    )
  ])

  assert.match(
    main,
    /CallServices:\s*communications,/,
    'CallServices is wired to the raw agent again, which skips the line-ID translation'
  )
  assert.doesNotMatch(main, /CallServices:\s*agent,/)

  // The service has to actually do the translation, not just exist.
  assert.match(service, /resolveAttachedLine/, 'the call-services service resolves no line')
  assert.match(
    service,
    /return line\.EndpointID/,
    'the call-services service does not hand the agent an endpoint ID'
  )
  for (const method of ['CallServicesStatus', 'ApplyCallForwarding', 'ApplyCallWaiting']) {
    assert.match(
      service,
      new RegExp(`func \\(s \\*Service\\) ${method}\\(`),
      `${method} is not served by the resolving service`
    )
  }
})

test('an unknown line is not reported as a carrier registration problem', async () => {
  // When every supplementary query fails the agent says so once rather than
  // four times, which is right. What it blamed was not: it named carrier
  // registration, and the case that shipped was a line ID the agent does not
  // use. That sent an operator to check a registration that was fine.
  const supplementary = await readFile(
    new URL('../../agent/internal/modemmanager/supplementary.go', import.meta.url),
    'utf8'
  )
  assert.match(
    supplementary,
    /unknownLine == len\(services\.Errors\) && lastFailure != nil/,
    'the aggregate no longer separates an unknown line from a silent network'
  )
  assert.match(
    supplementary,
    /operationError\.Code == domain\.ErrorNotFound/,
    'the not-found failures are no longer counted by code'
  )
  // The network explanation must still say the line was reachable, so it
  // cannot be read as covering the unknown-line case too.
  assert.match(supplementary, /it is reachable, so check that it is registered/)
})
