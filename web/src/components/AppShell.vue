<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterView, useRoute, useRouter } from 'vue-router'
import {
  AudioLines,
  ArrowLeft,
  BellOff,
  BellRing,
  ChartNoAxesCombined,
  House,
  Menu,
  MessageSquareText,
  Phone,
  PhoneCall,
  Forward,
  Server,
  UserRound,
  Settings,
  TestTube2,
  UsersRound,
  X,
  HeartHandshake,
  MessageCircleHeart
} from '@lucide/vue'
import { fixtureMode } from '../api/client'
import {
  callState,
  initializeCallRuntime,
  isLiveCallSession,
  occupiedLineIDs,
  shutdownCallRuntime
} from '../state/call'
import {
  initializeBrowserAudio,
  shutdownAudioDevices
} from '../state/audio'
import {
  browserNotificationState,
  initializeBrowserNotifications,
  shutdownBrowserNotifications,
  toggleBrowserNotifications
} from '../state/browserNotifications'
import {
  initializeBrowserSounds,
  shutdownBrowserSounds
} from '../state/browserSounds'
import {
  initializeMessageRuntime,
  shutdownMessageRuntime
} from '../state/messageRuntime'
import { shutdownDTMFAudio } from '../state/dtmfAudio'
import { initializeForegroundCommunicationRefresh } from '../state/foregroundCommunication'
import {
  initializeRuntimeEvents,
  shutdownRuntimeEvents
} from '../state/runtimeEvents'
import { sessionState } from '../state/session'
import {
  initializeApplicationVersionChecks
} from '../state/staleAssetRecovery'
import { openDialer, showCallSurface, uiState } from '../state/ui'
import {
  bootstrapResource,
  contactsResource,
  loadBootstrap,
  loadContacts
} from '../state/workspace'
import AudioSettingsMenu from './AudioSettingsMenu.vue'
import BaseAvatar from './BaseAvatar.vue'
import AppearanceToggle from './AppearanceToggle.vue'
import DialerPanel from './DialerPanel.vue'
import GlobalSearch from './GlobalSearch.vue'
import IncomingCallModeControl from './IncomingCallModeControl.vue'
import LineStatusStrip from './LineStatusStrip.vue'
import OverlayDialog from './OverlayDialog.vue'
import PopoverTransition from './PopoverTransition.vue'
import RuntimeStatusCard from './RuntimeStatusCard.vue'
import { profileAvatarState } from '../state/profileAvatar'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const permanentDialer = ref(false)
const nonModalDialer = ref(false)
const mobileMoreOpen = ref(false)
const occupiedLineCount = computed(() => occupiedLineIDs().size)
const profileContact = computed(() =>
  contactsResource.data.find(contact => contact.id === sessionState.profileContactID)
)
// Rail avatar: user-uploaded avatar wins, then the profile contact's photo.
const railAvatar = computed(
  () => profileAvatarState.avatar || profileContact?.value?.avatar || ''
)
const runtimeCardOpen = ref(false)
const runtimeCardRoot = ref<HTMLElement | null>(null)

function toggleRuntimeCard(): void {
  runtimeCardOpen.value = !runtimeCardOpen.value
}

function handleRuntimeClickOutside(event: MouseEvent): void {
  if (!runtimeCardOpen.value) return
  if (runtimeCardRoot.value?.contains(event.target as Node)) return
  runtimeCardOpen.value = false
}

function onRuntimeKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') runtimeCardOpen.value = false
}
const activeCallPresent = computed(() =>
  callState.sessions.some(isLiveCallSession)
)
const incomingCallRinging = computed(
  () =>
    !callState.owned &&
    callState.sessions.some(
      session =>
        session.direction === 'incoming' &&
        session.phase === 'ringing' &&
        session.control_state === 'available'
    )
)
const minimizedIncomingCall = computed(
  () => incomingCallRinging.value && uiState.callMinimized
)
const minimizedActiveCall = computed(
  () =>
    callState.sessions.some(
      session => session.control_state === 'owned' && session.phase === 'active'
    ) && uiState.callMinimized
)
const mobileCallTitle = computed(() => {
  if (incomingCallRinging.value) return t('shell.openIncomingCall')
  if (activeCallPresent.value) return t('shell.returnToCall')
  return t('shell.openDialer')
})
const browserNotificationTitle = computed(() => {
  if (!browserNotificationState.secureContext) return t('shell.notificationsRequireHTTPS')
  if (!browserNotificationState.supported) return t('shell.notificationsUnsupported')
  if (browserNotificationState.requesting) return t('shell.notificationsRequesting')
  if (browserNotificationState.preferenceEnabled) return t('shell.notificationsDisable')
  if (browserNotificationState.permission === 'denied') {
    return t('shell.notificationsDenied')
  }
  if (browserNotificationState.error) return browserNotificationState.error
  return t('shell.notificationsEnable')
})

function openMobileCall(): void {
  if (activeCallPresent.value) showCallSurface()
  else openDialer()
}
let dialerMediaQuery: MediaQueryList | undefined
let nonModalDialerMediaQuery: MediaQueryList | undefined
let mountGeneration = 0
let stopApplicationVersionChecks: (() => void) | undefined
let stopForegroundCommunicationRefresh: (() => void) | undefined
const primaryNav = computed(() => [
  { name: 'dashboard', label: t('shell.home'), icon: House },
  { name: 'contacts', label: t('shell.contacts'), icon: UsersRound },
  { name: 'messages', label: t('shell.messages'), icon: MessageSquareText },
  { name: 'calls', label: t('shell.calls'), icon: Phone },
  { name: 'recordings', label: t('shell.recordings'), icon: AudioLines },
  { name: 'traffic', label: t('shell.traffic'), icon: ChartNoAxesCombined }
])
const mobileSettingsNavItem = computed(() => ({
  name: 'settings',
  label: t('shell.settings'),
  icon: Settings,
  to: { name: 'settings' }
}))
const mobileNavBeforeDial = computed(() =>
  primaryNav.value
    .filter(item => ['dashboard', 'contacts', 'messages'].includes(item.name))
    .map(item => ({ ...item, to: { name: item.name } }))
)
const mobileNavAfterDial = computed(() =>
  [
    ...primaryNav.value
      .filter(item => ['calls', 'recordings'].includes(item.name))
      .map(item => ({ ...item, to: { name: item.name } })),
    mobileSettingsNavItem.value
  ]
)
const mobileSecondaryNav = computed(() => [
  ...primaryNav.value
    .filter(item => ['contacts', 'recordings'].includes(item.name))
    .map(item => ({ ...item, to: { name: item.name } })),
  mobileSettingsNavItem.value
])
const mobileMoreCurrent = computed(() =>
  ['contacts', 'recordings', 'traffic', 'settings'].includes(String(route.name))
)
const mobileSettingsSection = computed(() => {
  if (route.name !== 'settings') return ''
  const section = String(route.params.section || '')
  const labels: Record<string, string> = {
    preferences: t('settings.system'),
    security: t('settings.account'),
    users: t('settings.users'),
    contacts: t('settings.contactsSync'),
    audio: t('settings.audio'),
    pairing: t('settings.iosApp'),
    devices: t('settings.devices'),
    telegram: t('settings.telegram'),
            bark: t('settings.bark'),
            f50: t('settings.f50'),
            'net-switches': t('settings.netSwitches'),
    connectivity: t('settings.tls'),
    diagnostics: t('settings.diagnostics'),
    about: t('settings.about')
  }
  return labels[section] || ''
})
const mobileOverviewFromSettings = computed(
  () => route.name === 'dashboard' && route.query.from === 'settings'
)
const mobileTrafficFromSettings = computed(
  () => route.name === 'traffic' && route.query.from === 'settings'
)
const settingsUserDetailOpen = computed(
  () =>
    route.name === 'settings' &&
    route.params.section === 'users' &&
    (typeof route.query.user === 'string' || route.query.newUser === '1')
)
const settingsTelegramDetailOpen = computed(
  () =>
    route.name === 'settings' &&
    route.params.section === 'telegram' &&
    (typeof route.query.bot === 'string' || route.query.newBot === '1')
)
const settingsDeviceDetailOpen = computed(
  () =>
    route.name === 'settings' &&
    route.params.section === 'devices' &&
    typeof route.query.device === 'string'
)
const mobileCommunicationDetailOpen = computed(() => {
  switch (route.name) {
    case 'dashboard':
      return typeof route.query.item === 'string'
    case 'contacts':
      return typeof route.params.contactId === 'string'
    case 'messages':
      return (
        typeof route.params.threadRef === 'string' ||
        Object.prototype.hasOwnProperty.call(route.query, 'compose')
      )
    case 'calls':
    case 'recordings':
      return typeof route.query.selected === 'string'
    default:
      return false
  }
})
const mobileShellBackVisible = computed(
  () =>
    Boolean(mobileSettingsSection.value) ||
    mobileOverviewFromSettings.value ||
    mobileTrafficFromSettings.value ||
    mobileCommunicationDetailOpen.value
)
const mobileDrilldownControlsHidden = computed(() => mobileShellBackVisible.value)
const mobileBackTitle = computed(() => {
  if (settingsUserDetailOpen.value) return t('users.backToUsers')
  if (mobileOverviewFromSettings.value || mobileTrafficFromSettings.value) {
    return t('settings.back')
  }
  switch (route.name) {
    case 'dashboard':
      return t('dashboard.backHome')
    case 'contacts':
      return t('contacts.back')
    case 'messages':
      return t('messages.back')
    case 'calls':
      return t('calls.back')
    case 'recordings':
      return t('recordings.back')
    default:
      return t('settings.back')
  }
})
const mobilePageTitle = computed(() => {
  if (route.name === 'dashboard') return t('dashboard.mobileOverview')
  if (route.name === 'settings') return mobileSettingsSection.value || t('shell.settings')
  if (route.name === 'nmobile') return t('homesim.nmobile.title')
  if (route.name === 'sponsor') return t('homesim.sponsor.title')
  return primaryNav.value.find(item => item.name === route.name)?.label || ''
})

function mobileNavItemCurrent(name: string): boolean {
  return (
    route.name === name ||
    (name === 'settings' && route.name === 'traffic')
  )
}

function toggleMobileMore(): void {
  if (mobileMoreOpen.value) {
    closeMobileMore()
    return
  }
  mobileMoreOpen.value = true
}

function closeMobileMore(): void {
  mobileMoreOpen.value = false
}

watch(
  () => sessionState.status,
  status => {
    if (status !== 'anonymous') return
    void router.replace({
      name: 'login',
      query: { redirect: route.fullPath }
    })
  }
)

watch(
  () => route.fullPath,
  () => {
    mobileMoreOpen.value = false
  }
)

async function bootstrap(): Promise<void> {
  await loadBootstrap(true)
}

async function initializeWorkspaceRuntime(currentGeneration: number): Promise<void> {
  await bootstrap()
  if (currentGeneration !== mountGeneration) return
  if (!fixtureMode && !import.meta.env.DEV) {
    stopApplicationVersionChecks = initializeApplicationVersionChecks(
      window,
      document
    )
  }
  initializeRuntimeEvents()
}

function syncDialerMode(): void {
  permanentDialer.value = dialerMediaQuery?.matches ?? false
  nonModalDialer.value = nonModalDialerMediaQuery?.matches ?? false
}

function handleMobileBack(): void {
  if (settingsUserDetailOpen.value) {
    void router.push({
      name: 'settings',
      params: { section: 'users' }
    })
    return
  }
  if (settingsTelegramDetailOpen.value) {
    void router.push({
      name: 'settings',
      params: { section: 'telegram' }
    })
    return
  }
  if (settingsDeviceDetailOpen.value) {
    void router.push({
      name: 'settings',
      params: { section: 'devices' }
    })
    return
  }
  if (mobileOverviewFromSettings.value || mobileTrafficFromSettings.value) {
    void router.push({ name: 'settings' })
    return
  }

  const query = { ...route.query }
  switch (route.name) {
    case 'dashboard':
      delete query.item
      void router.push({ name: 'dashboard', query })
      return
    case 'contacts':
      void router.push({ name: 'contacts' })
      return
    case 'messages':
      delete query.compose
      void router.push({ name: 'messages', query })
      return
    case 'calls':
      delete query.selected
      void router.push({ name: 'calls', query })
      return
    case 'recordings':
      delete query.selected
      void router.push({ name: 'recordings', query })
      return
    default:
      void router.push({ name: 'settings' })
  }
}

onMounted(() => {
  mountGeneration += 1
  const currentGeneration = mountGeneration
  void initializeBrowserAudio()
  initializeBrowserNotifications()
  initializeBrowserSounds()
  initializeCallRuntime(router)
  initializeMessageRuntime(router)
  stopForegroundCommunicationRefresh = initializeForegroundCommunicationRefresh()
  void initializeWorkspaceRuntime(currentGeneration)
  void loadContacts()
  dialerMediaQuery = window.matchMedia('(min-width: 1480px)')
  nonModalDialerMediaQuery = window.matchMedia('(min-width: 861px)')
  dialerMediaQuery.addEventListener('change', syncDialerMode)
  nonModalDialerMediaQuery.addEventListener('change', syncDialerMode)
  syncDialerMode()
  document.addEventListener('click', handleRuntimeClickOutside)
  document.addEventListener('keydown', onRuntimeKeydown)
})

onBeforeUnmount(() => {
  mountGeneration += 1
  dialerMediaQuery?.removeEventListener('change', syncDialerMode)
  nonModalDialerMediaQuery?.removeEventListener('change', syncDialerMode)
  document.removeEventListener('click', handleRuntimeClickOutside)
  document.removeEventListener('keydown', onRuntimeKeydown)
  stopApplicationVersionChecks?.()
  stopApplicationVersionChecks = undefined
  stopForegroundCommunicationRefresh?.()
  stopForegroundCommunicationRefresh = undefined
  shutdownRuntimeEvents()
  shutdownMessageRuntime()
  shutdownCallRuntime()
  shutdownBrowserSounds()
  shutdownDTMFAudio()
  shutdownBrowserNotifications()
  shutdownAudioDevices()
})
</script>

<template>
  <div class="app-shell">
    <aside class="rail" :aria-label="t('shell.primaryNavigation')">
      <div ref="runtimeCardRoot" class="brand-mark-wrap">
        <button
          class="brand-mark"
          type="button"
          :aria-expanded="runtimeCardOpen"
          :aria-label="t('runtimeStatus.title')"
          :title="t('runtimeStatus.title')"
          @click.stop="toggleRuntimeCard"
        >
          <span class="brand-mark__avatar">
            <BaseAvatar
              :name="profileContact?.display_name || sessionState.username"
              :src="railAvatar"
              fallback="person"
              :palette-key="profileContact?.id || sessionState.userID"
            />
          </span>
          <strong>{{ sessionState.username }}</strong>
        </button>
        <PopoverTransition>
          <div v-if="runtimeCardOpen" class="runtime-card-pop" @click.stop>
            <RuntimeStatusCard />
          </div>
        </PopoverTransition>
      </div>

      <nav class="rail__primary">
        <RouterLink
          v-for="item in primaryNav"
          :key="item.name"
          class="rail-link"
          :class="{ 'is-current': route.name === item.name }"
          :to="{ name: item.name }"
          :title="item.label"
        >
          <component :is="item.icon" :size="22" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <nav class="rail__secondary">
        <RouterLink class="rail-link" :class="{ 'is-current': route.name === 'nmobile' }" :to="{ name: 'nmobile' }" :title="t('homesim.nmobile.title')">
          <MessageCircleHeart :size="22" /><span>{{ t('homesim.nmobile.title') }}</span>
        </RouterLink>
        <RouterLink v-if="sessionState.role === 'admin'" class="rail-link" :class="{ 'is-current': route.name === 'settings' && route.params.section === 'sms-forward' }" :to="{ name: 'settings', params: { section: 'sms-forward' } }" :title="t('homesim.forward')">
          <Forward :size="22" /><span>{{ t('homesim.forward') }}</span>
        </RouterLink>
        <RouterLink v-if="sessionState.role === 'admin'" class="rail-link" :class="{ 'is-current': route.name === 'settings' && ['devices', 'diagnostics', 'about'].includes(String(route.params.section)) }" :to="{ name: 'settings', params: { section: 'devices' } }" :title="t('homesim.system')">
          <Server :size="22" /><span>{{ t('homesim.system') }}</span>
        </RouterLink>
        <RouterLink class="rail-link" :class="{ 'is-current': route.name === 'settings' && route.params.section === 'preferences' }" :to="{ name: 'settings', params: { section: 'preferences' } }" :title="t('homesim.profile')">
          <UserRound :size="22" /><span>{{ t('homesim.profile') }}</span>
        </RouterLink>
        <RouterLink class="rail-link" :class="{ 'is-current': route.name === 'sponsor' }" :to="{ name: 'sponsor' }" :title="t('homesim.sponsor.title')">
          <HeartHandshake :size="22" /><span>{{ t('homesim.sponsor.title') }}</span>
        </RouterLink>
        <RouterLink
          class="rail-link"
          :class="{ 'is-current': route.name === 'settings' && !['devices', 'diagnostics', 'about', 'telegram', 'preferences'].includes(String(route.params.section)) }"
          :to="{ name: 'settings' }"
          :title="t('shell.settings')"
        >
          <Settings :size="22" />
          <span>{{ t('shell.settings') }}</span>
        </RouterLink>
      </nav>
    </aside>

    <main class="shell-main">
      <header class="shell-header">
        <button
          v-if="mobileShellBackVisible"
          class="icon-button mobile-shell-back"
          type="button"
          :title="mobileBackTitle"
          :aria-label="mobileBackTitle"
          @click="handleMobileBack"
        >
          <ArrowLeft :size="20" />
        </button>
        <div class="mobile-brand">{{ mobilePageTitle }}</div>
        <div class="shell-header__loc">
          <div class="shell-location" aria-hidden="true">
            <span>NasSimHub</span><span>/</span><strong>{{ mobilePageTitle }}</strong>
          </div>
          <LineStatusStrip />
        </div>
        <GlobalSearch />
        <div class="shell-header__right">
          <div
            v-if="fixtureMode"
            class="fixture-badge"
            :title="t('shell.fixtureDataHint')"
          >
            <TestTube2 :size="15" />
            {{ t('homesim.fixture') }}
          </div>
          <div
            class="shell-header__controls"
            :class="{ 'is-hidden-on-mobile': mobileDrilldownControlsHidden }"
          >
            <IncomingCallModeControl v-if="sessionState.role === 'admin'" />
            <AppearanceToggle />
            <button
              class="icon-button"
              :class="{ 'is-active': browserNotificationState.active }"
              type="button"
              :disabled="
                browserNotificationState.requesting ||
                !browserNotificationState.secureContext ||
                !browserNotificationState.supported ||
                (browserNotificationState.permission === 'denied' &&
                  !browserNotificationState.preferenceEnabled)
              "
              :title="browserNotificationTitle"
              :aria-label="browserNotificationTitle"
              :aria-pressed="browserNotificationState.active"
              @click="toggleBrowserNotifications"
            >
              <BellRing v-if="browserNotificationState.active" :size="19" />
              <BellOff v-else :size="19" />
            </button>
            <AudioSettingsMenu />
            <button
              class="icon-button shell-dialer-toggle"
              type="button"
              :title="t('shell.openDialer')"
              :aria-label="t('shell.openDialer')"
              @click="openDialer()"
            >
              <PhoneCall :size="19" />
            </button>
          </div>
        </div>
      </header>

      <div
        v-if="bootstrapResource.status === 'error' || bootstrapResource.status === 'forbidden'"
        class="bootstrap-alert"
        role="alert"
      >
        <span>{{ bootstrapResource.error }}</span>
        <button v-if="bootstrapResource.status === 'error'" type="button" @click="bootstrap">
          {{ t('common.retry') }}
        </button>
      </div>

      <div class="route-stage">
        <RouterView v-slot="{ Component }">
          <Transition name="route-view">
            <div :key="String(route.name || route.path)" class="route-view">
              <component :is="Component" />
            </div>
          </Transition>
        </RouterView>
      </div>
    </main>

    <OverlayDialog
      :open="mobileMoreOpen"
      size="large"
      labelledby="mobile-more-title"
      initial-focus=".mobile-more-links a"
      @close="closeMobileMore"
    >
      <div id="mobile-more-menu" class="mobile-more-sheet">
          <header class="mobile-more-sheet__header">
            <h2 id="mobile-more-title">{{ t('common.more') }}</h2>
            <button
              class="icon-button"
              type="button"
              :title="t('common.close')"
              :aria-label="t('common.close')"
              @click="closeMobileMore"
            >
              <X :size="20" />
            </button>
          </header>
          <nav class="mobile-more-links" :aria-label="t('common.more')">
            <RouterLink
              v-for="item in mobileSecondaryNav"
              :key="item.name"
              :class="{ 'is-current': mobileNavItemCurrent(item.name) }"
              :to="item.to"
              @click="closeMobileMore"
            >
              <component :is="item.icon" :size="21" />
              <span>{{ item.label }}</span>
            </RouterLink>
          </nav>
      </div>
    </OverlayDialog>

    <nav class="mobile-nav" :aria-label="t('shell.mobileNavigation')">
      <RouterLink
        v-for="item in mobileNavBeforeDial"
        :key="item.name"
        :class="{
          'is-current': mobileNavItemCurrent(item.name),
          'mobile-nav__overflow': item.name === 'contacts'
        }"
        :to="item.to"
        :title="item.label"
        :aria-label="item.label"
      >
        <component :is="item.icon" :size="21" />
        <span class="mobile-nav__label">{{ item.label }}</span>
      </RouterLink>
      <button
        class="mobile-nav__dial"
        :class="{
          'is-current': uiState.dialerOpen || activeCallPresent,
          'is-ringing': minimizedIncomingCall,
          'is-active-call': minimizedActiveCall
        }"
        type="button"
        :title="mobileCallTitle"
        :aria-label="mobileCallTitle"
        :aria-pressed="uiState.dialerOpen || activeCallPresent"
        @click="openMobileCall"
      >
        <span class="mobile-nav__dial-icon">
          <span
            v-if="minimizedIncomingCall"
            class="mobile-nav__ring-wave mobile-nav__ring-wave--inner"
            aria-hidden="true"
          />
          <span
            v-if="minimizedIncomingCall"
            class="mobile-nav__ring-wave mobile-nav__ring-wave--outer"
            aria-hidden="true"
          />
          <PhoneCall :size="23" />
          <span
            v-if="occupiedLineCount > 0"
            class="mobile-nav__call-count"
            aria-hidden="true"
          >
            {{ occupiedLineCount }}
          </span>
        </span>
        <span class="mobile-nav__label">{{ t('shell.mobileCall') }}</span>
      </button>
      <RouterLink
        v-for="item in mobileNavAfterDial"
        :key="item.name"
        :class="{
          'is-current': mobileNavItemCurrent(item.name),
          'mobile-nav__overflow': item.name !== 'calls'
        }"
        :to="item.to"
        :title="item.label"
        :aria-label="item.label"
      >
        <component :is="item.icon" :size="21" />
        <span class="mobile-nav__label">{{ item.label }}</span>
      </RouterLink>
      <button
        class="mobile-nav__more"
        :class="{ 'is-current': mobileMoreCurrent }"
        type="button"
        :title="t('common.more')"
        :aria-label="t('common.more')"
        aria-controls="mobile-more-menu"
        :aria-expanded="mobileMoreOpen"
        @click="toggleMobileMore"
      >
        <Menu :size="21" />
        <span class="mobile-nav__label">{{ t('common.more') }}</span>
      </button>
    </nav>

    <DialerPanel :permanent="permanentDialer"
      :non-modal="nonModalDialer"
    />
  </div>
</template>

<style scoped>
.mobile-nav__dial-icon {
  position: relative;
}

.mobile-nav__call-count {
  position: absolute;
  top: -7px;
  right: -10px;
  display: inline-flex;
  min-width: 18px;
  height: 18px;
  align-items: center;
  justify-content: center;
  padding: 0 4px;
  color: var(--on-accent);
  font-size: 10px;
  font-weight: 800;
  line-height: 1;
  background: var(--danger);
  border: 2px solid var(--surface);
  border-radius: var(--radius-control);
}

@media (max-width: 860px) {
  .mobile-more-sheet {
    width: 100%;
    max-height: min(480px, calc(100dvh - var(--mobile-nav-height) - 20px));
    overflow-y: auto;
  }

  .mobile-more-sheet__header {
    display: flex;
    min-height: 58px;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px 8px 18px;
    border-bottom: 1px solid var(--border);
  }

  .mobile-more-sheet__header h2 {
    margin: 0;
    font-size: 17px;
  }

  .mobile-more-links {
    display: grid;
    padding: 6px 10px 14px;
  }

  .mobile-more-links a {
    display: flex;
    min-height: 52px;
    align-items: center;
    gap: 13px;
    padding: 0 12px;
    color: var(--text);
    font-size: 14px;
    font-weight: 650;
    border-radius: var(--radius-control);
    transition:
      color var(--motion-fast) var(--ease-standard),
      background var(--motion-fast) var(--ease-standard),
      transform var(--motion-fast) var(--ease-standard);
  }

  .mobile-more-links a:hover,
  .mobile-more-links a:focus-visible,
  .mobile-more-links a.is-current {
    color: var(--accent-strong);
    background: var(--accent-soft);
  }

  .mobile-more-links a:active {
    transform: scale(0.985);
  }
}
</style>
