import { reactive } from 'vue'

// The WireGuard traffic chart stays hidden until someone types "wireguard".
//
// Unlike the NKN and DHT reveals, which live on the dial pad, this one listens
// on the window: the chart appears on two different pages and the dial pad is
// not open on either of them, so requiring a detour through the dialer would
// make the gesture undiscoverable exactly where it is meant to be used. The
// dial pad still works too, for phones with no keyboard.
const STORAGE_KEY = 'modemdeck.wireguardChartUnlocked'
const PHRASE = 'wireguard'

function restore(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

export const wireGuardUnlockState = reactive({
  unlocked: restore()
})

export function unlockWireGuardChart(): void {
  if (wireGuardUnlockState.unlocked) return
  wireGuardUnlockState.unlocked = true
  try {
    window.localStorage.setItem(STORAGE_KEY, '1')
  } catch {
    // Privacy-mode failures only cost the unlock on the next reload.
  }
}

/** Forgets the unlock. Exposed so the reveal can be undone without dev tools. */
export function lockWireGuardChart(): void {
  wireGuardUnlockState.unlocked = false
  try {
    window.localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Nothing to do: the in-memory flag is already cleared.
  }
}

let typed = ''
let listening = false

function onKeydown(event: KeyboardEvent): void {
  // Never consume keystrokes aimed at a field. Someone typing "wireguard" into
  // a search box or a peer name means it as text, not as a gesture.
  const target = event.target as HTMLElement | null
  if (target) {
    const tag = target.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable) return
  }
  // Modified keys are shortcuts, not typing.
  if (event.ctrlKey || event.metaKey || event.altKey) return
  if (event.key.length !== 1) return

  typed = (typed + event.key.toLowerCase()).slice(-PHRASE.length)
  if (typed === PHRASE) {
    typed = ''
    unlockWireGuardChart()
  }
}

/**
 * Starts listening for the phrase. Safe to call more than once; the listener is
 * installed only on the first call and lives as long as the app does, so the
 * gesture works from any page.
 */
export function listenForWireGuardPhrase(): void {
  if (listening || typeof window === 'undefined') return
  listening = true
  window.addEventListener('keydown', onKeydown)
}
