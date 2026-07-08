# iHomeland Client Architecture

## 1. 文档目的

本文档用于约定 Unity 客户端的基础架构设计理念、目录组织、模块边界、命名规范和后续扩展原则。

本架构目标是让项目在早期保持简单，同时为后续扩展 UI、音频、资源加载、场景切换、网络、战斗、ECS 等模块预留清晰边界。

当前 Unity 工程已经跑通基础应用链路：

```text
MainScene
  ↓
LoadingPage
  ↓
LoginPage
  ↓
HomePage
  ↓
Start Game
  ↓
LoadingPage
  ↓
BattleScene
```

该链路用于验证外层生命周期、UI 打开关闭和场景切换。第一里程碑仍以“自定义房间大厅”为目标，`Start Game` 后续应改为进入房间大厅或创建/加入房间流程；在 battle server 设计完成前，不把 `BattleScene` 扩展成正式高频战斗模块。

---

## 2. 总体设计理念

客户端采用：

```text
AppRoot + AppBootstrap + Systems
```

作为基础运行架构。

核心思想是：

```text
一个全局应用根节点
一个明确的启动流程
多个受统一生命周期管理的系统模块
业务内容通过系统进行加载、创建和管理
```

架构重点不是让所有类都变成单例，而是让全局入口集中在 `AppRoot`，其他模块由 `AppRoot` 创建、初始化、更新和关闭。

---

## 3. 设计目标

### 3.1 明确入口

项目运行时应有一个明确入口：

```text
AppRoot
```

`AppRoot` 是客户端应用的全局根节点，负责持有和管理所有基础系统。

### 3.2 明确启动流程

启动流程由：

```text
AppBootstrap
```

负责。

`AppBootstrap` 只负责启动顺序，不承担具体业务系统逻辑。

### 3.3 明确系统边界

基础能力通过 `System` 表达，例如：

```text
AssetSystem
SceneSystem
UISystem
AudioSystem
AccountSystem
NetworkSystem
SaveSystem
BattleSystem
```

每个系统负责一个完整功能领域。

### 3.4 避免全局单例泛滥

项目只保留一个主要全局入口：

```csharp
AppRoot.Instance
```

其他系统通过 `AppRoot` 访问：

```csharp
AppRoot.Instance.UI
AppRoot.Instance.Asset
AppRoot.Instance.Audio
AppRoot.Instance.Scene
```

不推荐每个系统都做成：

```csharp
XXXSystem.Instance
XXXManager.Instance
```

---

## 4. 推荐目录结构

推荐 Unity 客户端主体内容放在：

```text
Assets/App/
```

示例结构：

```text
Assets/
  App/
    Scripts/
      App.asmdef
      Editor/
        App.Editor.asmdef
        WebSocketSmokeTestMenu.cs

      Core/
        AppRoot.cs
        AppBootstrap.cs
        AppConfig.cs

      Systems/
        IAppSystem.cs
        AppSystemBase.cs
        AssetSystem.cs
        SceneSystem.cs
        UISystem.cs
        AudioSystem.cs

      UI/
        UIPanel.cs
        HomePage.cs

      Utils/
        Log.cs

    Resources/
      UI/
        Pages/
          HomePage.prefab

      Audio/
        BGM/
        SFX/

    Prefabs/
    Art/
    Audio/

  Settings/
```

说明：

```text
Assets/App/       项目主体内容
Assets/Settings/  Unity / URP 等项目配置资源
```

`Scripts` 是否保留可根据团队习惯决定。若保留 `Scripts`，命名空间中不建议包含 `Scripts`。

---

## 5. 场景设计

基础场景建议保持简单。

推荐：

```text
MainScene
  AppRoot
```

`MainScene` 负责承载应用入口。

`AppRoot` 是一个空物体，挂载：

```text
AppRoot.cs
```

其他基础系统由 `AppRoot` 在运行时创建。

---

## 6. 启动流程

客户端启动流程如下：

```text
Unity 加载 MainScene
↓
AppRoot.Awake()
↓
创建基础 Systems
↓
AppRoot.Start()
↓
创建并运行 AppBootstrap
↓
AppBootstrap.Run()
↓
AppRoot.Initialize()
↓
初始化所有 Systems
↓
进入应用主流程
```

示例流程：

```text
AppRoot
  AssetSystem
  SceneSystem
  UISystem
  AudioSystem
  AppBootstrap
```

---

## 7. 核心模块职责

### 7.1 AppRoot

职责：

```text
维护全局唯一入口
创建基础系统
初始化基础系统
每帧驱动系统 Tick
关闭系统
在场景切换时保持常驻
```

`AppRoot` 是客户端生命周期的总控。

不建议把具体业务逻辑直接写进 `AppRoot`。

---

### 7.2 AppBootstrap

职责：

```text
定义启动顺序
初始化应用
进入第一个业务流程
打开初始 UI
连接必要服务
加载必要配置
```

`AppBootstrap` 是启动流程类，不是系统，不是管理器。

它应该保持轻量。

---

### 7.3 AppSystemBase

所有基础系统的统一基类。

推荐系统生命周期：

```csharp
Initialize()
Tick(float deltaTime)
Shutdown()
```

统一生命周期的好处：

```text
初始化顺序明确
关闭顺序明确
系统边界清晰
方便后续扩展
```

---

### 7.4 AssetSystem

资源系统。

职责：

```text
加载资源
实例化 Prefab
统一资源访问入口
后续可替换为 Addressables 或 AssetBundle
```

早期可以使用：

```csharp
Resources.Load<T>()
```

后期如项目规模增大，可将内部实现替换为 Addressables，但保持外部接口尽量稳定。

---

### 7.5 SceneSystem

场景系统。

职责：

```text
加载场景
切换场景
记录当前场景
处理异步场景加载
```

场景系统不应直接承载 UI、音频、战斗等复杂业务逻辑。

---

### 7.6 UISystem

UI 系统。

职责：

```text
创建 UI 根节点
创建 EventSystem
打开页面
关闭页面
销毁页面
缓存页面
维护 UI 层级
```

UI 页面推荐做成 Prefab，由 `UISystem` 动态加载。

示例资源路径：

```text
Assets/App/Resources/UI/Pages/HomePage.prefab
```

---

### 7.7 AudioSystem

音频系统。

职责：

```text
创建音频根节点
播放 BGM
停止 BGM
播放音效
控制音量
维护音频播放状态
```

音频系统负责音频能力，不建议散落到各个业务脚本中直接创建 `AudioSource`。

---

### 7.8 AccountSystem

账号系统。

职责：

```text
维护当前登录状态
发起注册、登录、登出和会话恢复
保存服务端返回的玩家资料和 session 过期时间
在会话失效时清理本地账号状态
为房间大厅流程提供当前玩家身份
```

`AccountSystem` 的状态必须以服务端响应为准。客户端表单校验只用于提前提示空账号、空密码等输入问题，不得把本地校验成功视为已登录。

---

### 7.9 NetworkSystem

网络系统。

职责：

```text
请求版本接口
管理 WebSocket 连接
收发 Protobuf envelope
维护 request_id 和 pending request
发送心跳并处理空闲或断线状态
分发结构化错误和业务响应
```

账号、房间和后续联机模块应通过 `NetworkSystem` 发送请求，不应各自直接持有 WebSocket 连接。

---

## 8. System 与 Manager 的使用原则

### 8.1 System 的定位

`System` 表示一个完整功能子系统。

适合命名为 `System` 的模块：

```text
AssetSystem
SceneSystem
UISystem
AudioSystem
NetworkSystem
SaveSystem
InputSystem
BattleSystem
HomelandSystem
```

判断标准：

```text
是否代表一个完整能力模块？
是否有自己的初始化和关闭流程？
是否属于 AppRoot 管理的一级模块？
是否对外提供一组功能能力？
```

如果答案是肯定的，优先使用 `System`。

---

### 8.2 Manager 的定位

`Manager` 表示管理一批对象或状态集合。

适合命名为 `Manager` 的模块：

```text
UnitManager
BuffManager
QuestManager
InventoryManager
PoolManager
BuildingManager
WorkerManager
```

判断标准：

```text
是否维护 List / Dictionary？
是否负责创建、销毁、查找一批对象？
是否主要职责是管理某类对象集合？
```

如果答案是肯定的，可以使用 `Manager`。

---

### 8.3 推荐关系

推荐：

```text
AppRoot
  BattleSystem
    UnitManager
    BuffManager
    SkillManager
```

不推荐：

```text
AppRoot
  GameManager
  UIManager
  AudioManager
  SceneManager
  DataManager
```

原则：

```text
System 负责能力
Manager 负责对象
```

---

## 9. 单例使用规范

### 9.1 推荐做法

项目只保留一个主要全局入口：

```csharp
AppRoot.Instance
```

其他系统由 `AppRoot` 持有：

```csharp
AppRoot.Instance.UI
AppRoot.Instance.Asset
AppRoot.Instance.Scene
AppRoot.Instance.Audio
```

### 9.2 不推荐做法

不推荐每个模块都写成单例：

```csharp
UISystem.Instance
AudioSystem.Instance
SceneSystem.Instance
AssetSystem.Instance
```

原因：

```text
依赖关系隐藏
初始化顺序不清楚
生命周期难控制
后续测试和重构困难
全局状态容易失控
```

### 9.3 何时允许单例

只有在满足以下条件时才考虑单例：

```text
全局唯一
生命周期简单
不依赖复杂 Unity 场景状态
不会破坏模块边界
```

即使如此，也应优先考虑由 `AppRoot` 或某个 `System` 持有。

---

## 10. 命名空间规范

推荐使用统一顶层命名空间：

```csharp
namespace App.Core
namespace App.Systems
namespace App.UI
namespace App.Utils
namespace App.Battle
namespace App.Network
```

推荐示例：

```text
App.Core.AppRoot
App.Core.AppBootstrap
App.Systems.UISystem
App.Systems.AudioSystem
App.UI.HomePage
App.Utils.Log
```

不推荐使用过于泛化的命名空间：

```csharp
namespace Core
namespace UI
namespace Systems
```

原因：

```text
过于通用
容易和第三方库冲突
项目归属不清晰
```

---

## 11. Assembly Definition 规范

项目可以使用：

```text
App.asmdef
```

将客户端代码编译成独立程序集。

推荐配置：

```text
Name: App
Auto Referenced: On
No Engine References: Off
Use GUIDs: On
Any Platform: On
```

如果代码使用 Unity UI 和新输入系统，需要引用：

```text
Unity.ugui
Unity.InputSystem
```

`asmdef` 的主要作用：

```text
划分程序集
明确依赖关系
减少默认 Assembly-CSharp 混杂
方便后续拆分 Editor / Tests / Runtime
```

编辑器工具必须放在仅 Editor 平台编译的程序集内，例如：

```text
Assets/App/Scripts/Editor/App.Editor.asmdef
```

该程序集可以引用运行时 `App` assembly，但不得被 Windows、Android、iOS 等玩家运行时构建包含。`UnityEditor.MenuItem`、Inspector 扩展、本地 smoke test 等开发工具都应放在 Editor-only assembly 中。

注意：

```text
命名空间和 asmdef 相关，但不是同一个概念
即使没有 asmdef，也可以使用 namespace App.Core
```

---

## 12. UI 设计规范

### 12.1 页面 Prefab 化

UI 页面推荐做成 Prefab。

示例：

```text
Assets/App/Resources/UI/Pages/HomePage.prefab
```

Prefab 根节点挂对应脚本：

```text
HomePage.cs
```

并继承：

```csharp
UIPanel
```

### 12.2 UI 生命周期

推荐 UI 页面拥有以下生命周期：

```csharp
Initialize()
Show()
Hide()
Close()
DestroySelf()
```

对应内部回调：

```csharp
OnInitialize()
OnShow()
OnHide()
```

### 12.3 UI 打开方式

推荐通过 `UISystem` 打开：

```csharp
AppRoot.Instance.UI.OpenPage("HomePage");
```

不推荐业务脚本直接到处：

```csharp
Resources.Load()
Instantiate()
```

---

## 13. 资源加载规范

早期阶段可以使用 `Resources`。

资源路径示例：

```text
Assets/App/Resources/UI/Pages/HomePage.prefab
Assets/App/Resources/Audio/BGM/MainTheme.wav
Assets/App/Resources/Audio/SFX/ButtonClick.wav
```

加载时路径不包含 `Resources` 和扩展名：

```csharp
UI/Pages/HomePage
Audio/BGM/MainTheme
Audio/SFX/ButtonClick
```

后续项目规模增大后，可以将 `AssetSystem` 内部替换为：

```text
Addressables
AssetBundle
自定义资源加载器
```

外部调用方式尽量保持稳定。

---

## 14. 场景与 Prefab 的关系

推荐使用混合模式：

```text
场景负责承载入口、地图、灯光、烘焙、环境
Prefab 负责 UI、角色、特效、音效、可复用对象
```

不强制所有内容都动态加载。

对于 3D 游戏，场景仍然适合承载：

```text
地图
灯光
反射探针
后处理
NavMesh
关卡摆放
环境物体
```

Prefab 更适合承载：

```text
UI 页面
角色
怪物
技能特效
音效对象
可复用系统对象
```

---

## 15. 后续 ECS 战斗扩展原则

如果后续局内战斗采用 ECS 设计，不会与当前架构冲突。

推荐分层：

```text
AppRoot / App Systems 负责客户端外层生命周期
BattleSystem 负责战斗模块入口和生命周期
ECS Systems 负责战斗内部数据处理逻辑
```

示例结构：

```text
AppRoot
  BattleSystem
    Battle ECS World
      MovementSystem
      SkillSystem
      DamageSystem
      BuffSystem
      DeathSystem
```

推荐命名空间：

```csharp
namespace App.Systems
{
    public sealed class BattleSystem : AppSystemBase
    {
    }
}
```

```csharp
namespace App.Battle.ECS.Systems
{
    public partial struct MovementSystem
    {
    }
}
```

注意：

```text
App.Systems.BattleSystem 是应用级系统
App.Battle.ECS.Systems.MovementSystem 是战斗内部 ECS 系统
```

两者层级不同，不冲突。

---

## 16. ECS 与外层系统的边界

ECS 内部逻辑不建议直接依赖：

```text
AppRoot.Instance
Unity UI
AudioSource
Resources.Load
MonoBehaviour
GameObject
```

推荐边界：

```text
ECS 负责数据和规则
BattleSystem 负责战斗生命周期
View 层负责表现
NetworkSystem 负责网络收发
UISystem 负责界面展示
AudioSystem 负责音频播放
```

例如：

```text
DamageSystem 只计算伤害
View 层负责飘字和动画
AudioSystem 负责播放受击音效
UISystem 负责更新血条显示
```

---

## 17. 代码职责规范

### 17.1 AppRoot

可以做：

```text
创建系统
初始化系统
关闭系统
转发 Tick
维护全局入口
```

不应该做：

```text
UI 具体逻辑
战斗具体逻辑
角色控制逻辑
网络协议处理细节
资源路径硬编码过多
```

---

### 17.2 AppBootstrap

可以做：

```text
定义启动顺序
进入初始流程
打开初始页面
触发配置加载
```

不应该做：

```text
管理所有业务状态
承担大型业务逻辑
变成新的 GameManager
```

---

### 17.3 System

可以做：

```text
管理某个功能领域
提供对外接口
维护内部状态
处理初始化和关闭
```

不应该做：

```text
越界访问其他系统内部细节
承担无关业务
变成万能管理器
```

---

### 17.4 Manager

可以做：

```text
管理一批对象
维护对象集合
提供查找、创建、销毁接口
```

不应该做：

```text
作为全局入口
承担整个模块生命周期
跨多个业务域管理杂项逻辑
```

---

## 18. 命名规范

### 18.1 类命名

使用 PascalCase。

示例：

```text
AppRoot
AppBootstrap
AssetSystem
SceneSystem
UISystem
AudioSystem
HomePage
UIPanel
```

### 18.2 字段命名

私有字段使用 `_camelCase`。

示例：

```csharp
private Canvas _canvas;
private bool _isInitialized;
```

### 18.3 属性命名

公开属性使用 PascalCase。

示例：

```csharp
public UISystem UI { get; private set; }
public bool IsInitialized { get; private set; }
```

### 18.4 方法命名

方法使用 PascalCase。

示例：

```csharp
Initialize()
Shutdown()
OpenPage()
ClosePage()
PlayBgm()
```

### 18.5 资源命名

资源命名应清晰表达用途。

推荐：

```text
HomePage.prefab
MainTheme.wav
ButtonClick.wav
Player.prefab
EnemyGoblin.prefab
```

不推荐：

```text
NewPrefab.prefab
Test.prefab
aaa.prefab
ui1.prefab
```

---

## 19. Git 提交规范

Unity 项目推荐提交：

```text
Assets/
Packages/manifest.json
Packages/packages-lock.json
ProjectSettings/
```

必须提交：

```text
.meta 文件
```

不提交：

```text
Library/
Temp/
Obj/
Logs/
UserSettings/
Build/
Builds/
.vs/
.idea/
*.csproj
*.sln
```

---

## 20. 推荐开发流程

### 20.1 新增基础能力

例如新增存档系统：

```text
Systems/
  SaveSystem.cs
```

然后在 `AppRoot` 中创建和初始化。

### 20.2 新增 UI 页面

步骤：

```text
1. 创建 UI Prefab
2. 放入 Resources/UI/Pages/
3. 创建对应 UIPanel 子类
4. Prefab 根节点挂脚本
5. 使用 UISystem.OpenPage 打开
```

### 20.3 新增业务模块

例如新增家园模块：

```text
Systems/
  HomelandSystem.cs

Homeland/
  BuildingManager.cs
  WorkerManager.cs
  ResourceManager.cs
```

原则：

```text
HomelandSystem 负责家园模块生命周期
Manager 负责模块内部对象集合
```

### 20.4 新增战斗模块

例如新增战斗模块：

```text
Systems/
  BattleSystem.cs

Battle/
  Core/
  ECS/
  View/
  Config/
```

原则：

```text
BattleSystem 是外层入口
Battle/ECS 是内部模拟逻辑
Battle/View 是表现层
```

---

## 21. 架构总结

本架构的核心原则是：

```text
一个入口
统一生命周期
系统负责能力
管理器负责对象
业务模块边界清晰
避免全局单例泛滥
为 ECS 和大型模块扩展预留空间
```

推荐访问方式：

```csharp
AppRoot.Instance.UI.OpenPage("HomePage");
AppRoot.Instance.Audio.PlayBgm("MainTheme");
AppRoot.Instance.Scene.LoadScene("BattleScene");
```

整体结构：

```text
AppRoot
  AppBootstrap
  AssetSystem
  SceneSystem
  UISystem
  AudioSystem
  NetworkSystem
  SaveSystem
  BattleSystem
```

随着项目发展，可以逐步扩展：

```text
AppRoot
  Systems
    BattleSystem
      ECS
      View
      Config

    HomelandSystem
      BuildingManager
      WorkerManager
      ResourceManager
```

架构目标不是一次性设计到最复杂，而是在简单起步的同时，保证后续扩展不会失控。
