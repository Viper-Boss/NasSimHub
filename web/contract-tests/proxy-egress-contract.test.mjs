import assert from 'node:assert/strict'
import test from 'node:test'
import { parseProxyCollectionResponse } from '../src/api/contract.ts'

// A tunnel proxy has no line, and the client has to agree with the server about
// that or the whole page dies.
//
// The server deliberately stores a WireGuard-egress proxy with an EMPTY
// line_id -- requiring one would put a meaningless "which SIM?" choice in front
// of the operator and would break the proxy whenever that SIM went away. There
// is a server test asserting exactly that
// (internal/networkruntime/proxy_egress_test.go). The client contract was never
// relaxed to match, so the first tunnel proxy anyone created made the entire
// collection fail to parse: the network page showed
// "proxy_collection.proxies[0] 缺少 line_id" in place of the list, and every
// other proxy disappeared with it.

function proxyRow(overrides = {}) {
  return {
    id: 'proxy-1',
    name: '隧道代理',
    line_id: '',
    enabled: true,
    mode: 'socks5',
    listen_address: '127.0.0.1',
    listen_port: 1080,
    auth_enabled: false,
    username: '',
    has_password: false,
    egress: 'wireguard',
    egress_interface: 'wg-nassim',
    egress_dns: ['10.77.0.2'],
    revision: 3,
    applied_revision: 3,
    apply_state: 'applied',
    created_at: '2026-09-11T00:00:00Z',
    updated_at: '2026-09-11T00:00:00Z',
    ...overrides
  }
}

test('a tunnel proxy with no line parses', () => {
  const proxies = parseProxyCollectionResponse({ proxies: [proxyRow()] })
  assert.equal(proxies.length, 1)
  assert.equal(proxies[0].line_id, '')
})

// The egress fields have to survive the parse, and for a second reason. Without
// them the editor loads a saved tunnel proxy with egress undefined, which reads
// as 'line' -- so opening a tunnel proxy and saving it silently converted it
// back to a line proxy and dropped its interface.
test('a tunnel proxy keeps its egress, interface and DNS', () => {
  const [proxy] = parseProxyCollectionResponse({ proxies: [proxyRow()] })
  assert.equal(proxy.egress, 'wireguard')
  assert.equal(proxy.egress_interface, 'wg-nassim')
  assert.deepEqual(proxy.egress_dns, ['10.77.0.2'])
})

// A line proxy still needs its line: that is the interface its traffic leaves
// through, and a row without one is a broken record rather than a tunnel.
test('a line proxy with no line is still rejected', () => {
  assert.throws(
    () =>
      parseProxyCollectionResponse({
        proxies: [proxyRow({ egress: 'line', line_id: '', egress_interface: undefined })]
      }),
    /line_id/
  )
})

// Records written before the egress existed have no field at all and must read
// as line proxies, not as tunnels with a missing interface.
test('a record from before the egress existed reads as a line proxy', () => {
  const [proxy] = parseProxyCollectionResponse({
    proxies: [
      proxyRow({
        line_id: 'line-a',
        egress: undefined,
        egress_interface: undefined,
        egress_dns: undefined
      })
    ]
  })
  assert.equal(proxy.egress, 'line')
  assert.equal(proxy.line_id, 'line-a')
  assert.equal(proxy.egress_interface, undefined)
})
