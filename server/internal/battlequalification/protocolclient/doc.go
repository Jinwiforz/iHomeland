// Package protocolclient 监督独立 C++ battle protocol client 的 closed stdio contract。
//
// 本包只处理进程、binary frame、一次性 credential 与低敏事件，不导入 production
// battle transport、simulation gameplay 或 application service。Credential 只写入继承
// stdin，任何错误、stderr 状态或 receipt 都不得返回其内容。
package protocolclient
