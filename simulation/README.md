# iHomeland Game Simulation Server

`simulation/` 是 C++20 Game Simulation Core 与本机 control child 的唯一工程根。当前
`ihomeland-sim-server` 支持离线 smoke 和 `--control-stdio`；后者只通过继承
stdin/stdout 交换 4-byte big-endian length-prefixed canonical JSON frame，不创建 listener、
端口、ticket、MySQL/Redis connection 或 battle wire。

构建与资格统一从仓库根目录执行：

```powershell
& .\tools\cpp\cpp.ps1 bootstrap
& .\tools\cpp\cpp.ps1 verify -Preset windows-msvc-release
& .\tools\simulation-control\simulation-control.ps1 -Action verify `
  -ServerQualificationReportPath <server-report.json> `
  -ClientQualificationReportPath <client-report.json>
```

Go parent 启动 child 前必须验证 binary 与 B0.3 qualification receipt 的 SHA-256，并在
hello 中核对 build/model/profile identity。stdout 污染、错误 nonce/sequence、unknown
field、oversize frame、EOF 或 child exit 都是 terminal failure；stderr 只允许低敏诊断。
每次 start 还绑定 `runtime/` canonical source 的 config/navigation/physics digest、
完整 AssignmentStamp、mapping generation、Go 派生并由 ready receipt 回显的 deterministic
seed 与 actor capacity；重复 start 的任一 immutable 字段漂移都必须拒绝。

正常关闭顺序是停止公开输入、drain instance、持久化并 ack ResultProposal、exact stop、
node shutdown，最后由 Go 释放 storage。UDP/KCP、Asio、AEAD、numeric battle message、
production 端口与 Unity runtime 仍由后续 OpenSpec change 交付。
