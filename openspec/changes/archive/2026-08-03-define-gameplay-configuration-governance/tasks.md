## 1. 建立 gameplay configuration source contract

- [x] 1.1 在 `shared/contracts/fixtures/battle/gameplay-config/README.md` 冻结 `gameplay-config-format-v1`、目录 owner、canonical UTF-8/LF JSON、package identity 算法、model/profile compatibility、敏感字段禁止项和 B0.8 consumer 边界。
- [x] 1.2 创建 closed schemas，分别约束 manifest、package、authority、presentation、bindings、typed reference、单位/范围与 coverage；拒绝 unknown field、路径逃逸、非法 identity、无单位数字和隐式默认值。
- [x] 1.3 创建 versioned registries，登记 semantic ID namespace、document/target kind、允许引用方向、authority/presentation 字段 owner、numeric unit/range/rounding/overflow policy 和 B0.8 required role coverage。
- [x] 1.4 创建 `governance-only` reference package，以 `fixture/` namespace完整表达 Owner/Visitor archetype、sword、fan、primary ability、ordinary monster、Boss phase、PersonalWorld encounter、collision/navigation 与 cue/resource mapping，但不把示例数值或资源标记为 production/qualified。
- [x] 1.5 创建 manifest 并双向登记 schema、registry、reference package、coverage 与 raw-file SHA-256，验证 `ConfigIdentity`、`NavigationIdentity`、`PhysicsIdentity` 保持独立且 package identity 可从 source 确定重算。

## 2. 实现确定只读 validator

- [x] 2.1 新增 `tools/gameplay-config/gameplay-config.ps1` 非交互 `validate` 入口，按项目 PowerShell/中文注释规范提供稳定退出码、低敏结果和 corpus-relative 诊断。
- [x] 2.2 实现 closed schema、manifest 双向完整、规范路径/LF/UTF-8、raw digest、format/package/model/profile compatibility 和 config/nav/physics binding 校验，不自动重写 source。
- [x] 2.3 实现 typed semantic ID、duplicate/retired/cross-kind/dangling reference、允许引用方向和非法 cycle 校验，并以 semantic ID 规范排序产生稳定摘要。
- [x] 2.4 实现单位、范围、scaled-integer/checked arithmetic、Boss threshold、projectile travel/lifetime、capacity reservation 与 encounter peak 的静态安全校验；不得执行 Tick、AI、damage、Jolt 或 Detour evaluator。
- [x] 2.5 实现 required role coverage、authority/presentation allowlist、共享 semantic parity、`governance-only`/production classification 与敏感字段校验，确保 C++ 配置不含 Unity GUID/path且 presentation 不复制权威数值。

## 3. 建立隔离失败回归

- [x] 3.1 新增 `tools/gameplay-config/gameplay-config.tests.ps1`，只在隔离临时副本覆盖 unknown/missing field、未登记/悬空文件、digest/format/model/profile 漂移、路径逃逸和非规范编码失败。
- [x] 3.2 增加 duplicate/retired/cross-kind/dangling ID、非法引用方向、自引用与多节点 cycle 失败回归，并验证每个 case 由预期稳定 reason 拒绝。
- [x] 3.3 增加非法单位/范围、signed 64-bit 中间值溢出、Boss threshold 非单调、projectile/encounter/capacity 超限与隐藏默认值失败回归。
- [x] 3.4 增加 authority 引用 Unity 资产、presentation 重复伤害/冷却/AI、required role/mapping 缺失、production 误用 fixture、敏感字段和绝对路径泄漏失败回归。
- [x] 3.5 验证连续两次 validate 输出字节一致、corpus 与 Git 工作区不变、无 listener/Docker/CMake/Unity/网络副作用，且 validator 未导入任何 gameplay runtime 实现。

## 4. 接入定向质量治理

- [x] 4.1 在 `tools/quality/catalog.json` 登记 incremental `gameplay-config-validate` check，并在 `quality.ps1` 通过唯一 validator/tests 入口执行；不得加入 full qualification、soak 或历史 B0.x finalize 链。
- [x] 4.2 扩展 quality contract/failure regressions，验证新 check 的 catalog schema、order、dispatch、dry-run 与未知/遗漏 check fail-closed 行为。
- [x] 4.3 在新 check 登记后更新并维护本 change 的 closed `validation.json`，只引用 `quality-contract`、`gameplay-config-validate` 与 `openspec-change-strict`，逐项说明纯配置治理的直接影响原因。

## 5. 同步 owner 文档与 B0.8 阶段门

- [x] 5.1 更新 `docs/gameplay-simulation-architecture.md`，登记 gameplay package、authority/presentation/config-nav-physics owner、单位/引用、instance 不可变、replacement/rollback 和低敏 evidence 语义。
- [x] 5.2 更新 `docs/file-structure.md` 与必要 architecture owner 说明，登记 corpus/validator/未来 B0.8 consumer 目录边界，明确 shared 中只有契约数据且 reference package 不能被 production 加载。
- [x] 5.3 更新 `docs/roadmap.md` 的独立配置治理节点、完成 evidence 和 B0.8 精确进入条件；保持剑/扇子/怪物/Boss/地图资产、runtime consumer 与最终产品资格未完成。
- [x] 5.4 复核协议、端口、Redis/MySQL 与 release 文档，确认本 change 没有新增 message、route、listener、key/table、客户端 config projection 或部署期 hot reload；只在真实 owner 边界变化处更新引用，不复制规则。

## 6. 完成定向验证与可回滚交付

- [x] 6.1 通过 `tools/quality/quality.ps1 impact -Change define-gameplay-configuration-governance` 预览影响面，并用 `check-change` 执行 closed validation plan；不得自动升级为完整 battle/product qualification。
- [x] 6.2 同步 `gameplay-configuration-governance` 与 `delivery-sequencing` delta 到主 specs，执行 OpenSpec strict、`git diff --check`、文档链接/格式、注释、secret/cache/generated 与本机绝对路径审计。
- [x] 6.3 复核全部 tasks、corpus digest、低敏输出、owner/parity/coverage 和回滚说明，记录“仅解锁 B0.8 提案、不代表可玩内容或最终资格”的 completion evidence，并保持提交可回滚到 B0.7/权威移动投影归档基线。
