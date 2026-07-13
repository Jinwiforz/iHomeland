// Package account 定义账号身份、凭据边界以及注册和登录应用用例。
//
// 该包不依赖 transport、generated protocol type 或具体存储实现。账号持久事实由
// AccountRepository 负责，session 的创建仍由 internal/session 统一拥有。调用方只能把
// plaintext password 交给 CredentialHasher，不能记录、持久化或跨请求保留凭据值；
// 正式 repository、hasher 与 listener 由后续 infrastructure/transport package 提供。
package account
