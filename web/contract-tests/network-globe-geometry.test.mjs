import test from 'node:test'
import assert from 'node:assert/strict'

import {
  ARC_LIFT,
  arcAltitude,
  arcOpacity,
  greatCircle,
  isLand,
  project,
  spherePoints
} from '../src/components/networkGlobeGeometry.ts'

// A globe that draws points on the wrong side of the sphere looks plausible in
// a screenshot and is wrong in motion, so the projection is pinned here.
test('the projection hides the hemisphere facing away from the viewer', () => {
  const centre = 100
  const radius = 50

  // Longitude 0 with no rotation faces the viewer.
  const front = project({ lat: 0, lon: 0 }, 0, centre, centre, radius)
  assert.equal(front.visible, true)
  assert.ok(Math.abs(front.x - centre) < 0.001, 'the sub-viewer point sits on the centre line')

  // The opposite side of the sphere must be culled.
  const back = project({ lat: 0, lon: 180 }, 0, centre, centre, radius)
  assert.equal(back.visible, false)

  // Rotating by 180 degrees brings it round to the front.
  const rotated = project({ lat: 0, lon: 180 }, 180, centre, centre, radius)
  assert.equal(rotated.visible, true)
})

test('north is up and the projection stays inside the disc', () => {
  const centre = 100
  const radius = 50
  const pole = project({ lat: 90, lon: 0 }, 0, centre, centre, radius)
  assert.ok(pole.y < centre, 'the north pole must render above the centre')

  for (const point of spherePoints(200)) {
    const projected = project(point, 37, centre, centre, radius)
    const distance = Math.hypot(projected.x - centre, projected.y - centre)
    assert.ok(distance <= radius + 0.001, `point escaped the globe: ${distance}`)
  }
})

test('sphere points are spread evenly instead of bunching at the poles', () => {
  const points = spherePoints(1000)
  assert.equal(points.length, 1000)

  // With an even spread, about half the points sit in each hemisphere. A
  // latitude/longitude grid would pile them onto the caps instead.
  const northern = points.filter(point => point.lat > 0).length
  assert.ok(Math.abs(northern - 500) < 60, `northern points = ${northern}`)

  const polar = points.filter(point => Math.abs(point.lat) > 60).length
  // The caps above 60 degrees are about 13% of a sphere's area.
  assert.ok(polar < 220, `too many polar points: ${polar}`)
})

test('the land mask agrees with well-known coordinates', () => {
  const land = [
    { name: 'Sahara', lat: 23, lon: 13 },
    { name: 'Siberia', lat: 62, lon: 100 },
    { name: 'central Australia', lat: -25, lon: 133 },
    { name: 'Amazon', lat: -5, lon: -63 },
    { name: 'central China', lat: 35, lon: 105 }
  ]
  for (const place of land) {
    assert.equal(isLand(place), true, `${place.name} should be land`)
  }

  const sea = [
    { name: 'mid Pacific', lat: 0, lon: -150 },
    { name: 'mid Atlantic', lat: 0, lon: -30 },
    { name: 'Indian Ocean', lat: -30, lon: 80 },
    { name: 'Southern Ocean', lat: -60, lon: 0 }
  ]
  for (const place of sea) {
    assert.equal(isLand(place), false, `${place.name} should be sea`)
  }
})

test('a great circle starts and ends where it was told to', () => {
  const from = { lat: 31, lon: 121 }
  const to = { lat: 51, lon: 0 }
  const path = greatCircle(from, to, 32)

  assert.equal(path.length, 33)
  assert.ok(Math.abs(path[0].lat - from.lat) < 0.01)
  assert.ok(Math.abs(path[0].lon - from.lon) < 0.01)
  assert.ok(Math.abs(path[path.length - 1].lat - to.lat) < 0.01)
  assert.ok(Math.abs(path[path.length - 1].lon - to.lon) < 0.01)
})

// The whole point of a great circle is that it is not the straight line you get
// by interpolating latitude and longitude: between two northern cities it bows
// towards the pole.
test('a great circle bows polewards rather than following a flat interpolation', () => {
  const from = { lat: 40, lon: -74 }
  const to = { lat: 40, lon: 116 }
  const path = greatCircle(from, to, 64)

  const highest = Math.max(...path.map(point => point.lat))
  assert.ok(highest > 55, `the path should arc north, peaked at ${highest}`)
})

// A link is lifted off the surface so it reads as a hop. Under an orthographic
// projection that lift pushes the point straight out of the disc, so it has to
// stay small enough that the beam does not fly off the edge of the card.
test('the arc lift stays within the padding the card leaves around the globe', () => {
  assert.ok(ARC_LIFT > 0, 'links must bow above the surface, not lie flat on it')
  // The card draws the globe at 0.42 of the canvas, leaving 0.08 of headroom.
  assert.ok(ARC_LIFT <= 0.08 / 0.42, `lift ${ARC_LIFT} would push a beam off the canvas`)

  assert.ok(Math.abs(arcAltitude(0) - 1) < 1e-9, 'a link starts on the surface')
  assert.ok(Math.abs(arcAltitude(1) - 1) < 1e-9, 'and lands back on it')
  assert.ok(Math.abs(arcAltitude(0.5) - (1 + ARC_LIFT)) < 1e-9, 'peaking in the middle')
  // Values outside the path are clamped rather than dipping below the surface.
  assert.ok(arcAltitude(-1) >= 1 && arcAltitude(2) >= 1)
})

// A beam whose far end is over the horizon must dissolve. Stopping it dead at
// the terminator leaves a bright line ending in mid-air.
test('links fade out as they cross the terminator', () => {
  assert.equal(arcOpacity(0), 0)
  assert.equal(arcOpacity(-0.5), 0)
  assert.equal(arcOpacity(1), 1)
  const near = arcOpacity(0.02)
  assert.ok(near > 0 && near < 0.3, `a beam at the limb should be faint, got ${near}`)
  assert.ok(arcOpacity(0.4) > arcOpacity(0.1), 'opacity must rise with depth')
})

test('a degenerate great circle does not divide by zero', () => {
  const point = { lat: 10, lon: 20 }
  const path = greatCircle(point, point, 16)
  assert.ok(path.length >= 2)
  for (const step of path) {
    assert.ok(Number.isFinite(step.lat) && Number.isFinite(step.lon))
  }
})
