## 1. Storage 接口与错误

- [x] 1.1 创建 `server/internal/storage` 包，定义 repository/cache 接口、稳定错误和数据结构
- [x] 1.2 定义 room summary repository、presence cache、room index cache、reconnect token cache 的最小接口
- [x] 1.3 添加 fake/in-memory storage adapter，供 room 和 storage 单元测试使用
- [x] 1.4 添加 storage 接口和 fake adapter 测试

## 2. Redis Key 边界

- [x] 2.1 更新 `docs/redis-keys.md`，记录 session、presence、room index、reconnect token、lock、rate limit 的 owner、用途、TTL、value、重建来源和清理触发
- [x] 2.2 在 `server/internal/storage` 中实现 Redis key builder 和 TTL 常量
- [x] 2.3 添加 Redis key builder 测试，覆盖 namespace、env、owner 和 TTL
- [x] 2.4 明确 Redis 丢失后的重建路径，不把 Redis 作为持久事实来源

## 3. MySQL Schema 与迁移

- [x] 3.1 新增 `server/internal/storage/migrations/` 目录和第一阶段 schema 迁移文件
- [x] 3.2 定义玩家基础资料占位表、房间摘要表和后续对局摘要预留表的最小字段
- [x] 3.3 为迁移文件补充回滚或兼容策略说明
- [x] 3.4 添加 migration 文件存在性和命名规则测试或验证脚本

## 4. Adapter 实现

- [x] 4.1 实现 MySQL room summary repository 的接口骨架和参数校验
- [x] 4.2 实现 Redis presence、room index、reconnect token cache 的接口骨架和参数校验
- [x] 4.3 确保 adapter 不在业务层泄漏具体 Redis/MySQL client 类型
- [x] 4.4 添加 adapter 参数校验和幂等语义测试

## 5. Room Service 接入边界

- [x] 5.1 调整 room service 依赖，使状态机继续可用 fake/in-memory storage 测试
- [x] 5.2 在创建、关闭或更新房间时通过 room summary repository 保存摘要
- [x] 5.3 在连接断开和重连恢复时通过 reconnect token cache 管理短期资格
- [x] 5.4 添加 room service 与 fake storage 的集成测试

## 6. 本地验证与文档

- [x] 6.1 更新 `server/README.md`，说明 storage 边界、本地依赖验证和测试策略
- [x] 6.2 更新 `docs/file-structure.md` 和 `docs/architecture.md` 中的 storage 当前状态
- [x] 6.3 如需调整验证脚本，更新 `server/scripts/verify-local.bat` 的说明或输出，区分依赖可达和业务恢复验证
- [x] 6.4 运行 `server/scripts/test.bat` 或等价 Go 测试命令，确认服务端测试通过
