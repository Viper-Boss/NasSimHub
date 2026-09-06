// Remote phone-number region lookup with a tiny cache. The built-in
// prefix table in utils/phoneRegion.ts stays as the instant fallback;
// server results (full offline phone.dat dataset) override it.
import { reactive } from 'vue'
import { fetchHomeSIMPhoneGeo } from '../api/client'

export type PhoneGeoRecord = { province: string; carrier: string }

const cache = reactive(new Map<string, PhoneGeoRecord>())
const pending = new Set<string>()

export function phoneGeoFor(number: string): PhoneGeoRecord | undefined {
  return cache.get(number)
}

export async function requestPhoneGeo(number: string): Promise<PhoneGeoRecord | undefined> {
  if (!number || cache.has(number) || pending.has(number)) {
    return cache.get(number)
  }
  pending.add(number)
  try {
    const geo = await fetchHomeSIMPhoneGeo(number)
    if (geo && geo.province) {
      cache.set(number, { province: geo.province, carrier: geo.carrier })
    }
    return cache.get(number)
  } finally {
    pending.delete(number)
  }
}
