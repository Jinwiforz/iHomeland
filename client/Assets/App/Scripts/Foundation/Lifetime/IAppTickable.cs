namespace IHomeland.Client.Foundation.Lifetime
{
    /// <summary>
    /// 定义由 AppComposition 显式登记并由唯一 AppRoot 逐帧驱动的工作。
    /// </summary>
    internal interface IAppTickable
    {
        /// <summary>
        /// 在 Unity 主线程执行一次逐帧更新。
        /// </summary>
        /// <param name="unscaledDeltaTimeSeconds">不受 Time.timeScale 影响的帧间隔，单位为秒。</param>
        void Tick(float unscaledDeltaTimeSeconds);
    }
}
