import { reactive, watch } from 'vue'

// Profile avatar chosen by the user in settings (data URL). Stored locally
// in this browser: no backend round-trip, instantly reflected in the rail.
const STORAGE_KEY = 'modemdeck.profileAvatar'

function restore(): string {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    return typeof raw === 'string' && raw.startsWith('data:image/') ? raw : ''
  } catch {
    return ''
  }
}

export const profileAvatarState = reactive({
  avatar: restore()
})

watch(
  () => profileAvatarState.avatar,
  value => {
    try {
      if (value) window.localStorage.setItem(STORAGE_KEY, value)
      else window.localStorage.removeItem(STORAGE_KEY)
    } catch {
      // Quota or privacy-mode failures must never break the settings UI.
    }
  }
)

export function setProfileAvatar(value: string): void {
  profileAvatarState.avatar = value
}
