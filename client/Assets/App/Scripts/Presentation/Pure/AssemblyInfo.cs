using System.Runtime.CompilerServices;

// 纯 Presentation 程序集只向 Composition 与登记测试开放内部合同。
[assembly: InternalsVisibleTo("IHomeland.Client.Runtime")]
[assembly: InternalsVisibleTo("IHomeland.Client.Runtime.EditModeTests")]
[assembly: InternalsVisibleTo("IHomeland.Client.Runtime.PlayModeTests")]
