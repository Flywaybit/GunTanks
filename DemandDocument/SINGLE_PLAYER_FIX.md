# 单人模式修复方案

> 目标：修复单人模式已知 bug，并使单机对局逻辑与原版 Guntanks 一致，同时保证不引入任何新 bug、不影响在线对局。
> 范围：纯前端，仅 `localMode` 相关代码路径；不修改服务器任何代码。

## 1. 现状与根因

1. **Leave 退不出**：本地 `battle.leave` 已同步切回大厅并返回 `true`，但 Leave 按钮沿用在线流程又切到 `LEAVING_BATTLE` 等服务器确认，本地永远等不到确认 → 卡在"正在离开"。
2. **落地错误 / 陷入地形**：落点 `land_y` 在点击瞬间用 `getTerrainAlphaData()` 计算，地形图可能未加载（返回 `null` → 回退 `land_y=300`），且本地 `terrainLayer` 跨局复用旧地形；x=1070 处地表高，坦克底边 `y+30` 低于地表 → 陷入，其余列悬空。
3. **第一轮后卡住**：本地开火"炮弹落地后才切换回合"，但 30 秒超时定时器在动画期间仍会推进回合/解锁输入，与动画完成回调覆盖过期状态、`actionLocked` 守卫形成竞态。
4. **缺原版机制**：无连续重力、无"砸坑后坠落"、无"越界淘汰"，地形被炸后坦克不会落坑、也无法因坠落出界结束对局。

## 2. 已确认决策

- 回合顺序：固定轮转 tank_1→2→3→4，与在线一致。
- 补上连续重力 + 砸坑坠落 + 越界淘汰。
- 卡死修复采用保守方案：动画期间冻结本地超时、开火加锁、回调保证必然解锁。

## 3. 修复设计

### 3.1 Leave 退出与开新对局

- 新增 `leaveLocalBattle()`：`localMode=false`、清所有本地定时器、清本地状态、重置地形画布、`page(PAGE.LOBBY)`、`leaveRequested=false`。
- Leave 按钮：`localMode` 时直接调 `leaveLocalBattle()` 并 `return`，不再进入在线式 `LEAVING_BATTLE`。
- 新增 `resetLocalState()`：清 `localTimer`/`localGravityTimer`/`localIntroTimer`、`clearBattleState()`、`clearInputState()`、`store.result=null`、`store.pendingBattle=null`、`setLeftBattle(false)`，并调用 renderer 的 `resetTerrain()`。
- `startSinglePlayer` 开头调用 `resetLocalState()`，保证每次开局干净、地形无旧弹坑。

### 3.2 落地与地形

- renderer 新增 `resetTerrain()`：置空 `terrainLayer`/`terrainBattleID`/`terrainReady`。
- renderer 新增 `waitForTerrainImage()`（Promise）与 `prepareLocalTerrain(canvas)`：等图片加载完成后重建本地地形层并返回 alpha 数据。
- `startSinglePlayer` 改为 async：`await prepareLocalTerrain($('stage'))` 后再算每辆坦克的 `land_y`（不再用空/旧数据），`await` 后检查 `if (!localMode) return;` 防止加载期间用户离开后继续写状态。
- 增加 `localStarting` 标志防止连点多次开局。

### 3.3 连续重力 + 砸坑坠落 + 越界淘汰

- localSim 新增纯函数 `applyLocalGravity(tanks, terrain)`：对每个存活坦克用 `settleLocalTankY` 落地；越界（`|x|>1200 || |y|>650`）则置 `alive=false`、`health=0` 并收集 id；返回 `{ changed, eliminated }`。
- localSim 新增 `finishLocalBattle(state)`：存活 1 → `phase=finished/result=win/winner_tank_id`；存活 0 → `draw`；否则返回 false。
- main.js 新增 `localGravityTimer`（约 100ms 一次），仅当 `localMode && phase==='playing' && !shotAnimating && !introAnimating` 时运行：`applyLocalGravity` 后若有 eliminated，则 `revision++/event_seq++`；若被淘汰的是当前回合坦克且未结束，按服务器 `nextTurn` 规则推进回合；`finishLocalBattle` 判定是否结束；`setBattle(state)` 刷新；若结束 `finalizeBattle(state)` 并清本地定时器。
- `advanceLocalTurnOn` 复用 `finishLocalBattle` 收尾，去掉内联的 `alive===1` 判断。

### 3.4 开火竞态（卡死）修复

- `handleLocalCommand` 的 `battle.fire`：入口先 `if (shotAnimating) return false;`（配合 `actionLocked` 双重防重入）。
- `localTimer` 与 `localGravityTimer` 的条件都追加 `&& !shotAnimating && !introAnimating`，动画/下落期间冻结超时与重力。
- 开火仍保持"落地后切回合"：动画完成回调用 `try/finally` 包裹，`finally` 里 `shotAnimating=false`；只要未结束就 `store.input.actionLocked=false`，保证异常也不锁死。
- 回调保留 `generation / page / battle_id` 守卫，避免写回过期对局。

### 3.5 本地定时器生命周期

| 定时器 | 启动 | 运行条件 | 停止 |
| --- | --- | --- | --- |
| `localIntroTimer`（1.5s） | 开局 | 一次性 | leave / 触发后清 |
| `localTimer`（250ms） | 开局 | `playing && !shotAnimating && !introAnimating` | leave / 结束 |
| `localGravityTimer`（100ms） | 开局 | `playing && !shotAnimating && !introAnimating` | leave / 结束 |
| 开火动画 rAF | 开火 | 动画完成即停 | 守卫失效即停 |
| 下落动画 rAF | 开局 | `introAnimating` | 落地/leave |

## 4. 文件改动清单

### `guntanks-client/src/localSim.js`

- 新增 `applyLocalGravity(tanks, terrain)`、`finishLocalBattle(state)`。

### `guntanks-client/src/renderer.js`

- 新增 `resetTerrain()`、`waitForTerrainImage()`、`prepareLocalTerrain(canvas)`。
- 其余现有函数（`render`/`playShot`/`applyTerrainDamage`/`loadTerrainSnapshot`）不改。

### `guntanks-client/src/main.js`

- 新增 `leaveLocalBattle()`、`resetLocalState()`、`localGravityTimer`、`localStarting`。
- 修改 `startSinglePlayer`（async + 地形就绪 + 干净重置）。
- 修改 Leave 按钮处理（本地直退）。
- 修改 `handleLocalCommand` fire 防重入 + done 回调 try/finally。
- 修改 `localTimer` 条件；复用 `finishLocalBattle`。

## 5. 边界场景

- 连点 Single Player：`localStarting` 拦重复。
- 加载地形期间点 Leave：`await` 后 `if (!localMode) return` 不落地。
- 下落/开火动画期间点 Leave：立即清定时器与动画守卫，回大厅。
- 开火砸坑 → 坑上坦克坠落；坠出边界 → 淘汰；当前回合坦克坠落 → 推进回合；仅剩 1 辆 → 结算。
- 移动越坑/斜坡：移动时仍走 `settleLocalTankY`，与重力一致。

## 6. 测试与验收

- localSim 单测新增：`applyLocalGravity`（支撑不变 / 悬空下落 / 越界淘汰）、`finishLocalBattle`（win / draw / 未结束）。
- 现有 `localSim.test.js` 9 项全绿；`node --check` 通过；`npm run build` 通过。
- 浏览器验收：开局 4 辆坦克 1.5s 落到地表不陷入不悬空；点 Leave 立即回大厅；再点 Single Player 开新局地形无旧弹坑；完整打一轮后回合回到 tank_1 可继续不卡住；开火砸坑后掉落、越界淘汰、结算正常；在线双人对局与断线重连回归不受影响。

## 7. 防回归（绝不改动）

- 不修改任何服务端代码（engine/actor/manager/web/ws 等）。
- 不改 `socket.js`、`api.js`、`matchController.js`、`stateMachine.js`、`inputController.js`。
- `renderer.js` 仅新增三个本地专用导出，在线渲染/快照/弹道动画路径零改动。
- 所有新增逻辑都限定在 `localMode` 分支，或纯函数 `localSim.js` 中，不影响在线对局。
