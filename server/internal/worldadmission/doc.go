// Package worldadmission 实现 transport-independent 的一次性世界准入签发与验证。
//
// Package 只拥有 opaque credential、受信 binding、幂等签发和单次消费编排；它不拥有
// HTTP/TLS-TCP adapter、Redis client lifecycle、连接 registry 或 PersonalWorld/VisitSession 状态机。
package worldadmission
