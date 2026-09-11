export type SettingsSection =
  | 'preferences'
  | 'security'
  | 'users'
  | 'contacts'
  | 'audio'
  | 'pairing'
  | 'devices'
  | 'telegram'
  | 'bark'
  | 'sms-forward'
  | 'f50'
  | 'net-switches'
  | 'nkn-network'
  | 'nkn-node'
  | 'wireguard'
  | 'dht'
  | 'connectivity'
  | 'diagnostics'
  | 'about'

export type SettingsSectionGroup = 'personal' | 'communication' | 'network' | 'management'

export type SettingsVisibility = {
  isAdmin: boolean
  canPairIOS: boolean
  advancedNetwork?: boolean
  nknUnlocked?: boolean
  dhtUnlocked?: boolean
}

export const SETTINGS_SECTION_ORDER: readonly SettingsSection[] = [
  'preferences',
  'security',
  'audio',
  'pairing',
  'contacts',
  'users',
  'devices',
  'telegram',
  'bark',
  'sms-forward',
  'f50',
  'net-switches',
  'nkn-network',
  'nkn-node',
  'wireguard',
  'dht',
  'connectivity',
  'diagnostics',
  'about'
]

const PERSONAL_SECTIONS = new Set<SettingsSection>([
  'preferences',
  'security',
  'audio',
  'pairing',
  'contacts'
])

const ADMIN_ONLY_SECTIONS = new Set<SettingsSection>([
  'nkn-node',
  'nkn-network',
  'wireguard',
  'dht',
  'bark',
  'sms-forward',
  'f50',
  'net-switches',
  'users',
  'connectivity',
  'diagnostics',
  'about'
])

export function settingsSectionGroup(section: SettingsSection): SettingsSectionGroup {
  if (section === 'nkn-node' || section === 'nkn-network') return 'network'
  if (section === 'wireguard' || section === 'dht') return 'network'
  if (['audio', 'devices', 'contacts', 'telegram', 'bark', 'sms-forward'].includes(section)) return 'communication'
  if (['f50', 'net-switches', 'connectivity', 'pairing'].includes(section)) return 'network'
  return PERSONAL_SECTIONS.has(section) ? 'personal' : 'management'
}

export function visibleSettingsSectionIDs(
  visibility: SettingsVisibility
): SettingsSection[] {
  return SETTINGS_SECTION_ORDER.filter(section => {
    if (section === 'wireguard') return visibility.isAdmin && visibility.advancedNetwork === true
    // The DHT runs by default, but it is second-line plumbing: the menu entry
    // stays out of the way until the operator asks for it by name.
    if (section === 'dht') return visibility.isAdmin && visibility.dhtUnlocked === true
    if (section === 'nkn-node' || section === 'nkn-network') {
      return visibility.isAdmin && visibility.nknUnlocked === true
    }
    if (section === 'pairing') return visibility.canPairIOS
    if (ADMIN_ONLY_SECTIONS.has(section)) return visibility.isAdmin
    return true
  })
}
