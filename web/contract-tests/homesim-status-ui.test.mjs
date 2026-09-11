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

// Pulling the module used to remove the whole status strip, leaving the NKN
// badge alone in the header flex row: the badge visibly shrank and the signal
// area simply vanished, which reads as a rendering fault rather than as "no
// module". The strip now stays, greyed, with a flat line where the bars were.
test('the header keeps a greyed flat-lined strip when no module is attached', async () => {
 const source = await readFile(new URL('../src/components/LineStatusStrip.vue', import.meta.url), 'utf8')
 // The vacant branch is the v-else of the same v-if that used to drop the strip.
 assert.match(source, /<div v-else class="line-status is-vacant"/)
 assert.match(source, /line-status__flatline/)
 assert.match(source, /\.line-status\.is-vacant \{[\s\S]*?opacity: 0\.62/)
 // A flat line, not a collapsed element: it has to hold the width the signal
 // bars had or the header still changes size when the module is pulled.
 assert.match(source, /\.line-status__flatline \{[\s\S]*?width: 26px[\s\S]*?height: 1px/)
 // Nothing to open, so it must not pretend to be a button.
 assert.match(source, /\.line-status\.is-vacant \{[\s\S]*?cursor: default/)
})

// The chart is hidden until the phrase is typed, and the listener lives in the
// shell so the gesture works from either page that can show it.
test('the WireGuard traffic chart stays hidden behind a typed phrase', async () => {
 const unlock = await readFile(new URL('../src/state/wireguardUnlock.ts', import.meta.url), 'utf8')
 assert.match(unlock, /const PHRASE = 'wireguard'/)
 // Typing the word into a field means it as text, not as a gesture.
 assert.match(unlock, /tag === 'INPUT' \|\| tag === 'TEXTAREA'/)
 assert.match(unlock, /isContentEditable/)

 const shell = await readFile(new URL('../src/components/AppShell.vue', import.meta.url), 'utf8')
 assert.match(shell, /listenForWireGuardPhrase\(\)/)

 for (const view of ['../src/views/DashboardView.vue', '../src/components/settings/WireGuardPanel.vue']) {
  const source = await readFile(new URL(view, import.meta.url), 'utf8')
  assert.match(source, /WireGuardTrafficCard/, `${view} shows the chart`)
  assert.match(source, /wireGuardUnlockState\.unlocked/, `${view} gates it`)
 }
})

// A counter that restarts is not a burst of traffic.
test('the WireGuard rate chart survives a counter reset', async () => {
 const source = await readFile(new URL('../src/state/wireguardTraffic.ts', import.meta.url), 'utf8')
 assert.match(source, /if \(current < earlier\) return 0/)
 // Pausing drops the baseline, or the first sample after a pause is reported
 // as everything that happened while nobody was watching.
 assert.match(source, /function stop\(\)[\s\S]*?previous = null/)
})

// The DHT is a second control path, not a second view of NKN, so it gets its
// own light and its own section -- both hidden until asked for by name.
test('the DHT stays hidden until unlocked, and reports isolated honestly', async () => {
 const nav = await readFile(new URL('../src/components/settings/settingsNavigation.ts', import.meta.url), 'utf8')
 assert.match(nav, /if \(section === 'dht'\) return visibility\.isAdmin && visibility\.dhtUnlocked === true/)

 const dialer = await readFile(new URL('../src/components/DialerPanel.vue', import.meta.url), 'utf8')
 assert.match(dialer, /lower\.includes\('dht'\)/)

 const strip = await readFile(new URL('../src/components/LineStatusStrip.vue', import.meta.url), 'utf8')
 assert.match(strip, /v-if="dhtUnlockState\.unlocked"/)
 // "isolated" means the node runs and has found nobody. That is neither
 // healthy nor broken, and showing it green would claim a path that cannot
 // carry a lookup.
 assert.match(strip, /dhtState\.value === 'ready'\) return 'is-green'/)
 assert.match(strip, /'starting' \|\| dhtState\.value === 'isolated'\) return 'is-amber'/)

 const panel = await readFile(new URL('../src/components/settings/DHTPanel.vue', import.meta.url), 'utf8')
 // A bootstrap address without /p2p/<PeerID> names a host but not an identity.
 assert.match(panel, /includes\('\/p2p\/'\)/)
 assert.match(panel, /dht\.isolatedHint/)
 // Polling must not replace the settings half under a half-typed address.
 assert.match(panel, /if \(data\.value\) data\.value\.status = next\.status/)
})
