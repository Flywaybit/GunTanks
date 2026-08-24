# GunTanks 对局卡死与退出重连修复要求

## 1. 目标

修复以下两个已确认的问题：

1. 玩家移动并发射炮弹后，客户端没有同步最新战斗状态，导致双方后续操作无响应。
2. 玩家在对局中主动点击 `Leave` 后没有正确结束对局，刷新页面后错误进入 `Reconnect`。

本次修复不得改变原版战斗规则、移动方式、三种武器、弹道计算、伤害、地形破坏和键盘操作方式。

## 2. 已确认根因

### 2.1 发射后状态丢失

`guntanks-server/battle/manager.go` 构造事件时原本包含最新 `State`，但遇到射击事件后使用 `ev.Shot` 覆盖了整个 payload。

因此 `battle.shot_resolved` 只包含弹道数据，不包含发射后的权威状态。客户端仍保留旧的：

- `revision`
- `event_seq`
- `current_tank_id`
- 坦克生命值和存活状态
- 回合截止时间
- 武器冷却状态

客户端随后发送的操作携带旧 revision，服务端返回 `STALE_REVISION`。客户端没有自动重新同步，因此双方表现为卡死。

如果炮弹直接结束对局，最新状态中的 `phase=finished` 也会丢失，客户端无法得知对局已经结束。

### 2.2 主动退出被旧 revision 拒绝

当前 `battle.leave` 与普通战斗操作共用 revision 校验。发射后客户端 revision 已过期时，主动退出也会收到 `STALE_REVISION`，服务端实际上没有淘汰玩家，也没有结束对局。

客户端发送 Leave 后将 `leaveRequested` 设为 `true`，但错误处理没有恢复该状态。刷新页面时服务端仍存在活跃对局，因此刷新造成的 WebSocket 断线会进入局内重连。

### 2.3 对局结束后客户端状态未清理

客户端收到结束事件后虽然进入 `RESULT` 页面，但仍保留 `store.battle`。连接稍后断开时，客户端会因为 `store.battle` 非空而错误进入 `RECONNECTING`。

## 3. 射击事件修复

### 3.1 服务端事件结构

`battle.shot_resolved` 必须同时携带弹道结果和发射后的完整权威状态，不允许再用 Shot 覆盖 State。

建议结构：

```json
{
  "type": "battle.shot_resolved",
  "battle_id": "battle_xxx",
  "revision": 12,
  "event_seq": 12,
  "payload": {
    "shot": {
      "trajectory": [],
      "impact": {},
      "terrain_destroyed": {},
      "damages": [],
      "eliminated_tank_ids": []
    },
    "state": {
      "battle_id": "battle_xxx",
      "revision": 12,
      "event_seq": 12,
      "phase": "playing"
    }
  }
}
```

事件顶层的 `revision`、`event_seq` 必须与 `payload.state` 一致。

### 3.2 客户端处理顺序

客户端收到 `battle.shot_resolved` 后必须按以下顺序处理：

1. 保持战斗输入锁定。
2. 使用 `payload.shot` 播放弹道和地形破坏动画。
3. 动画完成后一次性应用 `payload.state`。
4. 更新 revision、回合、生命值、存活状态、武器状态和回合截止时间。
5. 重新渲染权威状态。
6. 如果 `state.phase == "finished"`，立即进入统一的对局结束流程。
7. 只有状态应用完成且对局仍为 `playing` 时才解除输入锁。

不得在弹道动画开始时提前解除 `actionLocked`。

### 3.3 过期状态恢复

客户端收到 `STALE_REVISION` 时必须：

1. 锁定战斗输入。
2. 发送 `battle.resync`，携带当前 `last_event_seq`。
3. 收到并应用 `battle.snapshot` 后发送 `battle.resync_ack`。
4. 完成同步后，根据当前回合决定是否恢复输入。

`STALE_REVISION` 不得只显示错误文字后继续使用旧状态。

服务端应只向发出过期命令的玩家返回该错误，不应广播给对局内所有玩家。

## 4. 主动退出规则

### 4.1 明确语义

玩家主动点击 `Leave` 表示不可撤销的认输：

- 退出者立即判负并被淘汰。
- 双人局立即结束，另一名玩家判胜。
- 多人局按现有自由混战规则处理；退出者淘汰，对局在只剩一名存活玩家时结束。
- 主动退出不进入断线重连宽限期。
- 主动退出不能因为客户端 revision 过期而失败。

### 4.2 服务端要求

1. `battle.leave` 必须跳过普通战斗操作的 stale revision 拒绝逻辑。
2. 服务端必须根据已认证的 `userID` 查找玩家和坦克，不能相信客户端提交的坦克 ID。
3. Leave 必须具备幂等性；同一玩家重复发送时不得重复结算或产生第二份结果。
4. 淘汰和结算完成后，向所有玩家发送包含完整最终 State 的权威结束事件。
5. 最终事件必须明确包含 `phase=finished`、胜者、每位玩家结果及最终 revision/event_seq。
6. 完成持久化和结算后删除 battle runtime，并清除所有参战玩家的 Redis reconnect key。
7. 主动退出、正常结束、登出和服务端优雅关闭均不得创建新的 reconnect key。

### 4.3 客户端要求

1. 点击 Leave 后立即进入 `LEAVING_BATTLE`，锁定全部战斗输入并禁止重复点击。
2. 在收到服务端最终结束事件前显示等待状态，不得直接在本地判定结果。
3. Leave 因临时网络错误未确认时，应等待连接恢复并向服务端查询权威战斗状态，不能直接创建一局新的对局。
4. 收到最终事件后统一执行 `finalizeBattle(result)`。
5. `finalizeBattle` 必须清除：
   - `store.battle`
   - `store.pendingBattle`
   - `store.reconnect`
   - `syncing`
   - `leaveRequested`
   - `lastBattleEvent`
   - 所有输入和动画锁
6. 最终结果必须单独保存在 `store.result`，清理 battle 时不得同时删除 result。
7. 双方都应离开战斗页面并进入 `RESULT`。从结果页返回大厅时再清除 `store.result`。

## 5. 断线与重连边界

只有未收到主动 Leave、正常结束或服务端关闭信号的异常 WebSocket 断线，才能进入60秒局内重连流程。

服务端必须区分：

| 原因 | 是否允许局内重连 | 处理 |
|---|---:|---|
| 网络中断、浏览器意外断线 | 是 | 保留对局，启动60秒宽限期 |
| 主动 `battle.leave` | 否 | 立即判负并结算 |
| 对局正常结束 | 否 | 结算并清理对局 |
| 用户主动登出 | 否 | 按主动退出处理 |
| 服务端优雅关闭 | 否 | 按既有 shutdown 方案处理中断状态 |

WebSocket 断开回调不得仅凭“用户曾经属于某个 battle”就设置 reconnect。设置前必须确认：

- battle runtime 仍存在且处于 `playing`。
- 玩家未主动退出。
- 玩家未收到最终结算。
- 断开原因允许重连。

重新连接时，如果服务端已没有进行中的对局，客户端必须回到大厅或结果页，不能一直停留在 `RECONNECTING`。

## 6. 重点修改文件

- `guntanks-server/battle/manager.go`
- `guntanks-server/battle/actor.go`
- `guntanks-server/web/ws.go`
- `guntanks-server/redis/presence.go`
- `guntanks-server/redis/client.go`
- `guntanks-client/src/main.js`
- `guntanks-client/src/store.js`
- `guntanks-client/src/renderer.js`
- `guntanks-client/src/stateMachine.js`

实现时可按职责增加小型辅助函数，但不得重写或改变现有战斗引擎规则。

## 7. 必须新增的测试

### 7.1 服务端测试

1. 射击事件同时包含 Shot 和最新 State。
2. 射击后事件顶层 revision 与 State revision 一致。
3. 射击后下一位玩家使用最新 revision 可以移动、瞄准和发射。
4. 终局射击向双方发送 `phase=finished` 和正确结果。
5. 使用旧 revision 发送 Leave 仍然成功。
6. 双人局主动 Leave 后，退出者为 loss、对手为 win。
7. Leave 重复提交不会重复结算。
8. 主动 Leave 后不创建 reconnect key。
9. 异常断线会创建 reconnect key，并允许宽限期内恢复。
10. 对局完成后 battle runtime 和双方 reconnect key 均被清除。

### 7.2 客户端测试

1. 弹道播放期间所有战斗输入保持锁定。
2. 动画结束后应用最新 State 并正确切换回合。
3. 收到 `STALE_REVISION` 会自动 resync。
4. 点击 Leave 后进入 `LEAVING_BATTLE` 且不能重复提交。
5. 收到最终结果后清除 battle/reconnect 状态并进入 RESULT。
6. RESULT 页面连接断开时不会进入局内 RECONNECTING。
7. 主动退出后刷新页面不会显示 Reconnect。

### 7.3 浏览器端到端测试

使用两个独立账号和两个浏览器上下文验证：

1. 玩家A移动并发射，动画结束后玩家B可以立即移动和发射。
2. 连续进行多个回合，双方 revision、event_seq 和 current_tank_id 始终一致。
3. 玩家A主动 Leave，A显示失败，B显示胜利，双方退出战斗页面。
4. 主动退出后双方刷新页面，不进入 Reconnect。
5. 仅断开玩家A网络且不点击 Leave，A在60秒内可以重连并恢复同一对局。

## 8. 验收标准

- 任意一次发射后双方都能继续正常操作，不再出现无响应。
- 炮弹导致对局结束时，双方都能收到并显示最终结果。
- 主动 Leave 始终生效，不受旧 revision 影响。
- 主动退出者判负，其他玩家按现有规则结算。
- 对局结束后双方均离开战斗页面，刷新后不会进入 Reconnect。
- 只有真实的异常断线会进入60秒局内重连。
- `go test ./...` 全部通过，并补齐上述服务端、客户端和端到端测试。
- 重新编译 `guntanks-server.exe`，确认运行文件时间晚于本次修改源码；浏览器强制刷新后再进行验收。
