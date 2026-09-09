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
        <span class="sponsor__badge">
          <HeartHandshake :size="26" />
        </span>
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
.sponsor {
  display: grid;
  gap: 28px;
  justify-items: center;
  max-width: 720px;
  margin: 0 auto;
}
.sponsor__head { display: grid; justify-items: center; gap: 12px; text-align: center; color: var(--text); }
.sponsor__badge {
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  border-radius: var(--radius-surface);
  background: linear-gradient(135deg, #f97316, #ec4899);
  color: #fff;
  box-shadow: 0 6px 18px rgb(236 72 153 / 25%);
}
.sponsor__head h1 { margin: 0; font-size: 26px; letter-spacing: 0.5px; }
.sponsor__head p {
  margin: 0;
  max-width: 480px;
  color: var(--muted);
  font-size: 13.5px;
  line-height: 1.8;
}

.sponsor__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
  width: 100%;
}
.sponsor__card {
  display: grid;
  gap: 14px;
  justify-items: center;
  margin: 0;
  padding: 22px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius-surface);
  background: var(--surface-subtle);
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}
.sponsor__card:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 24px rgb(0 0 0 / 18%);
}
.sponsor__card h2 { margin: 0; font-size: 15px; color: var(--text); }
.sponsor__card img {
  width: 100%;
  max-width: 300px;
  aspect-ratio: 1;
  border-radius: var(--radius-control);
  background: #fff;
  object-fit: contain;
}
.sponsor__placeholder {
  display: grid;
  place-items: center;
  width: 100%;
  max-width: 300px;
  aspect-ratio: 1;
  padding: 12px;
  border: 1px dashed var(--border);
  border-radius: var(--radius-control);
  color: var(--muted);
  font-size: 12px;
  text-align: center;
}

.sponsor__thanks {
  margin: 0;
  max-width: 480px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.8;
  text-align: center;
}
@media (max-width: 560px) {
  .sponsor__grid { grid-template-columns: 1fr; }
}
</style>
