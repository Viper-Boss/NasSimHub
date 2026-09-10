// Rebrand HomeSIM -> NasSimHub (NAS猫窝) for user-visible strings.
// Internal identifiers (homeSIM* funcs, homesim* files, /api/v1/settings/homesim/*,
// HOMESIM_* env names) intentionally stay unchanged.
import fs from 'node:fs'

// `optional` marks a file that may legitimately be absent in a partial
// checkout; it was accepted but never honoured, so a missing optional file
// still threw. Honour it instead of pretending the parameter does something.
function patch(file, pairs, optional = false) {
  if (optional && !fs.existsSync(file)) return 0
  let s = fs.readFileSync(file, 'utf8').replace(/\r\n/g, '\n')
  let changed = 0
  for (const [from, to] of pairs) {
    if (s.includes(from)) {
      s = s.split(from).join(to)
      changed++
    }
  }
  fs.writeFileSync(file, s)
  return changed
}

// --- web user-visible strings ---
patch('web/index.html', [['家卡 HomeSIM', 'NAS猫窝 NasSimHub']])
patch('web/package.json', [['"name": "modemdeck-web"', '"name": "nassimhub-web"']])

patch('web/src/i18n/homesimData.ts', [
  ["brand: 'HomeSIM'", "brand: 'NasSimHub'"],
  ["brand: '家卡 HomeSIM'", "brand: 'NAS猫窝 NasSimHub'"],
  ['Other HomeSIM channels still need server integration', 'Other NasSimHub channels still need server integration'],
  ['其余 HomeSIM 转发渠道尚需服务端对接', '其余 NasSimHub 转发渠道尚需服务端对接'],
  ["onlyTitle: 'HomeSIM only'", "onlyTitle: 'NasSimHub only'"],
  ["onlyTitle: '仅供家卡系统'", "onlyTitle: '仅供 NasSimHub'"],
  ['Takes effect immediately for outbound HomeSIM requests', 'Takes effect immediately for outbound NasSimHub requests'],
  ['开启后立即对家卡出站请求生效', '开启后立即对 NasSimHub 出站请求生效'],
  ['仅用于 HomeSIM 界面预览', '仅用于 NasSimHub 界面预览']
])

patch('web/src/components/AppShell.vue', [
  ['<strong>HomeSIM</strong>', '<strong>NasSimHub</strong>'],
  ['<span>HomeSIM</span>', '<span>NasSimHub</span>'],
  ['`HomeSIM ${t(\'shell.home\')}`', '`NasSimHub ${t(\'shell.home\')}`'],
  ['aria-label="HomeSIM"', 'aria-label="NasSimHub"']
])

// --- Go user-visible strings ---
patch('internal/barkpush/barkpush.go', [
  ['Group:         "HomeSIM"', 'Group:         "NasSimHub"']
])
patch('internal/barkpush/barkpush_test.go', [
  ['loaded.Group != "HomeSIM"', 'loaded.Group != "NasSimHub"'],
  ['settings.Group: ', 'settings.Group: '],
  ['Group: "HomeSIM"', 'Group: "NasSimHub"']
])
patch('internal/httpapi/homesim_bark.go', [
  ['Group:         "HomeSIM"', 'Group:         "NasSimHub"'],
  ['"title": "HomeSIM"', '"title": "NAS猫窝"'],
  ['"HomeSIM test push: the Bark channel is working."', '"NAS猫窝测试推送：Bark 通道工作正常。"']
])
patch('internal/httpapi/homesim_f50.go', [
  ['"HomeSIM server storage is unavailable"', '"NasSimHub server storage is unavailable"']
])
patch('internal/httpapi/homesim_quota.go', [
  ['"HomeSIM server storage is unavailable"', '"NasSimHub server storage is unavailable"'],
  ['"save HomeSIM quotas"', '"save NasSimHub quotas"'],
  ['"read HomeSIM quotas"', '"read NasSimHub quotas"']
])
patch('internal/httpapi/homesim_network.go', [
  ['"HomeSIM server storage is unavailable"', '"NasSimHub server storage is unavailable"'],
  ['"save network switches"', '"save network switches"']
])
patch('internal/httpapi/homesim_call_filter.go', [
  ['"read HomeSIM call filter"', '"read NasSimHub call filter"'],
  ['"save HomeSIM call filter"', '"save NasSimHub call filter"']
])
patch('internal/httpapi/homesim_draft.go', [
  ['"read HomeSIM draft"', '"read NasSimHub draft"'],
  ['"save HomeSIM draft"', '"save NasSimHub draft"'],
  ['"prepare HomeSIM draft"', '"prepare NasSimHub draft"']
])

console.log('brand rename done')
