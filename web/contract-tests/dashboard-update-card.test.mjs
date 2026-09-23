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
// and a "3/3 sources agree" badge. When the card was built only one of those
// was true, and these tests kept the other two dark. The release path has
// since been rebuilt (signed manifests, several mirrors), so the rows may now
// light -- but only from what the check REPORTED. The failure mode is still
// silent: a green tick costs nothing to render and is read by the person
// deciding whether an update is safe to install.

test('each verification row is lit only by what the check reported', () => {
  const block = card.slice(
    card.indexOf('const verifications = computed'),
    card.indexOf('const componentLabels')
  )
  assert.ok(block.length > 0, 'the verification rows are gone')

  const rows = ['integrity', 'consensus', 'signature'].map(key => {
    const from = block.indexOf(`key: '${key}'`)
    assert.ok(from >= 0, `the ${key} row is gone`)
    const to = block.indexOf('key:', from + 5)
    return block.slice(from, to < 0 ? undefined : to)
  })
  const [integrity, consensus, signature] = rows

  // Integrity is enforced on every pull by Docker itself: always on.
  assert.match(integrity, /active:\s*true/)
  // The other two must come from the check, never a literal.
  assert.doesNotMatch(consensus, /active:\s*true/, 'source agreement is claimed unconditionally')
  assert.doesNotMatch(signature, /active:\s*true/, 'the signature is claimed unconditionally')
  assert.match(consensus, /active:\s*sourceAgreement\.value\.agreeing >= 2/)
  assert.match(signature, /active:\s*signatureVerified\.value/)

  // And "verified" is derived from a mirror that reported a VERIFIED manifest
  // for the very version on offer -- not from the version merely existing.
  assert.match(controller, /entry\.state === 'verified' && entry\.version === latest/)
  assert.match(controller, /const signatureVerified = computed\(\(\) => sourceAgreement\.value\.agreeing > 0\)/)
})

test('the card does not invent sources or a consensus count', () => {
  // The mock-up's "3/3" badge. A count may appear only as computed from what
  // the mirrors returned.
  assert.doesNotMatch(
    code(card),
    /\b3\s*\/\s*3\b/,
    'a source-agreement count is hardcoded in the card'
  )
  const sources = card.slice(card.indexOf('const sources = computed'), card.indexOf('const sourcesSummary'))
  assert.match(sources, /releaseSources\.value\.map/, 'mirror rows no longer come from the check')
  // Mirrors are lit only when they verified.
  assert.match(sources, /wired: entry\.state === 'verified'/)
  // DHT and NKN are not built, and must say so.
  for (const key of ['dht', 'nkn']) {
    const row = sources.slice(sources.indexOf(`key: '${key}'`))
    assert.match(row.slice(0, 200), /wired: false/, `${key} is presented as a working source`)
  }
})

test('the ring is driven by real operation state, not a timer', () => {
  // The whole point of the ring is that it reports the operation. A timer or a
  // random walk would look better -- smoother, always moving -- which is
  // exactly why it must not be there: the gateway exposes no byte count, so
  // anything smoother than the component ladder would be invented motion.
  assert.doesNotMatch(code(card), /setInterval|setTimeout|Math\.random/)
  assert.match(card, /\n  progress,\n[\s\S]{0,400}\} = useSoftwareUpdate\(\)/)
  assert.match(card, /:stroke-dashoffset=/, 'the ring no longer draws from a computed offset')
  assert.match(card, /role="progressbar"/)
  // Indeterminate until the first frame arrives, rather than reporting zero:
  // a ring parked at 0% and a stalled update look identical.
  assert.match(card, /ringIndeterminate/)
  assert.match(
    card,
    /:aria-valuenow="ringIndeterminate \? undefined : ringValue"/,
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

test('the download is the updater\'s, shown as reported and never installed from here', () => {
  // The switch shows the preference the updater holds, and is hidden when
  // there is no updater to hold one -- not a hardcoded "on" or "off".
  assert.match(card, /v-if="autoDownloadKnown"/)
  assert.match(card, /role="switch"/)
  assert.match(card, /:aria-checked="autoDownload \? 'true' : 'false'"/)
  assert.doesNotMatch(code(card), /autoDownload\s*=\s*(true|false)\b/)

  // Downloading and preferences go through the controller like everything
  // else; the card has no request of its own.
  assert.match(card, /setAutoDownload\(/)
  assert.match(card, /startUpdateDownload/)
  assert.doesNotMatch(code(card), /writeJSON|\/updates\/download|\/updates\/preferences/)

  // The install button waits for a running download rather than racing it.
  // Checked inside canApply itself: the download button's own guard also
  // mentions !downloading.value, so a whole-file match would prove nothing.
  const canApplyStart = card.indexOf('const canApply = computed(')
  assert.ok(canApplyStart >= 0, 'canApply is gone')
  const canApplyBody = card.slice(canApplyStart, card.indexOf('\n)\n', canApplyStart))
  assert.match(canApplyBody, /!downloading\.value/)
  assert.match(canApplyBody, /!applying\.value/)

  // A download for another version is never shown as this one's.
  assert.match(controller, /candidate\.version !== current\?\.latest_version/)

  // The download ring counts images present, not an invented curve.
  assert.match(controller, /part\.state === 'ready'/)
  assert.match(card, /showingDownload\.value \? downloadProgress\.value : progress\.value/)

  // Polling while a download runs lives in the controller, gated on watchers
  // and on there being a download to watch.
  assert.match(controller, /if \(!downloading\.value \|\| watchers === 0\) return/)

  for (const locale of ['zh-CN', 'en-US']) {
    const labels = softwareUpdateCardLabels(locale)
    for (const key of ['autoDownloadOn', 'autoDownloadOff', 'downloading', 'downloadReadyHint', 'downloadFailedHint']) {
      assert.ok(labels[key], `${locale} has no ${key}`)
    }
  }
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
