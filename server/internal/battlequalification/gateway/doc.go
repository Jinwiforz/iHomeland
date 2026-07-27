// Package gateway 实现 B0.6 资格专用的 opaque UDP 故障边界。
//
// 该包只读取 secure datagram 的公开 header 投影，不解密、不改写 payload，也不导入
// production battle transport 或 gameplay 实现。Scheduler 与 socket lifecycle 分离，使
// 注入顺序、资源上限和失败裁决可在不创建 listener 的环境中独立验证。
package gateway
