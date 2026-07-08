# iHomeland Unity Client

本目录是 iHomeland 的 Unity 客户端工程根目录。

当前客户端已经具备基础运行链路：

```text
MainScene -> LoadingPage -> LoginPage -> HomePage -> LoadingPage -> BattleScene
```

该链路目前用于验证 AppRoot、Systems、UI 页面和场景切换。`AccountSystem` 已通过 `NetworkSystem` 接入服务端注册、登录和登出；Unity Editor 菜单 `iHomeland/Smoke Test/WebSocket Account` 可验证本地 `/ws`、心跳和账号 session 链路；`Start Game` 后续应进入房间大厅流程，第一里程碑完成前不推进正式战斗模块。

## 当前结构

```text
client/
  Assets/           Unity 资产、场景、脚本和配置资源
  Packages/         Unity Package Manager manifest 和 lock 文件
  ProjectSettings/  Unity 项目设置
  version.json      客户端版本元数据
```

## 接入文档

Unity 客户端接入服务端实时协议、Protobuf envelope、心跳、错误响应和房间大厅流程，见：

```text
../docs/client-integration.md
```
