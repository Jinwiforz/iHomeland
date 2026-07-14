# 存储 Schema 注释规范

本文档是 MySQL schema 与 Redis key/value 字段说明的长期 owner 文档。数据库注释用于帮助开发、评审、排障和数据治理，不替代约束、类型、OpenSpec requirement 或 adapter 契约。

## 通用原则

- 注释默认使用中文短名词或名词组合，不逐句复述实现。
- `ID`、`UTC`、`SHA-256`、`TTL` 等稳定技术标识保留英文。
- 注释必须描述业务含义，不使用“字段一”“相关信息”“数据”等无辨识度表述。
- 时间、时长、容量、速率、距离等有单位或精度的值必须在括号中注明实际单位；无量纲序号不得虚构单位。
- schema、key 或 value 字段变化必须同时更新注释、长期文档和相应 contract/integration test。

## MySQL

### 表注释

所有业务表和基础 metadata table 必须设置非空中文 `COMMENT`，使用以下格式：

```text
<中文名词短语>(owner=<module>)
```

示例：

```text
个人世界(owner=storage/personalworld)
迁移历史(owner=storage/mysql)
```

表注释只表达表的稳定职责与 owner，不罗列列名、索引或流程步骤。

### 列注释

每一列必须设置非空中文 `COMMENT`，优先使用简短名词组合：

| 列语义 | 推荐注释 |
|---|---|
| 玩家标识 | `玩家ID` |
| 个人世界所属玩家 | `所属玩家ID` |
| 生命周期枚举 | `生命周期` |
| 乐观并发版本 | `修订号` |
| SHA-256 摘要 | `幂等键摘要(SHA-256,32字节)` |

同一投影中的结果字段使用 `结果` 前缀，例如 `结果生命周期`；不要用含义模糊的 `值`、`内容` 或 `信息` 代替实际语义。

### 单位与时间

- `DATETIME(6)`、`TIMESTAMP(6)` 等绝对时间使用 `<语义>时间(UTC,微秒)`。
- Unix timestamp 数值字段在字段名中保留单位后缀，并在注释中重复真实单位，例如 `created_us` 对应 `创建时间(Unix微秒)`。
- duration 使用实际计量单位，例如 `租约时长(ms)`、`超时时长(s)`。
- 容量使用 `字节`、`KiB`、`MiB` 等真实单位，不混用十进制与二进制单位。
- generation、revision、fencing token、epoch 等无量纲序号只说明业务语义，不标记时间或容量单位。

### Migration 与验收

- `COMMENT` 是 schema contract，也是 migration checksum 的一部分。
- Migration SQL 必须由 `.gitattributes` 固定为 LF，禁止本机换行转换改变嵌入 bytes 与 checksum。
- 已进入共享历史或已归档 change 的 migration 禁止原地修改；必须追加 forward migration 更新历史表/列注释。
- 尚未进入共享历史的新 migration 可以在首次提交前补齐注释，但必须同步更新 catalog/checksum 测试。
- MySQL integration test 必须通过 `information_schema.tables` 与 `information_schema.columns` 验证所有受管表、列均有符合本规范的注释。

## Redis

Redis 服务端没有 MySQL `COMMENT` 的等价 schema metadata。项目使用两层信息承担等价治理责任：

1. owner-specific `Definition` 登记 key 的中文用途、TTL、恢复、清理、故障和 metrics metadata；
2. `docs/redis-keys.md` 为每个已实现 value schema 维护字段字典。

字段字典至少记录：英文字段名、类型/编码、中文短注释以及必填/可空规则。时间与容量字段必须在字段名和中文注释中注明单位；Hash/JSON/二进制 codec 的实际字段集合必须与文档和测试一致。

新增或修改 Redis value 字段时，owner 必须在同一 change 中更新 typed codec、字段字典、schema version/兼容策略和 corruption test。不得把中文字段名直接写入线上 key/value，也不得依赖运行时日志充当 schema 文档。

## 评审清单

- 每张 MySQL 表是否有中文短注释和明确 owner。
- 每个 MySQL 列是否有中文短注释。
- 时间、时长、容量和速率是否标注真实时区、精度或单位。
- 注释是否与类型、约束、codec 和实际持久语义一致。
- Redis definition 与字段字典是否覆盖全部已实现字段。
- 历史 migration 是否通过新的 forward migration 更新，而非原地重写。
