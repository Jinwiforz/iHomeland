# Client Battle Runtime Source

本目录由 `client-battle-runtime` owner 管理，冻结 B0.7 开始实现时的进入身份与
只读上游边界。

`source-manifest.json` 绑定起始 clean commit、Unity 与 client-v1 baseline，以及
battle model、network profile、wire、numeric route、Protobuf source 和 B0.6
代表性 development-readiness。它不保存 ticket、proof、traffic key、credential、
endpoint、PID、PlayerID 或本机路径，也不声明最终产品资格。

唯一验证入口为 `tools/client-battle-runtime/client-battle-runtime.ps1 -Action validate`。
入口只读 source，不启动 Unity、Go/C++ production process 或 listener，也不生成
tracked/ignored artifact。
