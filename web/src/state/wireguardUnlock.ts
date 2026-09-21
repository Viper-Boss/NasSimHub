import { reactive } from 'vue'

// The WireGuard traffic chart stays hidden until someone types "wireguard".
//
// This file holds the flag and nothing else. The phrase that sets it, and the
// keyboard listener that used to live here for this reveal alone, moved to
// state/hiddenCommands.ts when the other reveals were given the same
// treatment: typing on the page works for all of them now, not just this one.
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
