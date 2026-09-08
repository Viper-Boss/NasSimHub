import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { advancedEN, advancedZH } from './advancedNetwork'
import { nknNodeEN, nknNodeZH } from './nknNodeMessages'

// Feature copy loads with its route, not with every user's initial shell.
export function useAdvancedI18n() {
  const i18n = useI18n()
  watch(i18n.locale, locale => {
    i18n.mergeLocaleMessage(locale, { homesim: { advanced: locale.startsWith('zh') ? advancedZH : advancedEN, nknNode: locale.startsWith('zh') ? nknNodeZH : nknNodeEN } })
  }, { immediate: true, flush: 'sync' })
  return i18n
}
