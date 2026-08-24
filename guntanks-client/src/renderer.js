const terrainImage = new Image();
terrainImage.src = 'assets/terrain-full.png';
const tankRight = new Image();
tankRight.src = 'assets/tank-small-right.png';
const tankLeft = new Image();
tankLeft.src = 'assets/tank-small-left.png';

let terrainLayer;
let terrainBattleID = '';
let currentState;
let terrainReady = false;
let terrainLoadGeneration = 0;

function ensureTerrain(canvas, battleID) {
  if (terrainLayer && terrainBattleID === battleID) return;
  terrainLayer = document.createElement('canvas');
  terrainLayer.width = canvas.width;
  terrainLayer.height = canvas.height;
  terrainBattleID = battleID;
  terrainLoadGeneration += 1;
  terrainReady = false;
  const context = terrainLayer.getContext('2d');
  const draw = () => { if (terrainImage.naturalWidth > 0) context.drawImage(terrainImage, 50, 320); };
  if (terrainImage.naturalWidth > 0) draw(); else terrainImage.addEventListener('load', draw, { once: true });
}

export async function loadTerrainSnapshot(battleID, token) {
  if (!battleID || battleID === 'local') { terrainReady = true; return true; }
  const generation = terrainLoadGeneration;
  try {
    const response = await fetch(`/api/v1/battles/${encodeURIComponent(battleID)}/terrain-snapshot`, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
    if (!response.ok) throw new Error('terrain snapshot unavailable');
    const snapshot = await response.json();
    if (generation !== terrainLoadGeneration || terrainBattleID !== battleID) return false;
    const encoded = Uint8Array.from(atob(snapshot.data_base64), (c) => c.charCodeAt(0));
    if (snapshot.encoding !== 'gzip-bitset-v1') throw new Error('unsupported terrain encoding');
    const raw = new Uint8Array(await new Response(new Blob([encoded]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer());
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', raw)), (b) => b.toString(16).padStart(2, '0')).join('');
    if (snapshot.checksum && digest !== snapshot.checksum) throw new Error('terrain checksum mismatch');
    if (snapshot.width !== terrainLayer.width || snapshot.height !== terrainLayer.height) throw new Error('terrain dimensions mismatch');
    const ctx = terrainLayer.getContext('2d');
    const clearMask = () => {
      const image = ctx.getImageData(0, 0, snapshot.width, snapshot.height);
      for (let i = 0; i < snapshot.width * snapshot.height; i++) if ((raw[Math.floor(i / 8)] & (1 << (i % 8))) === 0) image.data[i * 4 + 3] = 0;
      ctx.putImageData(image, 0, 0);
      terrainReady = true;
    };
    if (terrainImage.naturalWidth > 0) { if (generation === terrainLoadGeneration) { ctx.drawImage(terrainImage, 50, 320); clearMask(); } }
    else terrainImage.addEventListener('load', () => { if (generation === terrainLoadGeneration && terrainBattleID === battleID) { ctx.drawImage(terrainImage, 50, 320); clearMask(); } }, { once: true });
    return true;
  } catch (_) { terrainReady = false; return false; }
}
export function isTerrainReady() { return terrainReady; }

export function render(canvas, state) {
  currentState = state;
  ensureTerrain(canvas, state?.battle_id || '');
  const context = canvas.getContext('2d');
  context.clearRect(0, 0, canvas.width, canvas.height);
  const sky = context.createLinearGradient(0, 0, 0, canvas.height);
  sky.addColorStop(0, 'blue'); sky.addColorStop(1, 'orange');
  context.fillStyle = sky; context.fillRect(0, 0, canvas.width, canvas.height);
  if (terrainLayer) context.drawImage(terrainLayer, 0, 0);
  context.font = '14px sans-serif';
  context.textAlign = 'center';
  for (const tank of state?.tanks || []) {
    if (!tank.alive) continue;
    const sprite = tank.facing === 'left' ? tankLeft : tankRight;
    if (sprite.complete) context.drawImage(sprite, tank.facing === 'left' ? tank.x - 10 : tank.x - 25, tank.y - 27);
    else { context.fillStyle = tank.id === state.current_tank_id ? '#53d769' : '#e05656'; context.fillRect(tank.x - 13, tank.y - 15, 26, 30); }
    context.fillStyle = '#d22';
    context.fillRect(tank.x - 18, tank.y + 18, 36, 4);
    context.fillStyle = '#2f5';
    context.fillRect(tank.x - 18, tank.y + 18, 36 * Math.max(0, Math.min(1, tank.health / 1000)), 4);
    const rings = [{radius:50, span:20, color:'rgba(255,255,255,.7)'}, {radius:50, span:5, color:'rgba(255,0,0,.7)'}, {radius:45, span:2, color:'rgba(255,0,0,1)'}];
    for (const ring of rings) {
      context.beginPath();
      if (tank.facing === 'left') context.arc(tank.x + 20, tank.y, ring.radius, (tank.angle + ring.span + 180) * Math.PI / 180, (tank.angle - ring.span + 180) * Math.PI / 180, true);
      else context.arc(tank.x, tank.y, ring.radius, (-tank.angle + ring.span) * Math.PI / 180, (-tank.angle - ring.span) * Math.PI / 180, true);
      context.strokeStyle = ring.color; context.lineWidth = 2; context.stroke();
    }
    context.fillStyle = '#fff';
    context.fillText(`${tank.id} ${Math.ceil(tank.health)}`, tank.x, tank.y - 34);
  }
}

export function playShot(canvas, shot, done = () => {}) {
  const points = shot?.trajectory || [];
  if (!points.length) { applyTerrainDamage(shot?.terrain_destroyed); done(); return; }
  const started = performance.now();
  const duration = Math.max(1, shot.duration_ms || points.at(-1).t_ms || 1);
  function frame(now) {
    const elapsed = Math.min(duration, now - started);
    let point = points.at(-1);
    for (const candidate of points) { if (candidate.t_ms >= elapsed) { point = candidate; break; } }
    render(canvas, currentState);
    const context = canvas.getContext('2d');
    context.beginPath(); context.arc(point.x, point.y, 8, 0, Math.PI * 2); context.fillStyle = '#ffec82'; context.fill();
    if (elapsed < duration) requestAnimationFrame(frame);
    else { applyTerrainDamage(shot.terrain_destroyed); render(canvas, currentState); done(); }
  }
  requestAnimationFrame(frame);
}

function applyTerrainDamage(damage) {
  if (!terrainLayer || !damage) return;
  const context = terrainLayer.getContext('2d');
  context.save(); context.globalCompositeOperation = 'destination-out';
  context.beginPath(); context.arc(damage.x, damage.y, damage.radius, 0, Math.PI * 2); context.fill(); context.restore();
}
