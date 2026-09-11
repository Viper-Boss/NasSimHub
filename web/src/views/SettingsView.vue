<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { useAdvancedI18n as useI18n } from '../i18n/useAdvancedI18n'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity,
  ArrowLeft,
  AudioLines,
  ChartNoAxesCombined,
  ContactRound,
  Globe2,
  House,
  Info,
  KeyRound,
  MessageSquareText,
  LoaderCircle,
  LogOut,
  RadioTower,
  BellRing,
  Network,
  PhoneForwarded,
  RefreshCw,
  Send,
  Wifi,
  ShieldCheck,
  SlidersHorizontal,
  UserRound,
  Volume2
} from '@lucide/vue'
import StatePanel from '../components/StatePanel.vue'
import PageContentFrame from '../components/PageContentFrame.vue'
import SettingsAsyncBoundary from '../components/settings/SettingsAsyncBoundary.vue'
import SettingsContentTransition from '../components/settings/SettingsContentTransition.vue'
import {
  settingsSectionGroup,
  visibleSettingsSectionIDs
} from '../components/settings/settingsNavigation'
import { nknUnlockState } from '../state/nknUnlock'
import { dhtUnlockState } from '../state/dhtUnlock'
import type {
  SettingsSection,
  SettingsSectionGroup
} from '../components/settings/settingsNavigation'
import {
  settingsSkeletonShape,
  type SettingsSkeletonShape
} from '../components/settings/settingsSkeleton'
import { fixtureMode } from '../api/client'
import { logout as logoutSession, sessionState } from '../state/session'
import {
  bootstrapResource,
  devicesResource,
  loadBootstrap,
  loadDevices,
  presentModuleLines
} from '../state/workspace'
import { isRegisteredNetwork } from '../utils/operatorNetwork'
import { advancedNetworkVisible } from '../state/advancedNetwork'

type SettingsSectionDefinition = {
  id: SettingsSection
  label: string
  description: string
  icon: typeof RadioTower
}

const loadAccountPreferencesPanel = () =>
  import('../components/AccountPreferencesPanel.vue')
const loadSecuritySettingsPanel = () =>
  import('../components/SecuritySettingsPanel.vue')
const loadUserSettingsPanel = () => import('../components/UserSettingsPanel.vue')
const loadAudioSettingsForm = () => import('../components/AudioSettingsForm.vue')
const loadAboutSettingsPanel = () => import('../components/AboutSettingsPanel.vue')
const loadContactSyncSettings = () => import('../components/ContactSyncSettings.vue')
const loadDeviceConfigurationPanel = () =>
  import('../components/DeviceConfigurationPanel.vue')
const loadDiagnosticsPanel = () => import('../components/DiagnosticsPanel.vue')
const NasPreflightPanel = defineAsyncComponent(() => import('../components/NasPreflightPanel.vue'))
const loadPairingSettingsPanel = () =>
  import('../components/PairingSettingsPanel.vue')
const loadConnectivitySettingsPanel = () =>
  import('../components/ConnectivitySettingsPanel.vue')
const loadTelegramSettingsForm = () => import('../components/TelegramSettingsForm.vue')
const loadBarkSettingsPanel = () => import('../components/settings/BarkSettingsPanel.vue')
const loadSmsForwardPanel = () => import('../components/settings/SmsForwardPanel.vue')
const loadNetworkSwitchesPanel = () => import('../components/settings/NetworkSwitchesPanel.vue')
const loadF50DevicePanel = () => import('../components/settings/F50DevicePanel.vue')

const AccountPreferencesPanel = defineAsyncComponent(loadAccountPreferencesPanel)
const SecuritySettingsPanel = defineAsyncComponent(loadSecuritySettingsPanel)
const UserSettingsPanel = defineAsyncComponent(loadUserSettingsPanel)
const AudioSettingsForm = defineAsyncComponent(loadAudioSettingsForm)
const HomeSIMServicesPanel = defineAsyncComponent(() => import('../components/HomeSIMServicesPanel.vue'))
const AboutSettingsPanel = defineAsyncComponent(loadAboutSettingsPanel)
const ContactSyncSettings = defineAsyncComponent(loadContactSyncSettings)
const DeviceConfigurationPanel = defineAsyncComponent(loadDeviceConfigurationPanel)
const DiagnosticsPanel = defineAsyncComponent(loadDiagnosticsPanel)
const PairingSettingsPanel = defineAsyncComponent(loadPairingSettingsPanel)
const ConnectivitySettingsPanel = defineAsyncComponent(
  loadConnectivitySettingsPanel
)
const TelegramSettingsForm = defineAsyncComponent(loadTelegramSettingsForm)
const BarkSettingsPanel = defineAsyncComponent(loadBarkSettingsPanel)
const SmsForwardPanel = defineAsyncComponent(loadSmsForwardPanel)
const NetworkSwitchesPanel = defineAsyncComponent(loadNetworkSwitchesPanel)
const F50DevicePanel = defineAsyncComponent(loadF50DevicePanel)
const loadWireGuardPanel = () => import('../components/settings/WireGuardPanel.vue')
const WireGuardPanel = defineAsyncComponent(loadWireGuardPanel)
const loadDHTPanel = () => import('../components/settings/DHTPanel.vue')
const DHTPanel = defineAsyncComponent(loadDHTPanel)
const loadCallServicesPanel = () => import('../components/settings/CallServicesPanel.vue')
const CallServicesPanel = defineAsyncComponent(loadCallServicesPanel)
const loadNknNodePanel = () => import('../components/settings/NknNodePanel.vue')
const loadNknNetworkPanel = () => import('../components/settings/NknNetworkPanel.vue')
const NknNodePanel = defineAsyncComponent(loadNknNodePanel)
const NknNetworkPanel = defineAsyncComponent(loadNknNetworkPanel)
const MobileEnrollmentPanel = defineAsyncComponent(() => import('../components/MobileEnrollmentPanel.vue'))

const route = useRoute()
// Manual refresh for the devices workspace: bumping the tick remounts the
// panel so it reloads every module list and hardware snapshot from scratch.
const devicesRefreshTick = ref(0)
function refreshDevices(): void {
  devicesRefreshTick.value += 1
}
const router = useRouter()
const { t } = useI18n()
const logoutPending = ref(false)
const logoutError = ref('')
const isAdmin = computed(() => sessionState.role === 'admin')
const canManageExternalAccess = isAdmin
const canPairIOS = computed(() => sessionState.iosPairingEnabled)
const lines = computed(() => bootstrapResource.data?.lines || [])
const presentModules = computed(() =>
  presentModuleLines(lines.value, devicesResource.data)
)
const onlineModules = computed(
  () => presentModules.value.filter(line => isRegisteredNetwork(line)).length
)
const overviewSummary = computed(() =>
  t('dashboard.modulesOnline', {
    online: onlineModules.value,
    total: presentModules.value.length
  })
)
const sectionCatalog = computed<SettingsSectionDefinition[]>(() => [
  { id: 'nkn-node', label: t('settings.nknNode'), description: t('settings.nknNodeDescription'), icon: Network },
  { id: 'nkn-network', label: t('settings.nknNetwork'), description: t('settings.nknNetworkDescription'), icon: Globe2 },
  { id: 'wireguard', label: 'WireGuard', description: t('homesim.advanced.text0'), icon: ShieldCheck },
  { id: 'dht', label: t('dht.title'), description: t('dht.subtitle'), icon: Network },
  {
    id: 'call-services',
    label: t('settings.callServices'),
    description: t('settings.callServicesDescription'),
    icon: PhoneForwarded
  },
  {
    id: 'preferences',
    label: t('settings.system'),
    description: t('settings.systemDescription'),
    icon: SlidersHorizontal
  },
  {
    id: 'security',
    label: t('settings.account'),
    description: t('settings.accountDescription'),
    icon: ShieldCheck
  },
  {
    id: 'audio',
    label: t('settings.audio'),
    description: t('settings.audioDescription'),
    icon: Volume2
  },
  {
    id: 'pairing',
    label: t('settings.iosApp'),
    description: t('settings.iosAppDescription'),
    icon: KeyRound
  },
  {
    id: 'contacts',
    label: t('settings.contactsSync'),
    description: t('settings.contactsSyncDescription'),
    icon: ContactRound
  },
  {
    id: 'users',
    label: t('settings.users'),
    description: t('settings.usersDescription'),
    icon: UserRound
  },
  {
    id: 'devices',
    label: t('settings.devices'),
    description: t('settings.devicesDescription'),
    icon: RadioTower
  },
  {
    id: 'telegram',
    label: t('settings.telegram'),
    description: t('settings.telegramDescription'),
    icon: Send
  },
  {
    id: 'bark',
    label: t('settings.bark'),
    description: t('settings.barkDescription'),
    icon: BellRing
  },
  {
    id: 'sms-forward',
    label: t('settings.smsForward'),
    description: t('settings.smsForwardDescription'),
    icon: MessageSquareText
  },
  {
    id: 'f50',
    label: t('settings.f50'),
    description: t('settings.f50Description'),
    icon: Wifi
  },
  {
    id: 'net-switches',
    label: t('settings.netSwitches'),
    description: t('settings.netSwitchesDescription'),
    icon: Network
  },
  {
    id: 'connectivity',
    label: t('settings.tls'),
    description: t('settings.tlsDescription'),
    icon: Globe2
  },
  {
    id: 'diagnostics',
    label: t('settings.diagnostics'),
    description: t('settings.diagnosticsDescription'),
    icon: Activity
  },
  {
    id: 'about',
    label: t('settings.about'),
    description: t('settings.aboutDescription'),
    icon: Info
  }
])
const sections = computed<SettingsSectionDefinition[]>(() => {
  const catalog = new Map(sectionCatalog.value.map(section => [section.id, section]))
  return visibleSettingsSectionIDs({
    isAdmin: isAdmin.value,
    canPairIOS: canPairIOS.value,
    advancedNetwork: advancedNetworkVisible.value,
    nknUnlocked: nknUnlockState.unlocked,
    dhtUnlocked: dhtUnlockState.unlocked
  })
    .map(sectionID => catalog.get(sectionID))
    .filter((section): section is SettingsSectionDefinition => section !== undefined)
})
const settingsQuery = ref('')
const sectionGroups = computed(() =>
  (['personal', 'communication', 'network', 'management'] as const).map((group: SettingsSectionGroup) => ({
    id: group,
    label: ({ personal: t('homesim.advanced.text3'), communication: t('homesim.advanced.text4'), network: t('homesim.advanced.text5'), management: t('homesim.advanced.text6') })[group],
    sections: sections.value.filter(section => settingsSectionGroup(section.id) === group && `${section.label} ${section.description}`.toLocaleLowerCase().includes(settingsQuery.value.trim().toLocaleLowerCase()))
  })).filter(group => group.sections.length > 0)
)
const selectedSection = computed<SettingsSection | ''>(() => {
  const value = String(route.params.section || '')
  return sections.value.some(section => section.id === value) ? (value as SettingsSection) : ''
})
const currentTitle = computed(
  () =>
    sections.value.find(section => section.id === selectedSection.value)?.label ||
    t('settings.title')
)
const settingsLoadingShape = computed<SettingsSkeletonShape>(() =>
  settingsSkeletonShape(selectedSection.value)
)
const settingsSkeletonHasSelection = computed(() => {
  if (selectedSection.value === 'users') {
    return typeof route.query.user === 'string' || route.query.newUser === '1'
  }
  if (selectedSection.value === 'telegram') {
    return typeof route.query.bot === 'string' || route.query.newBot === '1'
  }
  return false
})

watch(
  selectedSection,
  section => {
    if (section === 'devices') void loadBootstrap()
  },
  { immediate: true }
)

watch(
  [() => route.params.section, () => sessionState.role, canPairIOS],
  ([value]) => {
    const section = String(value || '')
    let destination: SettingsSection | '' = ''
    if (section === 'account') {
      destination = sessionState.role === 'admin' ? 'users' : 'preferences'
    } else if (section === 'external-access') {
      destination = canManageExternalAccess.value
        ? 'connectivity'
        : canPairIOS.value
          ? 'pairing'
          : ''
    } else if (section === 'web-certificate') {
      destination = canManageExternalAccess.value ? 'connectivity' : ''
    } else if (section === 'ios') {
      destination = canPairIOS.value ? 'pairing' : ''
    }
    if (!destination) return
    void router.replace({
      name: 'settings',
      params: { section: destination },
      query: route.query
    })
  },
  { immediate: true }
)

function openSection(section: SettingsSection): void {
  void router.push({ name: 'settings', params: { section } })
}

function preloadSection(section: SettingsSection): void {
  const loader = (() => {
    switch (section) {
      case 'preferences':
        return loadAccountPreferencesPanel
      case 'security':
        return loadSecuritySettingsPanel
      case 'users':
        return loadUserSettingsPanel
      case 'contacts':
        return loadContactSyncSettings
      case 'audio':
        return loadAudioSettingsForm
      case 'devices':
        return loadDeviceConfigurationPanel
      case 'telegram':
        return loadTelegramSettingsForm
      case 'bark':
        return loadBarkSettingsPanel
      case 'sms-forward':
        return loadSmsForwardPanel
      case 'f50':
        return loadF50DevicePanel
      case 'net-switches':
        return loadNetworkSwitchesPanel
      case 'wireguard':
        return loadWireGuardPanel
      case 'dht':
        return loadDHTPanel
      case 'call-services':
        return loadCallServicesPanel
      case 'nkn-node':
        return loadNknNodePanel
      case 'nkn-network':
        return loadNknNetworkPanel
      case 'pairing':
        return loadPairingSettingsPanel
      case 'connectivity':
        return loadConnectivitySettingsPanel
      case 'diagnostics':
        return loadDiagnosticsPanel
      case 'about':
        return loadAboutSettingsPanel
    }
  })()
  void loader().catch(() => undefined)
}

function backToSettings(): void {
  if (
    selectedSection.value === 'users' &&
    (typeof route.query.user === 'string' || route.query.newUser === '1')
  ) {
    void router.push({
      name: 'settings',
      params: { section: 'users' },
      query: { ...route.query, user: undefined, newUser: undefined }
    })
    return
  }
  if (
    selectedSection.value === 'telegram' &&
    (typeof route.query.bot === 'string' || route.query.newBot === '1')
  ) {
    void router.push({
      name: 'settings',
      params: { section: 'telegram' },
      query: { ...route.query, bot: undefined, newBot: undefined }
    })
    return
  }
  void router.push({ name: 'settings', params: { section: '' } })
}

function openDashboard(): void {
  void router.push({
    name: 'dashboard',
    query: { item: 'overview', from: 'settings' }
  })
}

function openTraffic(): void {
  void router.push({
    name: 'traffic',
    query: { from: 'settings' }
  })
}

async function logout(): Promise<void> {
  if (logoutPending.value) return
  logoutPending.value = true
  logoutError.value = ''
  try {
    await logoutSession()
    await router.replace({ name: 'login' })
  } catch (error) {
    logoutError.value =
      error instanceof Error ? error.message : t('settings.logoutFailed')
  } finally {
    logoutPending.value = false
  }
}

onMounted(() => {
  void Promise.all([loadBootstrap(), loadDevices()])
})
</script>

<template>
  <section class="workspace settings-workspace" :class="{ 'has-selection': selectedSection }">
    <aside class="list-pane settings-list-pane">
      <header class="pane-header"><h1>{{ t('settings.title') }}</h1></header>
      <div class="settings-search"><input v-model="settingsQuery" type="search" :placeholder="t('homesim.settingsSearch')" :aria-label="t('homesim.settingsSearch')" /><p v-if="!sectionGroups.length" role="status">{{ t('homesim.settingsNoResults') }}</p></div>
      <div class="item-list settings-list">
        <button
          class="list-item settings-overview-link"
          type="button"
          @click="openDashboard"
        >
          <span class="settings-icon"><House :size="19" /></span>
          <span class="list-item__content">
            <strong>{{ t('dashboard.mobileOverview') }}</strong>
            <small>{{ overviewSummary }}</small>
          </span>
        </button>
        <button
          class="list-item settings-overview-link settings-traffic-link"
          type="button"
          @click="openTraffic"
        >
          <span class="settings-icon"><ChartNoAxesCombined :size="19" /></span>
          <span class="list-item__content">
            <strong>{{ t('shell.traffic') }}</strong>
            <small>{{ t('traffic.overview') }}</small>
          </span>
        </button>
        <section
          v-for="group in sectionGroups"
          :key="group.id"
          class="settings-nav-group"
          :aria-labelledby="`settings-group-${group.id}`"
        >
          <h2 :id="`settings-group-${group.id}`" class="settings-nav-group__title">
            {{ group.label }}
          </h2>
          <button
            v-for="section in group.sections"
            :key="section.id"
            class="list-item"
            :class="{ 'is-selected': selectedSection === section.id }"
            :aria-current="selectedSection === section.id ? 'page' : undefined"
            type="button"
            @focus="preloadSection(section.id)"
            @pointerenter="preloadSection(section.id)"
            @click="openSection(section.id)"
          >
            <span class="settings-icon"><component :is="section.icon" :size="19" /></span>
            <span class="list-item__content">
              <strong>{{ section.label }}</strong>
              <small>{{ section.description }}</small>
            </span>
          </button>
        </section>
        <footer class="settings-account">
          <span class="settings-account__icon"><UserRound :size="19" /></span>
          <span class="settings-account__identity">
            <strong>{{ sessionState.username || t('settings.account') }}</strong>
            <small>
              {{
                sessionState.role === 'admin'
                  ? t('settings.administratorAccount')
                  : t('settings.memberAccount')
              }}
            </small>
            <small v-if="logoutError" class="settings-account__error" role="alert">
              {{ logoutError }}
            </small>
          </span>
          <button
            class="icon-button"
            type="button"
            :title="t('settings.logout')"
            :aria-label="t('settings.logout')"
            :disabled="fixtureMode || logoutPending"
            @click="logout"
          >
            <LoaderCircle v-if="logoutPending" class="spin" :size="19" />
            <LogOut v-else :size="19" />
          </button>
        </footer>
      </div>
    </aside>

    <article class="detail-pane settings-detail-pane">
      <template v-if="selectedSection">
        <header class="settings-detail-header">
          <button
            class="icon-button mobile-back"
            type="button"
            :title="t('settings.back')"
            @click="backToSettings"
          >
            <ArrowLeft :size="20" />
          </button>
          <h2>{{ currentTitle }}</h2>
        </header>

        <div v-if="isAdmin && ['devices', 'diagnostics', 'about'].includes(selectedSection)" class="homesim-system-header">
          <nav class="homesim-route-links" :aria-label="t('homesim.system')">
            <RouterLink :class="{ 'is-current': selectedSection === 'devices' }" :to="{ name: 'settings', params: { section: 'devices' } }"><RadioTower :size="17" />{{ t('settings.devices') }}</RouterLink>
            <RouterLink :class="{ 'is-current': selectedSection === 'diagnostics' }" :to="{ name: 'settings', params: { section: 'diagnostics' } }"><Activity :size="17" />{{ t('settings.diagnostics') }}</RouterLink>
            <RouterLink :to="{ name: 'recordings' }"><AudioLines :size="17" />{{ t('shell.recordings') }}</RouterLink>
            <RouterLink :to="{ name: 'traffic' }"><ChartNoAxesCombined :size="17" />{{ t('shell.traffic') }}</RouterLink>
            <button
              v-if="selectedSection === 'devices'"
              class="secondary-button homesim-route-links__refresh"
              type="button"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="refreshDevices"
            >
              <RefreshCw :size="15" />{{ t('common.refresh') }}
            </button>
          </nav>
        </div>
        <p v-if="selectedSection === 'telegram'" class="homesim-integration-note homesim-integration-note--header">{{ t('homesim.forwardHint') }}</p>

        <SettingsContentTransition :content-key="selectedSection">
          <SettingsAsyncBoundary
            :loading-title="t('common.loading')"
            :loading-shape="settingsLoadingShape"
            :has-selection="settingsSkeletonHasSelection"
          >
            <div v-if="selectedSection === 'preferences'" class="settings-content">
              <PageContentFrame mode="reading">
                <AccountPreferencesPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'security'" class="settings-content">
              <PageContentFrame mode="reading">
                <SecuritySettingsPanel />
              </PageContentFrame>
            </div>

            <div
              v-else-if="selectedSection === 'users'"
              class="settings-content settings-content--master-detail"
            >
              <UserSettingsPanel />
            </div>

            <div v-else-if="selectedSection === 'contacts'" class="settings-content">
              <PageContentFrame mode="reading">
                <ContactSyncSettings />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'audio'" class="settings-content">
              <PageContentFrame mode="reading">
                <AudioSettingsForm />
                <details v-if="isAdmin" class="settings-experimental"><summary class="ui-disclosure-summary">{{ t('homesim.settingsExperimental') }}</summary><HomeSIMServicesPanel /></details>
              </PageContentFrame>
            </div>

            <div
              v-else-if="selectedSection === 'devices'"
              class="settings-content settings-content--master-detail"
            >
              <DeviceConfigurationPanel :key="`devices-${devicesRefreshTick}`" />
            </div>

            <div
              v-else-if="selectedSection === 'telegram'"
              class="settings-content settings-content--master-detail"
            >
              <TelegramSettingsForm />
            </div>

            <div v-else-if="selectedSection === 'bark'" class="settings-content">
              <PageContentFrame mode="reading">
                <BarkSettingsPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'sms-forward'" class="settings-content">
              <PageContentFrame mode="reading">
                <SmsForwardPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'f50'" class="settings-content">
              <PageContentFrame mode="reading">
                <F50DevicePanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'net-switches'" class="settings-content">
              <PageContentFrame mode="reading">
                <NetworkSwitchesPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'nkn-node'" class="settings-content">
              <PageContentFrame mode="reading"><NknNodePanel /></PageContentFrame>
            </div>
            <div v-else-if="selectedSection === 'nkn-network'" class="settings-content">
              <PageContentFrame mode="reading"><NknNetworkPanel /></PageContentFrame>
            </div>
            <div v-else-if="selectedSection === 'wireguard'" class="settings-content">
              <PageContentFrame mode="reading"><WireGuardPanel /></PageContentFrame>
            </div>
            <div v-else-if="selectedSection === 'dht'" class="settings-content">
              <PageContentFrame mode="reading"><DHTPanel /></PageContentFrame>
            </div>
            <div v-else-if="selectedSection === 'call-services'" class="settings-content">
              <PageContentFrame mode="reading"><CallServicesPanel /></PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'pairing'" class="settings-content">
              <PageContentFrame mode="reading">
                <MobileEnrollmentPanel />
                <details class="homesim-sim-notice"><summary class="ui-disclosure-summary">{{ t('homesim.advanced.text7') }}</summary><PairingSettingsPanel /></details>
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'connectivity'" class="settings-content">
              <PageContentFrame mode="reading">
                <ConnectivitySettingsPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'diagnostics'" class="settings-content">
              <PageContentFrame mode="fluid">
                <p class="homesim-integration-note">{{ t('homesim.systemHint') }}</p>
                <NasPreflightPanel />
                <DiagnosticsPanel />
              </PageContentFrame>
            </div>

            <div v-else-if="selectedSection === 'about'" class="settings-content">
              <PageContentFrame mode="reading">
                <AboutSettingsPanel />
              </PageContentFrame>
            </div>
          </SettingsAsyncBoundary>
        </SettingsContentTransition>
      </template>

      <StatePanel v-else state="empty" :title="t('settings.selectSetting')" />
    </article>
  </section>
</template>

<style scoped>
.settings-search { padding: 0 16px 12px; }
.settings-experimental { margin-top: 24px; padding-top: 16px; border-top: 1px solid var(--border); }
.settings-search input { width: 100%; min-height: 40px; border: 1px solid var(--border); border-radius: var(--radius-control); padding: 8px 12px; background: var(--surface); color: var(--text); }
.settings-search p { color: var(--muted); font-size: 13px; padding-top: 8px; }
.settings-nav-group .list-item { border-radius: var(--radius-control); margin-block: 2px; }
.settings-nav-group .list-item__content small { line-height: 1.5; overflow-wrap: anywhere; }
.settings-nav-group {
  display: grid;
  padding-block: 10px 4px;
}

.settings-nav-group + .settings-nav-group {
  margin-top: 6px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

.settings-nav-group__title {
  margin: 0;
  padding: 0 12px 6px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 750;
  letter-spacing: 0.08em;
  line-height: 1.2;
  text-transform: uppercase;
}

.settings-content {
  flex: 1 1 auto;
  container-type: inline-size;
}

.settings-content.settings-content--master-detail {
  display: flex;
  min-height: 0;
  padding: 0;
  overflow: hidden;
}

/*
 * A compact desktop cannot carry the app rail, settings directory, resource
 * rail, and editor at once. Keep the desktop shell, but make the settings
 * directory and selected section separate steps until the workspace widens.
 */
@media (min-width: 861px) and (max-width: 1100px) {
  .settings-workspace {
    grid-template-columns: minmax(0, 1fr);
  }

  .settings-workspace.has-selection > .settings-list-pane,
  .settings-workspace:not(.has-selection) > .settings-detail-pane {
    display: none;
  }

  .settings-detail-header .mobile-back {
    display: inline-grid;
  }
}
</style>
