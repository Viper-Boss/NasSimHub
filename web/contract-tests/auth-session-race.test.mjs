import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  gateway,
  rotateAuthenticationRequestScope,
  setAuthenticationRequiredHandler
} from '../src/api/client.ts'

function unauthorizedResponse() {
  return new Response(
    JSON.stringify({
      code: 'authentication_required',
      message: 'Authentication is required'
    }),
    {
      status: 401,
      headers: { 'Content-Type': 'application/json' }
    }
  )
}

test('clone reset sends local confirmation without requiring an inherited session', async () => {
  const originalFetch = globalThis.fetch
  let invalidations = 0
  setAuthenticationRequiredHandler(() => { invalidations += 1 })
  globalThis.fetch = async (path, init) => {
    assert.equal(path, '/api/v1/provision/reset')
    assert.equal(init.method, 'POST')
    assert.deepEqual(JSON.parse(init.body), { confirm_machine_id: 'target', reset_token: 'local-code' })
    return unauthorizedResponse()
  }
  try {
    await assert.rejects(gateway.requestProvisionReset('target', 'local-code'), error => error.status === 401)
    assert.equal(invalidations, 0)
  } finally {
    globalThis.fetch = originalFetch
    setAuthenticationRequiredHandler(() => undefined)
  }
})

test('a stale protected 401 cannot invalidate a newer authentication scope', async () => {
  const originalFetch = globalThis.fetch
  let resolveStaleRequest
  let invalidations = 0
  globalThis.fetch = () =>
    new Promise(resolve => {
      resolveStaleRequest = resolve
    })
  setAuthenticationRequiredHandler(() => {
    invalidations += 1
  })

  try {
    const staleRequest = gateway.listContacts()
    rotateAuthenticationRequestScope()
    resolveStaleRequest(unauthorizedResponse())

    await assert.rejects(
      staleRequest,
      error => error?.status === 401 && error?.code === 'authentication_required'
    )
    assert.equal(invalidations, 0)

    globalThis.fetch = async () => unauthorizedResponse()
    await assert.rejects(
      gateway.listContacts(),
      error => error?.status === 401 && error?.code === 'authentication_required'
    )
    assert.equal(invalidations, 1)
  } finally {
    rotateAuthenticationRequestScope()
    setAuthenticationRequiredHandler(() => undefined)
    globalThis.fetch = originalFetch
  }
})

test('login failures do not masquerade as an expired authenticated session', async () => {
  const originalFetch = globalThis.fetch
  let invalidations = 0
  globalThis.fetch = async () => unauthorizedResponse()
  setAuthenticationRequiredHandler(() => {
    invalidations += 1
  })

  try {
    await assert.rejects(
      gateway.login({ username: 'member', password: 'incorrect' }),
      error => error?.status === 401 && error?.code === 'authentication_required'
    )
    assert.equal(invalidations, 0)
  } finally {
    setAuthenticationRequiredHandler(() => undefined)
    globalThis.fetch = originalFetch
  }
})

test('session state clears authenticated resources only once', async () => {
  const session = await readFile(
    new URL('../src/state/session.ts', import.meta.url),
    'utf8'
  )

  assert.match(
    session,
    /if \(state\.status === 'anonymous'\) \{[\s\S]*?return[\s\S]*?\}[\s\S]*?rotateAuthenticationRequestScope\(\)[\s\S]*?resetWorkspaceState\(\)/
  )
  assert.match(
    session,
    /rotateAuthenticationRequestScope\(\)[\s\S]*?state\.status = 'checking'[\s\S]*?gateway\.login/
  )
})
