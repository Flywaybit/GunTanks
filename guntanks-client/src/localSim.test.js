import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyLocalGravity,
  applyFireEffects,
  canSelectWeapon,
  computeLandY,
  destroyCircleAlpha,
  finishLocalBattle,
  randomWind,
  settleLocalTankY,
  simulateShot,
  windRerollAtRound,
} from './localSim.js';

const TANKS = [
  { id: 'tank_1', x: 100, y: 300, angle: 45, health: 1000, alive: true, facing: 'right', ss_cooldown: 0, weapon: 'shell1' },
  { id: 'tank_2', x: 700, y: 300, angle: 135, health: 1000, alive: true, facing: 'left', ss_cooldown: 0, weapon: 'shell1' },
];

function lastPoint(shot) {
  return shot.trajectory[shot.trajectory.length - 1];
}

test('wind speed 0 produces no lateral drift beyond gravity', () => {
  const calm = simulateShot({ tanks: TANKS, shooter: 'tank_1', weapon: 'shell1', power: 60, wind: { speed: 0, direction: 0 } });
  const windy = simulateShot({ tanks: TANKS, shooter: 'tank_1', weapon: 'shell1', power: 60, wind: { speed: 25, direction: 0 } });
  assert.ok(calm && windy);
  // Wind blowing right (cos(0)=1) must push the shell further right.
  assert.ok(lastPoint(windy).x > lastPoint(calm).x, `windy x=${lastPoint(windy).x} calm x=${lastPoint(calm).x}`);
});

test('right and left wind push in opposite horizontal directions', () => {
  const right = simulateShot({ tanks: TANKS, shooter: 'tank_1', weapon: 'shell1', power: 60, wind: { speed: 25, direction: 0 } });
  const left = simulateShot({ tanks: TANKS, shooter: 'tank_1', weapon: 'shell1', power: 60, wind: { speed: 25, direction: 180 } });
  assert.ok(right && left);
  assert.ok(lastPoint(right).x > lastPoint(left).x, `right=${lastPoint(right).x} left=${lastPoint(left).x}`);
});

test('shell leaves the arena and reports out_of_bounds', () => {
  const up = simulateShot({ tanks: TANKS, shooter: 'tank_1', weapon: 'shell1', power: 100, wind: { speed: 0, direction: 0 } });
  assert.ok(up);
  assert.equal(up.impact.kind, 'out_of_bounds');
  assert.ok(up.trajectory.length <= 240);
  assert.ok(up.duration_ms > 0);
});

test('terrain hit stops the shell', () => {
  const hit = simulateShot({
    tanks: TANKS,
    shooter: 'tank_1',
    weapon: 'shell2',
    power: 50,
    wind: { speed: 0, direction: 0 },
    solidAt: (x, y) => x >= 480 && x <= 520 && y >= -300 && y <= 650,
  });
  assert.ok(hit);
  assert.equal(hit.impact.kind, 'terrain');
  assert.equal(hit.terrain_destroyed.radius, 35);
  assert.deepEqual(hit.damages, []);
});

test('tank hit applies damage and elimination', () => {
  const shot = simulateShot({
    tanks: [
      { ...TANKS[0], angle: 0 },
      { ...TANKS[1], x: 320, y: 300, health: 200 },
    ],
    shooter: 'tank_1',
    weapon: 'shell1',
    power: 80,
    wind: { speed: 0, direction: 0 },
  });
  assert.ok(shot);
  assert.equal(shot.impact.kind, 'tank');
  assert.equal(shot.damages.length, 1);
  assert.equal(shot.damages[0].tank_id, 'tank_2');
  assert.equal(shot.damages[0].health_after, 70);
  assert.deepEqual(shot.eliminated_tank_ids, []);
});

test('SS cooldown semantics match the server', () => {
  let tank = { id: 'tank_1', weapon: 'shell1', ss_cooldown: 0, delay: 0 };
  assert.equal(canSelectWeapon(tank, 'ss'), true);
  tank = applyFireEffects(tank, 'ss');
  assert.equal(tank.ss_cooldown, 2, 'SS fires: set 3 then immediately decrement to 2');
  assert.equal(tank.weapon, 'shell1', 'SS resets the weapon to shell1');
  assert.equal(canSelectWeapon(tank, 'ss'), false, 'SS cannot be selected during cooldown');
  tank = applyFireEffects(tank, 'shell1');
  assert.equal(tank.ss_cooldown, 1);
  tank = applyFireEffects(tank, 'shell1');
  assert.equal(tank.ss_cooldown, 0);
  assert.equal(canSelectWeapon(tank, 'ss'), true);
  assert.equal(tank.delay, 750 * 2 + 1300, 'delay accumulates per weapon');
});

test('wind rerolls every full three rounds', () => {
  assert.equal(windRerollAtRound(4), true);
  assert.equal(windRerollAtRound(7), true);
  assert.equal(windRerollAtRound(3), false);
  assert.equal(windRerollAtRound(5), false);
});

test('random wind stays within speed 0-25 and direction 0-359', () => {
  for (let i = 0; i < 200; i += 1) {
    const wind = randomWind();
    assert.ok(wind.speed >= 0 && wind.speed <= 25);
    assert.ok(wind.direction >= 0 && wind.direction < 360);
  }
});

test('terrain alpha circle destruction and landing Y', () => {
  const width = 100;
  const height = 100;
  const alpha = new Uint8ClampedArray(width * height * 4).fill(255);
  destroyCircleAlpha(alpha, width, height, 50, 50, 10);
  assert.equal(alpha[((50 * width + 50) * 4) + 3], 0, 'center destroyed');
  assert.notEqual(alpha[((10 * width + 10) * 4) + 3], 0, 'outside circle remains');
  const floor = new Uint8ClampedArray(width * height * 4);
  for (let y = 50; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      floor[(y * width + x) * 4 + 3] = 255;
    }
  }
  const terrain = { width, height, data: floor };
  const settled = settleLocalTankY({ x: 50, y: 15 }, terrain);
  assert.equal(settled.y, 20, 'tank settles with bottom (y+30) on the floor at y=50');
  const landY = computeLandY(50, terrain);
  assert.equal(landY, 20);
});

function floorTerrain(width = 1200, height = 650, floorY = 420) {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let y = floorY; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      data[(y * width + x) * 4 + 3] = 255;
    }
  }
  return { width, height, data };
}

test('applyLocalGravity keeps a supported tank in place', () => {
  const terrain = floorTerrain(1200, 650, 420);
  const tanks = [{ id: 'tank_1', x: 100, y: 390, alive: true, health: 1000 }];
  const { changed, eliminated } = applyLocalGravity(tanks, terrain);
  assert.equal(changed, false);
  assert.deepEqual(eliminated, []);
  assert.equal(tanks[0].y, 390);
  assert.equal(tanks[0].alive, true);
});

test('applyLocalGravity drops a hanging tank onto the terrain', () => {
  const terrain = floorTerrain(1200, 650, 420);
  const tanks = [{ id: 'tank_1', x: 100, y: 385, alive: true, health: 1000 }];
  const { changed, eliminated } = applyLocalGravity(tanks, terrain);
  assert.equal(changed, true);
  assert.deepEqual(eliminated, []);
  assert.equal(tanks[0].y, 390, 'bottom (y+30) rests on the first solid row at y=420');
});

test('applyLocalGravity eliminates tanks out of bounds', () => {
  const terrain = floorTerrain(1200, 650, 420);
  const tanks = [
    { id: 'tank_1', x: 100, y: 390, alive: true, health: 1000 },
    { id: 'tank_2', x: 400, y: 700, alive: true, health: 1000 },
  ];
  const { changed, eliminated } = applyLocalGravity(tanks, terrain);
  assert.equal(changed, true);
  assert.deepEqual(eliminated, ['tank_2']);
  assert.equal(tanks[1].alive, false);
  assert.equal(tanks[1].health, 0);
  assert.equal(tanks[0].alive, true);
});

test('finishLocalBattle resolves win/draw and leaves ongoing battles open', () => {
  const ongoing = { tanks: [{ id: 'tank_1', alive: true }, { id: 'tank_2', alive: true }], phase: 'playing' };
  assert.equal(finishLocalBattle(ongoing), false);
  assert.equal(ongoing.phase, 'playing');

  const win = { tanks: [{ id: 'tank_1', alive: true }, { id: 'tank_2', alive: false }], phase: 'playing' };
  assert.equal(finishLocalBattle(win), true);
  assert.equal(win.phase, 'finished');
  assert.equal(win.result, 'win');
  assert.equal(win.winner_tank_id, 'tank_1');

  const draw = { tanks: [{ id: 'tank_1', alive: false }, { id: 'tank_2', alive: false }], phase: 'playing' };
  assert.equal(finishLocalBattle(draw), true);
  assert.equal(draw.phase, 'finished');
  assert.equal(draw.result, 'draw');
});
