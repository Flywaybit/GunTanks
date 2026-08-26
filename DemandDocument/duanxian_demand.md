一、服务端改动
1. 引擎状态增加暂停标记（engine.go）
State 新增一个字段：
Paused bool `json:"paused,omitempty"`
omitempty 保证旧客户端/旧事件解析不受影响；新增 ErrBattlePaused = errors.New("battle paused") 错误。
2. Actor 在暂停/恢复时维护权威标记（actor.go）
- pause 分支：a.State.Paused = true，同时清空 moveDirection/aimDirection（防止恢复后残留按键导致坦克自己动）。
- resume 分支：a.State.Paused = false（保留现有 TurnDeadlineMS += 暂停时长 逻辑）。
- 暂停时对局照常发 battle.tank_state（现有行为），现在这条广播里会带上 paused: true，在线方立刻知道对局已暂停；恢复时同样广播 paused: false 和顺延后的 deadline。
3. 暂停期间拒绝对战指令（actor.go apply）
在现有 intro 门控之后、命令分发之前，加一道同样的门控：
- 允许：pause、resume、leave、disconnect_timeout、move_stop、aim_stop（生命周期与清理指令，幂等无害）。
- 拒绝：move_start、aim_start、select_weapon、fire，返回 ErrBattlePaused，经 codeForError 映射为新的 BATTLE_PAUSED 错误码（ws.go 加一行映射）。
这样"对方掉线期间在线方开火"这个实测存在的逻辑缺口被彻底堵住，且与现有 intro 门控写法一致。
二、客户端改动
1. 暂停 UI（index.html + main.js）
- 对局区域新增 #pause-status 提示层（与 #intro-status 同款样式），内容"对方已断线，对局已暂停，等待重连…"。
- setBattle 里根据 state.paused：为真时显示提示层、隐藏主倒计时、锁输入；为假时恢复倒计时显示（deadline 已被服务端顺延，会从正确剩余时间继续）。
2. 倒计时冻结（main.js 定时器）
250ms 的倒计时刷新条件加 !store.battle?.paused：暂停期间不再按本地时钟递减，改为显示暂停提示。
3. 输入锁定
- inputController 的 active() 增加 !getBattle()?.paused。
- sendBattleCommand 的守卫增加 store.battle?.paused。
- handleEvent 里现有的解锁语句 if (!event.payload?.intro_end_ms || now >= intro_end_ms) actionLocked = false 必须加 && !event.payload?.paused，否则暂停广播会把锁误解开。
- syncIntroUI 的 else 分支解锁处同样加 && !state.paused。
- 收到 paused: true 时调用 clearInputState()（取消蓄力/清空按键），防止玩家按住空格/方向键时暂停后残留。
4. 错误处理
BATTLE_PAUSED 错误在客户端静默忽略（不弹错误、不跳页面）；服务端拒绝本身就是双保险，正常情况客户端锁输入后根本发不出去。
三、边界场景与防回归
- 下落中掉线：intro 门控本就允许 pause/resume/leave；暂停期间下落动画按现有逻辑继续播完并保持落地画面，intro_complete 到达后再应用权威落地状态；不会出现坦克悬空或位置跳变。
- 开火动画中掉线：暂停的 tank_state 会像其他状态一样被现有 deferBattleState 机制挂起，动画结束后才应用 paused=true，随后锁输入、显示暂停——与现有 shot 流程完全同构，不新增状态机。
- 恢复时序：掉线方重连 → 服务端 resume 顺延 deadline → 广播新状态；在线方和回归方都会收敛到同一份权威状态。快照可能短暂带旧 deadline，随后 resume 广播立即纠正（现有机制，非新问题）。
- 60 秒不回来：维持现有 reconnectGrace 到期 → battle.leave → 正常结算，在线方从暂停提示进入结算页，不再出现"卡在 0 秒"。
- 协议兼容：paused 字段 omitempty；旧客户端解析新事件忽略未知字段，新客户端遇到无 paused 字段的旧服务端按"未暂停"处理。
- 单人模式：本地对局不会产生 paused，所有改动路径对单机零影响。
四、测试与验收
- 服务端单测（新增，跑绿现有全部测试）：
  - 暂停期间 fire/move_start/aim_start/select_weapon 被拒；
  - 暂停期间无超时；resume 后 deadline 顺延暂停时长；
  - 掉线方 unsubscribe 后在线方收到 paused=true 的状态，重连后收到 paused=false 且 deadline 已顺延；
  - intro 期间掉线/恢复正常。
- 客户端：node --check + npm run build。
- 重建 guntanks-server.exe（workspace 本地 GOCACHE）。