import type { SIMCommandInput, SIMStatus } from '../api/types'

export function simCommandProblem(status: SIMStatus | null, input: SIMCommandInput, repeatedPIN = ''): string {
  if (!status?.present) return '请先刷新 SIM 状态并确认卡已插入。'
  const lock = status.unlock_required
  const puk = input.operation === 'send_puk'
  const attempts = status.unlock_retries[puk ? 'sim-puk' : 'sim-pin']
  if (attempts === 0) return '剩余尝试次数为 0，请联系运营商；不要继续尝试。'
  if (input.operation === 'send_pin' && lock !== 'sim-pin') return '当前 SIM 不处于 PIN 锁定状态，请刷新状态。'
  if (puk && lock !== 'sim-puk') return '仅在 SIM 被 PUK 锁定时提交运营商提供的 PUK。'
  if (['enable_pin', 'change_pin'].includes(input.operation) && lock !== 'none') return '请先解锁 SIM，再修改 PIN 保护。'
  if (puk ? !/^\d{8}$/.test(input.puk || '') : !/^\d{4,8}$/.test(input.pin || '')) return puk ? 'PUK 必须是运营商提供的 8 位数字。' : 'PIN 必须是 4–8 位数字。'
  if (puk || input.operation === 'change_pin') {
    if (!/^\d{4,8}$/.test(input.new_pin || '')) return '新 PIN 必须是 4–8 位数字。'
    if (input.new_pin !== repeatedPIN) return '两次输入的新 PIN 不一致。'
  }
  return ''
}
