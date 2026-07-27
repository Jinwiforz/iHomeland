## 1. 验证契约与影响面计划

- [x] 1.1 建立 closed validation catalog/schema，登记 incremental、targeted-expensive 与 final-only checks、owner、原因和稳定执行顺序。
- [x] 1.2 为 `qualify-battle-network`、`extend-battle-resync-expiry`、`add-battle-input-acknowledgement` 和本 change 建立机器可读 validation plan，移除重复最终资格责任。
- [x] 1.3 增加 catalog/plan validator 与 failure regression，拒绝 unknown command、final-only 注入、重复 check、缺失 reason 和任意脚本文本。

## 2. 单一公共质量入口

- [x] 2.1 实现 `tools/quality/quality.ps1 impact`，只读输出 change checks、成本分类、owner 与中文原因。
- [x] 2.2 实现 `check-change`，按稳定顺序复用既有 owner 执行增量/定向 checks，并在运行前拒绝 final-only check。
- [x] 2.3 实现 `diagnose`，显式委托现有 battle qualification 单场景且保证 evidence 不进入最终资格。
- [x] 2.4 实现 `qualify`，要求 clean current commit、构建一次候选、自动编排既有完整链、捕获两次 verify 与 soak RunId 后调用 finalize。
- [x] 2.5 增加统一入口 contract/failure tests，覆盖 dry-run、unknown change、dirty candidate、自动升级拒绝、owner failure 和低敏输出。

## 3. 复用现有工具并降低诊断成本

- [x] 3.1 为相同 source identity 的 battle diagnose 增加 ignored Go binary cache、digest receipt 和同卷 hard-link materialization，保持 run credential/evidence/cleanup 隔离。
- [x] 3.2 审计现有公开工具与嵌套调用，删除无唯一职责的重复包装，保留 Go/C++/Proto/storage/server/client/battle 各自唯一 owner。
- [x] 3.3 调整活跃 change tasks 和资格边界：handshake、resync、acknowledgement 只保留直接影响面验证，B0.6 tooling 完成不要求当前工作区产生最终 qualified report。

## 4. 流程、路线图与长期规格

- [x] 4.1 更新 workflow、engineering standards、roadmap 和 battle qualification runbook，默认只公开统一入口并明确完整资格只能由用户显式触发。
- [x] 4.2 更新工具/文件结构文档与注释，说明 catalog、validation plan、内部 owner、缓存和最终 candidate 生命周期。
- [x] 4.3 同步 project-validation、battle-network-qualification 与 delivery-sequencing delta specs，确保历史 B0.x tests 成为当前 capability suites 而非线性重建链。

## 5. 定向验证

- [x] 5.1 运行统一入口 validator、contract/failure tests、`impact` 和各活跃 change 的 `check-change` dry-run/定向 checks。
- [x] 5.2 验证 battle diagnose 缓存命中/漂移、run 隔离与 cleanup contract，不运行完整矩阵、连续 verify、soak 或 finalize。
- [x] 5.3 执行格式、PowerShell 静态/contract checks、相关 OpenSpec strict 和全仓 OpenSpec strict，确认 production 业务代码与最终资格标准未被重写或放宽。
