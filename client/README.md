# iHomeland Unity Client

本目录是 iHomeland 的 Unity 客户端工程根目录。

当前客户端已经具备基础运行链路：

```text
MainScene -> LoadingPage -> LoginPage -> HomePage -> RoomPage
```

该链路目前用于验证 AppRoot、Systems、UI 页面、账号会话和房间大厅入口。`AccountSystem` 已通过 `NetworkSystem` 接入服务端注册、登录和登出；`RoomSystem` 通过 `NetworkSystem` 接入创建、加入、准备、开始、退出、房主转移和重连恢复请求；Unity Editor 菜单 `iHomeland/Smoke Test/WebSocket Account` 可验证本地 `/ws`、心跳和账号 session 链路。`RoomPage.prefab` 已绑定开始按钮，开始成功后只展示房间已开始状态，不进入正式战斗模块。

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
