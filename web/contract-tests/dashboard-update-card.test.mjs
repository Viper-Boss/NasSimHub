import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { softwareUpdateCardLabels } from '../src/i18n/softwareUpdateCardLabels.ts'

const card = await readFile(
  new URL('../src/components/SoftwareUpdateCard.vue', import.meta.url),
  'utf8'
)
const controller = await readFile(
  new URL('../src/state/softwareUpdate.ts', import.meta.url),
  'utf8'
)
const dashboard = await readFile(
  new URL('../src/views/DashboardView.vue', import.meta.url),
  'utf8'
)

/**
 * The source with its comments removed.
 *
 * Several assertions below are of the form "this string must not appear", and
 * the file explains at length why each of those things is absent. Without this
 * the prose describing the mock-up's "3/3 sources agree" badge trips the test
 * that exists to keep that badge out -- which would push the next person to
 * delete the explanation rather than keep the rule.
 */
function code(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    .replace(/<!--[\s\S]*?-->/g, ' ')
}

// The update card at the foot of the dashboard. It was drawn from a mock-up
// that showed three green ticks -- integrity, source agreement, signature --
// and a "3/3 sources agree" badge, on a gateway where exactly one of those
// four things is true. The tests here are mostly about it staying honest,
// because the failure mode is silent: a green tick beside "signature verified"
// costs nothing to render, looks correct, and is read by the person deciding
// whether an update is safe to install.

test('the card claims only the one check the gateway actually performs', () => {
  // Exactly one verification row may be marked active. Integrity is real:
  // images are pulled by sha256 digest and Docker enforces it. Source
  // agreement needs a second source, which does not exist. Signature needs a
  // signature, which releases do not carry.
  const block = card.slice(
    card.indexOf('const verifications = computed'),
    card.indexOf('const componentLabels')
  )
  assert.ok(block.length > 0, 'the verification rows are gone')

  const actives = [...block.matchAll(/active:\s*(true|false)/g)].map(match => match[1])
  assert.equal(actives.length, 3, `expected three verification rows, found ${actives.length}`)
  assert.equal(
    actives.filter(value => value === 'true').length,
    1,
    'more than one verification row is marked active; only digest pinning is actually enforced'
  )

  // And the active one has to be the integrity row, not whichever row happens
  // to be first after an edit.
  const integrity = block.slice(block.indexOf("key: 'integrity'"), block.indexOf("key: 'consensus'"))
  assert.match(integrity, /active:\s*true/, 'the integrity row is no longer the enforced one')
})

test('the card does not invent sources or a consensus count', () => {
  // The mock-up's "3/3" badge. If a count like this ever appears it has to
  // come from something that counted, and today nothing does.
  assert.doesNotMatch(
    code(card),
    /\b3\s*\/\s*3\b/,
    'a source-agreement count is hardcoded in the card'
  )

  const sources = card.slice(card.indexOf('const sources = computed'), card.indexOf('/** The three verification'))
  const wired = [...sources.matchAll(/wired:\s*(true|false)/g)].map(match => match[1])
  assert.equal(wired.length, 3, `expected three sources, found ${wired.length}`)
  assert.equal(
    wired.filter(value => value === 'true').length,
    1,
    'more than one update source is marked wired up; only GitHub is queried'
  )
})

test('the ring is driven by real operation state, not a timer', () => {
  // The whole point of the ring is that it reports the operation. A timer or a
  // random walk would look better -- smoother, always moving -- which is
  // exactly why it must not be there: the gateway exposes no byte count, so
  // anything smoother than the component ladder would be invented motion.
  assert.doesNotMatch(code(card), /setInterval|setTimeout|Math\.random/)
  assert.match(card, /progress\s*\}\s*=\s*useSoftwareUpdate\(\)|progress[\s\S]{0,80}useSoftwareUpdate/)
  assert.match(card, /:stroke-dashoffset=/, 'the ring no longer draws from a computed offset')
  assert.match(card, /role="progressbar"/)
  // Indeterminate until the first frame arrives, rather than reporting zero:
  // a ring parked at 0% and a stalled update look identical.
  assert.match(card, /ringIndeterminate/)
  assert.match(
    card,
    /:aria-valuenow="ringIndeterminate \? undefined : progress"/,
    'the ring reports a number while it has none to report'
  )

  // The weights live in the controller, where both screens read them.
  assert.match(controller, /componentProgressWeights/)
  assert.doesNotMatch(
    code(card),
    /componentProgressWeights/,
    'the card keeps its own copy of the weights'
  )
})

test('the card goes through the shared controller rather than applying updates itself', () => {
  // Two owners of the single runtime event stream is the bug this avoids; see
  // the header of src/state/softwareUpdate.ts.
  assert.match(card, /useSoftwareUpdate\(\)/)
  assert.match(card, /applyUpdate/)
  assert.doesNotMatch(
    code(card),
    /gateway\.applySoftwareUpdate|gateway\.checkForUpdates|subscribeRuntimeUpdateOperations/,
    'the card drives the update itself instead of going through the controller'
  )
  // The controller is reference counted, so two mounted screens do not each
  // register a listener and the last one out does not close it for the other.
  assert.match(controller, /watchers \+= 1/)
  assert.match(controller, /watchers = Math\.max\(0, watchers - 1\)/)
})

test('the card is admin only and lazily loaded', () => {
  // Every /api/v1/updates/ route is admin-gated server side, so for anyone
  // else this card could only ever render an error.
  assert.match(card, /sessionState\.role === 'admin'/)
  assert.match(card, /v-if="visible"/)

  // Lazily imported: it sits below the fold and the startup budget has about a
  // kilobyte of headroom, so its code and its nine-locale strings must not
  // land in the shell every visitor downloads.
  assert.match(
    dashboard,
    /defineAsyncComponent\(\s*\(\) => import\('\.\.\/components\/SoftwareUpdateCard\.vue'\)\s*\)/,
    'the update card is imported eagerly into the dashboard shell'
  )
  assert.match(dashboard, /<SoftwareUpdateCard \/>/)
})

test('the card says that nothing is pre-downloaded', () => {
  // The mock-up showed "auto-download on, waiting to install". There is no
  // pre-download: `docker pull` runs inside apply. Saying otherwise would set
  // up the operator to expect an instant install and then watch a spinner.
  for (const locale of ['zh-CN', 'en-US']) {
    const labels = softwareUpdateCardLabels(locale)
    assert.ok(labels.autoDownloadOff, `${locale} has no auto-download-off label`)
    assert.ok(labels.autoDownloadHint, `${locale} does not explain what that means`)
  }
  assert.match(card, /autoDownloadOff/)
  assert.doesNotMatch(code(card), /autoDownloadOn|已自动下载/)
})

test('both shipped locales carry the same keys, and the rest fall back to English', () => {
  const english = softwareUpdateCardLabels('en-US')
  const chinese = softwareUpdateCardLabels('zh-CN')
  assert.deepEqual(
    Object.keys(chinese).sort(),
    Object.keys(english).sort(),
    'the two locales have drifted apart, so one of them renders a raw key'
  )
  for (const [key, value] of Object.entries(chinese)) {
    assert.ok(value.trim().length > 0, `zh-CN.${key} is empty`)
    assert.ok(english[key].trim().length > 0, `en-US.${key} is empty`)
  }
  // Traditional Chinese reads the simplified text, not English.
  assert.equal(softwareUpdateCardLabels('zh-TW'), chinese)
  // Anything else falls back rather than rendering undefined.
  assert.equal(softwareUpdateCardLabels('ja-JP'), english)
  assert.equal(softwareUpdateCardLabels('xx-XX'), english)
})

test('the labels merge on a locale watch, not once on mount', () => {
  // A merge done once in onMounted is a store mutation t() does not track, so
  // a card whose state barely changes renders raw key names until something
  // else forces an update. That was measured on the cards above this one.
  assert.match(card, /watch\(\s*\(\) => i18n\.global\.locale\.value/)
  assert.match(card, /immediate: true, flush: 'sync'/)
  assert.match(card, /mergeLocaleMessage/)
})
