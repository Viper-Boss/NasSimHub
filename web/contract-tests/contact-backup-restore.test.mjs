import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { contactBackupMessages } from '../src/i18n/contactBackupMessages.ts'

const panel = await readFile(
  new URL('../src/components/ContactBackupButton.vue', import.meta.url),
  'utf8'
)
const api = await readFile(
  new URL('../src/api/contactBackups.ts', import.meta.url),
  'utf8'
)
const homesim = await readFile(
  new URL('../src/i18n/homesimData.ts', import.meta.url),
  'utf8'
)

// The restore half of the NAS contact backup. The backup shipped on its own,
// which made the button a one-way door.
//
// The risk in a restore is not that it fails, it is that it succeeds
// destructively or duplicates everyone. So these guard the two properties that
// make it safe: it goes through the SAME import path the vCard flow uses, and
// it never deletes.

test('the restore imports through the existing path rather than a new one', () => {
  // planContactImport matches by phone number, merges missing numbers into a
  // match, and refuses an ambiguous multi-match. Reimplementing any of that
  // here would give restore different behaviour from an import of the same
  // people -- and a restore IS an import.
  assert.match(panel, /import \{ planContactImport/, 'the restore no longer uses the shared import planner')
  assert.match(panel, /planContactImport\(transfer, contactsResource\.data, defaultRegion\.value\)/)
  assert.match(panel, /if \(plan\.conflict \|\| !plan\.input\) \{\s*skipped \+= 1/, 'an ambiguous match is no longer skipped')
  assert.match(panel, /saveContact\(plan\.input, plan\.existing\?\.id\)/, 'the restore writes contacts some other way')
  // And it reports in the same words the vCard import does.
  assert.match(panel, /t\('contactSync\.importSummary'/, 'the restore invented its own summary')
})

test('nothing is deleted by a restore', () => {
  // The single property that separates "restore" from "revert". A restore that
  // could remove what you have now is a much scarier operation than what was
  // asked for, and neither the code nor the copy may drift into it.
  for (const destructive of ['deleteContact', 'deleteContacts', 'DeleteContacts']) {
    assert.doesNotMatch(panel, new RegExp(`\\b${destructive}\\b`), `the restore calls ${destructive}`)
  }
  for (const locale of ['en-US', 'zh-CN']) {
    const messages = contactBackupMessages(locale)
    assert.ok(messages.restoreHint, `${locale} lost the restore hint`)
    assert.ok(
      /deleted|删除/.test(messages.restoreHint),
      `${locale}'s restore hint no longer tells the reader nothing is deleted`
    )
    assert.ok(
      /deleted|删除/.test(messages.restoreConfirmMessage),
      `${locale}'s restore confirmation no longer says nothing is deleted`
    )
  }
})

test('the restore matches against the contacts that exist now', () => {
  // Matching against a stale list means matching against people who are no
  // longer there, which turns an update into a duplicate.
  const body = panel.slice(panel.indexOf('async function restore'))
  assert.match(
    body.slice(0, body.indexOf('for (const transfer')),
    /loadContacts\(true\)/,
    'the restore matches against whatever the list happened to have loaded'
  )
})

test('the restore is confirmed before it runs', () => {
  assert.match(
    panel,
    /const confirmed = await requestConfirmation\(\{[\s\S]{0,200}restoreConfirmMessage[\s\S]{0,80}\}\)\s*if \(!confirmed\) return/,
    'a restore now starts without asking'
  )
})

test('an unrecognised file is not imported as contacts', () => {
  // The backup directory belongs to the operator and may hold other things.
  // Importing an unknown shape would create a pile of nameless contacts that
  // someone then has to find and delete one by one.
  assert.match(
    api,
    /if \(payload\?\.format !== 'homesim-contacts-v1'\) \{\s*throw new Error/,
    'the reader no longer checks the backup format'
  )
  // And a row with neither a name nor a number is not a contact.
  assert.match(
    panel,
    /if \(!name && !phones\.length\) return null/,
    'an empty row is imported as a blank contact'
  )
})

test('the backup copy stays out of the startup bundle', () => {
  // homesimData.ts is imported by every locale chunk, so the bundler hoists it
  // into the app shell: eleven strings in two languages measured 425 bytes
  // there, paid by every visitor before the app has drawn anything.
  assert.doesNotMatch(
    homesim,
    /restoreTitle|restoreConfirmMessage/,
    'the restore copy moved back into the startup bundle'
  )
  assert.match(panel, /contactBackupMessages/, 'the panel no longer loads its own copy')
  // Merged during setup: `t()` does not track a merge as a dependency, and this
  // panel sits still until someone presses a button, so a merge from onMounted
  // would leave raw key names on screen.
  assert.match(
    panel,
    /watch\(\s*i18n\.locale,[\s\S]{0,220}flush: 'sync'/,
    'the copy is no longer merged synchronously during setup'
  )
  // Both languages carry every key, or one of them renders key names.
  const english = Object.keys(contactBackupMessages('en-US'))
  for (const key of english) {
    assert.ok(contactBackupMessages('zh-CN')[key], `zh-CN is missing homesim.${key}`)
  }
  assert.match(contactBackupMessages('zh-CN').restoreConfirmMessage, /\{filename\}/)
  assert.match(contactBackupMessages('en-US').restoreConfirmMessage, /\{filename\}/)
})

test('the picker is the app\'s own control, and says which of its three states it is in', () => {
  // A native select is a different object from every other control on the
  // screen; the app has one primitive for this.
  assert.doesNotMatch(panel, /<select\b/, 'the picker is a native select again')
  assert.match(panel, /<SelectControl/, 'the picker is no longer the app\'s control')
  // Loading, empty and ready are three different things, and a picker that
  // shows "choose a backup" while there are none is lying about the second.
  const placeholder = panel.slice(panel.indexOf('const pickerPlaceholder'), panel.indexOf('async function refreshBackups'))
  assert.match(placeholder, /listing\.value/, 'the picker no longer says when it is still loading')
  assert.match(placeholder, /restoreNone/, 'the picker no longer says when there are no backups')
})
