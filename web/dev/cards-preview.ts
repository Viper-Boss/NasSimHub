// A preview harness for the two network cards.
//
// It imports the SHIPPING painters, the shipping geometry and the shipping
// stylesheet, so what it renders is what the dashboard renders. The card markup
// is transcribed from the two Vue templates -- the one copy here that can drift
// -- and the class names are asserted against the components in the contract
// tests, which is what keeps the drift visible.
//
// Bundle with esbuild and open the sibling HTML file. The point of it is to be
// able to look at all four states (each theme, each channel up and down) at a
// real card width, instead of guessing from the code.

import { buildGlobe } from '../src/components/networkGlobeGeometry'
import { paintGlobe } from '../src/components/networkGlobePainter'
import { advance, buildField } from '../src/components/dhtMeshField'
import { paintMesh } from '../src/components/dhtMeshPainter'

const params = new URLSearchParams(location.search)
const live = params.get('state') !== 'down'
const time = Number(params.get('t') ?? 26)

const globeCard = (ok: boolean) => `
<article class="net-card globe-card" data-state="${ok ? 'online' : 'offline'}">
  <header class="net-card__head">
    <div>
      <div class="net-card__title">
        <svg class="net-card__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4">
          <circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/>
          <path d="M3 12h18M5 6.5h14M5 17.5h14"/></svg>
        <h4>NKN 地球网络</h4>
      </div>
      <p class="net-card__eyebrow">GLOBAL CONNECTIVITY</p>
    </div>
    <span class="net-card__badge">${ok ? '已连接' : '未连接'}</span>
  </header>
  <div class="net-stage">
    <canvas id="globe" role="img"></canvas>
    <p class="net-note">全球连接 · 持续在线</p>
  </div>
  <div class="net-card__body">
    <div class="net-row${ok ? '' : ' is-off'}">
      <strong><i class="net-row__dot"></i>NKN 主通路</strong><span>${ok ? '连接正常' : '等待连接'}</span>
    </div>
    <div class="net-row${ok ? '' : ' is-off'}">
      <strong><i class="net-row__dot is-green"></i>DHT 第二通路</strong>
      <span>${ok ? '可用 · 自动协同' : 'DHT 已关闭'}</span>
    </div>
    <dl class="net-stats">
      <div class="net-stat"><dt>24小时活跃</dt><dd>${ok ? '1,286' : '—'}</dd></div>
      <div class="net-stat"><dt>30天活跃</dt><dd>${ok ? '8,492' : '—'}</dd></div>
      <div class="net-stat"><dt>90天活跃</dt><dd>${ok ? '12,608' : '—'}</dd></div>
    </dl>
    <p class="net-card__note">共享 Topic 中的近期活跃身份数。统计窗口以订阅与续期策略为准，不代表实时在线数。</p>
  </div>
</article>`

const meshCard = (ok: boolean) => `
<article class="net-card mesh-card" data-state="${ok ? 'online' : 'offline'}">
  <header class="net-card__head">
    <div>
      <div class="net-card__title">
        <svg class="net-card__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4">
          <circle cx="5" cy="12" r="2.5"/><circle cx="18" cy="5" r="2.5"/><circle cx="18" cy="19" r="2.5"/>
          <path d="m7 11 9-5M7 13l9 5M18 8v8"/></svg>
        <h4>DHT 网状连接</h4>
      </div>
      <p class="net-card__eyebrow">SELF-ORGANIZING MESH</p>
    </div>
    <span class="net-card__badge">${ok ? '网络健康' : '已关闭'}</span>
  </header>
  <div class="net-stage">
    <canvas id="mesh" role="img"></canvas>
    <p class="net-note">局部连接示意</p>
    ${ok ? '<p class="net-note net-note--right net-note--signal">路由信号</p>' : ''}
  </div>
  <div class="net-card__body">
    <div class="net-row${ok ? '' : ' is-off'}">
      <strong><i class="net-row__dot"></i>节点发现</strong>
      <span>${ok ? '持续发现邻居' : '节点发现已暂停'}</span>
    </div>
    <div class="net-row${ok ? '' : ' is-off'}">
      <strong><i class="net-row__dot is-green"></i>第二通路</strong>
      <span>${ok ? '已启用' : 'DHT 已关闭'}</span>
    </div>
    <dl class="net-stats">
      <div class="net-stat"><dt>路由表节点</dt><dd>${ok ? '128' : '—'}</dd></div>
      <div class="net-stat"><dt>当前直连</dt><dd>${ok ? '24' : '—'}</dd></div>
      <div class="net-stat"><dt>局域网内</dt><dd>${ok ? '3' : '—'}</dd></div>
    </dl>
    <p class="net-card__note">动画为局部拓扑示意，节点的明暗与漂移都是装饰，不代表全网规模或真实地理位置。黄色信号只在 DHT 就绪时出现；三个数字来自本机路由表。</p>
  </div>
</article>`

document.getElementById('cards')!.innerHTML = globeCard(live) + meshCard(live)
document.getElementById('case-label')!.textContent =
  `${document.documentElement.dataset.theme === 'light' ? 'LIGHT' : 'DARK'} · ${live ? 'NKN ACTIVE · DHT READY' : 'NKN DOWN · DHT OFF'}`

function surface(id: string): { context: CanvasRenderingContext2D; width: number; height: number } | null {
  const element = document.getElementById(id) as HTMLCanvasElement | null
  if (!element) return null
  const box = element.getBoundingClientRect()
  const ratio = 2
  element.width = Math.round(box.width * ratio)
  element.height = Math.round(box.height * ratio)
  const context = element.getContext('2d')
  if (!context) return null
  context.setTransform(ratio, 0, 0, ratio, 0, 0)
  return { context, width: box.width, height: box.height }
}

const globe = surface('globe')
if (globe) {
  paintGlobe(globe.context, buildGlobe(), {
    width: globe.width,
    height: globe.height,
    time,
    strength: live ? 1 : 0.42,
    palette: {
      halo: '#5dbfe5',
      dot: '133 208 245',
      marker: '#a4e9ff',
      bodyNear: 'rgb(32 73 101 / 13%)',
      bodyFar: 'rgb(9 23 40 / 20%)',
      rim: 'rgb(120 205 239 / 13%)'
    }
  })
}

const mesh = surface('mesh')
if (mesh) {
  const field = buildField(mesh.width, mesh.height)
  // Run it forward the way the card does, so a bead is in flight and a node is
  // part way through a fade -- a first frame shows neither.
  for (let step = 0; step * (1 / 60) <= time; step += 1) advance(field, step / 60, live)
  paintMesh(mesh.context, field, {
    width: mesh.width,
    height: mesh.height,
    time,
    strength: live ? 1 : 0.22,
    palette: {
      strand: '124 169 217',
      nodeBloom: '120 191 239',
      nodeCore: '161 215 249',
      ring: '143 225 255',
      signal: '255 214 124',
      signalCore: '255 228 167'
    }
  })
}
