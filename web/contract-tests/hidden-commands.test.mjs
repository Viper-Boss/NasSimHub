import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { HIDDEN_COMMANDS, matchHiddenCommand } from '../src/state/hiddenCommands.ts'
import { hiddenCommandMessages } from '../src/i18n/hiddenCommandMessages.ts'

// Comments stripped everywhere this file looks for a CALL: a regex for
// `listenForHiddenCommands()` matches it just as happily when it has been
// commented out, and that mutation escaped the first time this was sabotaged.
const withoutComments = source => source.replace(/^\s*\/\/.*$/gm, '')
const registry = await readFile(
  new URL('../src/state/hiddenCommands.ts', import.meta.url),
  'utf8'
)
const dialer = withoutComments(
  await readFile(new URL('../src/components/DialerPanel.vue', import.meta.url), 'utf8')
)
const shell = withoutComments(
  await readFile(new URL('../src/components/AppShell.vue', import.meta.url), 'utf8')
)
const wireguardFlag = await readFile(
  new URL('../src/state/wireguardUnlock.ts', import.meta.url),
  'utf8'
)

// The app has hidden phrases that reveal panels. They were four branches in the
// dial pad plus a keyboard listener of their own, and they had drifted: two
// commands owned the same word, two said nothing when they fired, and one
// worked from anywhere while the rest needed the dial pad.
//
// These guard the properties that made the drift possible, not the current
// wording.

test('every reveal lives in the one registry', () => {
  // A reveal added as another branch in the dial pad is a reveal that will
  // drift again: it gets its own matching rule, its own confirmation or none,
  // and its own answer to whether typing on the page works.
  const ids = HIDDEN_COMMANDS.map(command => command.id)
  assert.deepEqual([...ids].sort(), ['dht', 'nkn', 'wireguard'])
  for (const command of HIDDEN_COMMANDS) {
    assert.equal(typeof command.run, 'function', `${command.id} has no reveal`)
    assert.equal(
      command.phrase,
      command.phrase.trim().toLowerCase(),
      `${command.id}'s phrase is not in the form it is compared against`
    )
  }
  // The dial pad reads the registry rather than calling the reveals itself.
  assert.match(dialer, /matchHiddenCommand\(entry\)/)
  assert.match(dialer, /if \(command\) runHiddenCommand\(command\)/)
  for (const bypass of ['unlockNknMenu', 'unlockDHT', 'unlockWireGuardChart', 'revealAdvancedNetwork']) {
    assert.doesNotMatch(
      dialer,
      new RegExp(`\\b${bypass}\\(`),
      `the dial pad calls ${bypass} directly again, around the registry`
    )
  }
})

test('the whole entry has to be the phrase', () => {
  // `includes` was the old rule, and it fires on ordinary text: "banknote"
  // contains "nkn", so typing a contact's name into the dial pad's search
  // silently changed what the app showed.
  assert.ok('banknote'.includes('nkn'), 'the example this test is built on stopped holding')
  assert.equal(matchHiddenCommand('banknote'), undefined)
  assert.equal(matchHiddenCommand('andhtolstoy'), undefined)
  assert.equal(matchHiddenCommand('13812345678'), undefined)
  assert.equal(matchHiddenCommand(''), undefined)
  assert.equal(matchHiddenCommand('   '), undefined)
  // And the phrases themselves still match, whatever the case or padding.
  assert.equal(matchHiddenCommand('nkn')?.id, 'nkn')
  assert.equal(matchHiddenCommand('  WireGuard  ')?.id, 'wireguard')
  assert.equal(matchHiddenCommand('DHT')?.id, 'dht')
  assert.doesNotMatch(registry, /\.includes\(command\.phrase\)/, 'substring matching is back')
})

test('no two commands own the same word', () => {
  // This is the defect the operator reported as "I typed wireguard and the
  // card does not show". Two features matched that word: the dial pad's
  // `editNumber` revealed the advanced-network settings, cleared the field and
  // returned, so the watch that reveals the traffic card never saw the phrase.
  // Measured before the fix, typing it letter by letter: the field held
  // "wireguar" and then emptied with the chart's flag still unset.
  const phrases = HIDDEN_COMMANDS.map(command => command.phrase)
  assert.equal(new Set(phrases).size, phrases.length, 'two commands share a phrase')
  // `editNumber` must not intercept anything: whatever is typed reaches
  // `number`, and the registry decides.
  const editNumber = dialer.slice(dialer.indexOf('function editNumber'))
  const body = editNumber.slice(0, editNumber.indexOf('\n}\n') + 2)
  assert.doesNotMatch(body, /wireguard/i, 'editNumber intercepts a phrase again')
  assert.doesNotMatch(body, /\breturn\b/, 'editNumber returns early again, swallowing the entry')
})

test('one word reveals both halves of its feature', () => {
  // "wireguard" names one thing, so it reveals that thing: the home page card
  // and the settings panel. A member gets the card alone -- the panel is not in
  // their menu -- and is told so, rather than promised a panel they will not
  // find.
  const wireguard = registry.slice(registry.indexOf("id: 'wireguard'"))
  const run = wireguard.slice(0, wireguard.indexOf('\n  },'))
  assert.match(run, /unlockWireGuardChart\(\)/, 'the traffic card is no longer revealed')
  assert.match(run, /revealAdvancedNetwork\(\)/, 'the settings panel is no longer revealed')
  assert.match(run, /sessionState\.role === 'admin'/, 'the panel is offered to non-administrators')
  assert.match(run, /return 'wireguardCard'/, 'a member is no longer told what they actually got')
})

test('every command confirms, through the same surface', () => {
  // Two of the five used to say nothing at all, and the two that did spoke
  // inside the dial pad -- invisible when the phrase was typed on a page with
  // no dial pad open -- for 4 and 5 seconds respectively.
  assert.match(registry, /export function runHiddenCommand/)
  assert.match(registry, /showFeedback\(translate\(`hiddenCommands\.\$\{key\}`\)\)/)
  // The dial pad's own bespoke hints are gone.
  assert.doesNotMatch(dialer, /nknHintVisible|guardHintVisible/, 'a bespoke inline hint is back')
  assert.doesNotMatch(dialer, /setTimeout\([^)]*Hint/, 'a bespoke hint timer is back')
  // The fraud-guard unlock is the fifth command and confirms the same way.
  assert.match(dialer, /unlockCallGuard\(\)[\s\S]{0,160}showFeedback\(t\('homesim\.fraudGuard\.unlocked'\)\)/)

  // Every key a command can return has copy in both languages, or the
  // confirmation renders as its own key name.
  const keys = [...registry.matchAll(/return '([a-zA-Z]+)'\n/g)].map(match => match[1])
  assert.ok(keys.length >= 4, 'the confirmation keys could not be read')
  for (const locale of ['en-US', 'zh-CN']) {
    const copy = hiddenCommandMessages(locale)
    for (const key of keys) {
      assert.ok(copy[key], `${locale} is missing hiddenCommands.${key}`)
      // A confirmation that only says "unlocked" leaves the operator hunting,
      // which is how a revealed card came to be reported as missing.
      assert.ok(
        copy[key].length > 20,
        `${locale}'s ${key} does not say where the thing appeared`
      )
    }
  }
})

test('a reveal works from the page, and a server-side action does not', () => {
  // Typing on the page used to work for "wireguard" alone. It works for every
  // reveal now -- they only change what this browser shows.
  assert.match(registry, /export function listenForHiddenCommands/)
  assert.match(shell, /listenForHiddenCommands\(\)/, 'the shell no longer installs the listener')
  assert.doesNotMatch(shell, /listenForWireGuardPhrase/, 'the one-phrase listener is back')
  assert.doesNotMatch(
    wireguardFlag,
    /addEventListener/,
    'the WireGuard flag file listens on its own again'
  )
  // The fraud-guard unlock is deliberately NOT in the registry: it changes what
  // the gateway does, not what this browser shows, and a phrase typed by
  // accident on a page should not lift a safety limit.
  assert.equal(matchHiddenCommand('unlock'), undefined)
  assert.doesNotMatch(registry, /unlockCallGuard/, 'the fraud-guard unlock moved into the page listener')
  assert.match(dialer, /entry === 'unlock'/, 'the fraud-guard unlock left the dial pad')

  // The listener must still ignore typing aimed at a field.
  const listener = registry.slice(registry.indexOf('function onKeydown'))
  assert.match(listener, /tag === 'INPUT'/)
  assert.match(listener, /isContentEditable/)
  assert.match(listener, /event\.ctrlKey \|\| event\.metaKey \|\| event\.altKey/)
})
