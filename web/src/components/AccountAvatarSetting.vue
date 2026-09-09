<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import ContactAvatarPicker from './ContactAvatarPicker.vue'
import SettingsPreferenceRow from './settings/SettingsPreferenceRow.vue'
import { profileAvatarState, setProfileAvatar } from '../state/profileAvatar'

// Settings row that lets the user upload their own rail avatar. The image
// is compressed to a data URL and stored locally (localStorage), so it
// applies instantly and needs no backend round-trip.

const { t } = useI18n()
</script>

<template>
  <SettingsPreferenceRow
    class="account-avatar-setting"
    :title="t('runtimeStatus.avatarTitle')"
    title-id="account-avatar-title"
    :description="t('runtimeStatus.avatarDescription')"
    control-size="wide"
    icon-tone="bare"
  >
    <template #icon>
      <ContactAvatarPicker
        :model-value="profileAvatarState.avatar"
        :name="t('runtimeStatus.avatarTitle')"
        @update:model-value="setProfileAvatar"
      />
    </template>
  </SettingsPreferenceRow>
</template>

<style scoped>
.account-avatar-setting :deep(.contact-avatar-picker__copy),
.account-avatar-setting :deep(.contact-avatar-picker__actions) {
  display: none;
}

.account-avatar-setting :deep(.contact-avatar-picker) {
  gap: 0;
}
</style>
