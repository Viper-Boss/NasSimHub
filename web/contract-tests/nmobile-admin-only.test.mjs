import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const [shell, router, contacts, sessionsPanel] = await Promise.all([
  readFile(new URL('../src/components/AppShell.vue', import.meta.url), 'utf8'),
  readFile(new URL('../src/router/index.ts', import.meta.url), 'utf8'),
  readFile(new URL('../src/views/ContactsView.vue', import.meta.url), 'utf8'),
  readFile(new URL('../src/components/AccountSessionsPanel.vue', import.meta.url), 'utf8')
])

// Review P1-6: /api/v1/nkn/chat/* is administrator-only, so members get no
// entry point that could only ever show refusals.
test('nMobile is offered to administrators only', () => {
  const link = shell.match(/<RouterLink[^>]*name: 'nmobile'[^>]*>/)
  assert.ok(link, 'the nMobile rail link exists')
  assert.match(link[0], /v-if="sessionState\.role === 'admin'"/)
  assert.match(router, /to\.name === 'nmobile'[\s\S]{0,80}sessionState\.role !== 'admin'/)
  assert.match(contacts, /if \(isAdministrator\.value\) void loadNknPeerNotes\(\)/)
})

// Review P1-4: browser sessions have a server-side expiry, shown per device.
test('signed-in devices show when a browser session expires', () => {
  assert.match(sessionsPanel, /session\.expires_at/)
  assert.match(sessionsPanel, /t\('account\.expiresAt'/)
})
