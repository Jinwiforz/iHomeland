// Package session 实现与 transport 和 storage adapter 解耦的服务端身份核心。
//
// 该包统一拥有 session epoch、opaque token、一次性 connection ticket 和
// AuthContext 语义。调用方必须传入经过上游账号域验证的 Principal；客户端
// payload、raw token 和 socket 均不能成为业务授权身份来源。
package session
