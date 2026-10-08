import assert from 'node:assert/strict'
import test from 'node:test'
import { createRemoteTestTone } from '../src/state/callTone.ts'

function fixture() {
  const connections = [], schedule = [], stops = []
  let disconnected = 0
  const envelope = { gain: { value: 0, setValueAtTime: (...args) => schedule.push(args), linearRampToValueAtTime: (...args) => schedule.push(args) }, connect: to => { connections.push(to); return to }, disconnect: () => { disconnected++ } }
  const oscillator = { frequency: { value: 0 }, onended: null, connect: to => { connections.push(to); return to }, disconnect: () => { disconnected++ }, start: at => schedule.push(['start', at]), stop: at => stops.push(at) }
  const destination = { name: 'remote-stream' }, speakers = { name: 'local-speakers' }
  const context = { currentTime: 10, destination: speakers, createOscillator: () => oscillator, createGain: () => envelope }
  return { context, destination, speakers, oscillator, connections, schedule, stops, disconnected: () => disconnected }
}
test('test tone uses only outgoing media and ends after three quiet ramps', async () => {
  const f = fixture(), tone = createRemoteTestTone(f.context, f.destination)
  assert.ok(f.connections.includes(f.destination))
  assert.ok(!f.connections.includes(f.speakers))
  assert.equal(f.oscillator.frequency.value, 880)
  assert.equal(f.schedule.filter(([value]) => value === .12).length, 6)
  assert.equal(f.stops[0], 11.55)
  f.oscillator.onended()
  await tone.done
  assert.equal(f.disconnected(), 2)
})
test('hanging up cancels and releases a pending tone immediately and once', async () => {
  const f = fixture(), tone = createRemoteTestTone(f.context, f.destination)
  tone.cancel(); tone.cancel()
  await tone.done
  assert.equal(f.disconnected(), 2)
  assert.equal(f.oscillator.onended, null)
  assert.equal(f.stops.length, 2)
})
