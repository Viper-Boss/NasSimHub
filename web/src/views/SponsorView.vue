<script setup lang="ts">
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { HeartHandshake, Copy, Check } from '@lucide/vue'
import PageContentFrame from '../components/PageContentFrame.vue'

const { t } = useI18n()

const failed = reactive<Record<string, boolean>>({})

function markFailed(key: string): void {
  failed[key] = true
}

// ── 区块链捐助地址 ──────────────────────────────────────────────
// 把你的接收地址填到下面的 address 字段（留空 = 页面上显示待填提示）。
// 收款二维码（可选）：把图片放到 web/public/sponsor/<key>.png，
// 例如 web/public/sponsor/btc.png，有图会显示二维码，没图只显示地址。
const CRYPTO = [
  { key: 'usdt', name: 'USDT', network: 'TRC-20 · Tron', address: '' },
  { key: 'btc', name: 'BTC', network: 'Bitcoin', address: '' },
  { key: 'eth', name: 'ETH / USDT', network: 'ERC-20 · Ethereum', address: '' },
  { key: 'sol', name: 'SOL', network: 'Solana', address: '' },
  { key: 'ltc', name: 'LTC', network: 'Litecoin', address: '' },
  { key: 'xmr', name: 'XMR', network: 'Monero', address: '' }
]

const copiedKey = reactive<Record<string, boolean>>({})
let copiedTimer: number | undefined

async function copyAddress(entry: (typeof CRYPTO)[number]): Promise<void> {
  if (!entry.address) return
  try {
    await navigator.clipboard.writeText(entry.address)
    copiedKey[entry.key] = true
    if (copiedTimer) window.clearTimeout(copiedTimer)
    copiedTimer = window.setTimeout(() => { copiedKey[entry.key] = false }, 2000)
  } catch {
    // clipboard unavailable (insecure context): ignore
  }
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
          <figcaption>{{ t('homesim.sponsor.replaceHint') }}</figcaption>
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
          <figcaption>{{ t('homesim.sponsor.replaceHint') }}</figcaption>
        </figure>
      </div>

      <section class="sponsor__crypto">
        <h2>{{ t('homesim.sponsor.cryptoTitle') }}</h2>
        <div class="sponsor__crypto-grid">
          <div v-for="entry in CRYPTO" :key="entry.key" class="sponsor__crypto-card">
            <header>
              <strong>{{ entry.name }}</strong>
              <span>{{ entry.network }}</span>
            </header>
            <img
              v-if="!failed[entry.key]"
              class="sponsor__crypto-qr"
              :src="`/sponsor/${entry.key}.png`"
              :alt="entry.name"
              loading="lazy"
              @error="markFailed(entry.key)"
            >
            <code v-if="entry.address" class="sponsor__addr">{{ entry.address }}</code>
            <div v-else class="sponsor__crypto-empty">{{ t('homesim.sponsor.cryptoEmpty') }}</div>
            <button
              v-if="entry.address"
              type="button"
              class="sponsor__copy"
              @click="copyAddress(entry)"
            >
              <Check v-if="copiedKey[entry.key]" :size="14" />
              <Copy v-else :size="14" />
              {{ copiedKey[entry.key] ? t('homesim.sponsor.copied') : t('homesim.sponsor.copy') }}
            </button>
          </div>
        </div>
      </section>

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
.sponsor__card figcaption { font-size: 11px; color: var(--muted); text-align: center; }

.sponsor__crypto { width: 100%; max-width: 640px; display: grid; gap: 12px; }
.sponsor__crypto > h2 { font-size: 15px; color: var(--text); text-align: center; }
.sponsor__crypto-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.sponsor__crypto-card {
  display: grid;
  gap: 8px;
  justify-items: center;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--surface-subtle);
}
.sponsor__crypto-card header { display: grid; justify-items: center; gap: 2px; }
.sponsor__crypto-card strong { font-size: 13px; color: var(--text); }
.sponsor__crypto-card header span { font-size: 11px; color: var(--muted); }
.sponsor__crypto-qr {
  width: 100%;
  max-width: 150px;
  aspect-ratio: 1;
  border-radius: 8px;
  background: #fff;
  object-fit: contain;
  padding: 4px;
}
.sponsor__addr {
  width: 100%;
  padding: 8px;
  border-radius: 8px;
  background: var(--border);
  color: var(--text);
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11px;
  word-break: break-all;
  text-align: center;
}
.sponsor__crypto-empty {
  padding: 8px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  color: var(--muted);
  font-size: 11px;
  text-align: center;
}
.sponsor__copy {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: transparent;
  color: var(--text);
  font-size: 12px;
  cursor: pointer;
}
.sponsor__copy:hover { background: var(--border); }

.sponsor__thanks { color: var(--muted); font-size: 12.5px; }
@media (max-width: 560px) {
  .sponsor__grid, .sponsor__crypto-grid { grid-template-columns: 1fr; }
}
</style>
