import { ref } from 'vue'
let saved = false
try { saved = localStorage.getItem('homesim.advanced-network') === 'enabled' } catch { /* Optional preference. */ }
export const advancedNetworkVisible = ref(saved)
export function revealAdvancedNetwork() {
  advancedNetworkVisible.value = true
  try { localStorage.setItem('homesim.advanced-network', 'enabled') } catch { /* Session-only fallback. */ }
}
