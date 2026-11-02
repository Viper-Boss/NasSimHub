import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { visibleSettingsSectionIDs } from '../src/components/settings/settingsNavigation.ts'
import { airsimMessages } from '../src/i18n/airsimMessages.ts'

const source = path => readFile(new URL(path, import.meta.url), 'utf8')

test('the AirSIM iPhone section is administrator-only', () => {
  assert.ok(visibleSettingsSectionIDs({ isAdmin: true, canPairIOS: true }).includes('airsim'))
  assert.ok(!visibleSettingsSectionIDs({ isAdmin: false, canPairIOS: true }).includes('airsim'))
})

test('the AirSIM panel talks only to its own settings endpoints', async () => {
  const api = await source('../src/api/airsim.ts')
  for (const path of ['/settings/airsim`', '/settings/airsim/pairing`', '/settings/airsim/devices/revoke`']) {
    assert.ok(api.includes(path), path)
  }
  const panel = await source('../src/components/settings/AirSIMPanel.vue')
  assert.match(panel, /from '\.\.\/\.\.\/api\/airsim'/)
  // The code is shown, never put in a URL or stored.
  assert.doesNotMatch(panel, /localStorage|sessionStorage|router\.push/)
})

test('AirSIM copy has the same keys in every language it ships', () => {
  const english = Object.keys(airsimMessages('en-US')).sort()
  for (const locale of ['zh-CN', 'zh-TW', 'ja-JP', 'de-DE']) {
    assert.deepEqual(Object.keys(airsimMessages(locale)).sort(), english, locale)
  }
})
