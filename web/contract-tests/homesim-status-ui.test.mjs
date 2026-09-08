import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('VoWiFi requires an active bearer and device replies cannot cross lines', async () => {
 const source = await readFile(new URL('../src/components/LineStatusStrip.vue', import.meta.url), 'utf8')
 assert.match(source, /session\.line_id === line\.value\?\.id && session\.phase === 'active' && session\.bearer === 'vowifi'/)
 assert.match(source, /request !== deviceRequest \|\| line\.value\?\.id !== value\.id/)
 assert.ok(source.indexOf('const dataOn = ref') < source.indexOf('watch(() => line.value?.id'))
 assert.doesNotMatch(source, /animation: line-status-data-blink/)
})

test('settings search filters visible sections without changing route authorization', async () => {
 const source = await readFile(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
 assert.match(source, /sections\.value\.some\(section => section\.id === value\)/)
 assert.match(source, /filter\(group => group\.sections\.length > 0\)/)
 assert.match(source, /<AudioSettingsForm \/>[\s\S]*settings-experimental/)
})
