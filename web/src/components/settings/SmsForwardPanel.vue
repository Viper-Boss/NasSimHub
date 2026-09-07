<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { LoaderCircle, Plus, Save, Send, Trash2 } from '@lucide/vue'
import {
  getForwarderSettings,
  saveForwarderSettings,
  testForwarderChannel,
  type ForwarderChannel,
  type ForwarderSettings
} from '../../api/client'

const { t } = useI18n()
const settings = ref<ForwarderSettings>({ revision: 1, forwarders: [] })
const maxChannels = ref(8)
const runtimeReady = ref(false)
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const saved = ref(false)
const saving = ref(false)
const testingID = ref('')
const testResult = ref<Record<string, string>>({})

const channelTypes = ['webhook', 'dingtalk', 'feishu', 'wecom', 'serverchan', 'pushplus'] as const

const atCapacity = computed(() => settings.value.forwarders.length >= maxChannels.value)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  saved.value = false
  try {
    const result = await getForwarderSettings()
    settings.value = result.settings
    maxChannels.value = result.max_channels
    runtimeReady.value = result.runtime_ready
    loaded.value = true
  } catch (reason) {
    loaded.value = false
    error.value = reason instanceof Error ? reason.message : t('homesim.forwarder.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (saving.value || !loaded.value) return
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    const result = await saveForwarderSettings(settings.value)
    settings.value = result.settings
    runtimeReady.value = result.runtime_ready
    saved.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.forwarder.loadFailed')
  } finally {
    saving.value = false
  }
}

function addChannel(): void {
  if (atCapacity.value || !loaded.value) return
  settings.value.forwarders.push({
    id: `fw-${Date.now().toString(36)}`,
    name: t(`homesim.forwarder.types.${channelTypes[0]}`),
    type: 'webhook',
    enabled: true,
    events: ['sms'],
    config: { webhook_url: '' }
  })
}

function removeChannel(index: number): void {
  settings.value.forwarders.splice(index, 1)
}

function typeLabel(kind: ForwarderChannel['type']): string {
  return t(`homesim.forwarder.types.${kind}`)
}

function needsURL(kind: ForwarderChannel['type']): boolean {
  return kind === 'webhook' || kind === 'dingtalk' || kind === 'feishu' || kind === 'wecom'
}

function urlLabel(kind: ForwarderChannel['type']): string {
  if (kind === 'feishu') return t('homesim.forwarder.feishuUrl')
  if (kind === 'wecom') return t('homesim.forwarder.wecomUrl')
  if (kind === 'dingtalk') return t('homesim.forwarder.dingtalkToken')
  return t('homesim.forwarder.webhookUrl')
}

function urlHint(kind: ForwarderChannel['type']): string {
  if (kind === 'feishu') return t('homesim.forwarder.feishuUrlHint')
  if (kind === 'wecom') return t('homesim.forwarder.wecomUrlHint')
  if (kind === 'dingtalk') return t('homesim.forwarder.dingtalkSecretHint')
  return t('homesim.forwarder.webhookUrlHint')
}

async function testChannel(channel: ForwarderChannel): Promise<void> {
  testingID.value = channel.id
  try {
    await testForwarderChannel(channel)
    testResult.value = { ...testResult.value, [channel.id]: t('homesim.forwarder.testSent') }
  } catch (reason) {
    testResult.value = {
      ...testResult.value,
      [channel.id]: reason instanceof Error ? `${t('homesim.forwarder.testFailed')}: ${reason.message}` : t('homesim.forwarder.testFailed')
    }
  } finally {
    testingID.value = ''
  }
}

onMounted(load)
</script>

<template>
  <section class="sms-forward" aria-label="SMS forwarding">
    <header class="sms-forward__header">
      <div>
        <h2>{{ t('homesim.forwarder.title') }}</h2>
        <p>{{ t('homesim.forwarder.subtitle') }}</p>
      </div>
      <button class="secondary-button" type="button" :disabled="loading || atCapacity" @click="addChannel">
        <Plus :size="16" />{{ t('homesim.forwarder.addChannel') }}
      </button>
    </header>
    <small v-if="atCapacity" class="sms-forward__note">{{ t('homesim.forwarder.maxChannels') }}</small>
    <small class="sms-forward__note">{{ t('homesim.forwarder.credentialHint') }}</small>

    <div v-if="loading" class="sms-forward__loading"><LoaderCircle class="spin" :size="18" /></div>
    <template v-else-if="loaded">
      <p v-if="!settings.forwarders.length" class="sms-forward__empty">{{ t('homesim.forwarder.noChannels') }}</p>

      <article v-for="(channel, index) in settings.forwarders" :key="channel.id" class="sms-forward-card" :class="{ 'is-disabled': !channel.enabled }">
        <div class="sms-forward-card__head">
          <label class="sms-forward-card__enabled">
            <input v-model="channel.enabled" class="ui-check" type="checkbox" />
            <span>{{ t('homesim.forwarder.enabled') }}</span>
          </label>
          <button class="icon-button" type="button" :aria-label="t('common.delete')" @click="removeChannel(index)">
            <Trash2 :size="16" />
          </button>
        </div>
        <div class="sms-forward-card__grid">
          <label>
            <span>{{ t('homesim.forwarder.channelName') }}</span>
            <input v-model="channel.name" maxlength="64" autocomplete="off" />
          </label>
          <label>
            <span>{{ t('homesim.forwarder.channelType') }}</span>
            <select v-model="channel.type">
              <option v-for="kind in channelTypes" :key="kind" :value="kind">{{ typeLabel(kind) }}</option>
            </select>
          </label>
        </div>
        <label v-if="needsURL(channel.type)">
          <span>{{ urlLabel(channel.type) }}</span>
          <input v-model="channel.config.webhook_url" type="url" spellcheck="false" autocomplete="off" :placeholder="channel.type === 'dingtalk' ? 'https://oapi.dingtalk.com/robot/send?access_token=…' : 'https://'" />
          <small>{{ urlHint(channel.type) }}</small>
        </label>
        <label v-if="channel.type === 'dingtalk'">
          <span>{{ t('homesim.forwarder.dingtalkSecret') }}</span>
          <input v-model="channel.config.secret" spellcheck="false" autocomplete="off" placeholder="SEC…" />
          <small>{{ t('homesim.forwarder.dingtalkSecretHint') }}</small>
        </label>
        <label v-if="channel.type === 'serverchan' || channel.type === 'pushplus'">
          <span>{{ channel.type === 'serverchan' ? t('homesim.forwarder.serverchanKey') : t('homesim.forwarder.pushplusToken') }}</span>
          <input v-model="channel.config.credential_ref" spellcheck="false" autocomplete="off" placeholder="HOMESIM_SERVERCHAN_KEY" />
          <small>{{ channel.type === 'serverchan' ? t('homesim.forwarder.serverchanKeyHint') : t('homesim.forwarder.pushplusTokenHint') }}</small>
        </label>
        <div class="sms-forward-card__foot">
          <span>{{ t('homesim.forwarder.events') }}: {{ t('homesim.forwarder.eventSms') }}</span>
          <button class="secondary-button" type="button" :disabled="testingID === channel.id" @click="testChannel(channel)">
            <LoaderCircle v-if="testingID === channel.id" class="spin" :size="14" />
            <Send v-else :size="14" />{{ t('homesim.forwarder.test') }}
          </button>
        </div>
        <p v-if="testResult[channel.id]" class="sms-forward-card__result" role="status">{{ testResult[channel.id] }}</p>
      </article>

      <p v-if="runtimeReady && settings.forwarders.length" class="sms-forward__ready" role="status">{{ t('homesim.forwarder.runtimeDraft') }}</p>
    </template>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <p v-if="saved" class="sms-forward__saved" role="status">{{ t('homesim.forwarder.saved') }}</p>
    <footer class="sms-forward__footer">
      <button class="primary-action" type="button" :disabled="saving || !loaded" @click="save">
        <LoaderCircle v-if="saving" class="spin" :size="16" />
        <Save v-else :size="16" />{{ t('homesim.forwarder.save') }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.sms-forward { display: grid; gap: 16px; }
.sms-forward__header { display: flex; flex-wrap: wrap; gap: 14px; align-items: flex-start; justify-content: space-between; }
.sms-forward__header h2 { font-size: 18px; }
.sms-forward__header p { margin-top: 4px; color: var(--muted); font-size: 13px; }
.sms-forward__note, .sms-forward__empty { color: var(--muted); font-size: 12.5px; }
.sms-forward__loading { display: grid; place-items: center; padding: 30px; color: var(--muted); }
.sms-forward-card { display: grid; gap: 12px; padding: 16px; border: 1px solid var(--border); border-radius: 14px; background: var(--surface-subtle); }
.sms-forward-card.is-disabled { opacity: .55; }
.sms-forward-card__head { display: flex; align-items: center; justify-content: space-between; }
.sms-forward-card__enabled { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 650; }
.sms-forward-card__grid { display: grid; gap: 12px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
.sms-forward-card label { display: grid; gap: 6px; font-size: 13px; }
.sms-forward-card label > span { font-weight: 650; }
.sms-forward-card input:not([type=checkbox]), .sms-forward-card select { width: 100%; min-width: 0; box-sizing: border-box; padding: 9px 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 9px; }
.sms-forward-card small { color: var(--muted); font-size: 11.5px; }
.sms-forward-card__foot { display: flex; align-items: center; justify-content: space-between; gap: 10px; color: var(--muted); font-size: 12px; }
.sms-forward-card__result { overflow-wrap: anywhere; color: var(--muted); font-size: 12px; }
.sms-forward__saved { color: var(--success); font-size: 13px; }
.sms-forward__ready { padding: 10px 12px; color: var(--success); font-size: 12.5px; background: var(--success-soft); border: 1px solid var(--success-border); border-radius: 9px; }
.sms-forward__footer { display: flex; justify-content: flex-end; }
@media (max-width: 560px) { .sms-forward-card__grid { grid-template-columns: 1fr; } }
</style>
