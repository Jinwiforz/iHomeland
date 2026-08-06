using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;
using NUnit.Framework;

namespace IHomeland.Client.AppShell.Tests.EditMode
{
    /// <summary>验证client-v1资格manifest、registry与evidence示例的闭合契约。</summary>
    public sealed class ClientQualificationContractTests
    {
        /// <summary>schema必须声明闭合根对象和完整必填字段，防止fixture校验规则暗中放宽。</summary>
        [Test]
        public void SchemasKeepClosedRequiredContracts()
        {
            using (var manifestSchema = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("manifest.schema.json"))))
            using (var evidenceSchema = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("evidence.schema.json"))))
            using (var checklistSchema = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("manual-checklist.schema.json"))))
            using (var diagnosticChecklistSchema = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("diagnostic-checklist.schema.json"))))
            using (var reportSchema = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("report.schema.json"))))
            {
                AssertClosedSchemaRoot(
                    manifestSchema.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "scenarios");
                AssertClosedSchemaRoot(
                    evidenceSchema.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "contractDigest",
                    "buildDigests",
                    "records");
                AssertClosedSchemaRoot(
                    checklistSchema.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "contractDigest",
                    "buildDigests",
                    "profiles",
                    "scenarios");
                AssertClosedSchemaRoot(
                    diagnosticChecklistSchema.RootElement,
                    "schemaVersion",
                    "diagnosticVersion",
                    "qualificationEvidence",
                    "scenarioId",
                    "contractDigest",
                    "developmentBuildDigest",
                    "buildArtifact",
                    "launcherArtifact",
                    "profiles",
                    "automatedStages",
                    "connectionGuide",
                    "manualSteps");
                AssertClosedSchemaRoot(
                    reportSchema.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "startedAt",
                    "durationMs",
                    "contractDigest",
                    "buildDigests",
                    "toolVersions",
                    "qualified",
                    "stages",
                    "records",
                    "failure",
                    "cleanup");

                var scenario = manifestSchema.RootElement
                    .GetProperty("$defs")
                    .GetProperty("scenario");
                Assert.That(
                    scenario.GetProperty("additionalProperties").GetBoolean(),
                    Is.False);
                AssertRequired(
                    scenario,
                    "id",
                    "group",
                    "mandatory",
                    "execution",
                    "preconditions",
                    "budgetMs",
                    "expectedOutcome",
                    "buildProfiles",
                    "evidenceOwner");
            }
        }

        /// <summary>manifest字段闭合、ID唯一且automatic registry双向完整。</summary>
        [Test]
        public void ManifestIsClosedUniqueAndRegistryComplete()
        {
            using (var manifest = JsonDocument.Parse(File.ReadAllText(ContractPath("manifest.json"))))
            using (var registry = JsonDocument.Parse(File.ReadAllText(ContractPath("automatic-registry.json"))))
            {
                AssertClosed(
                    manifest.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "scenarios");
                Assert.That(manifest.RootElement.GetProperty("schemaVersion").GetInt32(), Is.EqualTo(1));
                Assert.That(
                    manifest.RootElement.GetProperty("qualificationVersion").GetString(),
                    Is.EqualTo("client-v1"));

                var automatic = new HashSet<string>(StringComparer.Ordinal);
                var all = new HashSet<string>(StringComparer.Ordinal);
                foreach (var scenario in manifest.RootElement.GetProperty("scenarios").EnumerateArray())
                {
                    AssertClosed(
                        scenario,
                        "id",
                        "group",
                        "mandatory",
                        "execution",
                        "preconditions",
                        "budgetMs",
                        "expectedOutcome",
                        "buildProfiles",
                        "evidenceOwner");
                    var id = scenario.GetProperty("id").GetString();
                    Assert.That(id, Does.Match("^[a-z0-9]+(?:-[a-z0-9]+)*$"));
                    Assert.That(all.Add(id), Is.True, $"重复scenario ID：{id}");
                    Assert.That(scenario.GetProperty("mandatory").GetBoolean(), Is.True);
                    Assert.That(scenario.GetProperty("expectedOutcome").GetString(), Is.EqualTo("pass"));
                    if (scenario.GetProperty("execution").GetString() != "player-manual")
                    {
                        automatic.Add(id);
                    }
                }

                AssertClosed(registry.RootElement, "schemaVersion", "entries");
                var registered = new HashSet<string>(StringComparer.Ordinal);
                foreach (var entry in registry.RootElement.GetProperty("entries").EnumerateArray())
                {
                    AssertClosed(entry, "scenarioId", "runner", "selectors");
                    Assert.That(
                        registered.Add(entry.GetProperty("scenarioId").GetString()),
                        Is.True);
                    var selectors = entry.GetProperty("selectors")
                        .EnumerateArray()
                        .Select(value => value.GetString())
                        .ToArray();
                    Assert.That(selectors, Is.Not.Empty);
                    Assert.That(selectors, Has.All.Not.Empty);
                    Assert.That(
                        selectors.Distinct(StringComparer.Ordinal).Count(),
                        Is.EqualTo(selectors.Length));
                }

                Assert.That(registered.SetEquals(automatic), Is.True);
            }
        }

        /// <summary>开发诊断registry必须闭合且只复用现有Unity automatic scenario ownership。</summary>
        [Test]
        public void DiagnosticRegistryIsClosedAndReferencesUnityOwners()
        {
            using (var automatic = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("automatic-registry.json"))))
            using (var diagnostic = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("diagnostic-registry.json"))))
            {
                var unityOwners = new HashSet<string>(
                    automatic.RootElement
                        .GetProperty("entries")
                        .EnumerateArray()
                        .Where(entry =>
                            entry.GetProperty("runner").GetString() == "unity-editmode" ||
                            entry.GetProperty("runner").GetString() == "unity-playmode")
                        .Select(entry => entry.GetProperty("scenarioId").GetString()),
                    StringComparer.Ordinal);
                AssertClosed(diagnostic.RootElement, "schemaVersion", "scenarios");
                Assert.That(diagnostic.RootElement.GetProperty("schemaVersion").GetInt32(), Is.EqualTo(1));
                var ids = new HashSet<string>(StringComparer.Ordinal);
                foreach (var scenario in diagnostic.RootElement.GetProperty("scenarios").EnumerateArray())
                {
                    AssertClosed(scenario, "id", "automaticScenarios", "connectionGuide", "manualSteps");
                    var id = scenario.GetProperty("id").GetString();
                    Assert.That(id, Does.Match("^[a-z0-9]+(?:-[a-z0-9]+)*$"));
                    Assert.That(ids.Add(id), Is.True, $"重复diagnostic scenario ID：{id}");
                    var automaticScenarios = scenario.GetProperty("automaticScenarios")
                        .EnumerateArray()
                        .Select(value => value.GetString())
                        .ToArray();
                    Assert.That(automaticScenarios, Is.Not.Empty);
                    Assert.That(automaticScenarios, Has.All.Matches<string>(unityOwners.Contains));
                    Assert.That(
                        automaticScenarios.Distinct(StringComparer.Ordinal).Count(),
                        Is.EqualTo(automaticScenarios.Length));
                    var connectionGuide = scenario.GetProperty("connectionGuide");
                    if (connectionGuide.ValueKind != JsonValueKind.Null)
                    {
                        AssertClosed(connectionGuide, "role", "channel", "remotePort", "protectedRemotePort");
                        Assert.That(
                            new[] { "owner", "visitor" },
                            Does.Contain(connectionGuide.GetProperty("role").GetString()));
                        var channel = connectionGuide.GetProperty("channel").GetString();
                        var remotePort = connectionGuide.GetProperty("remotePort").GetInt32();
                        var protectedRemotePort = connectionGuide.GetProperty("protectedRemotePort").GetInt32();
                        Assert.That(new[] { "control", "gameplay" }, Does.Contain(channel));
                        Assert.That(remotePort, Is.EqualTo(channel == "control" ? 8080 : 8444));
                        Assert.That(protectedRemotePort, Is.EqualTo(channel == "control" ? 8444 : 8080));
                    }
                    var manualSteps = scenario.GetProperty("manualSteps").EnumerateArray().ToArray();
                    Assert.That(manualSteps, Is.Not.Empty);
                    var stepIds = new HashSet<string>(StringComparer.Ordinal);
                    foreach (var manualStep in manualSteps)
                    {
                        AssertClosed(manualStep, "id", "instruction");
                        var stepId = manualStep.GetProperty("id").GetString();
                        var instruction = manualStep.GetProperty("instruction").GetString();
                        Assert.That(stepId, Does.Match("^[a-z0-9]+(?:-[a-z0-9]+)*$"));
                        Assert.That(stepIds.Add(stepId), Is.True, $"重复diagnostic manual step：{stepId}");
                        Assert.That(instruction, Is.Not.Empty.And.Not.Contains("\r").And.Not.Contains("\n"));
                        Assert.That(instruction.Length, Is.LessThanOrEqualTo(160));
                    }
                }
            }
        }

        /// <summary>evidence示例必须闭合且只保留digest与空记录，不保存运行identity。</summary>
        [Test]
        public void EvidenceExampleUsesClosedLowSensitivityShape()
        {
            using (var evidence = JsonDocument.Parse(
                       File.ReadAllText(ContractPath("evidence.example.json"))))
            {
                AssertClosed(
                    evidence.RootElement,
                    "schemaVersion",
                    "qualificationVersion",
                    "contractDigest",
                    "buildDigests",
                    "records");
                AssertClosed(
                    evidence.RootElement.GetProperty("buildDigests"),
                    "development",
                    "release");
                Assert.That(
                    evidence.RootElement.GetProperty("contractDigest").GetString(),
                    Does.Match("^[a-f0-9]{64}$"));
                Assert.That(
                    evidence.RootElement.GetProperty("records").GetArrayLength(),
                    Is.Zero);
            }
        }

        /// <summary>闭合校验器必须拒绝未知字段，防止runner静默接受schema漂移。</summary>
        [Test]
        public void ClosedShapeRejectsUnknownField()
        {
            using (var document = JsonDocument.Parse("{\"schemaVersion\":1,\"hidden\":true}"))
            {
                Assert.Throws<InvalidDataException>(() =>
                    AssertClosed(document.RootElement, "schemaVersion"));
            }
        }

        /// <summary>取得仓库内资格fixture绝对路径；该路径只用于测试读取，不进入报告。</summary>
        private static string ContractPath(string fileName)
        {
            var directory = new DirectoryInfo(Environment.CurrentDirectory);
            while (directory != null)
            {
                var contractDirectory = Path.Combine(
                    directory.FullName,
                    "shared",
                    "contracts",
                    "fixtures",
                    "client-qualification");
                if (File.Exists(Path.Combine(contractDirectory, "manifest.json")))
                {
                    return Path.Combine(contractDirectory, fileName);
                }

                directory = directory.Parent;
            }

            throw new DirectoryNotFoundException("无法定位 client-v1 资格契约目录。");
        }

        /// <summary>验证JSON object仅包含调用方登记的字段。</summary>
        private static void AssertClosed(JsonElement value, params string[] allowed)
        {
            if (value.ValueKind != JsonValueKind.Object)
            {
                throw new InvalidDataException("资格契约节点必须是object。");
            }

            var names = new HashSet<string>(allowed, StringComparer.Ordinal);
            var unknown = value.EnumerateObject()
                .Select(property => property.Name)
                .FirstOrDefault(name => !names.Contains(name));
            if (unknown != null)
            {
                throw new InvalidDataException("资格契约包含未知字段。");
            }
        }

        /// <summary>验证资格schema根对象本身声明closed object和精确required集合。</summary>
        /// <param name="schema">待验证的JSON Schema根对象。</param>
        /// <param name="required">必须精确出现的业务字段。</param>
        private static void AssertClosedSchemaRoot(JsonElement schema, params string[] required)
        {
            Assert.That(schema.GetProperty("type").GetString(), Is.EqualTo("object"));
            Assert.That(schema.GetProperty("additionalProperties").GetBoolean(), Is.False);
            AssertRequired(schema, required);
        }

        /// <summary>验证schema的required字段与调用方登记集合双向一致。</summary>
        /// <param name="schema">包含required数组的JSON Schema节点。</param>
        /// <param name="required">期望的完整必填字段集合。</param>
        private static void AssertRequired(JsonElement schema, params string[] required)
        {
            var actual = new HashSet<string>(
                schema.GetProperty("required")
                    .EnumerateArray()
                    .Select(value => value.GetString()),
                StringComparer.Ordinal);
            Assert.That(actual.SetEquals(required), Is.True);
        }
    }
}
