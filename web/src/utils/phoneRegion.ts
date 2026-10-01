import { parsePhoneNumberFromString } from 'libphonenumber-js'

// 中国大陆运营商号段（工信部公开段位）
const CN_OPERATOR_SEGMENTS: Array<[string, string[]]> = [
  [
    '中国移动',
    ['134', '135', '136', '137', '138', '139', '147', '148', '150', '151', '152', '157', '158', '159', '165', '172', '178', '182', '183', '184', '187', '188', '195', '197', '198']
  ],
  [
    '中国联通',
    ['130', '131', '132', '145', '146', '155', '156', '166', '167', '185', '186', '196']
  ],
  [
    '中国电信',
    ['133', '149', '153', '173', '174', '177', '180', '181', '189', '190', '191', '193', '199']
  ]
]

// 归属地演示前缀表（按 7 位号段）。完整城市级库由后端提供，这里只覆盖常用演示号段。
const CN_REGION_TABLE: Record<string, string> = {
  '1380000': '北京',
  '1390189': '上海',
  '1380166': '上海',
  '1862153': '上海',
  '1380010': '北京',
  '1390139': '北京',
  '1375024': '广州',
  '1380222': '广州',
  '1390731': '长沙',
  '1392887': '深圳'
}

function digitsOf(value: string): string {
  return String(value || '')
    .trim()
    .replace(/^\+86/, '')
    .replace(/\D/g, '')
}

export function cnCarrierOf(value: string): string {
  const n = digitsOf(value)
  if (!/^1\d{10}$/.test(n)) return ''
  const segment = n.slice(0, 3)
  for (const [name, segments] of CN_OPERATOR_SEGMENTS) {
    if (segments.includes(segment)) return name
  }
  return ''
}

// 运营商 PLMN 代码（MCC+MNC）→ 运营商名称。顶栏把 46011 这类代码翻译成中文名展示。
const CN_PLMN_NAMES: Record<string, string> = {
  '46000': '中国移动',
  '46002': '中国移动',
  '46004': '中国移动',
  '46007': '中国移动',
  '46008': '中国移动',
  '46001': '中国联通',
  '46006': '中国联通',
  '46009': '中国联通',
  '46003': '中国电信',
  '46005': '中国电信',
  '46011': '中国电信',
  '46020': '中国铁通'
}

export function cnOperatorNameOf(code: string): string {
  const aliases: Record<string,string> = { 'CHN-CT': '中国电信', 'CHN-CU': '中国联通', 'CHN-CM': '中国移动' }
  const alias = aliases[String(code || '').trim().toUpperCase()]
  if (alias) return alias
  const key = String(code || '').replace(/\D/g, '')
  return CN_PLMN_NAMES[key] || ''
}

export function cnRegionOf(value: string): string {
  const n = digitsOf(value)
  if (!/^1\d{10}$/.test(n)) return ''
  return CN_REGION_TABLE[n.slice(0, 7)] || ''
}

export interface OwnNumberFacts {
  formatted: string
  carrier: string
  region: string
  geoLine: string
}

export function ownNumberFacts(value: string, fallbackOperator?: string): OwnNumberFacts {
  const raw = String(value || '').trim()
  const parsed = parsePhoneNumberFromString(raw)
  const formatted = parsed ? parsed.formatInternational() : raw
  const carrier = cnCarrierOf(raw) || (cnCarrierOf(raw) === '' ? '' : '')
  const region = cnRegionOf(raw)
  const operator = carrier || fallbackOperator || ''
  const geoParts = [region, operator].filter(Boolean)
  return {
    formatted,
    carrier,
    region,
    geoLine: geoParts.join(' · ')
  }
}

export function accessTechnologyLabel(accessTechnology?: string): string {
  switch (String(accessTechnology || '').toLowerCase()) {
    case 'gsm':
      return '2G'
    case 'wcdma':
    case 'umts':
    case 'tdscdma':
      return '3G'
    case 'lte':
      return '4G'
    case 'nr':
      return '5G'
    default:
      return ''
  }
}
