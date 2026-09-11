// Geometry for the rotating dot-matrix globe.
//
// Kept out of the component so the projection and great-circle maths can be
// tested on their own — a globe that draws points on the wrong side of the
// sphere is the kind of bug that is obvious in a test and easy to miss by eye.

export interface LatLon {
  lat: number
  lon: number
}

export interface Projected {
  x: number
  y: number
  /** Depth along the view axis: positive is the hemisphere facing the viewer. */
  z: number
  visible: boolean
}

const DEGREES = Math.PI / 180

/**
 * Orthographic projection of a point on a unit sphere, rotated by `spin`
 * degrees of longitude. The far hemisphere is reported as not visible so the
 * caller can skip it rather than drawing dots that belong behind the globe.
 */
export function project(
  point: LatLon,
  spin: number,
  centerX: number,
  centerY: number,
  radius: number
): Projected {
  const lat = point.lat * DEGREES
  const lon = (point.lon + spin) * DEGREES
  const cosLat = Math.cos(lat)
  const x = cosLat * Math.sin(lon)
  const y = Math.sin(lat)
  const z = cosLat * Math.cos(lon)
  return {
    x: centerX + x * radius,
    y: centerY - y * radius,
    z,
    visible: z > 0
  }
}

/**
 * Points spread evenly over the sphere using the Fibonacci lattice. An even
 * spread matters: latitude/longitude steps bunch up at the poles and the globe
 * ends up with two bright caps.
 */
export function spherePoints(count: number): LatLon[] {
  const points: LatLon[] = []
  const golden = Math.PI * (3 - Math.sqrt(5))
  for (let index = 0; index < count; index += 1) {
    const y = 1 - (index / Math.max(1, count - 1)) * 2
    const lat = Math.asin(Math.max(-1, Math.min(1, y))) / DEGREES
    const lon = ((index * golden) / DEGREES) % 360 - 180
    points.push({ lat, lon })
  }
  return points
}

// A 180x90 land/sea grid (2 degrees per cell), generated offline from Natural
// Earth's 110m land geometry and packed into base64. It is baked in rather than
// fetched or computed so the globe needs no geo dependency at runtime and costs
// the bundle about 2.7 KB.
//
// The alternative — approximating continents with hand-written boxes — was
// tried first and rendered as unrecognisable blobs. A wrong map is worse than
// no map.
const LAND_MASK_COLUMNS = 180
const LAND_MASK_ROWS = 90
const LAND_MASK_BASE64 =
  'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAf4AP/AAAAAAAAAAAAAAAAAAAAAAAAX/z///+AAAAAAAABAAAAAAAAAAAAAYd8P///wAA+AAAAAA8AAAAAAAAAAAwAnw////4AAIAAAAAAGAAAAAAAAAAADivwAf//wAAAAADAAf/wAHYAAAAAADoi3sAP//gAAAAAMAD///sAAAAgBgACfwz/AD/+gAAAwAEHf///////+AP//////////////4A//EgAAAAAAAgH///////////////gAFAAgAAAAAAAAz///////////////BhQAAAAAAAAAAAAP/////4A0B4AAAD5/////////////Af3////gHgA4AAAH5///////////LwAHgH///gHkAAAAAH4/////////+CIAABAB///4D+AAAAGCx/////////4A8AAIAAf///n/gAAAOCD/////////wA4AAAAAf///n/wAAAbP///////////AgAAAAAP/////wAAADf//////////9AAAAAAAF////0YAAAB///////////9AAAAAAAD////8EAAAB///////////5AAAAAAAD////2AAAAB/f5fP//////wAAAAAAAD////gAAAAfxnwPP//////jAAAAAAAD////AAAAAPCb3/n/////+CAAAAAAAD///8AAAAAfALf/n////+ECAAAAAAAB///8AAAAAGHQP/n/////mMAAAAAAAA///8AAAAAH+Ai///////E8AAAAAAAAf//wAAAAAP/AA///////BgAAAAAAAAP//gAAAAAf/73///////gAAAAAAAAAD/AQAAAAAf////f/////gAAAAAAAAAF+AQAAAAB///+/n/////AAAAAAAAAAC+AAAAAAB///+f0H////AAAAAAAAAAAeAwAAAAD////f/B///8gAAAAAAAAAAeGEAAAAH////v+B/z/AAAAAAAAAAAAPMAgAAAD////n+A/B+gAAAAAAAAAAAD8AAAAAD////n4AeB/AgAAAAAAAAAAAPAAAAAH////3gAcAfAgAAAAAAAAAAADAAAAAD////6AAcAfggAAAAAAAAAAABDwAAAD////8wAMATAIAAAAAAAAAAAAr/AAAB/////gAKASAAAAAAAAAAAAAAH/gAAA/////gACAAAIAAAAAAAAAAAAH/8AAAaH///AAAAsGAAAAAAAAAAAAAH/+AAAAB//+AAAAUOAAAAAAAAAAAAAP/+AAAAB//8AAAAYegAAAAAAAAAAAAP//gAAAD//4AAAAMeBgAAAAAAAAAAAP//8AAAB//wAAAAGdiuAAAAAAAAAAAf///AAAA//wAAAACAQHgAAAAAAAAAAP///gAAA//wAAAABwAHwgAAAAAAAAAH///AAAA//wAAAAACIDQIAAAAAAAAAH//+AAAAf/wAAAAAAAAAAAAAAAAAAAD//+AAAA//wgAAAAABxAAAAAAAAAAAD//+AAAA//wgAAAAAPxgBAAAAAAAAAA//8AAAA//jgAAAAAf5gAAAAAAAAAAAf/8AAAA//DgAAAAAf/gAAAAAAAAAAAf/8AAAAf/DAAAAAD//4CAAAAAAAAAAf/wAAAAf/DAAAAAH//4AAAAAAAAAAAf/AAAAAf+CAAAAAH//8AAAAAAAAAAAf/AAAAAP8AAAAAAH//+AAAAAAAAAAA/+AAAAAP8AAAAAAH//+AAAAAAAAAAA/+AAAAAH4AAAAAAD//+AAAAAAAAAAA/8AAAAAHwAAAAAADwf8AAAAAAAAAAA/gAAAAAAAAAAAAACAH4AIAAAAAAAAB/wAAAAAAAAAAAAAAAD4AEAAAAAAAAB+AAAAAAAAAAAAAAAAAAAGAAAAAAAAB6AAAAAAAAAAAAAAAAAwAMAAAAAAAAA8AAAAAAAAAAAAAAAAAQAYAAAAAAAAB4AAAAAAAAAAAAAAAAAAAwAAAAAAAAB4AAAAAAAAAAAAAAAAAAAAAAAAAAAADwAAAAAAAAAACAAAAAAAAAAAAAAAAADgAAAAAAAAAAAAAAAAAAAAAAAAAAAABwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACAAAAAAAAAAAAAAAAAAAAAAAAAAAAAMAAAAAAAAAeAAIP+f/gAAAAAAAAAAAMAAAAAAABP/+H//////AAAAAAAAAAA+AAAAAf////8////////AAAAAAAOEAPAAAB///////////////gAAAP//T//8AAAH//////////////+AAAH/////4AAAH///////////////8AAE//////4ABw////////////////8AAAD//////gCA////////////////wAAAf/////////////////////////+AAH/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

let landBits: Uint8Array | null = null

function maskBytes(): Uint8Array {
  if (landBits) return landBits
  const binary = typeof atob === 'function'
    ? atob(LAND_MASK_BASE64)
    : Buffer.from(LAND_MASK_BASE64, 'base64').toString('binary')
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
  landBits = bytes
  return bytes
}

export function isLand(point: LatLon): boolean {
  const column = Math.floor(((point.lon + 180) / 360) * LAND_MASK_COLUMNS)
  const row = Math.floor(((90 - point.lat) / 180) * LAND_MASK_ROWS)
  if (column < 0 || column >= LAND_MASK_COLUMNS || row < 0 || row >= LAND_MASK_ROWS) return false
  const bit = row * LAND_MASK_COLUMNS + column
  const bytes = maskBytes()
  const byte = bytes[bit >> 3]
  if (byte === undefined) return false
  return (byte & (128 >> (bit & 7))) !== 0
}

/**
 * How far a link bows above the surface at its midpoint, as a fraction of the
 * globe's radius. Small on purpose: under an orthographic projection a lifted
 * point moves straight out from the centre of the disc, so a link whose far end
 * sits near the limb leaves the globe's silhouette by exactly this fraction. Too
 * much lift and the beam shoots off the edge of the card.
 */
export const ARC_LIFT = 0.11

/** The radius multiplier for a point `fraction` of the way along a link. */
export function arcAltitude(fraction: number): number {
  return 1 + ARC_LIFT * Math.sin(Math.max(0, Math.min(1, fraction)) * Math.PI)
}

/**
 * How strongly a point on a link is drawn, given its depth. Links fade out as
 * they approach the terminator instead of stopping dead, so a beam whose far end
 * is over the horizon dissolves rather than ending in a stray line.
 */
export function arcOpacity(z: number): number {
  if (z <= 0) return 0
  return Math.min(1, z * 3.2)
}

/**
 * Points along the great circle between two coordinates — the path a link
 * actually follows on a sphere. A straight screen-space line between two
 * projected points would cut through the globe instead of lying on it.
 */
export function greatCircle(from: LatLon, to: LatLon, segments = 48): LatLon[] {
  const lat1 = from.lat * DEGREES
  const lon1 = from.lon * DEGREES
  const lat2 = to.lat * DEGREES
  const lon2 = to.lon * DEGREES

  const delta = 2 * Math.asin(Math.sqrt(
    Math.sin((lat2 - lat1) / 2) ** 2 +
    Math.cos(lat1) * Math.cos(lat2) * Math.sin((lon2 - lon1) / 2) ** 2
  ))
  if (!Number.isFinite(delta) || delta === 0) return [from, to]

  const path: LatLon[] = []
  for (let step = 0; step <= segments; step += 1) {
    const fraction = step / segments
    const a = Math.sin((1 - fraction) * delta) / Math.sin(delta)
    const b = Math.sin(fraction * delta) / Math.sin(delta)
    const x = a * Math.cos(lat1) * Math.cos(lon1) + b * Math.cos(lat2) * Math.cos(lon2)
    const y = a * Math.cos(lat1) * Math.sin(lon1) + b * Math.cos(lat2) * Math.sin(lon2)
    const z = a * Math.sin(lat1) + b * Math.sin(lat2)
    path.push({
      lat: Math.atan2(z, Math.sqrt(x * x + y * y)) / DEGREES,
      lon: Math.atan2(y, x) / DEGREES
    })
  }
  return path
}
