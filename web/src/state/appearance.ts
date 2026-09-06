import { ref } from 'vue'

// Appearance only. Never store account credentials or communication data here.
const key = 'homesim.appearance'
export const appearance = ref<'dark' | 'light'>('dark')

export function initializeAppearance(): void {
  try { appearance.value = localStorage.getItem(key) === 'light' ? 'light' : 'dark' } catch { /* Storage may be disabled. */ }
  applyAppearance()
}

function applyAppearance(): void {
  document.documentElement.dataset.theme = appearance.value
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', appearance.value === 'dark' ? '#0b101b' : '#eef2f7')
}

export function toggleAppearance(): void {
  appearance.value = appearance.value === 'dark' ? 'light' : 'dark'
  applyAppearance()
  try { localStorage.setItem(key, appearance.value) } catch { /* Keep the current session usable. */ }
}
