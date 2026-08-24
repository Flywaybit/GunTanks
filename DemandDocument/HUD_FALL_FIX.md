# GunTanks HUD 裁剪与掉落判负修复要求

## 1. 目标

一次性修复以下关联问题，且不得破坏此前已经完成或已确认的功能：

1. 战斗页面下方的炮弹选择、Angle、发射力度条，以及 Wind、Delay Queue、当前回合 `*` 和 Leave 被裁剪或互相覆盖。
2. 地形被破坏后，Tank 掉出 `1200x650` 战场仍保持存活，导致回合队列和对局卡死。
3. 炮弹动画期间收到的新状态可能被动画结束回调中的旧状态覆盖，导致位置、死亡或终局状态回退。

原版行为和视觉基准为：

- `C:\GoProject\GunTanks\Guntanks`
- 已确认的顶部中央倒计时要求优先于原版旧计时位置。

## 2. 已确认根因

### 2.1 HUD 被裁剪

当前 `.battle-shell` 使用 `1200 / 650` 比例并设置 `overflow:hidden`，但 `.original-hud` 和 `.battle-meta` 同时使用 `position:absolute; bottom:-90px`：

- 两部分都位于 `.battle-shell` 边界外，因此全部被裁掉。
- 两部分坐标相同，即使改成 `overflow:visible` 也会互相覆盖。
- Delay Queue 的 `*` 生成逻辑仍然存在，消失原因是父容器被裁剪。

### 2.2 Tank 掉落不判负

当前服务端 `battle.Actor.tick()` 只对 `CurrentTankID` 执行 `SettleY()`，并且没有 Tank 越界淘汰逻辑：

- 非当前 Tank 脚下被炸空后不会下落。
- 当前 Tank 可以越过 `y=650` 后继续保持 `alive=true`。
- 越界 Tank 仍在回合队列中，对局无法正确结束。
- 重力位置变化没有完整、单调的状态版本和事件顺序保证。

原版每帧处理所有 Tank，并在 `abs(x) > 1200 || abs(y) > 650` 时淘汰该 Tank。

### 2.3 动画结束后恢复旧状态

当前客户端在 `shotAnimating=true` 时仍会立即应用 `battle.tank_state` 和 `battle.player_eliminated`，但炮弹动画回调随后可能再次应用较旧的 `battle.shot_resolved.state`。这会导致：

- 已掉落 Tank 被恢复为存活。
- Tank 坐标和地形状态回退。
- 已进入 RESULT 的客户端被旧回调重新切回 BATTLE。

## 3. 客户端页面结构

### 3.1 强制结构

战斗页面必须拆分为以下独立区域：

1. `battle-stage`：固定逻辑尺寸 `1200x650`，作为 Canvas 和战场覆盖元素的唯一定位容器。
2. `battle-hud`：逻辑尺寸 `1200x90`，位于 `battle-stage` 下方的正常文档流中。
3. 外层 `battle-shell`：只负责整体宽度、响应式缩放和页面布局，不得继续固定为 `1200/650`。

要求：

- `stage` Canvas、顶部中央倒计时、Wind 和 Delay Queue 等战场覆盖元素相对于 `battle-stage` 定位。
- 炮弹 `1/2/SS`、Angle 和发射力度条放入 `battle-hud`，完全复刻原版尺寸、颜色和排列。
- Delay Queue 保留原版位置和视觉，当前 Tank 后仅显示一个 `*`。
- Leave 保留现有功能和可见入口，不得覆盖战场计时、Wind、Delay Queue 或底部 HUD；本次不得重写 Leave 状态机。
- 禁止再使用负 `bottom` 把 HUD 放到裁剪容器外。
- 禁止用 `overflow:visible` 作为唯一修复。
- 禁止将 `.original-hud` 和 `.battle-meta` 叠放在同一坐标。

### 3.2 响应式规则

- 宽屏下战场必须精确保持 `1200:650`，底部 HUD 精确保持 `1200:90`。
- 窄屏下战场、覆盖元素和底部 HUD 必须按同一比例整体缩放，不得分别缩放造成错位。
- 所有 UI 必须完整可见，不得换行、重叠、溢出或被裁剪。
- 页面高度不足时允许纵向滚动；不得因 `body` 垂直居中导致顶部或底部内容移出可访问区域。
- Canvas 的逻辑坐标始终为 `1200x650`，响应式处理不得改变渲染和命中检测坐标。

### 3.3 必须保持的视觉和交互

- 唯一的 30 秒倒计时固定在 `1200x650` 战场顶部中央，不跟随 Tank，不出现在右上角。
- 不显示 `Your turn`、`Opponent turn` 或重复倒计时。
- HUD 始终读取 `current_tank_id` 对应 Tank 的实时武器和角度。
- `1/2/SS` 指示灯、Angle、力度条和 Delay Queue 必须与原版一致。
- 仅移除原版 `Guntanks!` 标题；不得增加方向键、Fire 等屏幕操作按钮。

## 4. 服务端重力与越界淘汰

### 4.1 服务端权威原则

- Tank 重力、越界、死亡、回合切换和胜负只能由服务端决定。
- 客户端不得自行将越界 Tank 设为死亡，也不得自行决定胜负。
- Actor 暂停期间不得执行重力、越界检测、倒计时或任何战斗模拟。

### 4.2 每 Tick 处理顺序

每个未暂停、未结束的 Actor Tick 必须按以下顺序处理：

1. 处理当前 Tank 的合法水平移动。
2. 对所有 `alive=true` 的 Tank 执行地形重力，不限于当前 Tank。
3. 检查所有存活 Tank 是否满足原版边界：`abs(x) > 1200 || abs(y) > 650`。
4. 先收集本 Tick 全部越界 Tank，再作为一个批次淘汰。
5. 更新回合、终局和权威状态。
6. 广播一次该 Tick 的最终状态。

不得在遍历过程中逐个结算，否则同时掉落时可能重复切换回合、重复结算或产生错误胜者。

### 4.3 批量淘汰语义

同一 Tick 的掉落淘汰必须是原子操作：

- 所有越界 Tank 设置 `alive=false`、`health=0`。
- 如果当前 Tank 被淘汰，立即清除 Actor 的移动和瞄准方向；蓄力及客户端输入由最终状态统一清理。
- 如果对局尚未结束且当前 Tank 被淘汰，只选择一次下一个存活 Tank，并重置该回合剩余移动量和 30 秒截止时间。
- 如果掉落的是非当前 Tank，当前回合不得无故切换或重置倒计时。
- 双人局一方掉落后立即 `phase=finished`，另一方胜利。
- 同一 Tick 双方都掉落时结果为 draw。
- `finishIfDecided()`、`revision`、`event_seq`、广播、战绩写入和数据库结算分别只执行一次。
- 最终事件必须包含完整 State、正确的 `winner_tank_id/player_results` 和全部掉落 Tank ID。

建议在 `engine.State` 中提供批量淘汰方法，由 Actor 调用；不得在 Actor 中复制多套胜负规则。

### 4.4 地形破坏后的行为

- 炮弹破坏地形后，下一次权威物理 Tick 必须让所有失去支撑的 Tank 开始下落。
- 下落过程持续广播权威位置，双方看到的坐标必须一致。
- Tank 第一次越过边界的 Tick 即完成淘汰，不能等待该 Tank 再次获得回合。
- 已死亡 Tank 不再参与碰撞、重力、输入和回合队列。

## 5. 客户端状态顺序

### 5.1 动画期间的状态合并

`shotAnimating=true` 时：

- 保持全部战斗输入锁定。
- `battle.tank_state`、`battle.turn_changed`、`battle.player_eliminated`、`battle.finished` 和 `battle.result` 中的 State 不得立即覆盖动画基准状态。
- 将收到的 State 与 `battle.shot_resolved.state` 比较，只保留最高 `revision`；相同 revision 时保留最高 `event_seq`。
- 不得按网络到达顺序盲目覆盖较新状态。

动画结束后：

1. 只应用一次最高版本的权威 State。
2. 如果 `phase=finished`，立即执行统一 `finalizeBattle()`。
3. 如果仍为 `playing`，完成渲染后再解除输入锁。
4. 清空待处理状态，旧状态不得再次执行。

### 5.2 防止旧回调复活对局

- 每次炮弹动画使用递增的动画代次或取消标识。
- `finalizeBattle()`、离开当前 battle、断线重同步或开始新 battle 时，必须使旧动画回调失效。
- 动画回调执行前必须确认 battle ID、动画代次和当前页面仍匹配。
- RESULT 状态建立后，任何旧动画回调都不得调用 `setBattle()` 或重新进入 BATTLE。
- `lastBattleEvent` 只能向前推进，不得因旧动画状态减小。

## 6. 不得回归的既有要求

本次修改必须同时保持以下行为：

- 原版键盘操作：`A/D` 移动、`W/S` 调角、`1/2/3` 选武器、按住并松开 Space 发射。
- 不增加移动、方向或 Fire 屏幕按钮。
- 原版蓝橙背景、`terrain-full.png` 纹理、权威地形快照和地形破坏效果。
- 原版三层灰红瞄准圆弧、左右朝向和完整 360 度角度。
- 武器、Angle、力度条、Wind、Delay Queue 和 `*` 实时正确。
- Space 松开后力度显示立即归零，不能保留上次力度。
- 在线模式只保留双人匹配；不恢复 3/4 人匹配、房间或 AI。
- 单人模式仍为本地一人控制四个 Tank，不写战绩或数据库。
- 主动 Leave 立即判负、双方结束对局，且不创建重连状态。
- 关闭网页、网络波动或延迟断线不是主动 Leave：完整暂停对局并允许 60 秒重连。
- 重连暂停期间倒计时、移动、瞄准、蓄力、发射、弹体、重力和地形处理全部停止。
- 重连恢复同一 battle 和断线前剩余回合时间；60 秒超时只结算一次。
- 普通重复登录仍返回 `ALREADY_ONLINE`；只有有效局内重连允许新会话接管。

不得借本次修复重写上述模块、改变协议字段或调整游戏数值。

## 7. 重点修改文件

客户端：

- `guntanks-client/index.html`
- `guntanks-client/src/styles.css`
- `guntanks-client/src/main.js`
- 必要时仅小范围修改 `renderer.js`、`store.js` 和输入控制器

服务端：

- `guntanks-server/battle/actor.go`
- `guntanks-server/engine/engine.go`
- `guntanks-server/battle/manager.go`
- 对应测试文件

## 8. 必须新增的自动化测试

### 8.1 Engine/Actor

1. 当前 Tank 失去支撑后持续下落并在 `y>650` 当 Tick 淘汰。
2. 非当前 Tank 同样持续下落并被淘汰，当前回合不被错误切换。
3. 炮弹破坏脚下地形后触发掉落和淘汰。
4. 双人局一方掉落后立即产生正确胜负。
5. 双方同 Tick 掉落只产生一次 draw 结算。
6. 掉落批次只增加一次终局版本和事件，只持久化、结算一次。
7. 当前 Tank 掉落后移动/瞄准轴被清空。
8. Actor 暂停期间 Tank 不下落、不越界结算；恢复后继续。

### 8.2 客户端

1. 底部 HUD 和战场覆盖元素不存在负定位裁剪或坐标重叠。
2. 炮弹动画期间连续收到多个状态，只应用最高 `revision/event_seq`。
3. 动画期间收到掉落终局状态，动画结束后进入 RESULT 且不会恢复旧 Tank。
4. 已进入 RESULT 后触发旧动画回调，不会重新进入 BATTLE。
5. Delay Queue 中始终只有当前 Tank 带一个 `*`。

## 9. 浏览器验收

必须使用两个独立账号和两个独立浏览器上下文，加载最新客户端资源后完成：

1. 在 `1440x900` 和 `390x844` 视口分别截图，确认完整显示 `1200x650` 战场、底部 HUD、Wind、Delay Queue、`*` 和 Leave，无裁剪、重叠或横向丢失。
2. 验证倒计时只在战场顶部中央显示一次。
3. 验证 `1/2/3`、`W/S`、Space、`A/D` 对应 UI 和战斗行为仍正确。
4. 发射炮弹炸空当前玩家脚下，确认双方看到相同下落过程，越界后立即判负并进入结果页。
5. 炸空非当前玩家脚下，确认其无需等到自己的回合就会下落并被判负。
6. 在炮弹动画和下落过程中验证没有位置回退、死亡复活、页面卡死或 RESULT 返回 BATTLE。
7. 复测主动 Leave 和异常断线 60 秒暂停重连，确认语义没有变化。

截图和测试日志必须保留为验收证据，不能只依赖代码审查。

## 10. 构建与完成标准

- 执行 `go test ./...` 并全部通过。
- 执行客户端现有测试、构建和 JavaScript 语法检查。
- 重新构建 `C:\GoProject\GunTanks\guntanks-server\guntanks-server.exe`，可执行文件时间必须晚于本次服务端源码修改。
- 使用新 EXE 和强制刷新后的客户端完成双浏览器验收。
- 不得只修改源码而继续运行旧 EXE 或浏览器缓存。

只有全部自动化测试、双浏览器验收、防回归项目和新 EXE 构建都完成，才能判定本需求完成。
