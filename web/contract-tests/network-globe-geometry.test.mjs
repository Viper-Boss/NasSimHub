import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

import {
  HORIZON,
  MARKER_HORIZON,
  MARKER_LATITUDES,
  buildGlobe,
  isLand,
  markerAt,
  project,
  spherePoints
} from '../src/components/networkGlobeGeometry.ts'
import { globeAngle } from '../src/components/networkGlobePainter.ts'

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

  // The globe is tipped by a shear rather than a rotation, and a shear can push
  // a point outside the unit circle if its coefficients are chosen carelessly.
  // A dot outside the rim reads as a rendering fault, so the whole covering is
  // checked at several angles rather than trusted.
  for (const rotation of [0, 37, 90, 143, 211, 299]) {
    for (const point of spherePoints(400)) {
      const projected = project(point, rotation, centre, centre, radius)
      const distance = Math.hypot(projected.x - centre, projected.y - centre)
      assert.ok(distance <= radius + 0.001, `point escaped the globe: ${distance}`)
    }
  }
})

test('the limb is drawn a shade past the horizon, and no further', () => {
  // Culling exactly at the terminator ends the covering on a hard edge; the
  // overshoot is what lets the last dots fade out. It has to stay small, or
  // dots from the far side show through the sphere.
  assert.ok(HORIZON < 0, 'the covering must run slightly past the limb')
  assert.ok(HORIZON > -0.2, `${HORIZON} would show the back of the sphere`)

  const centre = 100
  const justPast = project({ lat: 0, lon: 90 + 3 }, 0, centre, centre, 50)
  assert.ok(justPast.z < 0 && justPast.visible, 'a dot just past the limb should still draw')
  const wellPast = project({ lat: 0, lon: 140 }, 0, centre, centre, 50)
  assert.equal(wellPast.visible, false, 'a dot on the far side must not draw')
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

test('the outlines cover enough of the sphere to read as continents', () => {
  // The land dots are drawn brighter and larger than the sea, so the outlines
  // are the whole picture. Land is about 29% of the Earth; a simplified
  // silhouette will not match that, but an order of magnitude out in either
  // direction means either the mask is broken or the globe is a solid ball.
  const built = buildGlobe(2000)
  const land = built.filter(point => point.land).length
  const share = (land / built.length) * 100
  assert.ok(share > 12 && share < 45, `${share.toFixed(1)}% of the sphere is land`)
})

test('buildGlobe caches the unit position that matches its coordinates', () => {
  // The draw loop uses the cached x/y/z and never looks at lat/lon again, so a
  // mismatch between the two would be invisible until the globe looked wrong.
  for (const point of buildGlobe(200)) {
    const expected = project(
      { lat: point.lat, lon: point.lon },
      0, 0, 0, 1
    )
    const cached = project(point, 0, 0, 0, 1)
    assert.ok(Math.abs(expected.x - cached.x) < 1e-9, 'cached x disagrees with the coordinate')
    assert.ok(Math.abs(expected.y - cached.y) < 1e-9, 'cached y disagrees with the coordinate')
    assert.ok(Math.abs(expected.z - cached.z) < 1e-9, 'cached z disagrees with the coordinate')
    assert.ok(Math.abs(Math.hypot(point.x, point.y, point.z) - 1) < 1e-9, 'not a unit vector')
  }
})

test('the markers ride the sphere rather than floating over it', () => {
  const centre = 100
  const radius = 50
  for (const rotation of [0, 61, 180, 300]) {
    for (let index = 0; index < MARKER_LATITUDES.length; index += 1) {
      const marker = markerAt(index, rotation, centre, centre, radius)
      const distance = Math.hypot(marker.x - centre, marker.y - centre)
      assert.ok(distance <= radius + 0.001, `marker ${index} escaped the disc: ${distance}`)
    }
  }
  // And they must not all be in view at once, or they read as three fixed pips
  // rather than as points on a turning sphere.
  const visible = new Set()
  for (let rotation = 0; rotation < 360; rotation += 15) {
    let count = 0
    for (let index = 0; index < MARKER_LATITUDES.length; index += 1) {
      if (markerAt(index, rotation, centre, centre, radius).z >= MARKER_HORIZON) count += 1
    }
    visible.add(count)
  }
  assert.ok(visible.size > 1, 'the same markers are always in view; the globe looks static')
})

test('the globe turns slowly, and completes a turn in about two minutes', () => {
  // Slow is the whole character of this drawing: fast enough that a still
  // screenshot is obviously a frame of something moving, slow enough that it
  // never pulls the eye away from the figures underneath.
  const perSecond = globeAngle(1) - globeAngle(0)
  const seconds = (Math.PI * 2) / perSecond
  assert.ok(seconds > 60 && seconds < 240, `a full turn takes ${seconds.toFixed(0)}s`)
  // Time-based, not frame-based: the same moment must give the same angle.
  assert.equal(globeAngle(7.5), globeAngle(7.5))
})

// The markers pulse. A pulsing marker reads as something happening there, which
// is a claim -- so it may only be drawn while NKN really is up. The reference
// build this card came from drew them unconditionally.
test('the globe only draws its markers while the channel is up', () => {
  const painter = readFileSync(
    new URL('../src/components/networkGlobePainter.ts', import.meta.url),
    'utf8'
  )
  const card = readFileSync(
    new URL('../src/components/NetworkGlobeCard.vue', import.meta.url),
    'utf8'
  )
  // The guard sits before the marker loop, and the loop is the only caller of
  // markerAt, so a missing guard means markers on a dead globe.
  const guard = painter.indexOf('if (strength < 0.999) return')
  assert.ok(guard > 0, 'the painter no longer gates the markers on the channel being up')
  assert.ok(
    guard < painter.indexOf('markerAt('),
    'the marker gate moved after the markers are drawn'
  )
  // And the card must actually vary strength, or the gate is never closed.
  assert.match(
    card,
    /strength:\s*nknActive\.value\s*\?\s*1\s*:/,
    'the card passes a fixed strength; the globe would look live while disconnected'
  )
})
