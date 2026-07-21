## MODIFIED Requirements

### Requirement: Unity 工程基线必须可重复恢复

客户端工程 MUST 使用 `versions.yaml` 锁定的 Unity Editor 版本，提交 Package manifest/lock、必要 ProjectSettings、Unity 源资产及其 Editor 生成的 `.meta`，并保持 Library、Temp、UserSettings、构建输出与 generated C# 不进入 Git。BootstrapScene MUST 是 PC 构建中首个且唯一的应用入口；已批准的内容 Scene MAY 作为后续 enabled scene 加入 build，但 MUST NOT 包含第二个 AppRoot、session、channel 或持久 UI root。Windows Player 的 Company Name 与 Product Name MUST 使用项目身份，不得保留 Unity 模板值。没有 Unity Services 消费者时 MUST 保持 Unity Cloud Project 未绑定且 Unity Connect 总开关关闭。客户端 MUST NOT 把 ProjectSettings 保存的 Standalone Application Identifier 当作 Windows UI 契约或质量门；平台专属标识的发布语义由实际启用对应目标平台的 change 管理。

#### Scenario: 从干净检出恢复客户端工程

- **WHEN** 在没有 Unity 本地缓存的新环境中使用锁定 Editor 打开仓库
- **THEN** Package 能按 lock 恢复，BootstrapScene、PersonalWorldScene、产品 UXML/USS/Prefab 直接引用不丢失，项目无需依赖未跟踪资产即可进入编译和测试

#### Scenario: Unity 版本、Windows 项目身份或构建场景漂移

- **WHEN** `ProjectVersion.txt` 与 `versions.yaml` 不一致，Windows Company Name/Product Name 不是登记的项目身份，BootstrapScene 不是 build index 0，或登记内容 Scene 缺失/重复应用根
- **THEN** 客户端基线验证失败并报告具体漂移项；修正前的后续测试或构建不能作为验收证据

#### Scenario: 客户端意外绑定 Unity Services

- **WHEN** ProjectSettings 保存了 Unity Cloud Project ID 或启用了 Unity Connect 总开关
- **THEN** 客户端基线验证失败，避免构建和 Player 隐式依赖未登记的云端项目或服务

## ADDED Requirements

### Requirement: 产品 Scene 必须通过唯一场景转换边界管理

App Scope MUST 由唯一 scene transition Host 按登记的 build scene identity 加载和卸载产品 Scene，并由既有 `SceneLifetimeOwner` 为每次提交分配 generation/cancellation。Scene transition Host MUST 只负责 Unity SceneManager 操作、当前 Scene handle 与 Context 发现/注入，不得决定 world target、保存业务 snapshot 或接受任意资源路径。加载后的 Scene MUST 恰好包含一个对应 `SceneContext`，且 Context MUST 在 generation current 时才能写入 Unity 对象。

#### Scenario: 加载 PersonalWorldScene

- **WHEN** application flow 请求为 current target generation 加载登记的 PersonalWorldScene
- **THEN** Host additive 加载该 build scene、验证唯一 Context、注入新 SceneLifetime，并只在全部步骤成功后发布可显示的 Scene Scope

#### Scenario: Scene 缺少或重复 Context

- **WHEN** 加载的 PersonalWorldScene 没有 Context 或包含两个同类 Context
- **THEN** scene transition 失败、使候选 generation 失效并卸载候选 Scene，不选择任意一个 Context 继续运行

#### Scenario: 目标切换或 App 停止

- **WHEN** current target generation 改变、SceneContext 卸载或 AppLifetime 停止
- **THEN** Host 先取消旧 SceneLifetime 和 scene-bound UI，再卸载旧 Scene，并拒绝旧 load/unload callback 覆盖新的 current Scene handle
