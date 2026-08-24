# GunTanks Client

模块化浏览器客户端：`api.js` 处理 REST，`socket.js` 集中管理 WebSocket 心跳/重连与消息发送，`store.js` 保存会话状态，`renderer.js` 只渲染服务端快照和事件。

服务端启动后，在此目录运行 `npx serve .` 或直接由 Go 服务端的 `STATIC_DIR` 提供静态文件。旧版 `Guntanks/assets` 资源保持不变，可按需复制到本目录 `assets/`。
