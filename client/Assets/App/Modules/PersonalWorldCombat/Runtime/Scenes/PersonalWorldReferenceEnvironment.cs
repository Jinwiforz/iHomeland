using System;
using System.Collections.Generic;
using UnityEngine;
using UnityEngine.Rendering;

using UnityEngine.Scripting.APIUpdating;

namespace IHomeland.Client.PersonalWorldCombat.Runtime.Scenes
{
    /// <summary>
    /// 为current PersonalWorld生成不参与Gameplay或Physics的稳定世界空间参照。
    /// </summary>
    /// <remarks>
    /// 该Host只创建三个合并Renderer：地面、米制网格与固定地标。它不创建Collider、不读取
    /// authority，也不把Scene Transform写回App Scope；正式地图资产接入后可整体替换。
    /// </remarks>
    [DisallowMultipleComponent]
    [MovedFrom(true, "IHomeland.Client.Scenes.PersonalWorld", "IHomeland.Client.Runtime", null)]
    public sealed class PersonalWorldReferenceEnvironment : MonoBehaviour
    {
        /// <summary>保存正方形参照地面的半边长，单位米。</summary>
        private const int GroundHalfExtentMeters = 30;

        /// <summary>保存普通网格线宽，单位米。</summary>
        private const float GridLineWidthMeters = 0.025f;

        /// <summary>保存主轴线宽，单位米。</summary>
        private const float AxisLineWidthMeters = 0.08f;

        /// <summary>保存网格相对ground的微小抬升，避免共面闪烁。</summary>
        private const float GridElevationMeters = 0.01f;

        /// <summary>保存由Scene直接引用并保证Player shader inclusion的Material模板。</summary>
        [SerializeField]
        private Material _materialTemplate;

        /// <summary>保存运行时创建且必须随Scene销毁的Mesh与Material。</summary>
        private readonly List<UnityEngine.Object> _ownedAssets =
            new List<UnityEngine.Object>();

        /// <summary>表示current Scene generation是否已经构建参照环境。</summary>
        private bool _built;

        /// <summary>在Scene激活时一次性构建合并的纯表现几何。</summary>
        private void Awake()
        {
            Build();
        }

        /// <summary>释放运行时Mesh与Material，避免反复进入Scene积累native对象。</summary>
        private void OnDestroy()
        {
            foreach (var asset in _ownedAssets)
            {
                if (asset != null)
                {
                    Destroy(asset);
                }
            }

            _ownedAssets.Clear();
            _built = false;
        }

        /// <summary>创建ground、grid与landmark三个有界Renderer。</summary>
        private void Build()
        {
            if (_built)
            {
                return;
            }

            if (_materialTemplate == null)
            {
                throw new InvalidOperationException(
                    "PersonalWorld reference environment requires a material template.");
            }

            var groundMaterial = CreateMaterial(
                _materialTemplate,
                "PersonalWorldReference-Ground",
                new Color(0.13f, 0.16f, 0.17f, 1f));
            var gridMaterial = CreateMaterial(
                _materialTemplate,
                "PersonalWorldReference-Grid",
                new Color(0.32f, 0.39f, 0.41f, 1f));
            var landmarkMaterial = CreateMaterial(
                _materialTemplate,
                "PersonalWorldReference-Landmarks",
                new Color(0.95f, 0.58f, 0.2f, 1f));
            CreateRenderer(
                "ReferenceGround",
                CreateGroundMesh(),
                groundMaterial);
            CreateRenderer(
                "ReferenceGrid",
                CreateGridMesh(),
                gridMaterial);
            CreateRenderer(
                "ReferenceLandmarks",
                CreateLandmarkMesh(),
                landmarkMaterial);
            _built = true;
        }

        /// <summary>创建由当前Scene独占的runtime Material。</summary>
        /// <param name="template">Player内始终可用的Unity内置默认Material。</param>
        /// <param name="name">低敏且稳定的诊断名称。</param>
        /// <param name="color">不依赖灯光的表现颜色。</param>
        /// <returns>随该Host销毁的Material。</returns>
        private Material CreateMaterial(
            Material template,
            string name,
            Color color)
        {
            var material = new Material(template)
            {
                name = name,
                hideFlags = HideFlags.DontSave,
            };
            if (material.HasProperty("_BaseColor"))
            {
                material.SetColor("_BaseColor", color);
            }
            else
            {
                material.color = color;
            }

            _ownedAssets.Add(material);
            return material;
        }

        /// <summary>创建一个不含Collider的Scene child并绑定合并Mesh。</summary>
        /// <param name="name">稳定的Scene object名称。</param>
        /// <param name="mesh">只读runtime Mesh。</param>
        /// <param name="material">该层唯一共享Material。</param>
        private void CreateRenderer(
            string name,
            Mesh mesh,
            Material material)
        {
            var child = new GameObject(name);
            child.layer = gameObject.layer;
            child.transform.SetParent(transform, worldPositionStays: false);
            var filter = child.AddComponent<MeshFilter>();
            filter.sharedMesh = mesh;
            var renderer = child.AddComponent<MeshRenderer>();
            renderer.sharedMaterial = material;
            renderer.shadowCastingMode = ShadowCastingMode.Off;
            renderer.receiveShadows = false;
            renderer.lightProbeUsage = LightProbeUsage.Off;
            renderer.reflectionProbeUsage = ReflectionProbeUsage.Off;
            renderer.motionVectorGenerationMode =
                MotionVectorGenerationMode.ForceNoMotion;
        }

        /// <summary>创建单quad地面Mesh。</summary>
        /// <returns>以world Y=0为表面的有界地面。</returns>
        private Mesh CreateGroundMesh()
        {
            var extent = (float)GroundHalfExtentMeters;
            var mesh = CreateMesh(
                "PersonalWorldReference-GroundMesh",
                new List<Vector3>
                {
                    new Vector3(-extent, 0f, -extent),
                    new Vector3(-extent, 0f, extent),
                    new Vector3(extent, 0f, extent),
                    new Vector3(extent, 0f, -extent),
                },
                new List<int> { 0, 1, 2, 0, 2, 3 });
            return mesh;
        }

        /// <summary>创建每米一格且原点主轴加宽的单Mesh。</summary>
        /// <returns>位于ground上方的网格Mesh。</returns>
        private Mesh CreateGridMesh()
        {
            var vertices = new List<Vector3>();
            var triangles = new List<int>();
            for (var coordinate = -GroundHalfExtentMeters;
                 coordinate <= GroundHalfExtentMeters;
                 coordinate++)
            {
                if (coordinate == 0)
                {
                    continue;
                }

                AppendStripAlongX(
                    vertices,
                    triangles,
                    coordinate,
                    GridLineWidthMeters);
                AppendStripAlongZ(
                    vertices,
                    triangles,
                    coordinate,
                    GridLineWidthMeters);
            }

            return CreateMesh(
                "PersonalWorldReference-GridMesh",
                vertices,
                triangles);
        }

        /// <summary>创建两条主轴与 server arena 三个静态障碍的单Mesh。</summary>
        /// <returns>与 versioned arena source 同坐标但不参与 authority 的地标Mesh。</returns>
        private Mesh CreateLandmarkMesh()
        {
            var vertices = new List<Vector3>();
            var triangles = new List<int>();
            AppendStripAlongX(
                vertices,
                triangles,
                0,
                AxisLineWidthMeters);
            AppendStripAlongZ(
                vertices,
                triangles,
                0,
                AxisLineWidthMeters);
            AppendBox(
                vertices,
                triangles,
                new Vector3(0f, 1f, 0f),
                new Vector3(5f, 2f, 2f));
            AppendBox(
                vertices,
                triangles,
                new Vector3(-9f, 1.5f, 6.5f),
                new Vector3(3f, 3f, 3f));
            AppendBox(
                vertices,
                triangles,
                new Vector3(9f, 1.5f, -6.5f),
                new Vector3(3f, 3f, 3f));
            return CreateMesh(
                "PersonalWorldReference-LandmarkMesh",
                vertices,
                triangles);
        }

        /// <summary>沿X方向追加一条水平quad strip。</summary>
        /// <param name="vertices">目标顶点集合。</param>
        /// <param name="triangles">目标triangle index集合。</param>
        /// <param name="z">strip中心Z坐标。</param>
        /// <param name="width">strip宽度，单位米。</param>
        private static void AppendStripAlongX(
            List<Vector3> vertices,
            List<int> triangles,
            float z,
            float width)
        {
            var extent = (float)GroundHalfExtentMeters;
            var halfWidth = width * 0.5f;
            AppendQuad(
                vertices,
                triangles,
                new Vector3(-extent, GridElevationMeters, z - halfWidth),
                new Vector3(-extent, GridElevationMeters, z + halfWidth),
                new Vector3(extent, GridElevationMeters, z + halfWidth),
                new Vector3(extent, GridElevationMeters, z - halfWidth));
        }

        /// <summary>沿Z方向追加一条水平quad strip。</summary>
        /// <param name="vertices">目标顶点集合。</param>
        /// <param name="triangles">目标triangle index集合。</param>
        /// <param name="x">strip中心X坐标。</param>
        /// <param name="width">strip宽度，单位米。</param>
        private static void AppendStripAlongZ(
            List<Vector3> vertices,
            List<int> triangles,
            float x,
            float width)
        {
            var extent = (float)GroundHalfExtentMeters;
            var halfWidth = width * 0.5f;
            AppendQuad(
                vertices,
                triangles,
                new Vector3(x - halfWidth, GridElevationMeters, -extent),
                new Vector3(x - halfWidth, GridElevationMeters, extent),
                new Vector3(x + halfWidth, GridElevationMeters, extent),
                new Vector3(x + halfWidth, GridElevationMeters, -extent));
        }

        /// <summary>追加一个朝上的quad。</summary>
        /// <param name="vertices">目标顶点集合。</param>
        /// <param name="triangles">目标triangle index集合。</param>
        /// <param name="first">左后顶点。</param>
        /// <param name="second">左前顶点。</param>
        /// <param name="third">右前顶点。</param>
        /// <param name="fourth">右后顶点。</param>
        private static void AppendQuad(
            List<Vector3> vertices,
            List<int> triangles,
            Vector3 first,
            Vector3 second,
            Vector3 third,
            Vector3 fourth)
        {
            var start = vertices.Count;
            vertices.Add(first);
            vertices.Add(second);
            vertices.Add(third);
            vertices.Add(fourth);
            triangles.Add(start);
            triangles.Add(start + 1);
            triangles.Add(start + 2);
            triangles.Add(start);
            triangles.Add(start + 2);
            triangles.Add(start + 3);
        }

        /// <summary>向合并Mesh追加一个无共享顶点的axis-aligned box。</summary>
        /// <param name="vertices">目标顶点集合。</param>
        /// <param name="triangles">目标triangle index集合。</param>
        /// <param name="center">box中心。</param>
        /// <param name="size">box三轴尺寸。</param>
        private static void AppendBox(
            List<Vector3> vertices,
            List<int> triangles,
            Vector3 center,
            Vector3 size)
        {
            var half = size * 0.5f;
            var minimum = center - half;
            var maximum = center + half;
            AppendFace(
                vertices,
                triangles,
                new Vector3(minimum.x, minimum.y, minimum.z),
                new Vector3(minimum.x, maximum.y, minimum.z),
                new Vector3(maximum.x, maximum.y, minimum.z),
                new Vector3(maximum.x, minimum.y, minimum.z));
            AppendFace(
                vertices,
                triangles,
                new Vector3(maximum.x, minimum.y, maximum.z),
                new Vector3(maximum.x, maximum.y, maximum.z),
                new Vector3(minimum.x, maximum.y, maximum.z),
                new Vector3(minimum.x, minimum.y, maximum.z));
            AppendFace(
                vertices,
                triangles,
                new Vector3(minimum.x, minimum.y, maximum.z),
                new Vector3(minimum.x, maximum.y, maximum.z),
                new Vector3(minimum.x, maximum.y, minimum.z),
                new Vector3(minimum.x, minimum.y, minimum.z));
            AppendFace(
                vertices,
                triangles,
                new Vector3(maximum.x, minimum.y, minimum.z),
                new Vector3(maximum.x, maximum.y, minimum.z),
                new Vector3(maximum.x, maximum.y, maximum.z),
                new Vector3(maximum.x, minimum.y, maximum.z));
            AppendFace(
                vertices,
                triangles,
                new Vector3(minimum.x, maximum.y, minimum.z),
                new Vector3(minimum.x, maximum.y, maximum.z),
                new Vector3(maximum.x, maximum.y, maximum.z),
                new Vector3(maximum.x, maximum.y, minimum.z));
            AppendFace(
                vertices,
                triangles,
                new Vector3(minimum.x, minimum.y, maximum.z),
                new Vector3(minimum.x, minimum.y, minimum.z),
                new Vector3(maximum.x, minimum.y, minimum.z),
                new Vector3(maximum.x, minimum.y, maximum.z));
        }

        /// <summary>追加一个带独立法线的box face。</summary>
        /// <param name="vertices">目标顶点集合。</param>
        /// <param name="triangles">目标triangle index集合。</param>
        /// <param name="first">face第一个顶点。</param>
        /// <param name="second">face第二个顶点。</param>
        /// <param name="third">face第三个顶点。</param>
        /// <param name="fourth">face第四个顶点。</param>
        private static void AppendFace(
            List<Vector3> vertices,
            List<int> triangles,
            Vector3 first,
            Vector3 second,
            Vector3 third,
            Vector3 fourth)
        {
            AppendQuad(
                vertices,
                triangles,
                first,
                second,
                third,
                fourth);
        }

        /// <summary>由owned集合创建并登记runtime Mesh。</summary>
        /// <param name="name">稳定的低敏diagnostic名称。</param>
        /// <param name="vertices">Mesh顶点。</param>
        /// <param name="triangles">顺时针triangle index。</param>
        /// <returns>随该Host销毁的只读表现Mesh。</returns>
        private Mesh CreateMesh(
            string name,
            List<Vector3> vertices,
            List<int> triangles)
        {
            var mesh = new Mesh
            {
                name = name,
                hideFlags = HideFlags.DontSave,
            };
            mesh.SetVertices(vertices);
            mesh.SetTriangles(triangles, 0, calculateBounds: true);
            mesh.RecalculateNormals();
            mesh.UploadMeshData(markNoLongerReadable: true);
            _ownedAssets.Add(mesh);
            return mesh;
        }
    }
}
