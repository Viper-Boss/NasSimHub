<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseAvatar from './BaseAvatar.vue'
import ContactAvatarPicker from './ContactAvatarPicker.vue'
import SettingsPreferenceRow from './settings/SettingsPreferenceRow.vue'
import { profileAvatarState, setProfileAvatar } from '../state/profileAvatar'
import { sessionState } from '../state/session'

// Settings row that lets the user upload their own rail avatar. The image
// is compressed to a data URL and stored locally (localStorage), so it
// applies instantly and needs no backend round-trip. Layout mirrors the
// other preference rows: 36px round preview on the left, actions on the
// right; only the picker's action buttons survive in the control slot.
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
      <BaseAvatar
        class="account-avatar-setting__preview"
        :name="sessionState.username"
        :src="profileAvatarState.avatar"
        fallback="person"
        :palette-key="sessionState.userID"
        size="large"
      />
    </template>
    <template #control>
      <ContactAvatarPicker
        class="account-avatar-setting__picker"
        :model-value="profileAvatarState.avatar"
        :name="sessionState.username"
        @update:model-value="setProfileAvatar"
      />
    </template>
  </SettingsPreferenceRow>
</template>

<style scoped>
/* The picker's own avatar button and copy text are hidden: the preview
   lives in the row's icon slot. The upload/remove buttons live INSIDE
   the copy block, so only its direct text children are hidden. */
.account-avatar-setting__picker :deep(.contact-avatar-picker__target) {
  display: none;
}

.account-avatar-setting__picker :deep(.contact-avatar-picker__copy) > strong,
.account-avatar-setting__picker :deep(.contact-avatar-picker__copy) > span {
  display: none;
}

.account-avatar-setting__picker :deep(.contact-avatar-picker) {
  gap: 0;
}

.account-avatar-setting__picker :deep(.contact-avatar-picker__actions) {
  justify-content: flex-end;
}

.account-avatar-setting__preview {
  width: 36px;
  height: 36px;
  border-radius: 50%;
}
</style>
