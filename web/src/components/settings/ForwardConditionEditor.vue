<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ForwardCondition } from '../../api/forwarding'
import SelectControl from '../SelectControl.vue'
const props = withDefaults(
  defineProps<{ modelValue: ForwardCondition; depth?: number }>(),
  { depth: 0 }
)
const emit = defineEmits<{ 'update:modelValue': [value: ForwardCondition] }>()
const { t } = useI18n()
const composite = computed(() =>
  ['and', 'or', 'not'].includes(props.modelValue.op)
)
function patch(value: Partial<ForwardCondition>): void {
  emit('update:modelValue', { ...props.modelValue, ...value })
}
function operator(op: string): void {
  emit(
    'update:modelValue',
    ['and', 'or', 'not'].includes(op)
      ? { op, children: [{ op: 'always' }] }
      : { op, field: 'body', value: '' }
  )
}
function child(index: number, value?: ForwardCondition): void {
  const children = [...(props.modelValue.children || [])]
  if (value) children[index] = value
  else children.splice(index, 1)
  patch({ children })
}
</script>
<template>
  <fieldset class="forward-condition">
    <legend>{{ t('forwarding.condition') }}</legend>
    <label class="field"
      ><span>{{ t('forwarding.op') }}</span
      ><SelectControl
        :model-value="modelValue.op"
        :options="
          [
            'always',
            'is',
            'notis',
            'contain',
            'notcontain',
            'regex',
            'and',
            'or',
            'not'
          ].map((value) => ({ value, label: t(`forwarding.${value}`) }))
        "
        :label="t('forwarding.op')"
        @update:model-value="operator"
    /></label>
    <template v-if="!composite && modelValue.op !== 'always'">
      <label class="field"
        ><span>{{ t('forwarding.field') }}</span
        ><SelectControl
          :model-value="modelValue.field || 'body'"
          :options="
            ['peer', 'body', 'line_id', 'event'].map((value) => ({
              value,
              label: t(`forwarding.${value}`)
            }))
          "
          :label="t('forwarding.field')"
          @update:model-value="patch({ field: $event })"
      /></label>
      <label class="field"
        ><span>{{ t('forwarding.match_value') }}</span
        ><input
          :value="modelValue.value"
          maxlength="4096"
          :placeholder="t('forwarding.match_value')"
          @input="patch({ value: ($event.target as HTMLInputElement).value })"
      /></label>
      <small>{{ t('forwarding.keywords') }}</small>
    </template>
    <template v-if="composite && depth < 12">
      <div v-for="(item, index) in modelValue.children || []" :key="index">
        <ForwardConditionEditor
          :model-value="item"
          :depth="depth + 1"
          @update:model-value="child(index, $event)"
        />
        <button
          class="secondary-button"
          v-if="
            modelValue.op !== 'not' && (modelValue.children?.length || 0) > 1
          "
          type="button"
          @click="child(index)"
        >
          {{ t('forwarding.remove') }}
        </button>
      </div>
      <button
        class="secondary-button"
        v-if="
          modelValue.op !== 'not' && (modelValue.children?.length || 0) < 32
        "
        type="button"
        @click="
          patch({
            children: [...(modelValue.children || []), { op: 'always' }]
          })
        "
      >
        {{ t('forwarding.add') }}
      </button>
    </template>
  </fieldset>
</template>
<style scoped>
.forward-condition {
  min-width: 0;
  margin: 0.75rem 0;
  padding: 0.75rem;
  border: 1px solid var(--border-color, #8885);
  border-radius: 0.75rem;
}
.forward-condition .forward-condition {
  margin-left: 0.25rem;
}
.field {
  display: grid;
  gap: 0.3rem;
  margin-bottom: 0.65rem;
}
input,
select {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
}
small {
  display: block;
  overflow-wrap: anywhere;
}
</style>
