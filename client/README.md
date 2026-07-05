# iHomeland Unity Client

本目录是 iHomeland 的 Unity 客户端工程根目录。

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
