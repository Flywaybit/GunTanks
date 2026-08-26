// Local single-player physics. These helpers intentionally mirror the online
// authoritative engine (guntanks-server/engine/engine.go) so a local match
// behaves like the server: same weapon params, wind formula, 0.5-step
// trajectory, 4000-step bound and out-of-bounds rules.

export const WEAPONS = Object.freeze({
  shell1: Object.freeze({ radius: 8, weight: 0.055, damage: 130, delay: 750, terrainRadius: 48 }),
  shell2: Object.freeze({ radius: 7, weight: 0.08, damage: 240, delay: 900, terrainRadius: 35 }),
  ss: Object.freeze({ radius: 10, weight: 0.08, damage: 350, delay: 1300, terrainRadius: 70 }),
});

export function randomWind() {
  return { speed: Math.floor(Math.random() * 26), direction: Math.floor(Math.random() * 360) };
}

// windChangesOnTurn mirrors the server nextTurn rule: a full cycle of alive
// players advances the round, and wind rerolls when entering round 4/7/10...
export function windChangesOnTurn(turnsCompleted, alive) {
  return alive > 0 && turnsCompleted % alive === 0;
}

export function windRerollAtRound(round) {
  return (round - 1) % 3 === 0;
}

export function canSelectWeapon(tank, weapon) {
  return weapon !== 'ss' || (tank.ss_cooldown || 0) <= 0;
}

// applyFireEffects mirrors the server: firing SS sets cooldown 3, immediately
// decrements it and resets the weapon to shell1; every subsequent fire
// decrements the cooldown by one.
export function applyFireEffects(tank, weapon) {
  const params = WEAPONS[weapon];
  const next = { ...tank, delay: (tank.delay || 0) + params.delay };
  if (weapon === 'ss') {
    next.ss_cooldown = 3;
    next.weapon = 'shell1';
  }
  next.ss_cooldown = Math.max(0, (next.ss_cooldown || 0) - 1);
  return next;
}

export function simulateShot({ tanks, shooter, weapon, power, wind, solidAt = () => false }) {
  const params = WEAPONS[weapon];
  const tank = tanks.find((t) => t.id === shooter);
  if (!tank) {
    return null;
  }
  const direction = tank.facing === 'left' ? -1 : 1;
  let x = tank.x + 25;
  let y = tank.y - 5;
  if (tank.facing === 'left') {
    x = tank.x - 16;
    y = tank.y - 10;
  }
  const xv = direction * power * 0.25 * Math.cos((tank.angle || 0) * Math.PI / 180);
  const yv = -power * 0.25 * Math.sin((tank.angle || 0) * Math.PI / 180);
  const points = [];
  let impact = { kind: 'out_of_bounds', x, y };
  let hit = null;
  const windSpeed = wind?.speed || 0;
  const windRad = (wind?.direction || 0) * Math.PI / 180;
  for (let step = 0, simTime = 0; step < 4000; step += 1, simTime += 0.5) {
    points.push({ x, y, t_ms: step * 16 });
    for (const candidate of tanks) {
      if (!candidate.alive) {
        continue;
      }
      const dx = x + params.radius - (candidate.x + 12.5);
      const dy = y + params.radius - (candidate.y + 15);
      if (Math.hypot(dx, dy) < params.radius + 12.5) {
        hit = candidate;
        impact = { kind: 'tank', x, y };
        break;
      }
    }
    if (hit) {
      break;
    }
    if (solidRect(solidAt, Math.round(x), Math.round(y), params.radius * 2, params.radius * 2)) {
      impact = { kind: 'terrain', x, y };
      break;
    }
    if (x < -params.radius || x > 1200 + params.radius || y < -200 || y > 650 + params.radius) {
      impact = { kind: 'out_of_bounds', x, y };
      break;
    }
    x += xv + 0.02 * windSpeed * Math.cos(windRad) * simTime;
    y += yv + 0.5 * params.weight * simTime * simTime - 0.02 * windSpeed * Math.sin(windRad) * simTime;
  }
  const damages = [];
  let healthAfter = hit ? Math.max(0, hit.health - params.damage) : 0;
  if (hit) {
    damages.push({ tank_id: hit.id, amount: params.damage, health_after: healthAfter });
  }
  return {
    shooter_tank_id: shooter,
    weapon,
    power,
    duration_ms: points.length ? points[points.length - 1].t_ms : 0,
    trajectory: samplePoints(points, 240),
    impact,
    terrain_destroyed: { x: impact.x, y: impact.y, radius: params.terrainRadius },
    damages,
    eliminated_tank_ids: hit && healthAfter === 0 ? [hit.id] : [],
    damage: params.damage,
  };
}

function solidRect(solidAt, x, y, width, height) {
  for (let py = y; py < y + height; py += 1) {
    for (let px = x; px < x + width; px += 1) {
      if (solidAt(px, py)) {
        return true;
      }
    }
  }
  return false;
}

export function samplePoints(points, max) {
  if (points.length <= max) {
    return points;
  }
  const out = [];
  for (let i = 0; i < max; i += 1) {
    out.push(points[Math.floor(i * (points.length - 1) / (max - 1))]);
  }
  return out;
}

export function destroyCircleAlpha(alpha, width, height, cx, cy, radius) {
  const r2 = radius * radius;
  for (let py = cy - radius; py <= cy + radius; py += 1) {
    for (let px = cx - radius; px <= cx + radius; px += 1) {
      if (px < 0 || py < 0 || px >= width || py >= height) {
        continue;
      }
      const dx = px - cx;
      const dy = py - cy;
      if (dx * dx + dy * dy <= r2) {
        alpha[(py * width + px) * 4 + 3] = 0;
      }
    }
  }
  return alpha;
}

// computeLandY finds the first terrain row below a tank (mirrors the server
// Configure scan: tank bottom at n+30 touches the first solid row).
export function computeLandY(x, terrain) {
  if (!terrain?.data) {
    return 300;
  }
  const { data, width, height } = terrain;
  for (let n = 0; n < height; n += 1) {
    if (solidAtRectAlpha(data, width, height, Math.round(x), n + 30, 25, 1)) {
      return n;
    }
  }
  return 300;
}

export function settleLocalTankY(tank, terrain) {
  const next = { ...tank };
  if (!terrain?.data) {
    return next;
  }
  const solid = (x, y) => solidAtRectAlpha(terrain.data, terrain.width, terrain.height, Math.round(x), Math.round(y), 25, 1);
  if (solid(next.x, next.y + 30)) {
    for (let i = 0; i < 30 && solid(next.x, next.y + 29); i += 1) {
      next.y -= 1;
    }
    return next;
  }
  for (let i = 0; i < 6; i += 1) {
    if (solid(next.x, next.y + 30)) {
      break;
    }
    next.y += 1;
  }
  return next;
}

function solidAtRectAlpha(data, width, height, x, y, w, h) {
  for (let py = y; py < y + h; py += 1) {
    for (let px = x; px < x + w; px += 1) {
      if (px < 0 || py < 0 || px >= width || py >= height) {
        continue;
      }
      if (data[(py * width + px) * 4 + 3] !== 0) {
        return true;
      }
    }
  }
  return false;
}
