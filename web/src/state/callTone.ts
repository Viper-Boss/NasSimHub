// A finite diagnostic signal sent only to the call's outgoing media stream.
// It never attaches to AudioContext.destination (the local loudspeakers).
export function createRemoteTestTone(context: AudioContext, destination: AudioNode): {
  done: Promise<void>
  cancel: () => void
} {
  const oscillator = context.createOscillator()
  const envelope = context.createGain()
  oscillator.frequency.value = 880
  envelope.gain.value = 0
  oscillator.connect(envelope).connect(destination)
  const start = context.currentTime + 0.05
  for (let i = 0; i < 3; i++) {
    const at = start + i * 0.6
    envelope.gain.setValueAtTime(0, at)
    envelope.gain.linearRampToValueAtTime(0.12, at + 0.01)
    envelope.gain.setValueAtTime(0.12, at + 0.25)
    envelope.gain.linearRampToValueAtTime(0, at + 0.27)
  }
  let resolveDone: () => void = () => undefined
  const done = new Promise<void>(resolve => { resolveDone = resolve })
  let ended = false
  const cleanup = (): void => {
    if (ended) return
    ended = true
    oscillator.onended = null
    oscillator.disconnect()
    envelope.disconnect()
    resolveDone()
  }
  oscillator.onended = cleanup
  oscillator.start(start)
  oscillator.stop(start + 1.5)
  return { done, cancel: () => { if (!ended) oscillator.stop(); cleanup() } }
}
