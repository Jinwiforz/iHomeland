# Client Runtime 规格

## Purpose

定义 Unity 客户端进入条件、Composition Root、App/Scene Scope、Service/Host 和双 UI 行为。

## Requirements

### Requirement: 客户端必须等待服务端 v1 冻结
Unity 客户端 MUST 只在服务端通过资格验收并冻结 schema、route registry、endpoint manifest 和 contract fixtures 后开始运行时实现。

#### Scenario: 服务端契约未冻结
- **WHEN** 服务端 v1 仍允许修改基础 session、错误或消息路由语义
- **THEN** 客户端不得实现依赖这些未冻结行为的运行时代码

### Requirement: 客户端必须使用 Composition Root
AppBootstrap MUST 调用唯一 AppComposition 创建 AppRoot、纯 C# Services、必要 Unity Hosts 和显式依赖，并管理失败回滚与逆序关闭。

#### Scenario: 重复执行 bootstrap
- **WHEN** 启动场景重复加载或应用根已经存在
- **THEN** 客户端不得创建第二套 session、network channels、UI hosts 或业务 services

### Requirement: App Scope 与 Scene Scope 必须分离
App Scope MUST 持有账号、网络、PersonalWorld、WorldInstance/VisitSession 投影和应用流程，Scene Scope MUST 持有 camera、lighting、地图、角色和场景型 UI；场景对象不得成为应用事实 owner。

#### Scenario: 卸载 gameplay scene
- **WHEN** SceneContext 随场景卸载
- **THEN** App Scope 保持账号、连接、world identity 与 visit lifecycle，并释放所有已卸载场景引用、订阅和迟到 callback

### Requirement: 业务逻辑必须优先使用纯 C# Service
不依赖 Unity 生命周期函数、GameObject 或 Inspector 引用的账号、个人世界、访客会话、网络状态与规则 MUST 使用可独立测试的普通 C# 类型。

#### Scenario: 测试 PersonalWorld Service
- **WHEN** 测试 own-world admission、world revision、Visitor role 和 safe-return 投影
- **THEN** 测试无需创建 GameObject、Scene 或具体 UI view

### Requirement: Unity Host 必须保持轻量
MonoBehaviour MUST 只承担引擎回调、Coroutine、主线程投递、GameObject 生命周期、Audio、UI 和 SceneContext adapter，不得实现业务状态机。

#### Scenario: 网络线程收到 world push
- **WHEN** 后台 receive pump 收到更高 revision 的 world 或 visit snapshot
- **THEN** 它通过主线程 Host 更新对应纯 C# Service，活动 view 与 Scene adapter 再读取只读状态

### Requirement: 双 UI 必须按页面适配度选择
客户端 MUST 以完整逻辑页面为单位选择 UI Toolkit 或 uGUI，不得为展示技术栈而强制混搭；同一 screen 只能有一个 active owner。

#### Scenario: 实现邀请与访问列表
- **WHEN** 页面包含好友邀请、访问状态、筛选和可复用样式
- **THEN** 客户端优先评估 UI Toolkit，并通过统一 UI 入口管理生命周期和输入

#### Scenario: 实现世界空间血条
- **WHEN** UI 需要跟随场景角色和 camera
- **THEN** 客户端优先使用 uGUI 或明确适合世界空间的实现

### Requirement: Unity 资产不得保存在线业务事实
Scene、Prefab 和 ScriptableObject MUST 只保存内容、表现、配置和共享定义，不得作为 session、socket、PersonalWorld、WorldInstance、VisitSession 或玩家资产的最终 owner。

#### Scenario: 收到最新世界快照
- **WHEN** 服务端推送更高 revision 的 PersonalWorld 或 VisitSession snapshot
- **THEN** App Scope Service 更新唯一运行时事实，UI 与场景只从该事实派生展示
