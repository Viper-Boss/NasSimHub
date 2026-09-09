import { reactive } from 'vue'

// The NKN settings sections stay out of the settings menu until the user
// types "nkn" on the dial pad. The unlock is remembered locally so the
// sections stay visible on this browser afterwards.
const STORAGE_KEY = 'modemdeck.nknUnlocked'

function restore(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

export const nknUnlockState = reactive({
  unlocked: restore()
})

export function unlockNknMenu(): void {
  nknUnlockState.unlocked = true
  try {
    window.localStorage.setItem(STORAGE_KEY, '1')
  } catch {
    // Privacy-mode failures only reset the unlock on next reload.
  }
}
