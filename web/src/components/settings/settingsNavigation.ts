export type SettingsSection =
  | 'preferences'
  | 'security'
  | 'users'
  | 'contacts'
  | 'audio'
  | 'call-services'
  | 'pairing'
  | 'devices'
  | 'telegram'
  | 'bark'
  | 'sms-forward'
  | 'forwarding'
  | 'f50'
  | 'airsim'
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
  'call-services',
  'pairing',
  'contacts',
  'users',
  'devices',
  'telegram',
  'forwarding',
  'f50',
  'airsim',
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
  // Forwarding is a change to the subscriber's own network service and can
  // divert every incoming call, so it is not something a non-admin user of a
  // shared gateway should be able to set.
  'call-services',
  'nkn-node',
  'nkn-network',
  'wireguard',
  'dht',
  'forwarding',
  'f50',
  // Pairs a phone that then acts for the administrator who paired it.
  'airsim',
  'net-switches',
  'users',
  'connectivity',
  'diagnostics',
  'about'
])

export function settingsSectionGroup(section: SettingsSection): SettingsSectionGroup {
  if (section === 'nkn-node' || section === 'nkn-network') return 'network'
  if (section === 'wireguard' || section === 'dht') return 'network'
  if (['audio', 'call-services', 'devices', 'contacts', 'telegram', 'forwarding', 'airsim'].includes(section)) return 'communication'
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
