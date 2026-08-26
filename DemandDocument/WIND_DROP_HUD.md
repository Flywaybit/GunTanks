# 需求方案：局内辨识度 / 风力指示 / 开局坦克下落

> 适用范围：GunTanks（guntanks-client + guntanks-server + 单人模式）。
> 原则：只新增与修正，不改原版键位（A/D/W/S、1/2/3、空格）与既有在线对局规则；
> 所有改动必须保证现有对局逻辑（开火、换弹、超时、淘汰、结算、断线重连）不回归。

## 0. 已确认决策

| 编号 | 决策 |
| --- | --- |
| 1A | 对局内坦克上方标签、回合队列、结算界面均把 tank_1/tank_2 替换为 username |
| 2C | 当前回合玩家的红色标识只加在坦克头顶标签上（回合队列不加红） |
| 3A | 风力保持每满 3 回合重掷一次，与原版一致 |
| 4A | 风速保持随机 0–25，无风(0)偶尔出现，不人为提高无风概率 |
| 5  | 风力圆形 UI 下方加左右方向箭头：按风向水平分量判断左右，speed=0 时隐藏箭头、数字显示 0 |
| 6A | 下落过场采用服务端主导：落地前冻结回合计时、拒绝对战指令，落地后才开始 30 秒倒计时与输入 |
| 7A | 下落动画时长固定 1.5 秒 |
| 8A | 下落期间隐藏主倒计时并显示"准备中…"，落地后开始 30 秒倒计时 |
| 9A | 单人模式同样加入下落过场 |
| 附加 | 单人模式恢复风力（生成、变化、弹道影响与在线/原版一致） |

---

## 1. 需求一：对局内显示 username，当前回合坦克标签变红

### 1.1 现状

- 画布上每辆坦克头顶标签显示 `tank_1 1000`（guntanks-client/src/renderer.js）。
- 右上角回合队列以 `li` 显示 `tank_1`，当前回合追加 `*`（guntanks-client/src/main.js）。
- 结算界面显示 `Winner: tank_1`（guntanks-client/src/main.js finalizeBattle）。
- 服务端 `battle.started` / `battle.snapshot` 已携带 `players`（`user_id`、`username`、`tank_id`），
  客户端 `store.battleIdentity.playersByTankId` 已建立 tank_id → player 映射，当前渲染未使用。

### 1.2 改动点（纯客户端）

1. **坦克头顶标签**：显示 `username 血量`；当前回合玩家的 username 文字用红色（#f00），
   其余玩家保持白色；血量条等其余绘制不变。
2. **回合队列**：`li` 显示 username；当前回合保留既有 `*` 标记（不加红，符合 2C）。
3. **结算界面**：`Winner:` 后显示胜者 username。
4. **映射与回退**：username 缺失时回退显示 tank_id；单人模式继续显示 `Player 1..4`。
5. **实现方式**：在 `setBattle` 时把 battleIdentity 的 tank_id → username 映射挂到渲染态
   （仅客户端内存字段，不进入协议、不参与序列化），renderer 与结算逻辑统一读取；
   单机模式沿用现有本地 battleIdentity。

### 1.3 验收

- 双人匹配双浏览器：双方看到的坦克标签、回合队列均为各自 username；
  当前回合方的坦克头顶 username 为红色，回合队列仍带 `*`。
- 结算页显示胜者 username。
- 单人模式：标签与队列显示 Player 1..4，当前回合标签红色。
- 断线重连后标签/队列仍为 username（快照重建映射）。

---

## 2. 需求二：风力指示与风向箭头 + 单人模式恢复风力

### 2.1 现状

- 在线对局（服务端权威）：开局必生成 `speed 0–25`、`direction 0–359` 的风；
  每满 3 回合重掷一次；两次重掷之间恒定、不衰减；弹道公式为
  `x += xv + 0.02*speed*cos(dir)*t`、`y += yv + 0.5*w*t*t - 0.02*speed*sin(dir)*t`（t 步进 0.5），
  与原版 Guntanks 完全一致（guntanks-server/engine/engine.go）。
- HUD 仅有数字 `#wind-text`，无方向指示（guntanks-client/index.html）。
- 单人模式（本地模式）：`wind` 硬编码 `{speed:0, direction:0}` 且从不变化，
  本地开火为即时伤害、不计算弹道与风——这是与原版的行为差异（原版本地 4 人对战有完整风力）。

### 2.2 改动点：在线 HUD（纯客户端）

1. 在圆形风力 UI（`#wind-container`）下方新增箭头元素（如 `#wind-arrow`，
   可用 CSS/SVG/字符箭头实现）。
2. 显示规则：
   - `speed === 0`：隐藏箭头，数字显示 `0`。
   - `speed > 0`：按方向水平分量显示箭头——`cos(direction) >= 0` 显示右箭头，否则左箭头；
     数字始终显示当前权威风速。
3. 数字与箭头在每次权威状态更新（`setBattle`）时同步刷新；现有"数字在状态推送时刷新"的
   行为保持不变，仅新增箭头同步。
4. 不改变在线风力数值分布、变化频率、弹道公式与服务器逻辑。

### 2.3 改动点：单人模式恢复风力（本次新增）

1. **生成与变化**：本地对局开局随机生成 `speed 0–25`、`direction 0–359`；
   本地回合推进到满 3 回合时按在线 `nextTurn` 的同一规则重掷
   （全员各行动一次计 1 回合，进入第 4/7/10…回合时变化）。
2. **本地开火改为本地弹道模拟**（与原版、在线一致的物理）：
   - 武器参数与在线一致：shell1（radius 8 / weight .055 / damage 130 / delay 750）、
     shell2（7 / .08 / 240 / 900）、SS（10 / .08 / 350 / 1300，冷却 3 且开火后武器复位 shell1）。
   - 弹道逐 0.5 步进模拟，公式与服务器 `FireWithTerrain` 一致（含当前风力作用、4000 步上限、越界判定）。
   - 地形碰撞按本地地形图层 alpha 判定（同一张 `assets/terrain-full.png`，绘制偏移 50,320），
     命中地形按武器半径（48/35/70）销毁地形圆。
   - 命中坦克结算伤害/淘汰，SS 冷却语义与服务器对齐
     （开火时置 3 并立即减 1，之后每次开火递减，冷却结束前不可选 SS）。
3. **流程与防重入**：开火 → 计算弹道与结算状态 → 复用现有 `playShot` 播放炮弹动画 →
   动画结束后应用结算状态并推进回合（与在线 `battle.shot_resolved` 流程同构）；
   本地开火期间锁定输入，动画结束前不允许重复开火/移动/瞄准。
4. **HUD**：单机同样显示风力数字与方向箭头，规则与 2.2 一致。

### 2.4 明确不做

- 不改在线风力的分布、频率、公式。
- 不新增风声音效（`wind.mp3` 资源已存在，本次不接入）。
- 不改原版键位与操作方式。

---

## 3. 需求三：开局坦克下落过场（服务端主导）

### 3.1 设计原则

- 服务端权威：下落阶段自对局创建起固定 1500ms（7A）。
- 期间服务端冻结回合计时并拒绝一切对战指令（move/aim/select_weapon/fire），
  只允许生命周期操作（leave、断线消除、pause/resume）。
- 落地（intro 结束）后才进入 playing 状态：30 秒倒计时从落地时刻起算（8A），
  键盘操作才生效；客户端同时锁定输入，与服务端拒绝形成双保险。
- 引擎默认行为不变：`NewState` 保持现有语义（出生 Y=300、无 intro），
  下落阶段由 Manager/Actor 在创建时叠加，保证现有引擎单测不受影响。

### 3.2 服务端改动

1. **引擎 State**：新增 `IntroEndMS int64`（`json:"intro_end_ms,omitempty"`）；
   **Tank** 新增 `LandY float64`（`json:"land_y,omitempty"`，下落期间的目标落地 Y，权威值）。
2. **创建流程（Manager.Create / Actor.Configure）**：
   - 计算每辆坦克的权威落地 Y（复用现有 `settleTankY` 对地形的判定）。
   - `IntroEndMS = 创建时刻 + 1500ms`。
   - 状态中坦克 Y 置为出生 Y（画布上方，如 -200，与原版一致），`LandY` 记录落地 Y。
   - `TurnDeadlineMS = IntroEndMS + turnTimeout`（落地后才开始 30 秒）。
3. **Actor 行为**：
   - `IntroEndMS` 之前：跳过重力下落与超时检查；`move_start/aim_start/select_weapon/fire`
     一律拒绝（返回 `INVALID_STATE` 或新增 `INTRO_IN_PROGRESS` 错误码）；
     `leave`、`disconnect_timeout`、`pause/resume` 正常处理。
   - `IntroEndMS` 到达瞬间：各坦克 Y = LandY，发 `battle.intro_complete`
     （携带完整 state，phase=playing，turn_deadline_ms 已起算）；随后照常执行回合逻辑。
   - 防御：落地时执行一次越界消除检查，防止"落地位置越界漏检"。
4. **事件与协议（只增不改）**：
   - `battle.started` 及下落期间的 `battle.snapshot`（重连）携带 `intro_end_ms` 与各坦克 `land_y`。
   - 新增 `battle.intro_complete` 事件（EventSeq 递增，客户端按现有状态合并规则处理）。
   - intro 期间对局结束（如玩家 leave 导致剩 1 人）：正常发 `battle.finished`，
     不再发 `intro_complete`；客户端按现有结算流程处理。

### 3.3 客户端改动

1. **开场流程**：收到 `battle.started`（或本地开局）后进入"准备中…"状态：
   - 隐藏主倒计时，显示"准备中…"提示（可复用主计时区或独立提示层）。
   - 锁定输入：`canReceiveBattleInput`/inputController 在 intro 期间返回不可操作。
   - 坦克按下落动画绘制：以 `server_time_ms + serverOffset` 校准本地时间，
     从出生 Y 到 `land_y` 在 `intro_end_ms` 前完成插值（仅视觉，不写回权威状态）。
2. **落地**：收到 `battle.intro_complete` 后应用权威状态、退出动画、解锁输入、
   显示剩余倒计时；若动画先于事件结束，则停在 land_y 等待事件。
3. **重连**：intro 已结束 → 直接应用快照进入对局；intro 进行中 → 按剩余时长继续动画；
   期间任何指令都会被服务端拒绝（双保险）。
4. **单人模式（9A）**：本地同样执行 1.5s 下落（land_y 由本地地形图层按与服务器相同规则计算），
   本地 30 秒计时从落地后起算，下落期间本地输入锁定；与 2.3 的风力/弹道模拟共存。
5. **动画安全**：复用现有"代际 + 页面 + battle_id"守卫，页面切换/对局结束/断线时
   立即取消动画与定时器，防止回调写回过期状态。

### 3.4 验收

- 双浏览器双人匹配：双方同时看到坦克从上方落下约 1.5s，落地后出现 30 秒倒计时；
  下落期间按 A/D/W/S/空格/1/2/3 无任何效果，服务端日志确认指令被拒绝。
- 落地后首回合正常：移动、瞄准、蓄力开火、换弹、超时、淘汰、结算全部与改动前一致。
- 单人模式：4 辆坦克同样下落，落地后开始倒计时与操作，风力 HUD 正常。
- 断线重连：分别覆盖"下落中重连"与"落地后重连"两种场景，重连后状态与对方一致。
- 下落中玩家 leave：对局正常结束，无残留动画或状态错误。

---

## 4. 回归安全与测试

### 4.1 防回归措施

- 协议只增不改：所有新字段 `omitempty`，旧客户端/旧事件解析不受影响。
- 在线对局物理、计时、结算逻辑除 intro 门控外保持不变；intro 门控只在
  `IntroEndMS` 之前生效，且不修改既有 `NewState` 默认语义。
- 本地与在线代码路径隔离：单人弹道模拟为独立工具函数/模块，在线流程不引用。
- 输入与动画复用现有守卫机制（actionLocked、animationGeneration、page/battle_id 校验），
  不新增全局状态纠缠。

### 4.2 自动化测试

- 服务端（`go test ./...`，GOCACHE 使用 `C:\GoProject\GunTanks\.gocache`）：
  - 新增：intro 期间对战指令被拒绝、intro 结束前回合不超时、intro 结束时坦克落地且
    30 秒计时起算、intro 期间 leave 正常结束对局。
  - 保留并跑绿：现有 engine/actor/manager/ws 全部测试（出生 Y、风力 3 回合节奏等不受影响）。
- 客户端：`node --check` 语法检查 + `npm run build` 构建通过；
  本地弹道模拟（含风）写成纯函数并单测覆盖：风速 0、左/右风、越界、地形命中、坦克命中、
  SS 冷却语义。

### 4.3 构建与手工验收

- 重建 `guntanks-server.exe`（workspace 本地 GOCACHE，必要时提权）。
- 双浏览器完成：1.3、2.2、3.4 全部验收项；单机完成 1.3/2.3/3.4 单人项。
- 回归抽查：匹配 → 开火 → 超时 → 淘汰 → 结算 → 返回大厅 → 再来一局；
  断线重连；服务端启停。全部通过后才算完成。

### 4.4 已知既有问题（不作为本次验收项）

- `go vet` 对既有 JSON 标签（Height 重复 width）的告警为改动前已存在，与本次无关。
