<script setup lang="ts">
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { HeartHandshake } from '@lucide/vue'
import PageContentFrame from '../components/PageContentFrame.vue'

const { t } = useI18n()

const failed = reactive<Record<string, boolean>>({})

function markFailed(key: string): void {
  failed[key] = true
}
</script>

<template>
  <PageContentFrame>
    <div class="sponsor">
      <header class="sponsor__head">
        <HeartHandshake :size="28" />
        <h1>{{ t('homesim.sponsor.title') }}</h1>
        <p>{{ t('homesim.sponsor.subtitle') }}</p>
      </header>

      <div class="sponsor__grid">
        <figure class="sponsor__card">
          <h2>{{ t('homesim.sponsor.wechat') }}</h2>
          <img
            v-if="!failed.wechat"
            src="/sponsor/wechat.png"
            :alt="t('homesim.sponsor.wechat')"
            loading="lazy"
            @error="markFailed('wechat')"
          >
          <div v-else class="sponsor__placeholder">{{ t('homesim.sponsor.replaceHint') }}</div>
        </figure>
        <figure class="sponsor__card">
          <h2>{{ t('homesim.sponsor.alipay') }}</h2>
          <img
            v-if="!failed.alipay"
            src="/sponsor/alipay.png"
            :alt="t('homesim.sponsor.alipay')"
            loading="lazy"
            @error="markFailed('alipay')"
          >
          <div v-else class="sponsor__placeholder">{{ t('homesim.sponsor.replaceHint') }}</div>
        </figure>
      </div>

      <p class="sponsor__thanks">{{ t('homesim.sponsor.thanks') }}</p>
    </div>
  </PageContentFrame>
</template>

<style scoped>
.sponsor { display: grid; gap: 18px; justify-items: center; }
.sponsor__head { display: grid; justify-items: center; gap: 6px; text-align: center; color: var(--text); }
.sponsor__head h1 { font-size: 22px; }
.sponsor__head p { color: var(--muted); font-size: 13px; max-width: 480px; }
.sponsor__grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; width: 100%; max-width: 640px; }
.sponsor__card {
  display: grid;
  gap: 10px;
  justify-items: center;
  margin: 0;
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: 16px;
  background: var(--surface-subtle);
}
.sponsor__card h2 { font-size: 14px; color: var(--text); }
.sponsor__card img {
  width: 100%;
  max-width: 240px;
  aspect-ratio: 1;
  border-radius: 10px;
  background: var(--border);
  object-fit: contain;
}
.sponsor__placeholder {
  display: grid;
  place-items: center;
  width: 100%;
  max-width: 240px;
  aspect-ratio: 1;
  padding: 12px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  color: var(--muted);
  font-size: 12px;
  text-align: center;
}
.sponsor__thanks { color: var(--muted); font-size: 12.5px; }
@media (max-width: 560px) {
  .sponsor__grid { grid-template-columns: 1fr; }
}
</style>
