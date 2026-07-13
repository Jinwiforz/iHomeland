// Package placement 实现 PersonalWorld 运行承载的 assignment、lease 与 fencing 规则。
//
// 该包只消费 personalworld.PersonalWorldID，不持有持久世界内容、网络 endpoint、socket
// 或 storage 实现。所有 current assignment 变更必须由 PlacementStore 原子线性化；连接
// 和客户端 payload 不能恢复已过期或被替换的 WorldInstance 写资格。
package placement
