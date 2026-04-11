/**
 * curve-utils.js — Bezier / Catmull-Rom curve utilities for Leaflet maps.
 *
 * Provides:
 *   smoothRoute(latlngs, opts)      — waypoint route → smooth Bezier curve
 *   smoothTrail(latlngs, opts)      — real-time trail → Catmull-Rom spline
 *   animatePolyline(map, latlngs, opts) — progressive draw animation
 *
 * All functions accept and return arrays of [lat, lng].
 */

/* ---- helpers ---- */
function _haversineM(a, b) {
  const R = 6371000, toR = Math.PI / 180;
  const dLat = (b[0] - a[0]) * toR, dLng = (b[1] - a[1]) * toR;
  const s = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * toR) * Math.cos(b[0] * toR) * Math.sin(dLng / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(s));
}

function _bearing(a, b) {
  const toR = Math.PI / 180;
  const dLng = (b[1] - a[1]) * toR;
  const y = Math.sin(dLng) * Math.cos(b[0] * toR);
  const x = Math.cos(a[0] * toR) * Math.sin(b[0] * toR) - Math.sin(a[0] * toR) * Math.cos(b[0] * toR) * Math.cos(dLng);
  return (Math.atan2(y, x) * 180 / Math.PI + 360) % 360;
}

/* ---- cubic Bezier evaluator ---- */
function _cubicBezier(t, p0, p1, p2, p3) {
  const u = 1 - t;
  return [
    u*u*u*p0[0] + 3*u*u*t*p1[0] + 3*u*t*t*p2[0] + t*t*t*p3[0],
    u*u*u*p0[1] + 3*u*u*t*p1[1] + 3*u*t*t*p2[1] + t*t*t*p3[1],
  ];
}

/**
 * smoothRoute — convert waypoint array into a smooth Bezier polyline.
 *
 * For each pair of adjacent waypoints, a cubic Bezier is computed with
 * control point offsets proportional to segment distance and heading change.
 *
 * @param {Array<[number,number]>} latlngs  — waypoints [[lat,lng], ...]
 * @param {Object} opts
 * @param {number} opts.segPoints  — sample points per segment (default 16)
 * @param {number} opts.tension    — 0-1, higher = straighter (default 0.35)
 * @returns {Array<[number,number]>} densified smooth polyline
 */
function smoothRoute(latlngs, opts) {
  opts = opts || {};
  const segPts = opts.segPoints || 16;
  const tension = opts.tension != null ? opts.tension : 0.35;
  if (!latlngs || latlngs.length < 2) return latlngs || [];
  if (latlngs.length === 2) return latlngs.slice();

  const result = [latlngs[0]];

  for (let i = 0; i < latlngs.length - 1; i++) {
    const p0 = latlngs[i];
    const p3 = latlngs[i + 1];
    const dist = _haversineM(p0, p3);

    // Adaptive curvature: more arc for longer segments and sharper turns
    let curveFactor = 0.25; // base
    if (dist > 2000) curveFactor = 0.38;
    else if (dist > 500) curveFactor = 0.30;
    else if (dist < 100) curveFactor = 0.15;

    // Heading change increases curvature
    const prevBearing = i > 0 ? _bearing(latlngs[i - 1], p0) : _bearing(p0, p3);
    const nextBearing = i < latlngs.length - 2 ? _bearing(p3, latlngs[i + 2]) : _bearing(p0, p3);
    const curBearing = _bearing(p0, p3);
    const turnIn = Math.abs(((curBearing - prevBearing) + 540) % 360 - 180);
    const turnOut = Math.abs(((nextBearing - curBearing) + 540) % 360 - 180);
    if (turnIn > 60) curveFactor *= 1.3;
    if (turnOut > 60) curveFactor *= 1.3;

    curveFactor *= (1 - tension);

    // Convert distance to degree offset (rough)
    const offsetDeg = (dist / 111000) * curveFactor;
    // Perpendicular offset direction
    const midLat = (p0[0] + p3[0]) / 2;
    const midLng = (p0[1] + p3[1]) / 2;
    const dLat = p3[0] - p0[0];
    const dLng = p3[1] - p0[1];
    const len = Math.sqrt(dLat * dLat + dLng * dLng) || 1e-10;
    // Perpendicular unit vector (rotated 90 deg)
    const perpLat = -dLng / len;
    const perpLng = dLat / len;

    // Alternate perpendicular side to create S-curve feel
    const side = (i % 2 === 0) ? 1 : -1;
    const cp1 = [
      p0[0] + dLat * 0.33 + perpLat * offsetDeg * side,
      p0[1] + dLng * 0.33 + perpLng * offsetDeg * side,
    ];
    const cp2 = [
      p0[0] + dLat * 0.67 - perpLat * offsetDeg * side * 0.5,
      p0[1] + dLng * 0.67 - perpLng * offsetDeg * side * 0.5,
    ];

    for (let s = 1; s <= segPts; s++) {
      result.push(_cubicBezier(s / segPts, p0, cp1, cp2, p3));
    }
  }
  return result;
}

/**
 * smoothTrail — Catmull-Rom spline through real-time trail points.
 *
 * Suitable for already-collected GPS trail data where we want smooth
 * interpolation *through* the actual recorded points.
 *
 * @param {Array<[number,number]>} pts  — recorded positions
 * @param {Object} opts
 * @param {number} opts.segPoints  — interpolation points per segment (default 6)
 * @param {number} opts.alpha      — 0=uniform, 0.5=centripetal, 1=chordal (default 0.5)
 * @returns {Array<[number,number]>} densified smooth polyline
 */
function smoothTrail(pts, opts) {
  opts = opts || {};
  const seg = opts.segPoints || 6;
  if (!pts || pts.length < 3) return pts ? pts.slice() : [];

  const result = [pts[0]];
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(i - 1, 0)];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[Math.min(i + 2, pts.length - 1)];

    for (let s = 1; s <= seg; s++) {
      const t = s / seg;
      const t2 = t * t, t3 = t2 * t;
      // Catmull-Rom matrix
      const lat = 0.5 * ((2*p1[0]) + (-p0[0]+p2[0])*t + (2*p0[0]-5*p1[0]+4*p2[0]-p3[0])*t2 + (-p0[0]+3*p1[0]-3*p2[0]+p3[0])*t3);
      const lng = 0.5 * ((2*p1[1]) + (-p0[1]+p2[1])*t + (2*p0[1]-5*p1[1]+4*p2[1]-p3[1])*t2 + (-p0[1]+3*p1[1]-3*p2[1]+p3[1])*t3);
      result.push([lat, lng]);
    }
  }
  return result;
}

/**
 * animatePolyline — progressively draw a polyline on a Leaflet map.
 *
 * @param {L.Map} map
 * @param {Array<[number,number]>} latlngs — full set of points
 * @param {Object} opts
 * @param {string} opts.color
 * @param {number} opts.weight
 * @param {number} opts.opacity
 * @param {number} opts.durationMs  — total animation time (default 2000)
 * @param {boolean} opts.useCanvas  — prefer Canvas renderer (default true)
 * @returns {{ polyline: L.Polyline, cancel: Function }}
 */
function animatePolyline(map, latlngs, opts) {
  opts = opts || {};
  const duration = opts.durationMs || 2000;
  const renderer = opts.useCanvas !== false ? L.canvas() : undefined;

  const polyOpts = {
    color: opts.color || '#6366f1',
    weight: opts.weight || 3,
    opacity: opts.opacity || 0.85,
    dashArray: opts.dashArray || null,
    renderer: renderer,
  };
  const polyline = L.polyline([], polyOpts).addTo(map);

  const total = latlngs.length;
  if (total === 0) return { polyline, cancel: function(){} };

  let cancelled = false;
  const startTime = performance.now();

  function step(now) {
    if (cancelled) return;
    const elapsed = now - startTime;
    const progress = Math.min(elapsed / duration, 1);
    const idx = Math.floor(progress * total);
    polyline.setLatLngs(latlngs.slice(0, idx + 1));
    if (progress < 1) requestAnimationFrame(step);
  }
  requestAnimationFrame(step);

  return {
    polyline: polyline,
    cancel: function() { cancelled = true; },
  };
}
