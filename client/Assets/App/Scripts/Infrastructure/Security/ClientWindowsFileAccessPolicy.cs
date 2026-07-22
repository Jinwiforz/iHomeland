using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;

namespace IHomeland.Client.Infrastructure.Security
{
    /// <summary>
    /// 为 Windows secure session profile 建立显式 owner-only DACL。
    /// </summary>
    /// <remarks>
    /// DPAPI 是credential机密性的主要边界；受保护DACL额外阻止同机普通账号读取、替换或删除
    /// ciphertext。Object owner、SYSTEM与Administrators保留FullControl，但其他继承项被移除。
    /// </remarks>
    internal static class ClientWindowsFileAccessPolicy
    {
        /// <summary>SDDL revision 1。</summary>
        private const uint SecurityDescriptorRevision = 1;

        /// <summary>只提交security descriptor的DACL。</summary>
        private const int DaclSecurityInformation = 0x00000004;

        /// <summary>
        /// Protected DACL：object owner、LocalSystem与Builtin Administrators继承FullControl。
        /// </summary>
        private const string OwnerOnlyDirectorySddl =
            "D:P(A;OICI;FA;;;OW)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)";

        /// <summary>把profile目录收敛到owner、SYSTEM与Administrators。</summary>
        /// <param name="directoryPath">已经位于persistentDataPath下的精确profile目录。</param>
        internal static void ApplyToDirectory(string directoryPath)
        {
            if (string.IsNullOrWhiteSpace(directoryPath) || !Path.IsPathRooted(directoryPath))
            {
                throw new ArgumentException("Secure session ACL path 必须是绝对路径。", nameof(directoryPath));
            }

            IntPtr descriptor = IntPtr.Zero;
            try
            {
                if (!ConvertStringSecurityDescriptorToSecurityDescriptor(
                        OwnerOnlyDirectorySddl,
                        SecurityDescriptorRevision,
                        out descriptor,
                        out _))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                }

                if (!SetFileSecurity(directoryPath, DaclSecurityInformation, descriptor))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                }
            }
            finally
            {
                if (descriptor != IntPtr.Zero)
                {
                    LocalFree(descriptor);
                }
            }
        }

        /// <summary>把封闭SDDL转换为self-relative security descriptor。</summary>
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(
            string stringSecurityDescriptor,
            uint stringSdRevision,
            out IntPtr securityDescriptor,
            out uint securityDescriptorSize);

        /// <summary>把受控DACL设置到精确profile目录。</summary>
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool SetFileSecurity(
            string fileName,
            int securityInformation,
            IntPtr securityDescriptor);

        /// <summary>释放ConvertStringSecurityDescriptor返回的LocalAlloc buffer。</summary>
        [DllImport("kernel32.dll", SetLastError = false)]
        private static extern IntPtr LocalFree(IntPtr memory);
    }
}
