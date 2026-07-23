using UnityEngine;

namespace IHomeland.Client.Presentation.Navigation
{
    /// <summary>
    /// 为 Unity Inspector 提供可序列化的产品页面 binding 类型边界。
    /// </summary>
    /// <remarks>
    /// Unity 不能直接序列化 interface 字段，因此 Host 使用该抽象组件过滤对象选择器；
    /// Host 在缓存时还会验证派生类型实现 <see cref="IClientUiProductBinding"/> 的完整生命周期契约。
    /// </remarks>
    public abstract class ClientUiProductBindingBehaviour : MonoBehaviour
    {
    }
}
