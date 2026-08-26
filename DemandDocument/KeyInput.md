问题描述：
单人模式有如下bug：按键按一下，tank移动或者发射角调整只会动一点，不像双人模式或者原版游戏，按键一直按着输入一直有用；
根因
1. 输入层刻意屏蔽了"按住重复触发"。inputController 的 handleKeyDown 里有 if (store.input.heldKeys.has(key)) return;，所以一个键只会在按下瞬间触发一次 battle.move_start/battle.aim_start，浏览器后续的按键重复事件都被忽略（[inputController.js (line 139)](C:/GoProject/GunTanks/guntanks-client/src/inputController.js:139) 附近）。
2. 双人模式之所以按住一直动，是因为服务端 actor 的 60Hz tick 循环里，只要 moveDirection 还挂着就每帧继续移动 1.5px，直到收到 move_stop。而本地模式没有这个循环——handleLocalCommand 的 battle.move_start 只执行一次 1.5px 就返回（[main.js (line 283)](C:/GoProject/GunTanks/guntanks-client/src/main.js:283)），aim_start 也只转一次 2°。所以本地"按一下只动一下"。
一句话：在线的持续移动靠服务端循环，本地缺了等价循环；而输入层又把按键重复关了，结果本地只剩单步。
修改方案（只动本地，零在线影响）
1. 在 main.js 加一个本地输入循环 localInputTimer（约 16ms 一次，对应在线 60Hz），只读 inputController 已经维护好的 store.input.moveDirection / store.input.aimDirection，每 tick：
   - 有移动方向且 moved < 80：distance = min(1.5, 80 - moved)，按方向平移 distance，更新 facing、moved += distance、tank.y = settleLocalTankY(...)；
   - 有瞄准方向：角度 ±2°，并 %360 回绕；
   - 有变化就 render($('stage'), state) 并刷新角度显示（不调用完整 setBattle，避免副作用）。
   - 运行条件：localMode && phase==='playing' && !shotAnimating && !introAnimating && !store.input.actionLocked。
2. 把 handleLocalCommand 里的 battle.move_start / battle.aim_start 改成只 return true（方向已由 inputController 锁存，实际移动交给循环），避免重复叠加一步。
3. startSinglePlayer 里启动 localInputTimer；resetLocalState/leaveLocalBattle 里清除它。
4. 顺带把本地 moved 的累加改成"距离制"（每 tick +1.5，封顶 80），与在线服务器完全一致，避免之前"本地按步数算、在线按距离算"的隐性差异。
不要引入新 bug
- 不改 inputController.js（继续复用它锁存的 moveDirection/aimDirection，keyup/blur 自动清空），不改任何在线文件、服务器文件。
- 循环只读 store.input 的已锁存方向，靠现有 actionLocked/shotAnimating/introAnimating 门控，开火、下落、离开时自动停。
- 移动上限仍和在线一样（80 单位），按住到上限就停，不会无限移动。