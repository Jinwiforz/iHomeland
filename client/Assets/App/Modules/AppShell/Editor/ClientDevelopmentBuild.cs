using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;
using IHomeland.Client.PersonalWorld.Runtime.Scenes;
using IHomeland.Client.PersonalWorldCombat.Runtime.Scenes;
using UnityEditor;
using UnityEditor.Animations;
using UnityEditor.Build.Reporting;
using UnityEngine;

namespace IHomeland.Client.AppShell.Editor
{
    /// <summary>提供Development与Release共用校验和输出规则的唯一Windows Player构建owner。</summary>
    internal static class ClientDevelopmentBuild
    {
        /// <summary>定义所有Windows Player必须使用的公司身份。</summary>
        private const string ExpectedCompanyName = "Jinwiforz";

        /// <summary>定义所有Windows Player必须使用的产品身份。</summary>
        private const string ExpectedProductName = "iHomeland";

        /// <summary>定义Windows Player唯一允许的有序场景基线。</summary>
        private static readonly string[] ExpectedScenes =
        {
            "Assets/App/Modules/AppShell/Content/Scenes/BootstrapScene.unity",
            "Assets/App/Modules/PersonalWorld/Content/Scenes/PersonalWorldScene.unity",
        };

        /// <summary>定义跨 actor 复用的原地 locomotion Clip 路径。</summary>
        private const string InPlaceLocomotionPath =
            "Assets/App/Modules/PersonalWorldCombat/Content/Animations/InPlaceLocomotion.anim";

        /// <summary>定义四个 production Animator 与各自 ability state 的 authoring 输入。</summary>
        private static readonly (string Path, string AbilityState)[] CombatAnimators =
        {
            ("Assets/App/Modules/PersonalWorldCombat/Content/Animations/SwordPrimary.controller", "Ability"),
            ("Assets/App/Modules/PersonalWorldCombat/Content/Animations/FanPrimary.controller", "Ability"),
            ("Assets/App/Modules/PersonalWorldCombat/Content/Animations/MonsterStrike.controller", "Ability"),
            ("Assets/App/Modules/PersonalWorldCombat/Content/Animations/BossSlam.controller", "Ability"),
        };

        /// <summary>从启用的 Build Settings 场景构建 Windows 64-bit Development Player。</summary>
        /// <exception cref="InvalidOperationException">缺少输出参数、启用场景或构建失败时抛出。</exception>
        public static void BuildWindowsDevelopment()
        {
            BuildWindows(BuildOptions.Development);
        }

        /// <summary>从同一启用场景与product identity构建Windows 64-bit Release Player。</summary>
        /// <exception cref="InvalidOperationException">缺少输出参数、启用场景或构建失败时抛出。</exception>
        public static void BuildWindowsRelease()
        {
            BuildWindows(BuildOptions.None);
        }

        /// <summary>
        /// 通过 Unity authoring API 建立跨 actor 的 in-place locomotion 状态机契约。
        /// </summary>
        /// <remarks>
        /// 该入口保持幂等，可由命令行 execute method 或 Editor 菜单调用。它不删除 Idle
        /// 呼吸动画，只保证移动期间 VisualRoot 不再继承 Idle 的垂直位移和缩放曲线。
        /// </remarks>
        [MenuItem("iHomeland/Combat/Normalize Animator Locomotion")]
        public static void NormalizeCombatAnimatorLocomotion()
        {
            var locomotion = CreateOrUpdateInPlaceLocomotion();
            for (var index = 0; index < CombatAnimators.Length; index++)
            {
                NormalizeCombatAnimator(
                    CombatAnimators[index].Path,
                    CombatAnimators[index].AbilityState,
                    locomotion);
            }

            AssetDatabase.SaveAssets();
            AssetDatabase.Refresh(ImportAssetOptions.ForceSynchronousImport);
        }

        /// <summary>独立执行Windows build共用的production combat content gate。</summary>
        /// <remarks>该入口不创建Player，可用于内容authoring后的快速fail-closed复核。</remarks>
        [MenuItem("iHomeland/Combat/Validate Production Content")]
        public static void ValidateCombatContent()
        {
            ValidateCombatResourceCatalog();
        }

        /// <summary>执行两种profile共享的场景、输出和结果校验。</summary>
        /// <param name="options">只允许None或Development。</param>
        private static void BuildWindows(BuildOptions options)
        {
            if (options != BuildOptions.None && options != BuildOptions.Development)
            {
                throw new ArgumentOutOfRangeException(nameof(options));
            }

            var output = ReadArgument("-ihomelandBuildOutput");
            if (string.IsNullOrWhiteSpace(output))
            {
                throw new InvalidOperationException("命令行缺少 -ihomelandBuildOutput。");
            }

            var scenes = EditorBuildSettings.scenes
                .Where(scene => scene.enabled)
                .Select(scene => scene.path)
                .ToArray();
            ValidateProductBaseline(scenes);

            var fullOutput = Path.GetFullPath(output);
            var executablePath = string.Equals(
                Path.GetExtension(fullOutput),
                ".exe",
                StringComparison.OrdinalIgnoreCase)
                ? fullOutput
                : Path.Combine(fullOutput, "iHomeland.exe");
            Directory.CreateDirectory(Path.GetDirectoryName(executablePath) ??
                                      throw new InvalidOperationException("构建输出目录非法。"));
            var report = BuildPipeline.BuildPlayer(
                scenes,
                executablePath,
                BuildTarget.StandaloneWindows64,
                options);
            if (report.summary.result != BuildResult.Succeeded)
            {
                throw new InvalidOperationException(
                    $"Windows Player构建失败：{report.summary.result}。");
            }
        }

        /// <summary>在进入BuildPipeline前验证两种profile共用的产品身份与场景顺序。</summary>
        /// <param name="scenes">从Build Settings读取的启用场景有序快照。</param>
        /// <exception cref="InvalidOperationException">产品身份、版本或场景基线漂移时抛出。</exception>
        private static void ValidateProductBaseline(string[] scenes)
        {
            if (!string.Equals(
                    PlayerSettings.companyName,
                    ExpectedCompanyName,
                    StringComparison.Ordinal) ||
                !string.Equals(
                    PlayerSettings.productName,
                    ExpectedProductName,
                    StringComparison.Ordinal) ||
                string.IsNullOrWhiteSpace(PlayerSettings.bundleVersion))
            {
                throw new InvalidOperationException("Windows Player产品身份基线无效。");
            }

            if (!scenes.SequenceEqual(ExpectedScenes, StringComparer.Ordinal))
            {
                throw new InvalidOperationException("Windows Player启用场景基线无效。");
            }

            ValidateCombatResourceCatalog();
        }

        /// <summary>验证唯一 tracked catalog 与 production JSON、Prefab 和 Animator 双向完整。</summary>
        private static void ValidateCombatResourceCatalog()
        {
            const string expectedAssetPath =
                "Assets/App/Modules/PersonalWorldCombat/Content/Config/ClientCombatResourceCatalog.asset";
            var catalogPaths = AssetDatabase.FindAssets("t:ClientCombatResourceCatalog")
                .Select(AssetDatabase.GUIDToAssetPath)
                .OrderBy(path => path, StringComparer.Ordinal)
                .ToArray();
            if (catalogPaths.Length != 1 ||
                !string.Equals(catalogPaths[0], expectedAssetPath, StringComparison.Ordinal))
            {
                throw new InvalidOperationException(
                    "Windows Player 必须包含唯一且位于 production Content 根的 ClientCombatResourceCatalog。");
            }

            var catalog = AssetDatabase.LoadAssetAtPath<ClientCombatResourceCatalog>(
                catalogPaths[0]);
            if (catalog == null)
            {
                throw new InvalidOperationException(
                    "ClientCombatResourceCatalog asset 无法加载。");
            }

            catalog.ValidateCompleteness();
            ValidatePresentationParity(catalog);
            ValidateCatalogAssets(catalog);
        }

        /// <summary>把 catalog logical/numeric mapping 与同一 production package source 做双向比较。</summary>
        /// <param name="catalog">唯一 tracked production catalog。</param>
        private static void ValidatePresentationParity(
            ClientCombatResourceCatalog catalog)
        {
            var repositoryRoot = Path.GetFullPath(
                Path.Combine(Application.dataPath, "..", ".."));
            var packageRoot = Path.Combine(
                repositoryRoot,
                "shared",
                "contracts",
                "gameplay",
                "battle",
                "packages",
                ClientCombatResourceCatalog.ProductionPackageID);
            using var presentation = ReadJson(
                Path.Combine(packageRoot, "presentation.json"));
            using var wireMapping = ReadJson(
                Path.Combine(packageRoot, "wire-mapping.json"));
            ValidateProductionDocument(presentation.RootElement, "presentation-catalog");
            ValidateProductionDocument(wireMapping.RootElement, "wire-mapping");

            var resources = catalog.GetResourceReferences();
            var resourceByKey = resources.ToDictionary(
                item => item.LogicalKey,
                item => item,
                StringComparer.Ordinal);
            var sourceResources = presentation.RootElement.GetProperty("resources");
            if (resourceByKey.Count != resources.Count ||
                sourceResources.GetArrayLength() != resourceByKey.Count)
            {
                throw new InvalidOperationException(
                    "ClientCombatResourceCatalog logical resource 数量与 production source 不一致。");
            }

            foreach (var source in sourceResources.EnumerateArray())
            {
                var logicalKey = source.GetProperty("logical_key").GetString();
                var sourceKind = source.GetProperty("kind").GetString();
                if (logicalKey == null ||
                    sourceKind == null ||
                    !resourceByKey.TryGetValue(logicalKey, out var resource) ||
                    !string.Equals(
                        ResourceKindName(resource.Kind),
                        sourceKind,
                        StringComparison.Ordinal) ||
                    resource.Asset == null)
                {
                    throw new InvalidOperationException(
                        "ClientCombatResourceCatalog logical resource parity 漂移。");
                }
            }

            var mappings = catalog.GetNumericMappings();
            var mappingBySemantic = mappings.ToDictionary(
                item => $"{item.Kind}\n{item.SemanticID}",
                item => item,
                StringComparer.Ordinal);
            var numericIDs = new HashSet<uint>();
            var sourceMappings = wireMapping.RootElement.GetProperty("mappings");
            if (mappingBySemantic.Count != mappings.Count ||
                sourceMappings.GetArrayLength() != mappingBySemantic.Count)
            {
                throw new InvalidOperationException(
                    "ClientCombatResourceCatalog numeric mapping 数量与 production source 不一致。");
            }

            foreach (var source in sourceMappings.EnumerateArray())
            {
                var kind = source.GetProperty("kind").GetString();
                var semanticID = source.GetProperty("semantic_id").GetString();
                var numericID = source.GetProperty("numeric_id").GetUInt32();
                var key = $"{kind}\n{semanticID}";
                if (numericID == 0 ||
                    kind == null ||
                    semanticID == null ||
                    !numericIDs.Add(numericID) ||
                    !mappingBySemantic.TryGetValue(key, out var mapping) ||
                    mapping.NumericID != numericID)
                {
                    throw new InvalidOperationException(
                        "ClientCombatResourceCatalog numeric mapping parity 漂移。");
                }
            }
        }

        /// <summary>验证 Animator、VFX、HUD 与 camera assets 没有 authority 或缺失脚本入口。</summary>
        /// <param name="catalog">已通过 source parity 的唯一 catalog。</param>
        private static void ValidateCatalogAssets(ClientCombatResourceCatalog catalog)
        {
            foreach (var resource in catalog.GetResourceReferences())
            {
                if (resource.Asset is GameObject gameObject &&
                    GameObjectUtility.GetMonoBehavioursWithMissingScriptCount(gameObject) != 0)
                {
                    throw new InvalidOperationException(
                        $"Combat resource 包含 missing script：{resource.LogicalKey}。");
                }

                if (resource.Kind == ClientCombatResourceKind.Animator)
                {
                    ValidateAnimator(resource);
                }
            }
        }

        /// <summary>验证 ability Animator 的参数、三态 clip 与零 Animation Event 契约。</summary>
        /// <param name="resource">Animator logical resource。</param>
        private static void ValidateAnimator(ClientCombatResourceReference resource)
        {
            if (!(resource.Asset is AnimatorController controller))
            {
                throw new InvalidOperationException(
                    $"Combat animator 必须是 AnimatorController：{resource.LogicalKey}。");
            }

            var dead = controller.parameters.Count(parameter =>
                string.Equals(parameter.name, "Dead", StringComparison.Ordinal) &&
                parameter.type == AnimatorControllerParameterType.Bool);
            var ability = controller.parameters.Count(parameter =>
                string.Equals(parameter.name, "Ability", StringComparison.Ordinal) &&
                parameter.type == AnimatorControllerParameterType.Trigger);
            var moving = controller.parameters.Count(parameter =>
                string.Equals(parameter.name, "Moving", StringComparison.Ordinal) &&
                parameter.type == AnimatorControllerParameterType.Bool);
            var clips = controller.animationClips
                .Distinct()
                .ToArray();
            var locomotion = clips.SingleOrDefault(clip =>
                string.Equals(clip.name, "InPlaceLocomotion", StringComparison.Ordinal));
            if (dead != 1 || ability != 1 || moving != 1 || clips.Length < 4 || locomotion == null)
            {
                throw new InvalidOperationException(
                    $"Combat animator 参数或 idle/locomotion/ability/dead clips 不完整：{resource.LogicalKey}。");
            }

            ValidateInPlaceLocomotion(locomotion, resource.LogicalKey);
            ValidateAnimatorStateMachine(controller, resource.LogicalKey);

            foreach (var clip in clips)
            {
                if (AnimationUtility.GetAnimationEvents(clip).Length != 0)
                {
                    throw new InvalidOperationException(
                        $"Combat animation 禁止 Animation Event：{resource.LogicalKey}。");
                }
            }
        }

        /// <summary>创建或重建只写 VisualRoot 基线的共享 locomotion Clip。</summary>
        /// <returns>已由 AssetDatabase 持有的 tracked AnimationClip。</returns>
        private static AnimationClip CreateOrUpdateInPlaceLocomotion()
        {
            var clip = AssetDatabase.LoadAssetAtPath<AnimationClip>(InPlaceLocomotionPath);
            if (clip == null)
            {
                clip = new AnimationClip
                {
                    name = "InPlaceLocomotion",
                    frameRate = 60f,
                };
                AssetDatabase.CreateAsset(clip, InPlaceLocomotionPath);
            }

            foreach (var binding in AnimationUtility.GetCurveBindings(clip))
            {
                AnimationUtility.SetEditorCurve(clip, binding, null);
            }

            SetConstantTransformCurve(clip, "m_LocalPosition.x", 0f);
            SetConstantTransformCurve(clip, "m_LocalPosition.y", 0f);
            SetConstantTransformCurve(clip, "m_LocalPosition.z", 0f);
            SetConstantTransformCurve(clip, "m_LocalScale.x", 1f);
            SetConstantTransformCurve(clip, "m_LocalScale.y", 1f);
            SetConstantTransformCurve(clip, "m_LocalScale.z", 1f);
            var settings = AnimationUtility.GetAnimationClipSettings(clip);
            settings.loopTime = true;
            settings.loopBlend = true;
            AnimationUtility.SetAnimationClipSettings(clip, settings);
            AnimationUtility.SetAnimationEvents(clip, Array.Empty<AnimationEvent>());
            EditorUtility.SetDirty(clip);
            return clip;
        }

        /// <summary>把一个 production controller 规范化为 Idle/Locomotion/Ability/Dead 四态图。</summary>
        /// <param name="path">Controller asset 路径。</param>
        /// <param name="abilityStateName">既有 ability state 名称。</param>
        /// <param name="locomotion">共享原地 locomotion Clip。</param>
        private static void NormalizeCombatAnimator(
            string path,
            string abilityStateName,
            AnimationClip locomotion)
        {
            var controller = AssetDatabase.LoadAssetAtPath<AnimatorController>(path);
            if (controller == null || controller.layers.Length != 1)
            {
                throw new InvalidOperationException($"Combat Animator 无法规范化：{path}。");
            }

            EnsureParameter(controller, "Dead", AnimatorControllerParameterType.Bool);
            EnsureParameter(controller, "Ability", AnimatorControllerParameterType.Trigger);
            EnsureParameter(controller, "Moving", AnimatorControllerParameterType.Bool);

            var stateMachine = controller.layers[0].stateMachine;
            var idle = FindState(stateMachine, "Idle", path);
            var ability = FindState(stateMachine, abilityStateName, path);
            var dead = FindState(stateMachine, "Dead", path);
            var locomotionState = stateMachine.states
                .Select(item => item.state)
                .FirstOrDefault(state => string.Equals(
                    state.name,
                    "Locomotion",
                    StringComparison.Ordinal)) ?? stateMachine.AddState("Locomotion");
            locomotionState.motion = locomotion;
            locomotionState.writeDefaultValues = true;
            stateMachine.defaultState = idle;

            ClearTransitions(stateMachine, idle, locomotionState, ability, dead);
            ConfigureTransition(
                stateMachine.AddAnyStateTransition(dead),
                hasExitTime: false,
                (AnimatorConditionMode.If, "Dead"));
            ConfigureTransition(
                idle.AddTransition(locomotionState),
                hasExitTime: false,
                (AnimatorConditionMode.If, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ConfigureTransition(
                locomotionState.AddTransition(idle),
                hasExitTime: false,
                (AnimatorConditionMode.IfNot, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ConfigureTransition(
                idle.AddTransition(ability),
                hasExitTime: false,
                (AnimatorConditionMode.If, "Ability"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ConfigureTransition(
                locomotionState.AddTransition(ability),
                hasExitTime: false,
                (AnimatorConditionMode.If, "Ability"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ConfigureTransition(
                ability.AddTransition(idle),
                hasExitTime: true,
                (AnimatorConditionMode.IfNot, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ConfigureTransition(
                ability.AddTransition(locomotionState),
                hasExitTime: true,
                (AnimatorConditionMode.If, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            EditorUtility.SetDirty(controller);
        }

        /// <summary>确保 controller 参数存在且类型唯一匹配。</summary>
        /// <param name="controller">待更新 controller。</param>
        /// <param name="name">参数名称。</param>
        /// <param name="type">参数类型。</param>
        private static void EnsureParameter(
            AnimatorController controller,
            string name,
            AnimatorControllerParameterType type)
        {
            var matches = controller.parameters
                .Where(parameter => string.Equals(parameter.name, name, StringComparison.Ordinal))
                .ToArray();
            if (matches.Length == 0)
            {
                controller.AddParameter(name, type);
                return;
            }

            if (matches.Length != 1 || matches[0].type != type)
            {
                throw new InvalidOperationException(
                    $"Combat Animator 参数类型漂移：{controller.name}/{name}。");
            }
        }

        /// <summary>按 exact name 解析既有 state。</summary>
        /// <param name="stateMachine">唯一 base layer state machine。</param>
        /// <param name="name">Exact state 名称。</param>
        /// <param name="path">用于低敏错误定位的 asset 路径。</param>
        /// <returns>唯一匹配 state。</returns>
        private static AnimatorState FindState(
            AnimatorStateMachine stateMachine,
            string name,
            string path)
        {
            var matches = stateMachine.states
                .Select(item => item.state)
                .Where(state => string.Equals(state.name, name, StringComparison.Ordinal))
                .ToArray();
            if (matches.Length != 1)
            {
                throw new InvalidOperationException($"Combat Animator state 漂移：{path}/{name}。");
            }

            return matches[0];
        }

        /// <summary>清空规范四态图的旧 transition，防止并行状态路径继续消费 Idle 曲线。</summary>
        /// <param name="stateMachine">唯一 base layer state machine。</param>
        /// <param name="states">本 contract 拥有的全部 state。</param>
        private static void ClearTransitions(
            AnimatorStateMachine stateMachine,
            params AnimatorState[] states)
        {
            foreach (var transition in stateMachine.anyStateTransitions)
            {
                stateMachine.RemoveAnyStateTransition(transition);
            }

            foreach (var state in states)
            {
                foreach (var transition in state.transitions)
                {
                    state.RemoveTransition(transition);
                }
            }
        }

        /// <summary>配置一个不自转且固定短 blend 的表现 transition。</summary>
        /// <param name="transition">待配置 transition。</param>
        /// <param name="hasExitTime">是否等待 ability Clip 结束。</param>
        /// <param name="conditions">按序添加的 bool/trigger 条件。</param>
        private static void ConfigureTransition(
            AnimatorStateTransition transition,
            bool hasExitTime,
            params (AnimatorConditionMode Mode, string Parameter)[] conditions)
        {
            transition.hasExitTime = hasExitTime;
            transition.exitTime = hasExitTime ? 1f : 0f;
            transition.hasFixedDuration = true;
            transition.duration = 0.08f;
            transition.canTransitionToSelf = false;
            for (var index = 0; index < conditions.Length; index++)
            {
                transition.AddCondition(
                    conditions[index].Mode,
                    threshold: 0f,
                    conditions[index].Parameter);
            }
        }

        /// <summary>写入 VisualRoot 的单位时长常量 Transform 曲线。</summary>
        /// <param name="clip">目标 AnimationClip。</param>
        /// <param name="property">Unity serialized Transform 属性。</param>
        /// <param name="value">全周期常量。</param>
        private static void SetConstantTransformCurve(
            AnimationClip clip,
            string property,
            float value)
        {
            var binding = EditorCurveBinding.FloatCurve("VisualRoot", typeof(Transform), property);
            AnimationUtility.SetEditorCurve(
                clip,
                binding,
                AnimationCurve.Constant(0f, 1f, value));
        }

        /// <summary>验证共享 locomotion Clip 只写六条常量 VisualRoot 基线曲线。</summary>
        /// <param name="clip">Controller 引用的 locomotion Clip。</param>
        /// <param name="logicalKey">错误定位用 logical resource key。</param>
        private static void ValidateInPlaceLocomotion(
            AnimationClip clip,
            string logicalKey)
        {
            var bindings = AnimationUtility.GetCurveBindings(clip);
            if (bindings.Length != 6 ||
                AnimationUtility.GetAnimationEvents(clip).Length != 0)
            {
                throw new InvalidOperationException(
                    $"Combat locomotion Clip 曲线或 event 漂移：{logicalKey}。");
            }

            foreach (var binding in bindings)
            {
                var position = binding.propertyName.StartsWith(
                    "m_LocalPosition.",
                    StringComparison.Ordinal);
                var scale = binding.propertyName.StartsWith(
                    "m_LocalScale.",
                    StringComparison.Ordinal);
                var expected = scale ? 1f : 0f;
                var curve = AnimationUtility.GetEditorCurve(clip, binding);
                if (!string.Equals(binding.path, "VisualRoot", StringComparison.Ordinal) ||
                    (!position && !scale) ||
                    curve == null ||
                    curve.length != 2 ||
                    !Mathf.Approximately(curve.keys[0].value, expected) ||
                    !Mathf.Approximately(curve.keys[1].value, expected))
                {
                    throw new InvalidOperationException(
                        $"Combat locomotion Clip 不是原地基线：{logicalKey}。");
                }
            }
        }

        /// <summary>验证 production controller 的四态与七条exact transition契约。</summary>
        /// <param name="controller">待验证 controller。</param>
        /// <param name="logicalKey">错误定位用 logical resource key。</param>
        private static void ValidateAnimatorStateMachine(
            AnimatorController controller,
            string logicalKey)
        {
            if (controller.layers.Length != 1)
            {
                throw new InvalidOperationException(
                    $"Combat Animator layer数量漂移：{logicalKey}。");
            }

            var stateMachine = controller.layers[0].stateMachine;
            var states = stateMachine.states
                .Select(item => item.state)
                .ToDictionary(state => state.name, state => state, StringComparer.Ordinal);
            if (states.Count != 4 ||
                !states.TryGetValue("Idle", out var idle) ||
                !states.TryGetValue("Locomotion", out var locomotion) ||
                !states.TryGetValue("Ability", out var ability) ||
                !states.TryGetValue("Dead", out var dead) ||
                stateMachine.defaultState != idle)
            {
                throw new InvalidOperationException(
                    $"Combat Animator四态或default state漂移：{logicalKey}。");
            }

            ValidateTransition(
                stateMachine.anyStateTransitions,
                dead,
                hasExitTime: false,
                logicalKey,
                (AnimatorConditionMode.If, "Dead"));
            ValidateTransition(
                idle.transitions,
                locomotion,
                hasExitTime: false,
                logicalKey,
                (AnimatorConditionMode.If, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ValidateTransition(
                locomotion.transitions,
                idle,
                hasExitTime: false,
                logicalKey,
                (AnimatorConditionMode.IfNot, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ValidateTransition(
                idle.transitions,
                ability,
                hasExitTime: false,
                logicalKey,
                (AnimatorConditionMode.If, "Ability"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ValidateTransition(
                locomotion.transitions,
                ability,
                hasExitTime: false,
                logicalKey,
                (AnimatorConditionMode.If, "Ability"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ValidateTransition(
                ability.transitions,
                idle,
                hasExitTime: true,
                logicalKey,
                (AnimatorConditionMode.IfNot, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
            ValidateTransition(
                ability.transitions,
                locomotion,
                hasExitTime: true,
                logicalKey,
                (AnimatorConditionMode.If, "Moving"),
                (AnimatorConditionMode.IfNot, "Dead"));
        }

        /// <summary>验证指定source集合中恰好一条匹配的transition。</summary>
        /// <param name="transitions">同一source的transition集合。</param>
        /// <param name="destination">Exact destination state。</param>
        /// <param name="hasExitTime">期望exit-time语义。</param>
        /// <param name="logicalKey">错误定位用logical resource key。</param>
        /// <param name="conditions">顺序无关的exact条件集合。</param>
        private static void ValidateTransition(
            AnimatorStateTransition[] transitions,
            AnimatorState destination,
            bool hasExitTime,
            string logicalKey,
            params (AnimatorConditionMode Mode, string Parameter)[] conditions)
        {
            var matches = transitions.Where(transition =>
                    transition.destinationState == destination &&
                    transition.hasExitTime == hasExitTime &&
                    transition.conditions.Length == conditions.Length &&
                    conditions.All(expected => transition.conditions.Any(actual =>
                        actual.mode == expected.Mode &&
                        string.Equals(
                            actual.parameter,
                            expected.Parameter,
                            StringComparison.Ordinal))))
                .ToArray();
            if (matches.Length != 1)
            {
                throw new InvalidOperationException(
                    $"Combat Animator transition漂移：{logicalKey}/{destination.name}。");
            }
        }

        /// <summary>读取必须存在且可解析的 production JSON。</summary>
        /// <param name="path">Repository-local JSON path。</param>
        /// <returns>由调用方释放的 JSON document。</returns>
        private static JsonDocument ReadJson(string path)
        {
            if (!File.Exists(path))
            {
                throw new InvalidOperationException(
                    "Combat production package source 缺失。");
            }

            return JsonDocument.Parse(File.ReadAllText(path));
        }

        /// <summary>验证 package ID、classification 与 document kind。</summary>
        /// <param name="root">JSON root element。</param>
        /// <param name="documentKind">期望 document kind。</param>
        private static void ValidateProductionDocument(
            JsonElement root,
            string documentKind)
        {
            if (!string.Equals(
                    root.GetProperty("package_id").GetString(),
                    ClientCombatResourceCatalog.ProductionPackageID,
                    StringComparison.Ordinal) ||
                !string.Equals(
                    root.GetProperty("qualification_state").GetString(),
                    "production",
                    StringComparison.Ordinal) ||
                !string.Equals(
                    root.GetProperty("document_kind").GetString(),
                    documentKind,
                    StringComparison.Ordinal))
            {
                throw new InvalidOperationException(
                    "Combat production document identity 漂移。");
            }
        }

        /// <summary>把 Unity enum 映射为 production presentation kind。</summary>
        /// <param name="kind">Catalog resource kind。</param>
        /// <returns>Exact lower-case source token。</returns>
        private static string ResourceKindName(ClientCombatResourceKind kind)
        {
            switch (kind)
            {
                case ClientCombatResourceKind.Display:
                    return "display";
                case ClientCombatResourceKind.Animator:
                    return "animator";
                case ClientCombatResourceKind.Vfx:
                    return "vfx";
                case ClientCombatResourceKind.Audio:
                    return "audio";
                case ClientCombatResourceKind.Hud:
                    return "hud";
                case ClientCombatResourceKind.Camera:
                    return "camera";
                default:
                    throw new InvalidOperationException(
                        "Unknown combat resource kind。");
            }
        }

        /// <summary>读取紧随指定名称之后的命令行参数值。</summary>
        /// <param name="name">包含前导连字符的参数名。</param>
        /// <returns>参数值；不存在时返回空字符串。</returns>
        private static string ReadArgument(string name)
        {
            var arguments = Environment.GetCommandLineArgs();
            for (var index = 0; index < arguments.Length - 1; index++)
            {
                if (string.Equals(arguments[index], name, StringComparison.Ordinal))
                {
                    return arguments[index + 1];
                }
            }

            return string.Empty;
        }
    }
}
