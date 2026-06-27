## 1. Go Module 与目录结构

- [x] 1.1 在 `server/` 目录初始化 Go module，module path 暂定为 `ihomeland/server`
- [x] 1.2 创建 `server/cmd/server/main.go`，仅保留启动编排逻辑
- [x] 1.3 创建 `server/internal/config`、`server/internal/logger`、`server/internal/ops`、`server/internal/app` 基础包
- [x] 1.4 确认服务端基础包命名符合 Go 小写短名规则

## 2. 配置系统

- [x] 2.1 在 `server/internal/config` 中定义配置结构体和默认值
- [x] 2.2 支持通过 `server/config/local.yaml` 和环境变量覆盖 HTTP 地址、日志等级、协议版本和版本文件路径
- [x] 2.3 实现配置校验，拒绝非法 HTTP 地址和不支持的日志等级
- [x] 2.4 添加配置默认值、环境变量覆盖和非法配置测试

## 3. 日志系统

- [x] 3.1 在 `server/internal/logger` 中封装基于 `slog` 的 logger 初始化
- [x] 3.2 支持从配置设置日志等级
- [x] 3.3 为 logger 包添加导出标识符 Go doc 注释

## 4. 版本元数据

- [x] 4.1 在 `server/internal/ops` 中定义版本响应结构
- [x] 4.2 实现从 `release.json`、`server/version.json`、`client/version.json` 读取版本元数据
- [x] 4.3 在版本文件缺失或格式错误时返回可诊断错误
- [x] 4.4 添加版本元数据读取测试

## 5. HTTP 基础接口

- [x] 5.1 在 `server/internal/ops` 中实现 Gin route 注册函数
- [x] 5.2 实现 `/healthz`，返回机器可读的存活状态
- [x] 5.3 实现 `/readyz`，返回当前进程基础就绪状态
- [x] 5.4 实现 `/version`，返回 release、server、client 和 protocol 版本元数据
- [x] 5.5 添加 `/healthz`、`/readyz`、`/version` handler 测试

## 6. 应用启动与优雅关闭

- [x] 6.1 在 `server/internal/app` 中创建应用组装函数，连接 config、logger 和 ops routes
- [x] 6.2 在 `main.go` 中实现启动 HTTP server 和接收退出信号
- [x] 6.3 实现带 timeout 的优雅关闭
- [x] 6.4 确保启动和关闭路径输出结构化日志

## 7. 文档与验证

- [x] 7.1 更新 `server/README.md`，加入服务端本地运行命令
- [x] 7.2 更新或补充 `docs/file-structure.md` 中服务端基础包说明
- [x] 7.3 运行 Go 格式化
- [x] 7.4 运行服务端相关测试并记录结果
- [x] 7.5 添加 `server/scripts/test.bat`，使用相对路径封装测试环境变量
- [x] 7.6 添加 `server/scripts/run.bat`，使用相对路径封装本地启动环境变量
