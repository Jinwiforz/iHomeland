using System;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using IHomeland.Client.Session.Application;

namespace IHomeland.Client.Session.Infrastructure.Security
{
    /// <summary>
    /// 使用 owner-specific 文件、跨进程 mutex 与 OS protector 持久化唯一 secure session record。
    /// </summary>
    /// <remarks>
    /// 本类型只删除精确 record/temp 文件，不递归删除目录。所有操作由单一 semaphore 串行化，Stop
    /// 取得同一 owner 后才释放 mutex，保证文件提交与生命周期不会交错。
    /// </remarks>
    internal sealed class ClientSecureSessionFileStore : IClientSecureSessionStore
    {
        /// <summary>外层 protected envelope magic。</summary>
        private static readonly byte[] EnvelopeMagic = { 0x49, 0x48, 0x53, 0x50 };

        /// <summary>当前 protected envelope schema。</summary>
        private const int EnvelopeVersion = 1;

        /// <summary>限制磁盘 ciphertext envelope 大小。</summary>
        internal const int MaximumEnvelopeBytes = 65536;

        /// <summary>Owner-specific record 文件名。</summary>
        private const string RecordFileName = "session.v1.bin";

        /// <summary>仅当前 owner 使用的精确临时文件名。</summary>
        private const string TemporaryFileName = "session.v1.tmp";

        /// <summary>已从current namespace原子退休、等待尽力物理清理的文件名。</summary>
        private const string RetiredFileName = "session.v1.retired";

        /// <summary>串行化 read/replace/delete/stop。</summary>
        private readonly SemaphoreSlim _operations = new SemaphoreSlim(1, 1);

        /// <summary>完整 record binary codec。</summary>
        private readonly ClientSecureSessionRecordCodec _codec;

        /// <summary>Windows DPAPI 或测试 protector。</summary>
        private readonly IClientDataProtector _protector;

        /// <summary>当前环境binding。</summary>
        private readonly string _environmentBinding;

        /// <summary>从environment binding派生且不是secret的DPAPI entropy。</summary>
        private readonly byte[] _entropy;

        /// <summary>受控 profile 目录。</summary>
        private readonly string _profileDirectory;

        /// <summary>精确 record 路径。</summary>
        private readonly string _recordPath;

        /// <summary>精确 temp 路径。</summary>
        private readonly string _temporaryPath;

        /// <summary>精确retired路径，不会被Read视为current lineage。</summary>
        private readonly string _retiredPath;

        /// <summary>跨进程 profile mutex 名称。</summary>
        private readonly string _mutexName;

        /// <summary>是否允许当前平台使用所注入protector。</summary>
        private readonly bool _platformSupported;

        /// <summary>当前进程在专用 owner thread 上持有的 named mutex lease。</summary>
        private ClientNamedMutexLease _mutexLease;

        /// <summary>Store生命周期状态，只在operations owner内访问。</summary>
        private StoreState _state = StoreState.Created;

        /// <summary>创建受控 secure session file owner。</summary>
        /// <param name="persistentDataPath">Unity提供的当前产品持久数据根。</param>
        /// <param name="profile">经过ClientSecureSessionProfile验证的profile。</param>
        /// <param name="environmentBinding">当前环境SHA-256 binding。</param>
        /// <param name="protector">完整payload OS protector。</param>
        /// <param name="platformSupported">当前平台是否允许启用该adapter。</param>
        internal ClientSecureSessionFileStore(
            string persistentDataPath,
            string profile,
            string environmentBinding,
            IClientDataProtector protector,
            bool platformSupported)
        {
            if (string.IsNullOrWhiteSpace(persistentDataPath))
            {
                throw new ArgumentException("Persistent data path 不能为空。", nameof(persistentDataPath));
            }

            if (string.IsNullOrWhiteSpace(profile))
            {
                throw new ArgumentException("Secure session profile 不能为空。", nameof(profile));
            }

            if (!ClientSecureSessionProfile.IsValid(profile))
            {
                throw new ArgumentException("Secure session profile grammar 无效。", nameof(profile));
            }

            _protector = protector ?? throw new ArgumentNullException(nameof(protector));
            _environmentBinding = environmentBinding ??
                throw new ArgumentNullException(nameof(environmentBinding));
            if (_environmentBinding.Length != 64)
            {
                throw new ArgumentException("Environment binding 长度无效。", nameof(environmentBinding));
            }

            _codec = new ClientSecureSessionRecordCodec();
            _platformSupported = platformSupported;
            _entropy = Encoding.UTF8.GetBytes("ihomeland-dpapi-v1\n" + _environmentBinding);

            var root = Path.GetFullPath(persistentDataPath);
            if (!Path.IsPathRooted(root))
            {
                throw new ArgumentException("Persistent data path 必须是绝对路径。", nameof(persistentDataPath));
            }

            _profileDirectory = Path.GetFullPath(Path.Combine(root, "secure-session", profile));
            var rootPrefix = root.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar) +
                             Path.DirectorySeparatorChar;
            if (!_profileDirectory.StartsWith(rootPrefix, StringComparison.OrdinalIgnoreCase))
            {
                throw new ArgumentException("Secure session profile 超出持久数据根。", nameof(profile));
            }

            _recordPath = Path.Combine(_profileDirectory, RecordFileName);
            _temporaryPath = Path.Combine(_profileDirectory, TemporaryFileName);
            _retiredPath = Path.Combine(_profileDirectory, RetiredFileName);
            _mutexName = "Local\\iHomeland.SecureSession." + ComputeMutexDigest(_profileDirectory);
        }

        /// <inheritdoc />
        public async Task InitializeAsync(CancellationToken cancellationToken)
        {
            await _operations.WaitAsync(cancellationToken);
            try
            {
                if (_state != StoreState.Created)
                {
                    throw new InvalidOperationException("Secure session store 不能重复初始化。");
                }

                if (!_platformSupported)
                {
                    _state = StoreState.Unsupported;
                    return;
                }

                _mutexLease = ClientNamedMutexLease.TryAcquire(_mutexName);
                if (_mutexLease == null)
                {
                    _state = StoreState.ProfileInUse;
                    return;
                }

                Directory.CreateDirectory(_profileDirectory);
                var profileInfo = new DirectoryInfo(_profileDirectory);
                if ((profileInfo.Attributes & FileAttributes.ReparsePoint) != 0)
                {
                    throw new IOException("Secure session profile 不得是reparse point。");
                }

                ClientWindowsFileAccessPolicy.ApplyToDirectory(_profileDirectory);
                DeleteExactFileIfPresent(_temporaryPath);
                DeleteExactFileIfPresent(_retiredPath);
                _state = StoreState.Ready;
            }
            catch (UnauthorizedAccessException)
            {
                ReleaseMutexLease();
                _state = StoreState.StorageFailure;
            }
            catch (IOException)
            {
                ReleaseMutexLease();
                _state = StoreState.StorageFailure;
            }
            catch (System.ComponentModel.Win32Exception)
            {
                ReleaseMutexLease();
                _state = StoreState.StorageFailure;
            }
            catch
            {
                ReleaseMutexLease();
                _state = StoreState.Faulted;
                throw;
            }
            finally
            {
                _operations.Release();
            }
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionRecord>> ReadAsync(
            CancellationToken cancellationToken)
        {
            return RunSerializedAsync(ReadCore, cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> ReplaceAsync(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            if (record == null)
            {
                throw new ArgumentNullException(nameof(record));
            }

            return RunSerializedAsync(() => ReplaceCore(record, cancellationToken), cancellationToken);
        }

        /// <inheritdoc />
        public Task<ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>> DeleteAsync(
            CancellationToken cancellationToken)
        {
            return RunSerializedAsync(DeleteCore, cancellationToken);
        }

        /// <inheritdoc />
        public async Task StopAsync(CancellationToken cancellationToken)
        {
            await _operations.WaitAsync(cancellationToken);
            try
            {
                if (_state == StoreState.Stopped)
                {
                    return;
                }

                _state = StoreState.Stopped;
                ReleaseMutexLease();
                Array.Clear(_entropy, 0, _entropy.Length);
            }
            finally
            {
                _operations.Release();
            }
        }

        /// <summary>串行执行同步文件operation且统一映射生命周期结果。</summary>
        /// <typeparam name="T">Operation成功值类型。</typeparam>
        /// <param name="operation">operations semaphore内执行的同步函数。</param>
        /// <param name="cancellationToken">等待owner与提交前取消信号。</param>
        /// <returns>封闭store结果。</returns>
        private async Task<ClientSecureSessionStoreResult<T>> RunSerializedAsync<T>(
            Func<ClientSecureSessionStoreResult<T>> operation,
            CancellationToken cancellationToken)
            where T : class
        {
            try
            {
                await _operations.WaitAsync(cancellationToken);
            }
            catch (OperationCanceledException)
            {
                return ClientSecureSessionStoreResult<T>.Failed(
                    ClientSecureSessionStoreOutcome.Cancelled);
            }

            try
            {
                var unavailable = StateOutcome<T>();
                if (unavailable != null)
                {
                    return unavailable;
                }

                if (cancellationToken.IsCancellationRequested)
                {
                    return ClientSecureSessionStoreResult<T>.Failed(
                        ClientSecureSessionStoreOutcome.Cancelled);
                }

                return await Task.Run(operation);
            }
            catch (OperationCanceledException)
            {
                return ClientSecureSessionStoreResult<T>.Failed(
                    ClientSecureSessionStoreOutcome.Cancelled);
            }
            catch (UnauthorizedAccessException)
            {
                return ClientSecureSessionStoreResult<T>.Failed(
                    ClientSecureSessionStoreOutcome.StorageFailure);
            }
            catch (IOException)
            {
                return ClientSecureSessionStoreResult<T>.Failed(
                    ClientSecureSessionStoreOutcome.StorageFailure);
            }
            finally
            {
                _operations.Release();
            }
        }

        /// <summary>把非Ready生命周期映射为store outcome。</summary>
        /// <typeparam name="T">调用方成功类型。</typeparam>
        /// <returns>非Ready时返回失败；Ready时返回null。</returns>
        private ClientSecureSessionStoreResult<T> StateOutcome<T>()
            where T : class
        {
            switch (_state)
            {
                case StoreState.Ready:
                    return null;
                case StoreState.Unsupported:
                    return ClientSecureSessionStoreResult<T>.Failed(
                        ClientSecureSessionStoreOutcome.Unsupported);
                case StoreState.ProfileInUse:
                    return ClientSecureSessionStoreResult<T>.Failed(
                        ClientSecureSessionStoreOutcome.ProfileInUse);
                case StoreState.StorageFailure:
                    return ClientSecureSessionStoreResult<T>.Failed(
                        ClientSecureSessionStoreOutcome.StorageFailure);
                case StoreState.Stopped:
                case StoreState.Faulted:
                case StoreState.Created:
                default:
                    return ClientSecureSessionStoreResult<T>.Failed(
                        ClientSecureSessionStoreOutcome.Stopped);
            }
        }

        /// <summary>读取、解保护并严格验证record。</summary>
        /// <returns>有效record、NotFound或稳定失败。</returns>
        private ClientSecureSessionStoreResult<ClientSecureSessionRecord> ReadCore()
        {
            if (!File.Exists(_recordPath))
            {
                return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                    ClientSecureSessionStoreOutcome.NotFound);
            }

            var fileInfo = new FileInfo(_recordPath);
            if (fileInfo.Length <= 0 || fileInfo.Length > MaximumEnvelopeBytes)
            {
                DeleteExactFileIfPresent(_recordPath);
                return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                    ClientSecureSessionStoreOutcome.InvalidRecord);
            }

            var envelope = ReadExactFile(_recordPath, (int)fileInfo.Length);
            byte[] ciphertext = null;
            byte[] plaintext = null;
            try
            {
                if (!TryDecodeEnvelope(envelope, out ciphertext))
                {
                    DeleteExactFileIfPresent(_recordPath);
                    return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                        ClientSecureSessionStoreOutcome.InvalidRecord);
                }

                var unprotected = _protector.Unprotect(ciphertext, _entropy);
                if (!unprotected.Succeeded)
                {
                    DeleteExactFileIfPresent(_recordPath);
                    return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                        ClientSecureSessionStoreOutcome.ProtectionFailure);
                }

                plaintext = unprotected.Data;
                if (!_codec.TryDecode(plaintext, out var record) ||
                    !string.Equals(
                        record.EnvironmentBinding,
                        _environmentBinding,
                        StringComparison.Ordinal))
                {
                    DeleteExactFileIfPresent(_recordPath);
                    return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Failed(
                        ClientSecureSessionStoreOutcome.InvalidRecord);
                }

                return ClientSecureSessionStoreResult<ClientSecureSessionRecord>.Success(record);
            }
            finally
            {
                Clear(envelope);
                Clear(ciphertext);
                Clear(plaintext);
            }
        }

        /// <summary>保护并原子替换完整record。</summary>
        /// <param name="record">已通过application contract的record。</param>
        /// <param name="cancellationToken">原子提交前取消信号。</param>
        /// <returns>成功或稳定失败。</returns>
        private ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty> ReplaceCore(
            ClientSecureSessionRecord record,
            CancellationToken cancellationToken)
        {
            if (!string.Equals(
                    record.EnvironmentBinding,
                    _environmentBinding,
                    StringComparison.Ordinal))
            {
                return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                    ClientSecureSessionStoreOutcome.InvalidRecord);
            }

            byte[] plaintext = null;
            byte[] ciphertext = null;
            byte[] envelope = null;
            try
            {
                plaintext = _codec.Encode(record);
                var protectedResult = _protector.Protect(plaintext, _entropy);
                if (!protectedResult.Succeeded)
                {
                    return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                        ClientSecureSessionStoreOutcome.ProtectionFailure);
                }

                ciphertext = protectedResult.Data;
                if (ciphertext == null || ciphertext.Length <= 0 ||
                    ciphertext.Length > MaximumEnvelopeBytes - 12)
                {
                    return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                        ClientSecureSessionStoreOutcome.ProtectionFailure);
                }

                envelope = EncodeEnvelope(ciphertext);
                WriteTemporary(envelope);
                if (cancellationToken.IsCancellationRequested)
                {
                    DeleteExactFileIfPresent(_temporaryPath);
                    return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                        ClientSecureSessionStoreOutcome.Cancelled);
                }

                CommitTemporary();
                return ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Success(
                    ClientSecureSessionStoreEmpty.Value);
            }
            finally
            {
                Clear(plaintext);
                Clear(ciphertext);
                Clear(envelope);
            }
        }

        /// <summary>删除精确record与遗留temp。</summary>
        /// <returns>成功或NotFound。</returns>
        private ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty> DeleteCore()
        {
            var existed = File.Exists(_recordPath) ||
                          File.Exists(_temporaryPath) ||
                          File.Exists(_retiredPath);
            DeleteExactFileIfPresent(_retiredPath);
            if (File.Exists(_recordPath))
            {
                File.Move(_recordPath, _retiredPath);
            }

            DeleteExactFileIfPresent(_temporaryPath);
            try
            {
                DeleteExactFileIfPresent(_retiredPath);
            }
            catch (UnauthorizedAccessException)
            {
                // Record已离开current namespace；下次初始化继续精确清理retired ciphertext。
            }
            catch (IOException)
            {
                // Record已离开current namespace；下次初始化继续精确清理retired ciphertext。
            }

            return existed
                ? ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Success(
                    ClientSecureSessionStoreEmpty.Value)
                : ClientSecureSessionStoreResult<ClientSecureSessionStoreEmpty>.Failed(
                    ClientSecureSessionStoreOutcome.NotFound);
        }

        /// <summary>把ciphertext包装为有界外层envelope。</summary>
        /// <param name="ciphertext">DPAPI ciphertext。</param>
        /// <returns>由调用方负责清理的envelope bytes。</returns>
        private static byte[] EncodeEnvelope(byte[] ciphertext)
        {
            if (ciphertext == null || ciphertext.Length <= 0 ||
                ciphertext.Length > MaximumEnvelopeBytes - 12)
            {
                throw new InvalidDataException("Secure session ciphertext size 无效。");
            }

            using (var stream = new MemoryStream())
            using (var writer = new BinaryWriter(stream, Encoding.UTF8, leaveOpen: true))
            {
                writer.Write(EnvelopeMagic);
                writer.Write(EnvelopeVersion);
                writer.Write(ciphertext.Length);
                writer.Write(ciphertext);
                writer.Flush();
                return stream.ToArray();
            }
        }

        /// <summary>严格解析外层envelope。</summary>
        /// <param name="envelope">完整磁盘bytes。</param>
        /// <param name="ciphertext">成功时返回新分配ciphertext。</param>
        /// <returns>Magic、version、length和EOF完全匹配时返回true。</returns>
        private static bool TryDecodeEnvelope(byte[] envelope, out byte[] ciphertext)
        {
            ciphertext = null;
            try
            {
                using (var stream = new MemoryStream(envelope, writable: false))
                using (var reader = new BinaryReader(stream, Encoding.UTF8, leaveOpen: true))
                {
                    var magic = reader.ReadBytes(EnvelopeMagic.Length);
                    if (magic.Length != EnvelopeMagic.Length)
                    {
                        return false;
                    }

                    for (var index = 0; index < EnvelopeMagic.Length; index++)
                    {
                        if (magic[index] != EnvelopeMagic[index])
                        {
                            return false;
                        }
                    }

                    if (reader.ReadInt32() != EnvelopeVersion)
                    {
                        return false;
                    }

                    var length = reader.ReadInt32();
                    if (length <= 0 || length > MaximumEnvelopeBytes - 12)
                    {
                        return false;
                    }

                    ciphertext = reader.ReadBytes(length);
                    return ciphertext.Length == length && stream.Position == stream.Length;
                }
            }
            catch (EndOfStreamException)
            {
                Clear(ciphertext);
                ciphertext = null;
                return false;
            }
            catch (IOException)
            {
                Clear(ciphertext);
                ciphertext = null;
                return false;
            }
        }

        /// <summary>使用FileShare.None精确读取预期字节数。</summary>
        /// <param name="path">精确record路径。</param>
        /// <param name="length">FileInfo验证后的长度。</param>
        /// <returns>完整文件bytes。</returns>
        private static byte[] ReadExactFile(string path, int length)
        {
            var output = new byte[length];
            using (var stream = new FileStream(
                       path,
                       FileMode.Open,
                       FileAccess.Read,
                       FileShare.None,
                       bufferSize: 4096,
                       FileOptions.SequentialScan))
            {
                var offset = 0;
                while (offset < output.Length)
                {
                    var read = stream.Read(output, offset, output.Length - offset);
                    if (read <= 0)
                    {
                        throw new EndOfStreamException();
                    }

                    offset += read;
                }

                if (stream.ReadByte() != -1)
                {
                    throw new InvalidDataException("Secure session file length 发生漂移。");
                }
            }

            return output;
        }

        /// <summary>写入同目录temp并强制flush。</summary>
        /// <param name="envelope">完整protected envelope。</param>
        private void WriteTemporary(byte[] envelope)
        {
            DeleteExactFileIfPresent(_temporaryPath);
            using (var stream = new FileStream(
                       _temporaryPath,
                       FileMode.CreateNew,
                       FileAccess.Write,
                       FileShare.None,
                       bufferSize: 4096,
                       FileOptions.WriteThrough))
            {
                stream.Write(envelope, 0, envelope.Length);
                stream.Flush(flushToDisk: true);
            }
        }

        /// <summary>在同目录原子提交temp，保留旧record直到replacement成功。</summary>
        private void CommitTemporary()
        {
            if (File.Exists(_recordPath))
            {
                File.Replace(_temporaryPath, _recordPath, destinationBackupFileName: null);
            }
            else
            {
                File.Move(_temporaryPath, _recordPath);
            }

            File.SetAttributes(_recordPath, FileAttributes.Hidden);
        }

        /// <summary>删除owner已知的单一文件，不递归或使用glob。</summary>
        /// <param name="path">构造时固定的record或temp路径。</param>
        private static void DeleteExactFileIfPresent(string path)
        {
            if (!File.Exists(path))
            {
                return;
            }

            File.SetAttributes(path, FileAttributes.Normal);
            File.Delete(path);
        }

        /// <summary>释放当前进程持有的profile mutex。</summary>
        private void ReleaseMutexLease()
        {
            if (_mutexLease == null)
            {
                return;
            }

            _mutexLease.Dispose();
            _mutexLease = null;
        }

        /// <summary>从规范绝对profile路径派生低敏mutex名称。</summary>
        /// <param name="profileDirectory">已验证位于persistent root下的路径。</param>
        /// <returns>32字符小写SHA-256前缀。</returns>
        private static string ComputeMutexDigest(string profileDirectory)
        {
            var bytes = Encoding.UTF8.GetBytes(profileDirectory.ToUpperInvariant());
            try
            {
                using (var sha256 = SHA256.Create())
                {
                    var digest = sha256.ComputeHash(bytes);
                    try
                    {
                        var builder = new StringBuilder(32);
                        for (var index = 0; index < 16; index++)
                        {
                            builder.Append(digest[index].ToString("x2"));
                        }

                        return builder.ToString();
                    }
                    finally
                    {
                        Clear(digest);
                    }
                }
            }
            finally
            {
                Clear(bytes);
            }
        }

        /// <summary>尽力归零managed byte array。</summary>
        /// <param name="buffer">可为空的待清理buffer。</param>
        private static void Clear(byte[] buffer)
        {
            if (buffer != null)
            {
                Array.Clear(buffer, 0, buffer.Length);
            }
        }

        /// <summary>限制file owner生命周期。</summary>
        private enum StoreState
        {
            /// <summary>尚未初始化。</summary>
            Created = 0,

            /// <summary>当前进程持有profile且可操作。</summary>
            Ready = 1,

            /// <summary>平台不受支持。</summary>
            Unsupported = 2,

            /// <summary>另一进程持有profile。</summary>
            ProfileInUse = 3,

            /// <summary>初始化发生不可恢复故障。</summary>
            Faulted = 4,

            /// <summary>初始化无法建立owner、目录或受控ACL。</summary>
            StorageFailure = 5,

            /// <summary>已停止。</summary>
            Stopped = 6,
        }
    }
}
