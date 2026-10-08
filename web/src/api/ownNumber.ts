import { writeJSON } from './client'

export async function saveOwnNumber(lineID: string, expectedICCID: string, number: string): Promise<void> {
  await writeJSON(`/api/v1/lines/${encodeURIComponent(lineID)}/own-number`, 'PATCH', {
    expected_iccid: expectedICCID,
    phone_number: number.trim()
  }, 200)
}
