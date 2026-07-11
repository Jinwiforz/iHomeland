# Git 提交规范

本文档是 iHomeland Git 提交消息与提交粒度的唯一 owner 文档。所有人工提交、AI 辅助提交、squash 结果和自动化提交都必须遵守本规范。

## 目标

- 让提交历史可以按类型、模块和破坏性影响检索。
- 让单个提交说明为什么修改、修改了什么以及如何验证。
- 支持 changelog、版本发布、问题关联和自动化校验。
- 保持每个提交可评审、可验证、可回滚。

## 标准格式

```text
<type>(<scope>)!: <subject>

<body>

<footer>
```

除 `type` 和 `subject` 外，其余部分按需使用。完整语法为：

```text
<type>[optional scope][optional !]: <subject>
```

硬规则：

- `type` 必填，使用小写英文。
- `scope` 可选；使用时只能写在英文圆括号中。
- `!` 可选，只用于破坏性变更，并放在 `scope` 或 `type` 之后、冒号之前。
- 冒号必须是英文半角 `:`，其后必须有一个空格。
- `subject` 必填，默认使用中文，代码标识符和技术名词保留英文。
- `body` 与标题之间、`footer` 与正文之间各空一行。
- 禁止使用方括号、中文括号、特殊装饰括号或省略冒号后的空格。

## Type

| Type | 使用场景 | 不适用场景 |
|---|---|---|
| `feat` | 新增用户可感知能力、协议能力或业务行为 | 仅重排现有代码 |
| `fix` | 修复错误行为、安全缺陷或回归 | 新增独立功能 |
| `perf` | 在行为不变前提下改善性能、内存或延迟 | 普通重构 |
| `refactor` | 行为不变的结构调整 | 修复 bug 或新增功能 |
| `docs` | 只修改文档、注释或示例 | 同时改变运行行为 |
| `style` | 只修改格式、空白、命名排版等非语义内容 | CSS/UI 视觉功能变更 |
| `test` | 只新增或调整测试与测试夹具 | 测试随功能一起交付 |
| `build` | 构建系统、依赖、生成链或打包配置 | CI 流程本身 |
| `ci` | CI/CD 配置、流水线和自动化检查 | 本地构建逻辑 |
| `chore` | 无法归入其他类型的仓库维护 | 用作含糊的默认类型 |
| `revert` | 撤销一个或多个既有提交 | 普通修复 |
| `release` | 版本号、发布说明和发布产物元数据 | 普通功能提交 |
| `workflow` | 分支、OpenSpec、评审或交付流程语义变更 | 仅修正文档措辞 |

类型按变更的主要意图选择：

- 功能同时包含测试和文档时仍使用 `feat`。
- bug 修复同时增加回归测试时仍使用 `fix`。
- 只调整测试时使用 `test`。
- 只调整文档文字且不改变流程语义时使用 `docs`。
- `chore` 是最后选择，不能用来掩盖范围不清的提交。

## Scope

`scope` 表示主要受影响的稳定模块或能力边界，不表示临时任务名或任意文件路径。

首选 scope：

- `server`
- `client`
- `auth`
- `world`
- `visit`
- `protocol`
- `network`
- `storage`
- `mysql`
- `redis`
- `ui`
- `build`
- `ci`
- `docs`
- `openspec`
- `repo`

规则：

- 使用小写英文；多个单词使用 kebab-case。
- 一个提交只写一个主要 scope。
- 影响范围清晰时应填写 scope；纯全局改动可省略或使用 `repo`。
- 新 scope 必须对应长期 owner 边界，不能为单次提交随意造词。
- 禁止使用 `global`、人员名称、分支名称或完整目录路径作为 scope。

## Subject

`subject` 是对结果的简短概述：

- 使用明确动作，例如“增加”“修复”“拒绝”“统一”“移除”。
- 描述变更结果，不写“更新代码”“修改问题”等无信息表述。
- 末尾不加句号。
- 建议不超过 72 个字符；无法说清时应检查提交是否过大。
- 不写 issue 链接、测试清单或 Breaking Change 细节，这些内容属于正文或脚注。

示例：

```text
feat(world): 增加个人世界进入用例
fix(auth): 拒绝 payload 覆盖连接会话身份
docs(workflow): 明确 Git 提交规范
perf(network): 减少消息路由表重复查找
```

## Body

正文用于补充标题无法表达的上下文，重点说明：

- 为什么需要修改。
- 采用了什么关键方案。
- 行为、兼容性、安全性或数据边界如何变化。
- 使用什么测试或检查完成验证。

以下提交必须写正文：

- 架构、协议、存储、安全或跨模块变更。
- 存在非显然取舍、迁移步骤或回滚风险。
- 单看 diff 无法理解修改原因。
- 破坏性变更。

正文不逐行复述 diff，也不记录会迅速失效的临时讨论。

## Footer

脚注承载机器可解析或需要长期保留的元信息。项目使用以下格式：

```text
OpenSpec: <change-name>
Refs: #<issue-number>
Closes #<issue-number>
Co-authored-by: Name <email>
BREAKING CHANGE: <migration and impact>
```

规则：

- 实现 OpenSpec change 的提交应写 `OpenSpec: <change-name>`。
- 仅关联问题使用 `Refs`；提交落地后可关闭问题时使用 `Closes`。
- `Co-authored-by` 必须使用可识别的姓名和有效邮箱。
- 不得在 footer 中写密钥、内部凭据或仅存在于本机的路径。

## Breaking Change

破坏性变更同时满足以下要求：

1. 标题在冒号前使用 `!`。
2. footer 使用大写 `BREAKING CHANGE:`。
3. 正文或 footer 明确受影响方、迁移步骤、兼容窗口和回滚方式。
4. 架构、协议、存储或流程破坏性变更必须关联 OpenSpec change。

示例：

```text
feat(protocol)!: 统一实时消息信封

将实时请求、响应和 push 收敛到同一 envelope，并冻结 message ID 路由规则。
已通过 Go golden packet 与路由契约测试。

OpenSpec: redesign-realtime-envelope
BREAKING CHANGE: 旧客户端必须升级协议生成物后才能连接 v2 endpoint。
```

## 提交粒度

- 一个提交只表达一个主要意图，并可以独立评审和回滚。
- 功能、对应测试、必要文档、生成配置和 fixtures/golden 应在同一提交中保持一致；可重复生成的 Go/C# code 不进入提交。
- 不把无关格式化、重命名或顺手重构混入业务提交。
- 不提交编译失败、测试失败或规格与实现不一致的中间状态。
- `fixup!`、`squash!` 和 `WIP` 只能存在于尚未共享的本地历史，合并前必须整理。
- 已共享历史原则上不重写；确需历史清理时必须明确影响范围并协调强制更新。

## Revert 与合并

撤销提交使用：

```text
revert(world): 撤销个人世界进入闸门

撤销原因以及替代处理方式。

Refs: <commit-hash>
```

- revert 必须说明撤销原因，不能只保留自动生成标题。
- squash 后的最终提交消息必须完整符合本规范。
- merge commit 无法避免时，标题应说明被合并的业务目标，不能只保留无意义分支名。

## 提交前检查

提交前至少确认：

1. `git status` 中没有无关文件、密钥、本机配置或缓存。
2. `git diff --cached` 与本次提交的单一意图一致。
3. formatter、linter、相关测试和 `git diff --check` 已通过。
4. OpenSpec tasks、主 specs 和 owner docs 已按实际进度更新。
5. 标题符合 `type(scope): subject`，正文与 footer 满足本规范。
6. 当前提交可以独立说明、验证和回滚。

## 完整示例

```text
fix(world): 拒绝访客执行 Owner 命令

世界命令统一从 AuthContext 与 VisitSession admission 取得操作者身份，并在领域 policy 中校验角色权限。
增加 Visitor 越权、过期 admission 和重复命令测试。

OpenSpec: enforce-personal-world-owner-policy
Refs: #42
```
