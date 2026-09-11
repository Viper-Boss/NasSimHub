import { reactive } from 'vue'

// The DHT settings section stays out of the settings menu, and the DHT
// indicator stays out of the header, until the operator types "dht" on the dial
// pad. Same gesture and same storage shape as the NKN reveal beside it.
//
// The reason for hiding it is not secrecy: the DHT runs by default and needs no
// attention in normal use, so its controls are second-line plumbing rather than
// something to put in front of everyone.
const STORAGE_KEY = 'modemdeck.dhtUnlocked'

function restore(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

export const dhtUnlockState = reactive({
  unlocked: restore()
})

export function unlockDHT(): void {
  if (dhtUnlockState.unlocked) return
  dhtUnlockState.unlocked = true
  try {
    window.localStorage.setItem(STORAGE_KEY, '1')
  } catch {
    // Privacy-mode failures only cost the unlock on the next reload.
  }
}
