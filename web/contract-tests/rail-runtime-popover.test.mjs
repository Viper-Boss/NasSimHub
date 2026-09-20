import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const shell = await readFile(
  new URL('../src/components/AppShell.vue', import.meta.url),
  'utf8'
)
const card = await readFile(
  new URL('../src/components/RuntimeStatusCard.vue', import.meta.url),
  'utf8'
)
const theme = await readFile(
  new URL('../src/styles/homesim.css', import.meta.url),
  'utf8'
)

const template = shell.slice(shell.indexOf('<template>'))
const rail = template.slice(template.indexOf('<aside class="rail"'), template.indexOf('</aside>'))

// The rail avatar and the status popover it opens. Three defects the operator
// reported live here: a gradient ring around a set avatar, a popover cropped to
// a quarter of itself, and figures inside it that did not read correctly.

test('the popover is not inside the scrolling rail', () => {
  // `.app-shell .rail { overflow-y: auto }` -- and a box with overflow in one
  // axis clips the other as well, so a 340px card positioned inside a ~90px
  // rail is cropped to the rail's width. Painting the card at 1000x640 with it
  // outside the rail showed all 340px; inside, the operator saw a sliver.
  assert.doesNotMatch(
    rail,
    /runtime-card-pop/,
    'the runtime popover is back inside the rail, which clips it'
  )
  assert.match(template, /class="runtime-card-pop"/, 'the runtime popover is gone entirely')
  // Fixed, so the viewport is its containing block and no ancestor's overflow
  // reaches it.
  assert.match(
    theme,
    /\.runtime-card-pop \{[^}]*position:\s*fixed/,
    'the popover is positioned relative to an ancestor again'
  )
  // And it layers with the app's own popovers rather than an invented number.
  assert.match(
    theme,
    /\.runtime-card-pop \{[^}]*z-index:\s*var\(--layer-popover\)/,
    'the popover layers on an ad-hoc z-index'
  )
})

test('the popover is placed from the avatar, and measured rather than assumed', () => {
  // A fixed box needs coordinates, and the only correct source for them is the
  // button that opened it.
  assert.match(shell, /function placeRuntimeCard/, 'the placement is gone')
  assert.match(
    shell,
    /runtimeCardRoot\.value\?\.getBoundingClientRect\(\)/,
    'the popover no longer reads the avatar position'
  )
  // The card's own size is measured, not copied: a width written here is a
  // second source of truth that goes stale the first time the card changes.
  assert.match(
    shell,
    /const card = runtimeCardPop\.value\?\.getBoundingClientRect\(\)/,
    'the popover size is no longer measured from the card itself'
  )
  // Clamped into the viewport, or a short window puts it off the bottom.
  assert.match(shell, /window\.innerWidth - width/, 'the popover can run off the right edge')
  assert.match(shell, /window\.innerHeight - height/, 'the popover can run off the bottom edge')
  // Re-placed when the window changes size.
  assert.match(shell, /addEventListener\('resize', onRuntimeViewportChange\)/)
  assert.match(shell, /removeEventListener\('resize', onRuntimeViewportChange\)/)
})

test('a click inside the popover does not close it', () => {
  // The card is no longer a descendant of the button's wrapper, so the
  // click-outside test has to check both. Without the second half, pressing
  // either of the card's own buttons closed the card under the press.
  const handler = shell.slice(
    shell.indexOf('function handleRuntimeClickOutside'),
    shell.indexOf('function onRuntimeViewportChange')
  )
  assert.match(handler, /runtimeCardRoot\.value\?\.contains\(target\)/)
  assert.match(
    handler,
    /runtimeCardPop\.value\?\.contains\(target\)/,
    'clicking inside the popover closes it'
  )
})

test('a set avatar fills its tile', () => {
  // The 40px tile carries the brand gradient; the fallback glyph sits on it at
  // 34px. Once a picture is set, that 3px difference reads as a gradient ring
  // around a circular photo. Measured before: tile 40, avatar 34, ring 3px.
  // After: tile 40, avatar 40, ring 0, no gradient, round.
  const rule = theme.match(/\.brand-mark__avatar:has\(\.avatar--image\) \{[^}]*\}/)
  assert.ok(rule, 'the image-avatar rule is gone; the gradient ring is back')
  assert.match(rule[0], /background:\s*none/, 'the tile still paints its gradient behind the picture')
  assert.match(rule[0], /border-radius:\s*50%/, 'the tile is still a rounded square behind a circle')
  assert.match(
    theme,
    /\.brand-mark__avatar:has\(\.avatar--image\) \.avatar \{[^}]*width:\s*100%/,
    'the picture no longer fills the tile'
  )
  // The unset case is untouched: that one was not reported and looks right.
  assert.match(
    theme,
    /\.brand-mark__avatar \.avatar \{[^}]*width:\s*34px/,
    'the fallback glyph size changed; only the image case was meant to'
  )
})

test('the status card stacks, so its own text is not squeezed by the gauges', () => {
  // It used to be a text column beside a 2x2 block of gauges: at 340px wide the
  // text column came out around 140px, and the device name, the version and a
  // three-part uptime all had to wrap or truncate in it. That is the "contents
  // display wrong" that was reported, and no popover width fixes it while the
  // gauges take a fixed 142px out of the middle.
  assert.match(
    card,
    /\.runtime-card \{[^}]*flex-direction:\s*column/,
    'the status card is back to two columns and will squeeze its own text'
  )
  assert.match(
    card,
    /\.runtime-card__gauges \{[^}]*grid-auto-flow:\s*column/,
    'the gauges are back in a block instead of one row'
  )
})

test('the process count is not printed twice', () => {
  // `dashboard.processes` already carries the count ("{count} procs"), so
  // prefixing the number again rendered "145 145 procs" in every language.
  assert.doesNotMatch(
    card,
    /\$\{snap\.process_count\} \$\{t\('dashboard\.processes'/,
    'the process count is prefixed to a label that already contains it'
  )
  assert.match(card, /t\('dashboard\.processes', \{ count: snap\.process_count \}\)/)
})

test('the update button checks, and hands the applying over to the panel that can do it', () => {
  // Applying an update is a multi-container operation with a progress list and,
  // for the hardware component, its own confirmation. None of that belongs in a
  // status popover, and a button here that looked like it applied one would be
  // a promise this card cannot keep.
  assert.match(card, /gateway\.checkForUpdates\(true\)/, 'the update button no longer checks for updates')
  // `true` forces a fresh read: a button that can answer from a cache is a
  // button that can say "up to date" without having asked anything.
  assert.doesNotMatch(card, /checkForUpdates\(false\)|checkForUpdates\(\)/, 'the check can be answered from a cache')
  // The verdict is the gateway's own, in the gateway's own words.
  assert.match(card, /t\(`about\.updateStatus\.\$\{updateStatus\.value\}\.title`\)/)
  // A failed check is its own status, not "up to date".
  assert.match(
    card,
    /catch \{[\s\S]{0,220}updateStatus\.value = 'unavailable'/,
    'a check that could not reach the release index reports something other than a failure'
  )
  // And nothing here applies an update.
  assert.doesNotMatch(card, /applyUpdate|startUpdate|gateway\.applyUpdate/, 'the popover applies updates itself')
  assert.match(card, /name: 'settings', params: \{ section: 'about' \}/, 'there is no way through to the update panel')
})

test('the restart button names what it restarts, and refuses to guess which one', () => {
  // There is no endpoint for restarting the NAS or the gateway process, so a
  // button labelled just "restart" would be read as offering one.
  assert.match(card, /t\('dashboard\.restartModem'\)/, 'the restart button no longer names the module')
  assert.match(card, /restartModem\(target\)/, 'the restart button no longer restarts the module')
  // Confirmed first: it is a forty-five-second outage.
  assert.match(
    card,
    /requestConfirmation\(\{[\s\S]{0,200}tone: 'danger'/,
    'the module restarts without asking'
  )
  // With two modules present and no default set there is no answer, and taking
  // the first of the list would toss a coin over which one goes down.
  const target = card.slice(card.indexOf('const restartTarget'), card.indexOf('const restarting'))
  assert.match(target, /present\.length === 1 \? present\[0\]!\.id : ''/, 'an ambiguous restart target is now guessed')
  assert.match(card, /:disabled="!restartTarget \|\| restarting"/, 'the button stays enabled with no module to restart')
})
