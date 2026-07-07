# 共享协议

`shared/proto/` 存放客户端与服务端共同依赖的 Protobuf 源文件。协议源文件是跨端通信契约，生成代码不得手工修改。

当前实时协议入口：

```text
shared/proto/realtime/v1/envelope.proto
```

跨端协议生成入口：

```bat
tools\proto\generate.bat
```

项目只保留这一个协议生成入口。运行该脚本会同时刷新服务端 Go 代码和 Unity C# 代码。

Unity C# 代码必须从 `shared/proto/` 生成，生成代码不得手工修改，也不得提交到 Git。具体输出目录和接入规则见：

```text
docs/client-integration.md
```
