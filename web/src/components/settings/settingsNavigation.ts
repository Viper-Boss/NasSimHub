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
  | 'nkn'
  | 'nkn-node'
  | 'nkn-network'
  | 'wireguard'
  | 'connectivity'
  | 'diagnostics'
  | 'about'

export type SettingsSectionGroup = 'personal' | 'communication' | 'network' | 'management'

export type SettingsVisibility = {
  isAdmin: boolean
  canPairIOS: boolean
  advancedNetwork?: boolean
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
  'nkn',
  'nkn-node',
  'nkn-network',
  'wireguard',
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
  'nkn',
  'wireguard',
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
  if (section === 'wireguard') return 'network'
  if (['audio', 'devices', 'contacts', 'telegram', 'bark', 'sms-forward'].includes(section)) return 'communication'
  if (['f50', 'net-switches', 'nkn', 'connectivity', 'pairing'].includes(section)) return 'network'
  return PERSONAL_SECTIONS.has(section) ? 'personal' : 'management'
}

export function visibleSettingsSectionIDs(
  visibility: SettingsVisibility
): SettingsSection[] {
  return SETTINGS_SECTION_ORDER.filter(section => {
    if (section === 'wireguard') return visibility.isAdmin && visibility.advancedNetwork === true
    if (section === 'pairing') return visibility.canPairIOS
    if (ADMIN_ONLY_SECTIONS.has(section)) return visibility.isAdmin
    return true
  })
}
