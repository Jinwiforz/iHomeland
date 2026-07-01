# 共享协议

`shared/proto/` 存放客户端与服务端共同依赖的 Protobuf 源文件。协议源文件是跨端通信契约，生成代码不得手工修改。

当前实时协议入口：

```text
shared/proto/realtime/v1/envelope.proto
```

服务端 Go 代码生成入口：

```bat
tools\proto\generate.bat
```

Unity 或 Godot 的具体生成目录和接入方式由后续 `document-client-integration` change 决定。
