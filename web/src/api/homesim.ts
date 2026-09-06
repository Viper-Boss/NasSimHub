export type HomeSIMIntegrationDraft = {
  revision: number
  spam: { provider: 'local' | 'juhe'; credential_ref: string; min_reports: number; blacklist: string[]; whitelist: string[] }
  identity: { provider: 'off' | 'juhe'; credential_ref: string }
  ai: { endpoint: string; credential_ref: string; model: string; greeting: string; answer_after_seconds: number; max_duration_seconds: number; transcript: boolean; summary: boolean; push_channel: 'none' | 'telegram' }
}

export function emptyIntegrationDraft(): HomeSIMIntegrationDraft {
  return {
    revision: 1,
    spam: { provider: 'local', credential_ref: 'HOMESIM_SPAM_API_KEY', min_reports: 10, blacklist: [], whitelist: [] },
    identity: { provider: 'off', credential_ref: 'HOMESIM_IDENTITY_API_KEY' },
    ai: { endpoint: '', credential_ref: 'HOMESIM_AI_API_KEY', model: '', greeting: '您好，我是机主的 AI 电话助理。本次通话将录音并转成文字，请问有什么事需要转达？', answer_after_seconds: 20, max_duration_seconds: 180, transcript: true, summary: true, push_channel: 'none' }
  }
}

export function integrationDraftProblem(value: HomeSIMIntegrationDraft): string {
  const key = /^[A-Z][A-Z0-9_]{2,79}$/
  if (![value.spam.credential_ref, value.identity.credential_ref, value.ai.credential_ref].every(item => key.test(item))) return '填写 NAS 环境变量名称，不要在这里填写 API 密钥。'
  if (!Number.isInteger(value.spam.min_reports) || value.spam.min_reports < 1 || value.spam.min_reports > 10000) return '标记阈值必须为 1–10000。'
  if (!Number.isInteger(value.ai.answer_after_seconds) || value.ai.answer_after_seconds < 5 || value.ai.answer_after_seconds > 60) return '代接等待时间必须为 5–60 秒。'
  if (!Number.isInteger(value.ai.max_duration_seconds) || value.ai.max_duration_seconds < 30 || value.ai.max_duration_seconds > 600) return '最长代接时间必须为 30–600 秒。'
  if (value.spam.blacklist.length > 500 || value.spam.whitelist.length > 500 || [...value.spam.blacklist, ...value.spam.whitelist].some(item => !/^\+?\d{3,20}$/.test(item))) return '每个名单最多 500 个号码；一行一个号码，可带 + 国家区号。'
  if (value.spam.blacklist.some(item => value.spam.whitelist.includes(item))) return '同一个号码不能同时在黑名单和白名单中。'
  if (!value.ai.greeting.trim() || value.ai.greeting.length > 500) return '请填写不超过 500 字的开场说明。'
  if (value.ai.model.length > 100) return '模型名称过长。'
  if (value.ai.endpoint) {
    try {
      const endpoint = new URL(value.ai.endpoint)
      if (!['wss:', 'https:'].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) return '服务地址只支持 HTTPS/WSS，请勿在 URL 中携带密钥、查询参数或用户名。'
    } catch { return '服务地址无效。' }
  }
  return ''
}
