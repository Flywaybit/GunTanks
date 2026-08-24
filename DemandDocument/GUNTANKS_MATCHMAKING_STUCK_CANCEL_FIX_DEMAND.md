# GunTanks 匹配卡住与取消匹配失败修复需求

## 1. 问题现象

- 两个账号选择双人匹配后一直停留在“匹配中”，无法进入战斗。
- 匹配中点击 `Cancel` 后无法结束匹配并返回大厅。

## 2. 已确认根因

根因位于 `guntanks-client/src/socket.js` 的 WebSocket 连接代次管理。

当前 `connect()` 先保存新连接代次：

```js
const connectionId = ++this.connectionId;
```

随后调用：

```js
this.close(false);
```

而 `close()` 会再次执行：

```js
this.connectionId += 1;
```

因此新 WebSocket 注册的所有回调从创建开始就持有过期的 `connectionId`，并被以下判断直接丢弃：

```js
if (connectionId !== this.connectionId) return;
```

实际结果是：

- WebSocket 底层连接可以打开，客户端也可能成功发送 `match.join` 和 `match.cancel`。
- 客户端忽略新连接的 `onopen`，连接状态不能正确更新。
- 客户端忽略所有 `onmessage`，收不到 `match.waiting`、`battle.started`、`match.cancelled` 和 `match.failed`。
- 客户端忽略 `onclose`，重连和清理逻辑也不可靠。

这一个错误可以同时解释“匹配成功后仍停留在匹配中”和“Cancel 无法返回大厅”。

## 3. 客户端修复要求

### 3.1 修复连接代次顺序

必须先淘汰并关闭旧连接，再为新连接生成代次：

```js
connect(token) {
  if (this.serverStopping) return false;

  this.close(false);
  const connectionId = ++this.connectionId;

  // 创建并绑定新的 WebSocket
}
```

也可以重构为独立的旧连接清理函数，但必须满足：

- 旧连接的异步回调不能影响新连接。
- 新连接的 `connectionId` 必须等于绑定回调时的 `this.connectionId`。
- `connect()` 内不得在绑定新连接回调前后再次使该代次失效。
- `close()` 必须幂等，重复调用不能关闭新连接或启动错误重连。

### 3.2 WebSocket 状态要求

- 新连接成功后，`onopen` 必须执行并将状态更新为 `OPEN`。
- `onmessage` 必须把当前连接收到的消息交给 `handleEvent()`。
- 旧连接迟到的 `onmessage/onclose` 必须按连接代次忽略。
- `onclose` 必须正确区分主动关闭、服务器关闭和异常断线。
- 每次连接只能存在一个 heartbeat 和一个重连 timer。

### 3.3 匹配事件要求

修复后必须正常接收并处理：

- `match.waiting`：保持匹配页面并显示等待状态。
- `battle.started`：进入 `MATCH_FOUND -> BATTLE_LOADING -> BATTLE`。
- `match.cancelled`：清理 `store.match` 并返回 `LOBBY`。
- `match.failed`：结束本次匹配，返回大厅并显示错误。
- `BATTLE_ALREADY_STARTED`：不得取消已开始的战斗，应进入战斗恢复流程。

### 3.4 Cancel 行为

- 点击 Cancel 只发送一次 `match.cancel`。
- 收到 `match.cancelled` 后立即停止匹配计时器、清理匹配状态、恢复匹配按钮并返回大厅。
- 3 秒未收到响应时恢复 Cancel 按钮并允许重试，不得永久卡在 `MATCH_CANCELING`。
- 当前 `main.js` 已提供 `onCancelRetry`，实现时必须保留并验证该回调确实重新启用按钮。

## 4. 服务端部署问题

当前文件时间显示：

- `guntanks-server.exe`：`2026-08-21 17:49`
- 当前匹配服务端源码：`2026-08-22 19:53`

现有 EXE 早于匹配修复源码。如果启动的是该 EXE，运行的仍是旧服务端代码。

修复客户端后必须重新构建服务端：

```powershell
$env:GOCACHE='C:\GoProject\GunTanks\.gocache'
go test ./...
go build -buildvcs=false -o guntanks-server.exe .
```

构建目录必须是：

```text
C:\GoProject\GunTanks\guntanks-server
```

启动前确认旧进程已退出，并确认正在运行的进程路径指向新生成的 EXE。

## 5. 浏览器缓存要求

客户端 `index.html` 直接加载 `src/main.js`，但服务端目前只对 HTML 设置 `Cache-Control: no-store`，JavaScript 文件仍可能使用浏览器缓存。

开发阶段必须做到以下至少一项：

- 对 `.js`、`.css` 和 HTML统一设置 `Cache-Control: no-store`；或
- 为静态资源增加构建版本号；或
- 测试时执行浏览器强制刷新并禁用开发者工具中的缓存。

必须通过浏览器 Network 面板确认实际加载的是修改后的 `socket.js`，不能仅检查磁盘源码。

## 6. 服务端匹配保障

客户端连接代次错误是本次两个现象的首要根因，但服务端仍需保留以下保障：

- 凑齐玩家后创建战斗；成功时向所有玩家广播 `battle.started`。
- 建局失败时回滚匹配预留并广播 `match.failed`。
- `match.cancel` 幂等处理，并始终返回 `match.cancelled` 或 `BATTLE_ALREADY_STARTED`。
- 日志记录 `match.join -> queue.ready -> battle.create -> battle.started` 和 `match.cancel -> match.cancelled`。

## 7. 测试要求

### 7.1 WebSocket 单元测试

- 调用 `connect()` 后，新连接的 `onopen` 能正常执行。
- 新连接收到消息后，`onEvent` 被调用一次。
- 旧连接迟到的消息被忽略。
- 主动关闭不触发自动重连。
- 异常断线只创建一个重连 timer。
- 连续两次 `connect()` 时，只有第二条连接能够更新状态和接收消息。

### 7.2 双人匹配验收

1. 使用两个不同账号登录。
2. 两端均选择双人匹配。
3. 两端先收到 `match.waiting` 或其中一端进入等待。
4. 凑齐后两端都收到同一个 `battle.started`。
5. 两端都进入同一个战斗页面，`battle_id` 一致。

### 7.3 取消匹配验收

1. 单个账号进入双人匹配。
2. 点击 Cancel。
3. 服务端收到 `match.cancel` 并返回 `match.cancelled`。
4. 客户端清理匹配状态并返回大厅。
5. 用户可以再次发起匹配。

### 7.4 运行版本验收

- `go test ./...` 通过。
- 新 EXE 的修改时间晚于相关 Go 源码。
- 运行进程路径是 `C:\GoProject\GunTanks\guntanks-server\guntanks-server.exe`。
- 浏览器实际加载的 `socket.js` 包含修正后的连接代次顺序。
- 浏览器控制台无 WebSocket 回调异常。
- 服务端日志能看到完整匹配与取消链路。

## 8. 验收标准

- 两个账号双人匹配后能够稳定进入同一场战斗。
- 匹配等待中点击 Cancel 能够稳定返回大厅。
- 客户端不再丢弃当前 WebSocket 的 `onopen/onmessage/onclose`。
- 旧连接事件不会污染新连接。
- 匹配或取消失败不会形成永久 loading 或永久禁用按钮。
- 重新编译的服务端和最新客户端资源已经实际运行，而不是只修改源码。
