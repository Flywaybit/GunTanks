# GunTanks 对战画面、HUD 与倒计时修复需求

## 1. 范围

本次只修复双人在线对战的三个问题：

1. 背景和可破坏地形显示混乱。
2. `1/2/3` 武器指示灯及 `W/S` 角度显示不更新。
3. 30 秒倒计时位置错误并闪烁 `Opponent turn`。

除倒计时位置采用用户最终确认的“游戏画面顶部中央且仅一份”外，其余视觉和交互以 `C:\GoProject\GunTanks\Guntanks` 为唯一基准。不得顺带修改战斗规则、键位、匹配或重连逻辑。

## 2. 地图与背景

### 已确认原因

- 原版战场背景是 `.main` 的 `linear-gradient(blue, orange)`，当前客户端却把未被原版战场使用的 `background.png` 拉伸到 `1200x650`。
- `background.png` 自带一份小比例地形，再叠加 `terrain-full.png` 后出现重复地形。
- 当前代码在 `destination-out` 状态下调用 `putImageData()`。`putImageData()` 不执行 Canvas 合成，导致空地被写成黑色、实体地形被写成透明，形成反向遮罩。

### 必须实现

- 删除 `renderer.js` 中 `background.png` 的加载和绘制。
- 战场逻辑尺寸固定为 `1200x650`，背景使用原版 `linear-gradient(blue, orange)`；响应式显示只能等比缩放整个战场，不得分别拉伸背景、地形或 HUD。
- 只绘制一次 `assets/terrain-full.png`，尺寸保持原图 `1100x520`，位置固定为 `(50, 320)`，不得缩放。
- 服务端 `gzip-bitset-v1` 快照只决定像素是否保留，不提供颜色或纹理。
- 应用快照时采用以下唯一流程：
  1. 清空 `terrainLayer`。
  2. 在 `(50, 320)` 绘制原版 `terrain-full.png`。
  3. 读取整层 `ImageData`。
  4. 对 bitset 中值为 `0` 的像素将 alpha 设为 `0`；值为 `1` 的像素保留原贴图 RGBA。
  5. 将修改后的 `ImageData` 写回。
- 禁止再用纯色填充地形，禁止使用 `putImageData + globalCompositeOperation` 充当遮罩。
- 炮弹破坏继续使用服务端下发的圆心和半径，通过 `destination-out + arc + fill` 清除贴图。
- 必须等待 `terrain-full.png` 完成 `load/decode` 且 `naturalWidth > 0` 后再应用快照，不能只判断 `image.complete`。
- 异步加载必须校验 `battle_id` 或加载代数；旧对局的图片/快照回调不得覆盖新对局地形。
- 地形快照失败时保持输入锁定并显示错误，不得进入可操作状态。

## 3. 武器和角度 HUD

### 已确认原因

- Go `service.User` 当前序列化为 `ID`、`Username`，客户端却读取 `store.user.id`，导致 `myTankId` 无法建立。
- 当前 HUD 读取本账号 Tank；原版 HUD 读取当前回合 Tank。

### 必须实现

- 为 `service.User` 添加明确 JSON 标签，至少统一为：

```text
id, username, wins, losses, draws, games_played
```

- 客户端只使用上述小写字段，不增加大小写双分支兼容代码。
- `battleIdentity.myTankId` 继续用于输入权限和本账号归属，但不得作为公共战斗 HUD 的数据源。
- 每次渲染通过 `state.current_tank_id` 从 `state.tanks` 找到 `currentTank`。
- `angle-display` 始终显示 `currentTank.angle`，按原版显示整数角度；`W/S` 调整时必须随权威 `battle.tank_state` 实时变化。
- 三个武器灯始终根据 `currentTank.weapon` 渲染：
  - `shell1`：`light1` 绿色，另外两个灰色。
  - `shell2`：`light2` 绿色，另外两个灰色。
  - `ss`：`lightss` 红色，另外两个灰色。
- 不得用白色边框代替指示灯颜色；初始 `shell1` 必须立即显示绿灯。
- 武器和 Angle HUD 必须复用原版尺寸与颜色：
  - 武器框 `30x50`、外边框 `1px` 黑色、圆角 `2px`、内边距 `2px`、外边距 `3px`。
  - `1/2` 框背景为 `rgb(41,205,255)`；SS 可用时为 `rgb(246,216,89)`，冷却时为 `rgb(60,60,60)`。
  - 指示灯 `14x8`、`2px` 黑色边框、底部外边距 `8px`。
  - Angle 容器宽 `50px`、字号 `18px`；数值框 `40x20`、黄色粗体、灰色背景和 `3px` 黑色边框。
- 客户端不得先行猜测武器或角度，只在收到服务端权威状态后更新。
- 以下事件都必须进入统一的 `setBattle/renderBattleHUD` 流程：
  - `battle.started`
  - `battle.snapshot`
  - `battle.tank_state`
  - `battle.turn_changed`
  - `battle.player_eliminated`
  - `battle.shot_resolved` 中的最终 `state`
- 服务端现有 `Aim`、`SelectWeapon` 和 `battle.tank_state` 广播链路应保留；不得将权威计算迁回客户端。
- 三层瞄准弧必须逐项使用原版公式，不能用一个带符号的通用 `base` 公式替代：
  - 右朝向圆心 `(x, y)`；起止角分别为 `(-angle + span)` 和 `(-angle - span)`。
  - 左朝向圆心 `(x + 20, y)`；起止角分别为 `(angle + span + 180)` 和 `(angle - span + 180)`，不得把 `angle` 取负。
  - 三层分别为半径/跨度/颜色：`50/20/rgba(255,255,255,0.7)`、`50/5/rgba(255,0,0,0.7)`、`45/2/rgba(255,0,0,1)`。
- 左朝向 Tank 贴图位置必须为 `(x - 10, y - 27)`，右朝向为 `(x - 25, y - 27)`；当前代码对左右统一使用 `x - 25` 不符合原版。

## 4. 30 秒倒计时

### 已确认原因

- 当前 `turn-status` 位于页面右上角。
- `setBattle()` 反复写入 `Your turn/Opponent turn`，250 毫秒定时器又写入秒数，两个写入源互相覆盖造成闪烁。
- 客户端未处理 `battle.turn_changed`。

### 必须实现

- 用户最终确认：倒计时固定在 `1200x650` 游戏画面的顶部中央，不跟随任何 Tank。
- 原仓库中同时存在右上角大计时和 Tank 上方小计时，本需求不得照搬这两个旧位置；最终界面只能保留一个顶部中央倒计时。
- 删除 `.battle-head`、右上角 `turn-status`、可见 Battle ID，以及所有 `Your turn`、`Opponent turn` 文案和写入逻辑。
- 在战场容器内部设置唯一的 `main-timer` 覆盖层：`position:absolute; top:13px; left:50%; transform:translateX(-50%);`，相对于游戏画面定位并随整个战场等比缩放。
- 倒计时沿用原版主计时视觉：红色、`60px` 字号、透明背景、水平居中；不得绘制 Tank 上方的棕色矩形、白色小数字或三角形。
- 秒数由权威 `turn_deadline_ms`、服务端时间偏移和当前时间计算，限制在 `0..30`。
- 倒计时更新只能有一个写入源；状态广播不得改写为任何回合提示文本。
- 必须处理 `battle.turn_changed`，立即切换当前 Tank、HUD 和倒计时。
- 炮弹动画、暂停和重连期间不得出现负数、跳回旧回合或多个倒计时。
- Delay Queue 中的 `*` 继续作为当前回合标识，不再增加其他回合文字提示。

## 5. 重点修改文件

- `guntanks-client/src/renderer.js`
- `guntanks-client/src/main.js`
- `guntanks-client/src/styles.css`
- `guntanks-client/index.html`
- `guntanks-server/service/auth.go`

只有确有必要时才修改其他文件。

## 6. 验收标准

### 自动检查

- 验证用户响应字段为小写 JSON，且能正确建立 `myTankId`。
- 验证快照中实体像素保留 `terrain-full.png` 原始颜色，空地 alpha 为 `0`，不存在黑色或棕色替代层。
- 验证 `shell1/shell2/ss` 分别产生绿、绿、红指示灯，其他灯为灰色。
- 验证 `battle.turn_changed`、持续瞄准和武器切换都会刷新当前 Tank HUD。
- 验证左、右朝向的三层瞄准弧与 Tank 贴图坐标符合原版公式。
- 验证页面中不存在 `Opponent turn`、`Your turn`、右上角倒计时和 Tank 上方倒计时。

### 双浏览器验收

1. 双方进入同一对局，只看到原版蓝橙渐变和一份完整原版地形台。
2. 炮弹破坏后双方显示相同缺口，周围纹理保持不变。
3. 当前玩家按 `1/2/3`，双方页面同步显示正确指示灯。
4. 当前玩家持续按 `W/S`，双方 Angle 和三层瞄准圆弧同步变化并支持完整 360 度。
5. 每回合倒计时始终固定在游戏画面顶部中央，切换回合时位置不变，且全局只有一个计时数字。
6. 整局不出现闪烁的 `Opponent turn` 或 `Your turn`。

### 完成要求

- 执行 `go test ./...` 和客户端构建/语法检查。
- 重新生成 `guntanks-server.exe`，确保运行文件与源码一致。
- 必须用两个真实浏览器窗口完成上述验收；仅通过单元测试或查看源码不算完成。
