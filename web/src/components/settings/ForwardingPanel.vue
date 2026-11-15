<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  defaultForwardConfig,
  forwardDeliveries,
  getForwarding,
  migrateForwarding,
  previewForwarding,
  retryForwarding,
  saveForwarding,
  testForwarding,
  type ForwardConfig,
  type ForwardDelivery,
  type ForwardKind,
  type ForwardSecret
} from '../../api/forwarding'
import { forwardingEN, forwardingZH } from '../../i18n/forwardingMessages'
import ForwardConditionEditor from './ForwardConditionEditor.vue'
import { fixtureMode } from '../../api/client'
import SelectControl from '../SelectControl.vue'
const i18n = useI18n()
const { t } = i18n
watch(
  i18n.locale,
  (locale) =>
    i18n.mergeLocaleMessage(locale, {
      forwarding: locale.startsWith('zh') ? forwardingZH : forwardingEN
    }),
  { immediate: true, flush: 'sync' }
)
const templateExample = '{{.Body}}'
function itemOptions(
  items: { id: string; name: string }[]
): { value: string; label: string }[] {
  return items.map((item) => ({ value: item.id, label: item.name || item.id }))
}
const config = ref<ForwardConfig>(defaultForwardConfig())
const catalog = ref<ForwardKind[]>([])
const tab = ref('channels'),
  loaded = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref(''),
  snapshot = ref('')
const channelID = ref(''),
  ruleID = ref(''),
  templateID = ref('default')
const deliveries = ref<ForwardDelivery[]>([]),
  before = ref(0),
  sample = ref('Synthetic preview message')
const preview = ref<{
  matched: boolean
  rendered: { title: string; body: string }
} | null>(null)
const dirty = computed(() => JSON.stringify(config.value) !== snapshot.value)
const channel = computed(() =>
  config.value.channels.find((item) => item.id === channelID.value)
)
const rule = computed(() =>
  config.value.rules.find((item) => item.id === ruleID.value)
)
const template = computed(() =>
  config.value.templates.find((item) => item.id === templateID.value)
)
const kind = computed(() =>
  catalog.value.find((item) => item.type === channel.value?.type)
)
let sequence = 0
const newID = (prefix: string): string =>
  `${prefix}_${Date.now()}_${sequence++}`
function accept(c: ForwardConfig): void {
  config.value = c
  snapshot.value = JSON.stringify(c)
  if (!c.channels.some((item) => item.id === channelID.value))
    channelID.value = c.channels[0]?.id || ''
  if (!c.rules.some((item) => item.id === ruleID.value))
    ruleID.value = c.rules[0]?.id || ''
}
async function run(operation: () => Promise<void>): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await operation()
  } catch (reason) {
    error.value =
      reason instanceof Error ? reason.message : 'Forwarding request failed.'
  } finally {
    busy.value = false
  }
}
async function reload(): Promise<void> {
  await run(async () => {
    const result = await getForwarding()
    catalog.value = result.catalog
    accept(result.config)
    loaded.value = true
  })
}
async function save(): Promise<void> {
  await run(async () => {
    accept(await saveForwarding(config.value))
    notice.value = t('forwarding.saved')
  })
}
function addChannel(): void {
  const id = newID('channel')
  config.value.channels.push({
    id,
    name: '',
    type: 'webhook',
    enabled: false,
    params: {},
    secrets: { url: {} }
  })
  channelID.value = id
}
function changeKind(): void {
  if (channel.value) {
    channel.value.params = {}
    channel.value.secrets = Object.fromEntries(
      (kind.value?.secrets || []).map((key) => [key, {}])
    )
  }
}
function secret(key: string): ForwardSecret {
  const ch = channel.value
  if (!ch) return {}
  return ch.secrets[key] || {}
}
function setSecret(key: string, field: 'value' | 'env', value: string): void {
  const ch = channel.value
  if (!ch) return
  const item = ch.secrets[key] || (ch.secrets[key] = {})
  item[field] = value
  if (value) {
    item[field === 'value' ? 'env' : 'value'] = ''
    item.set = false
  }
}
function removeChannel(): void {
  const id = channelID.value
  config.value.channels = config.value.channels.filter((item) => item.id !== id)
  for (const item of config.value.rules)
    item.channel_ids = item.channel_ids.filter((value) => value !== id)
  channelID.value = config.value.channels[0]?.id || ''
}
function addRule(): void {
  const id = newID('rule')
  config.value.rules.push({
    id,
    name: '',
    enabled: false,
    events: ['incoming_sms'],
    line_ids: [],
    condition: { op: 'always' },
    channel_ids: config.value.channels[0] ? [config.value.channels[0].id] : [],
    logic: 'all',
    template_id: config.value.templates[0]?.id || 'default',
    stop: false,
    quiet: {
      enabled: false,
      start: '22:00',
      end: '08:00',
      timezone: 'Asia/Shanghai',
      days: []
    }
  })
  ruleID.value = id
}
function move(index: number, offset: number): void {
  const values = rule.value?.channel_ids
  if (!values || !values[index] || !values[index + offset]) return
  const old = values[index]!
  values[index] = values[index + offset]!
  values[index + offset] = old
}
function removeRule(): void {
  config.value.rules = config.value.rules.filter(
    (item) => item.id !== ruleID.value
  )
  ruleID.value = config.value.rules[0]?.id || ''
}
function addTemplate(): void {
  const id = newID('template')
  config.value.templates.push({
    id,
    name: '',
    title: '{{.Peer}}',
    body: '{{.Body}}'
  })
  templateID.value = id
}
async function history(older = false): Promise<void> {
  await run(async () => {
    const result = await forwardDeliveries(older ? before.value : 0)
    deliveries.value = older
      ? [...deliveries.value, ...result.deliveries]
      : result.deliveries
    before.value = result.next_before
  })
}
function channelName(id: string): string {
  return config.value.channels.find((item) => item.id === id)?.name || id
}

function selectTab(name: string): void {
  tab.value = name
  if (name === 'deliveries') void history()
}
function removeTemplate(): void {
  if (
    config.value.templates.length <= 1 ||
    config.value.rules.some((item) => item.template_id === templateID.value)
  )
    return
  config.value.templates = config.value.templates.filter(
    (item) => item.id !== templateID.value
  )
  templateID.value = config.value.templates[0]?.id || ''
}
async function sendTest(): Promise<void> {
  await run(async () => {
    await testForwarding(channelID.value)
    notice.value = t('forwarding.tested')
  })
}
async function previewRule(): Promise<void> {
  await run(async () => {
    preview.value = await previewForwarding(
      config.value,
      ruleID.value,
      sample.value
    )
  })
}
async function retryDelivery(item: ForwardDelivery): Promise<void> {
  await run(async () => {
    await retryForwarding(item.id)
    item.status = 'pending'
    notice.value = t('forwarding.saved')
  })
}
async function importLegacy(): Promise<void> {
  await run(async () => {
    accept(await migrateForwarding(config.value.revision))
    notice.value = t('forwarding.saved')
  })
}

onMounted(reload)
</script>
<template>
  <section class="forwarding-panel" :aria-label="t('forwarding.title')">
    <p v-if="!loaded && !error" role="status">{{ t('forwarding.loading') }}</p>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <template v-if="loaded">
      <p v-if="fixtureMode" class="forward-note" role="note">
        {{ t('forwarding.demo_notice') }}
      </p>
      <div class="forward-toolbar">
        <label class="forward-check"
          ><input
            v-model="config.enabled"
            type="checkbox"
            class="ui-check"
            :disabled="busy"
          />{{ t('forwarding.enabled') }}</label
        ><button
          type="button"
          class="primary-button"
          :disabled="busy || !dirty"
          @click="save"
        >
          {{ t('forwarding.save') }}</button
        ><button
          class="secondary-button"
          type="button"
          :disabled="busy"
          @click="reload"
        >
          {{ t('forwarding.reload') }}</button
        ><span v-if="dirty" role="status">{{ t('forwarding.dirty') }}</span>
      </div>
      <p class="forward-note">{{ t('forwarding.off_hint') }}</p>
      <nav class="forward-tabs" :aria-label="t('forwarding.title')">
        <button
          class="secondary-button"
          v-for="name in ['channels', 'rules', 'templates', 'deliveries']"
          :key="name"
          type="button"
          :aria-pressed="tab === name"
          @click="selectTab(name)"
        >
          {{ t(`forwarding.${name}`) }}
        </button>
      </nav>
      <fieldset :disabled="busy" class="forward-body">
        <div v-if="tab === 'channels'">
          <div class="forward-toolbar">
            <label class="field"
              ><span>{{ t('forwarding.select') }}</span
              ><SelectControl
                v-model="channelID"
                :options="itemOptions(config.channels)"
                :label="t('forwarding.select')"
                :disabled="busy" /></label
            ><button
              class="secondary-button"
              type="button"
              :disabled="config.channels.length >= 100"
              @click="addChannel"
            >
              {{ t('forwarding.add') }}
            </button>
          </div>
          <div v-if="channel" class="forward-grid">
            <label class="field"
              ><span>{{ t('forwarding.name') }}</span
              ><input v-model="channel.name" maxlength="128" /></label
            ><label class="field"
              ><span>{{ t('forwarding.type') }}</span
              ><SelectControl
                v-model="channel.type"
                :options="
                  catalog.map((item) => ({
                    value: item.type,
                    label: t(`forwarding.kind_${item.type}`)
                  }))
                "
                :label="t('forwarding.type')"
                :disabled="busy"
                @change="changeKind" /></label
            ><label class="forward-check"
              ><input
                v-model="channel.enabled"
                type="checkbox"
                class="ui-check"
              />{{ t('forwarding.enabled_channel') }}</label
            >
            <label v-for="key in kind?.params || []" :key="key" class="field"
              ><span>{{ key }}</span
              ><input
                v-model="channel.params[key]"
                :name="`forward-param-${key}`"
                maxlength="4096"
            /></label>
            <div
              v-for="key in kind?.secrets || []"
              :key="key"
              class="forward-secret"
            >
              <strong>{{ key }}</strong
              ><label class="field"
                ><span>{{ t('forwarding.env') }}</span
                ><input
                  :value="secret(key).env || ''"
                  :name="`forward-env-${key}`"
                  maxlength="128"
                  autocomplete="off"
                  placeholder="FORWARDING_SECRET"
                  @input="
                    setSecret(
                      key,
                      'env',
                      ($event.target as HTMLInputElement).value
                    )
                  " /></label
              ><label class="field"
                ><span>{{ t('forwarding.secret_value') }}</span
                ><input
                  type="password"
                  :value="secret(key).value || ''"
                  :name="`forward-secret-${key}`"
                  maxlength="8192"
                  autocomplete="new-password"
                  @input="
                    setSecret(
                      key,
                      'value',
                      ($event.target as HTMLInputElement).value
                    )
                  " /></label
              ><template v-if="secret(key).set"
                ><small>{{ t('forwarding.secret_set') }}</small
                ><button
                  class="secondary-button"
                  type="button"
                  @click="channel.secrets[key] = {}"
                >
                  {{ t('forwarding.secret_clear') }}
                </button></template
              >
            </div>
            <div class="forward-wide">
              <p>{{ t('forwarding.secret_hint') }}</p>
              <p>{{ t('forwarding.network_hint') }}</p>
              <p>{{ t('forwarding.test_hint') }}</p>
              <div class="forward-toolbar">
                <button
                  class="secondary-button"
                  type="button"
                  :disabled="dirty"
                  @click="sendTest"
                >
                  {{ t('forwarding.test') }}</button
                ><button
                  class="secondary-button"
                  type="button"
                  @click="removeChannel"
                >
                  {{ t('forwarding.remove') }}
                </button>
              </div>
            </div>
          </div>
          <p v-else>{{ t('forwarding.empty') }}</p>
        </div>
        <div v-else-if="tab === 'rules'">
          <div class="forward-toolbar">
            <label class="field"
              ><span>{{ t('forwarding.select') }}</span
              ><SelectControl
                v-model="ruleID"
                :options="itemOptions(config.rules)"
                :label="t('forwarding.select')"
                :disabled="busy" /></label
            ><button
              class="secondary-button"
              type="button"
              :disabled="!config.channels.length || config.rules.length >= 64"
              @click="addRule"
            >
              {{ t('forwarding.add') }}
            </button>
          </div>
          <template v-if="rule">
            <div class="forward-grid">
              <label class="field"
                ><span>{{ t('forwarding.name') }}</span
                ><input v-model="rule.name" maxlength="128" /></label
              ><label class="forward-check"
                ><input
                  v-model="rule.enabled"
                  type="checkbox"
                  class="ui-check"
                />{{ t('forwarding.enabled_rule') }}</label
              >
              <fieldset>
                <legend>{{ t('forwarding.events') }}</legend>
                <label
                  v-for="name in [
                    'incoming_sms',
                    'incoming_call',
                    'missed_call'
                  ]"
                  :key="name"
                  class="forward-check"
                  ><input
                    v-model="rule.events"
                    type="checkbox"
                    class="ui-check"
                    :value="name"
                  />{{ t(`forwarding.${name}`) }}</label
                >
              </fieldset>
              <label class="field"
                ><span>{{ t('forwarding.lines') }}</span
                ><textarea
                  :value="rule.line_ids.join('\n')"
                  rows="3"
                  @input="
                    rule.line_ids = ($event.target as HTMLTextAreaElement).value
                      .split(/[\n,]/)
                      .map((value) => value.trim())
                      .filter(Boolean)
                  "
                />
              </label>
              <label class="field"
                ><span>{{ t('forwarding.logic') }}</span
                ><SelectControl
                  v-model="rule.logic"
                  :options="
                    ['all', 'until_success', 'until_fail'].map((value) => ({
                      value,
                      label: t(`forwarding.${value}`)
                    }))
                  "
                  :label="t('forwarding.logic')"
                  :disabled="busy" /></label
              ><label class="field"
                ><span>{{ t('forwarding.template') }}</span
                ><SelectControl
                  v-model="rule.template_id"
                  :options="itemOptions(config.templates)"
                  :label="t('forwarding.template')"
                  :disabled="busy"
              /></label>
              <fieldset class="forward-wide">
                <legend>{{ t('forwarding.channels') }}</legend>
                <label
                  v-for="item in config.channels"
                  :key="item.id"
                  class="forward-check"
                  ><input
                    v-model="rule.channel_ids"
                    type="checkbox"
                    class="ui-check"
                    :value="item.id"
                  />{{ item.name || item.id }}</label
                >
                <ol :aria-label="t('forwarding.order')">
                  <li v-for="(id, index) in rule.channel_ids" :key="id">
                    {{ channelName(id) }}
                    <button
                      class="secondary-button"
                      type="button"
                      :disabled="index === 0"
                      @click="move(index, -1)"
                    >
                      {{ t('forwarding.up') }}
                    </button>
                    <button
                      class="secondary-button"
                      type="button"
                      :disabled="index === rule.channel_ids.length - 1"
                      @click="move(index, 1)"
                    >
                      {{ t('forwarding.down') }}
                    </button>
                  </li>
                </ol>
              </fieldset>
            </div>
            <ForwardConditionEditor v-model="rule.condition" /><label
              class="forward-check"
              ><input v-model="rule.stop" type="checkbox" class="ui-check" />{{
                t('forwarding.stop')
              }}</label
            >
            <fieldset>
              <legend>
                <label class="forward-check"
                  ><input
                    v-model="rule.quiet.enabled"
                    type="checkbox"
                    class="ui-check"
                  />{{ t('forwarding.quiet') }}</label
                >
              </legend>
              <div v-if="rule.quiet.enabled" class="forward-grid">
                <label class="field"
                  ><span>{{ t('forwarding.start') }}</span
                  ><input v-model="rule.quiet.start" type="time" /></label
                ><label class="field"
                  ><span>{{ t('forwarding.end') }}</span
                  ><input v-model="rule.quiet.end" type="time" /></label
                ><label class="field"
                  ><span>{{ t('forwarding.timezone') }}</span
                  ><input
                    v-model="rule.quiet.timezone"
                    placeholder="Asia/Shanghai"
                /></label>
                <fieldset>
                  <legend>{{ t('forwarding.days') }}</legend>
                  <label
                    v-for="day in [0, 1, 2, 3, 4, 5, 6]"
                    :key="day"
                    class="forward-check"
                    ><input
                      v-model="rule.quiet.days"
                      type="checkbox"
                      class="ui-check"
                      :value="day"
                    />{{ day }}</label
                  >
                </fieldset>
              </div>
            </fieldset>
            <label class="field"
              ><span>{{ t('forwarding.preview_body') }}</span
              ><textarea v-model="sample" maxlength="4096" rows="2" />
            </label>
            <div class="forward-toolbar">
              <button
                class="secondary-button"
                type="button"
                @click="previewRule"
              >
                {{ t('forwarding.preview') }}</button
              ><button
                class="secondary-button"
                type="button"
                @click="removeRule"
              >
                {{ t('forwarding.remove') }}
              </button>
            </div>
            <div v-if="preview" role="status">
              <p>
                {{
                  t('forwarding.matched', { value: String(preview.matched) })
                }}
              </p>
              <pre
                >{{ preview.rendered.title }}{{ '\n'
                }}{{ preview.rendered.body }}</pre
              >
            </div>
          </template>
          <p v-else>{{ t('forwarding.empty') }}</p>
        </div>
        <div v-else-if="tab === 'templates'">
          <div class="forward-toolbar">
            <label class="field"
              ><span>{{ t('forwarding.select') }}</span
              ><SelectControl
                v-model="templateID"
                :options="itemOptions(config.templates)"
                :label="t('forwarding.template')"
                :disabled="busy" /></label
            ><button
              class="secondary-button"
              type="button"
              :disabled="config.templates.length >= 64"
              @click="addTemplate"
            >
              {{ t('forwarding.add') }}
            </button>
          </div>
          <template v-if="template"
            ><label class="field"
              ><span>{{ t('forwarding.name') }}</span
              ><input v-model="template.name" maxlength="128" /></label
            ><label class="field"
              ><span>{{ t('forwarding.template_title') }}</span
              ><input v-model="template.title" maxlength="4096" /></label
            ><label class="field"
              ><span>{{ t('forwarding.template_body') }}</span
              ><textarea v-model="template.body" rows="6" maxlength="16384" />
            </label>
            <p>
              {{ t('forwarding.template_hint', { example: templateExample }) }}
            </p>
            <button
              class="secondary-button"
              type="button"
              :disabled="
                config.templates.length === 1 ||
                config.rules.some((item) => item.template_id === templateID)
              "
              @click="removeTemplate"
            >
              {{ t('forwarding.remove') }}
            </button></template
          >
        </div>
        <div v-else-if="tab === 'deliveries'">
          <p>{{ t('forwarding.history_hint') }}</p>
          <button class="secondary-button" type="button" @click="history()">
            {{ t('forwarding.reload') }}
          </button>
          <ul class="forward-history">
            <li v-for="item in deliveries" :key="item.id">
              <strong
                >#{{ item.id }} ·
                {{ t(`forwarding.status_${item.status}`) }}</strong
              ><span
                >{{ item.event_id }} · {{ item.line_id }} ·
                {{ channelName(item.channel_id) }}</span
              ><span
                >{{ t('forwarding.attempts') }}: {{ item.attempts }} ·
                {{ item.error_class }}</span
              ><button
                class="secondary-button"
                v-if="item.status === 'failed'"
                type="button"
                @click="retryDelivery(item)"
              >
                {{ t('forwarding.retry') }}
              </button>
            </li>
          </ul>
          <p v-if="!deliveries.length">{{ t('forwarding.empty') }}</p>
          <button
            class="secondary-button"
            v-if="before"
            type="button"
            @click="history(true)"
          >
            {{ t('forwarding.more') }}
          </button>
        </div>
      </fieldset>
      <aside class="forward-note">
        <p>{{ t('forwarding.unsupported') }}</p>
        <p>{{ t('forwarding.migrate_hint') }}</p>
        <button
          class="secondary-button"
          type="button"
          :disabled="
            busy ||
            dirty ||
            config.channels.length > 0 ||
            config.rules.length > 0
          "
          @click="importLegacy"
        >
          {{ t('forwarding.migrate') }}
        </button>
      </aside> </template
    ><button
      class="secondary-button"
      v-else-if="error"
      type="button"
      @click="reload"
    >
      {{ t('forwarding.reload') }}
    </button>
  </section>
</template>
<style scoped>
.forwarding-panel {
  min-width: 0;
  width: 100%;
}
.forward-toolbar,
.forward-tabs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.65rem;
  margin-bottom: 1rem;
}
.forward-tabs button[aria-pressed='true'] {
  background: var(--color-primary, #2259bb);
  color: white;
}
.forward-body {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.forward-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr));
  gap: 1rem;
}
.forward-wide {
  grid-column: 1 / -1;
}
.field {
  display: grid;
  gap: 0.35rem;
  margin-bottom: 0.8rem;
  min-width: 0;
}
.forward-check {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 0.4rem 0.75rem 0.4rem 0;
}
input:not([type='checkbox']),
textarea,
select {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  box-sizing: border-box;
}
input[type='checkbox'] {
  flex-shrink: 0;
}
button {
  min-height: 2.5rem;
  max-width: 100%;
  padding: 0.4rem 0.8rem;
  overflow-wrap: anywhere;
}
fieldset:not(.forward-body),
.forward-secret,
.forward-note {
  min-width: 0;
  padding: 0.8rem;
  border: 1px solid var(--border-color, #8885);
  border-radius: 0.75rem;
  margin-bottom: 0.8rem;
}
.forward-note,
p,
small,
li,
span,
strong,
pre {
  overflow-wrap: anywhere;
}
pre {
  white-space: pre-wrap;
}
.forward-history {
  list-style: none;
  padding: 0;
}
.forward-history li {
  display: grid;
  gap: 0.4rem;
  padding: 0.8rem 0;
  border-bottom: 1px solid #8885;
}
</style>
