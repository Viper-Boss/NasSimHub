<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { LoaderCircle, LogIn, UserRoundPlus } from '@lucide/vue'
import { gateway } from '../api/client'
import AppearanceToggle from '../components/AppearanceToggle.vue'
import { login, sessionState, setup as setupAdministrator } from '../state/session'
import {
  minimumPasswordCharacters,
  passwordCharacterCount
} from '../utils/password'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const username = ref('')
const password = ref('')
const confirmation = ref('')
const submitting = ref(false)
const submitError = ref('')
const isSetup = computed(() => sessionState.setupRequired)

// A data directory that was created on another machine.
//
// Shown INSTEAD of the login and setup forms, not beside them: on a cloned
// gateway the inherited password is refused and setup is refused, so offering
// either would be offering a control that cannot work.
const needsProvisioning = computed(() => sessionState.provisionRequired)
const machineID = ref('')
const previousMachineID = ref('')
const divergingSources = ref<string[]>([])
const confirmMachineID = ref('')
const resetToken = ref('')
const resetting = ref(false)
const resetDone = ref(false)

onMounted(async () => {
  if (!needsProvisioning.value) return
  try {
    const status = await gateway.provisionStatus()
    machineID.value = status.machine_id
    previousMachineID.value = status.previous_machine_id || ''
    divergingSources.value = status.diverging_sources || []
  } catch {
    // Leaving the identifier blank is honest: the operator cannot confirm a
    // value the gateway would not tell us, and the reset refuses a wrong one.
  }
})

const canReset = computed(
  () => machineID.value !== '' && confirmMachineID.value.trim() === machineID.value && /^[a-f0-9]{64}$/i.test(resetToken.value.trim())
)

async function requestReset(): Promise<void> {
  if (!canReset.value || resetting.value) return
  resetting.value = true
  submitError.value = ''
  try {
    await gateway.requestProvisionReset(confirmMachineID.value.trim(), resetToken.value.trim())
    resetToken.value = ''
    resetDone.value = true
  } catch (error) {
    submitError.value = error instanceof Error ? error.message : t('auth.provision.failed')
  } finally {
    resetting.value = false
  }
}
const visibleError = computed(() => submitError.value || sessionState.error)
const visibleNotice = computed(() =>
  !isSetup.value && route.query.passwordChanged === '1' && !visibleError.value
    ? t('auth.passwordChanged')
    : ''
)

function destination(): string {
  const redirect = route.query.redirect
  if (
    typeof redirect === 'string' &&
    redirect.startsWith('/') &&
    !redirect.startsWith('//') &&
    redirect !== '/login'
  ) {
    return redirect
  }
  return '/contacts'
}

async function submit(): Promise<void> {
  const normalizedUsername = username.value.trim()
  if (!normalizedUsername || !password.value) {
    submitError.value = isSetup.value
      ? t('auth.enterSetupCredentials')
      : t('auth.enterCredentials')
    return
  }
  if (
    isSetup.value &&
    passwordCharacterCount(password.value) < minimumPasswordCharacters
  ) {
    submitError.value = t('auth.setupPasswordTooShort', {
      count: minimumPasswordCharacters
    })
    return
  }
  if (isSetup.value && password.value !== confirmation.value) {
    submitError.value = t('auth.passwordMismatch')
    return
  }

  submitting.value = true
  submitError.value = ''
  try {
    if (isSetup.value) {
      await setupAdministrator(normalizedUsername, password.value)
    } else {
      await login(normalizedUsername, password.value)
    }
    await router.replace(destination())
  } catch (error) {
    submitError.value =
      error instanceof Error
        ? error.message
        : isSetup.value
          ? t('auth.setupFailed')
          : t('auth.loginFailed')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <header class="login-brand" aria-label="NasSimHub">
      <span><img src="/favicon.svg" width="54" height="54" alt="" /></span>
      <strong>{{ t('homesim.brand') }}</strong>
    </header>
    <div class="login-appearance"><AppearanceToggle /></div>

    <section class="login-panel" aria-labelledby="login-title">
      <template v-if="needsProvisioning">
        <header>
          <h1 id="login-title">{{ t('auth.provision.title') }}</h1>
          <p>{{ sessionState.provisionReason || t('auth.provision.body') }}</p>
        </header>

        <aside class="provision-notice" role="alert">
          <p>{{ t('auth.provision.consequences') }}</p>
          <dl v-if="machineID" class="provision-facts">
            <div>
              <dt>{{ t('auth.provision.thisMachine') }}</dt>
              <dd>{{ machineID }}</dd>
            </div>
            <div v-if="previousMachineID">
              <dt>{{ t('auth.provision.previousMachine') }}</dt>
              <dd>{{ previousMachineID }}</dd>
            </div>
            <div v-if="divergingSources.length">
              <dt>{{ t('auth.provision.changed') }}</dt>
              <dd>{{ divergingSources.join(', ') }}</dd>
            </div>
          </dl>
        </aside>

        <p v-if="resetDone" class="provision-done" role="status">
          {{ t('auth.provision.pending') }}
        </p>

        <form v-else class="login-form" @submit.prevent="requestReset">
          <label class="field">
            <span>{{ t('serviceErrors.localResetCode') }}</span>
            <input v-model="resetToken" name="reset-token" type="password" autocomplete="off" :disabled="resetting" />
            <small>{{ t('serviceErrors.localResetHint') }}</small>
          </label>
          <label class="field">
            <span>{{ t('auth.provision.confirmLabel') }}</span>
            <input
              v-model="confirmMachineID"
              name="confirm-machine-id"
              type="text"
              autocomplete="off"
              autocapitalize="none"
              spellcheck="false"
              :placeholder="machineID"
              :disabled="resetting"
            />
          </label>
          <p v-if="visibleError" class="login-error" role="alert">{{ visibleError }}</p>
          <button type="submit" class="danger" :disabled="!canReset || resetting">
            <LoaderCircle v-if="resetting" class="spin" :size="18" />
            <span>{{ t('auth.provision.action') }}</span>
          </button>
        </form>
      </template>

      <template v-else>
      <header>
        <h1 id="login-title">{{ isSetup ? t('auth.quickStart') : t('auth.title') }}</h1>
        <p v-if="isSetup">{{ t('auth.quickStartDescription') }}</p>
      </header>

      <aside v-if="isSetup" class="setup-guard-notice" role="note">
        <strong>{{ t('homesim.fraudGuard.setupTitle') }}</strong>
        <p>{{ t('homesim.fraudGuard.body') }}</p>
        <p>{{ t('homesim.fraudGuard.detail') }}</p>
      </aside>

      <form class="login-form" @submit.prevent="submit">
        <label class="field">
          <span>{{ t('auth.username') }}</span>
          <input
            v-model="username"
            name="username"
            type="text"
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
            autofocus
            :disabled="submitting"
          />
        </label>

        <label class="field">
          <span>{{ t('auth.password') }}</span>
          <input
            v-model="password"
            name="password"
            type="password"
            :autocomplete="isSetup ? 'new-password' : 'current-password'"
            :disabled="submitting"
          />
        </label>

        <label v-if="isSetup" class="field">
          <span>{{ t('auth.confirmPassword') }}</span>
          <input
            v-model="confirmation"
            name="password-confirmation"
            type="password"
            autocomplete="new-password"
            :disabled="submitting"
          />
        </label>

        <p v-if="visibleError" class="login-error" role="alert">{{ visibleError }}</p>
        <p v-else-if="visibleNotice" class="login-notice" role="status">
          {{ visibleNotice }}
        </p>

        <button class="primary-button login-submit" type="submit" :disabled="submitting">
          <LoaderCircle v-if="submitting" class="spin" :size="18" />
          <UserRoundPlus v-else-if="isSetup" :size="18" />
          <LogIn v-else :size="18" />
          {{
            submitting
              ? isSetup
                ? t('auth.settingUp')
                : t('auth.loggingIn')
              : isSetup
                ? t('auth.completeSetup')
                : t('auth.login')
          }}
        </button>
      </form>
      </template>
    </section>
  </main>
</template>
