<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Activity } from '@lucide/vue'
import './insightCard.css'
import { nodeResourceCopy, nodeDashboardCopy } from '../i18n/nodeResources'
const props = defineProps<{ deviceId: string }>()
const { locale } = useI18n()
const copy = computed(() => nodeResourceCopy(locale.value))
const dashboardCopy = computed(() => nodeDashboardCopy(locale.value))
type Resources = { cpu_percent: number | null; memory_total_bytes: number; memory_available_bytes: number; uptime_seconds: number; process_count: number; observed_at: string; processes: { pid: number; name: string; rss_bytes: number }[] }
type Node = { node_id: string; online: boolean; resources?: Resources }
const node = ref<Node | null>(null)
let timer: ReturnType<typeof setInterval> | undefined
let controller: AbortController | undefined
let stopped = false
async function refresh() {
 controller?.abort(); const request = new AbortController(); controller = request
 try {
  const response = await fetch('/api/v1/nodes', { credentials: 'same-origin', signal: request.signal })
  if (!response.ok) throw new Error('unavailable')
  const body = await response.json() as { nodes?: Node[] }
  if (!stopped && !request.signal.aborted) node.value = Array.isArray(body.nodes) ? body.nodes.find(item => item.node_id === props.deviceId && item.online) ?? null : null
 } catch { if (!stopped && !request.signal.aborted) node.value = null }
}
onMounted(() => { void refresh(); timer = setInterval(() => { void refresh() }, 10000) })
onBeforeUnmount(() => { stopped = true; clearInterval(timer); controller?.abort() })
const resources = computed(() => node.value?.resources)
const memoryPercent = computed(() => { const r = resources.value; return r && r.memory_total_bytes > 0 ? Math.max(0, Math.min(100, 100 * (r.memory_total_bytes - r.memory_available_bytes) / r.memory_total_bytes)) : null })
function mib(bytes: number) { return `${(bytes / 1048576).toFixed(1)} MiB` }
</script>
<template>
 <article class="insight-card node-resources">
  <header class="insight-card__head"><Activity :size="15" /><h4>{{ dashboardCopy.title }}</h4></header>
  <div class="insight-card__body">
   <p class="node-resources__identity">{{ deviceId }}</p>
   <dl v-if="resources">
    <div><dt>CPU</dt><dd>{{ resources.cpu_percent == null ? '—' : `${resources.cpu_percent.toFixed(1)}%` }}</dd></div>
    <div><dt>{{ copy.memory }}</dt><dd>{{ mib(Math.max(0, resources.memory_total_bytes - resources.memory_available_bytes)) }} / {{ mib(resources.memory_total_bytes) }}</dd></div>
    <div><dt>{{ dashboardCopy.memoryUsage }}</dt><dd>{{ memoryPercent == null ? '—' : `${memoryPercent.toFixed(1)}%` }}</dd></div>
    <div><dt>{{ copy.uptime }}</dt><dd>{{ (resources.uptime_seconds / 3600).toFixed(1) }} h</dd></div>
    <div><dt>{{ copy.processes }}</dt><dd>{{ resources.process_count }}</dd></div>
   </dl>
   <p v-else class="node-resources__muted">{{ dashboardCopy.missing }}</p>
   <p class="node-resources__muted">{{ dashboardCopy.detailsMissing }}</p>
   <details v-if="resources?.processes.length"><summary class="ui-disclosure-summary">{{ copy.top }}</summary><dl><div v-for="process in resources.processes" :key="process.pid"><dt>{{ process.name }} · {{ process.pid }}</dt><dd>{{ mib(process.rss_bytes) }}</dd></div></dl></details>
  </div>
 </article>
</template>
<style scoped>
.node-resources__identity { margin: 0 0 16px; font: 12px ui-monospace, monospace; color: var(--accent); overflow-wrap: anywhere; }
.node-resources__muted { color: var(--muted); font-size: 12px; margin-top: 14px; }
dl { display: grid; gap: 12px; margin: 0; } dl div { display: flex; justify-content: space-between; gap: 12px; } dt { color: var(--muted); overflow-wrap: anywhere; } dd { margin: 0; text-align: right; } details { margin-top: 16px; } summary { cursor: pointer; color: var(--muted); margin-bottom: 12px; }
</style>
