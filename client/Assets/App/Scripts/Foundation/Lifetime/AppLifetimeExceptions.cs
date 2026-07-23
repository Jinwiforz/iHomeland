using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;

namespace IHomeland.Client.Foundation.Lifetime
{
    /// <summary>
    /// 表示 App Scope 初始化失败及其逆序回滚结果。
    /// </summary>
    internal sealed class AppStartupException : Exception
    {
        /// <summary>
        /// 保存触发回滚的原始启动错误。
        /// </summary>
        private readonly Exception _startupError;

        /// <summary>
        /// 保存逆序回滚期间收集的只读错误快照。
        /// </summary>
        private readonly ReadOnlyCollection<Exception> _rollbackErrors;

        /// <summary>
        /// 创建包含启动错误和回滚错误的失败结果。
        /// </summary>
        /// <param name="startupError">触发失败的原始初始化异常，不能为空。</param>
        /// <param name="rollbackErrors">回滚期间按发生顺序收集的错误快照，不能为空。</param>
        /// <exception cref="ArgumentNullException">任一参数为空时抛出。</exception>
        internal AppStartupException(Exception startupError, IReadOnlyList<Exception> rollbackErrors)
            : base(BuildMessage(startupError, rollbackErrors), startupError)
        {
            _startupError = startupError;
            _rollbackErrors = new ReadOnlyCollection<Exception>(CopyErrors(rollbackErrors));
        }

        /// <summary>
        /// 获取触发回滚的原始启动错误。
        /// </summary>
        internal Exception StartupError => _startupError;

        /// <summary>
        /// 获取回滚阶段的错误快照；空集合表示全部已初始化参与者均成功清理。
        /// </summary>
        internal IReadOnlyList<Exception> RollbackErrors => _rollbackErrors;

        /// <summary>
        /// 构造稳定且不包含敏感运行时状态的异常摘要。
        /// </summary>
        /// <param name="startupError">原始启动错误。</param>
        /// <param name="rollbackErrors">回滚错误集合。</param>
        /// <returns>包含错误类型和回滚错误数量的摘要。</returns>
        /// <exception cref="ArgumentNullException">任一参数为空时抛出。</exception>
        private static string BuildMessage(Exception startupError, IReadOnlyList<Exception> rollbackErrors)
        {
            if (startupError == null)
            {
                throw new ArgumentNullException(nameof(startupError));
            }

            if (rollbackErrors == null)
            {
                throw new ArgumentNullException(nameof(rollbackErrors));
            }

            return $"App Scope 初始化失败，已执行逆序回滚；启动错误={startupError.GetType().Name}，回滚错误数={rollbackErrors.Count}。";
        }

        /// <summary>
        /// 复制错误集合，避免调用方在异常创建后改变诊断结果。
        /// </summary>
        /// <param name="errors">需要复制的非空错误集合。</param>
        /// <returns>保持原顺序的新数组。</returns>
        private static Exception[] CopyErrors(IReadOnlyList<Exception> errors)
        {
            var copy = new Exception[errors.Count];
            for (var index = 0; index < errors.Count; index++)
            {
                copy[index] = errors[index];
            }

            return copy;
        }
    }

    /// <summary>
    /// 表示 App Scope 已完成尽力停止，但一个或多个清理动作失败或超时。
    /// </summary>
    internal sealed class AppShutdownException : Exception
    {
        /// <summary>
        /// 保存逆序停止期间收集的只读错误快照。
        /// </summary>
        private readonly ReadOnlyCollection<Exception> _errors;

        /// <summary>
        /// 创建包含全部停止错误的结果。
        /// </summary>
        /// <param name="errors">逆序停止期间按发生顺序收集的非空错误集合。</param>
        /// <exception cref="ArgumentException">错误集合为空时抛出。</exception>
        /// <exception cref="ArgumentNullException">错误集合为空引用时抛出。</exception>
        internal AppShutdownException(IReadOnlyList<Exception> errors)
            : base(BuildMessage(errors))
        {
            var copy = new Exception[errors.Count];
            for (var index = 0; index < errors.Count; index++)
            {
                copy[index] = errors[index];
            }

            _errors = new ReadOnlyCollection<Exception>(copy);
        }

        /// <summary>
        /// 获取逆序停止期间按发生顺序收集的错误快照。
        /// </summary>
        internal IReadOnlyList<Exception> Errors => _errors;

        /// <summary>
        /// 构造不泄露参与者内部状态的停止错误摘要。
        /// </summary>
        /// <param name="errors">停止错误集合。</param>
        /// <returns>包含错误数量的摘要。</returns>
        /// <exception cref="ArgumentException">错误集合为空时抛出。</exception>
        /// <exception cref="ArgumentNullException">错误集合为空引用时抛出。</exception>
        private static string BuildMessage(IReadOnlyList<Exception> errors)
        {
            if (errors == null)
            {
                throw new ArgumentNullException(nameof(errors));
            }

            if (errors.Count == 0)
            {
                throw new ArgumentException("停止错误集合不能为空。", nameof(errors));
            }

            return $"App Scope 已完成尽力停止，但收集到 {errors.Count} 个清理错误。";
        }
    }
}
