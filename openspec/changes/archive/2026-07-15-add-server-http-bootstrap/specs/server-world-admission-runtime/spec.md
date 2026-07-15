## MODIFIED Requirements

### Requirement: Admission issuance 必须按稳定 identity 幂等且可解析提交不确定性
Issuer MUST 以稳定 issuance ID 和完整授权 binding fingerprint 原子创建 issuance/credential records。Fingerprint MUST绑定 actor、role、target、purpose、session epoch、assignment、endpoint/channel 等稳定授权事实；相同 ID 改变任一稳定授权事实 MUST返回 idempotency conflict。`IssuedAt` 与由当前服务端时钟计算的 candidate expiry 不属于客户端可变语义：仅因重试时钟推进得到更晚 candidate deadline时，issuer MUST重放首次 raw credential与首次较短expiry，MUST NOT延长资格；当前session、membership、assignment lease或其他权威deadline收紧到早于首次expiry时，MUST返回 idempotency conflict。Redis mutation无法证明提交结果时 MUST返回 commit-unknown且不返回 raw credential，调用方只能用同一 ID与语义重试解析。

#### Scenario: Issuance response 丢失后时钟推进
- **WHEN**首次Redis mutation已提交但调用方只观察到commit-unknown，并以相同issuance ID与稳定授权binding重试，而服务端时钟推进产生更晚candidate expiry
- **THEN**issuer从注入key重新推导并返回与首次完全相同的opaque credential和首次较短expiry，不创建第二条资格也不延长有效期

#### Scenario: 相同 issuance ID 改变目标
- **WHEN**调用方复用issuance ID但把own-world改为另一个VisitSession、purpose、assignment、endpoint或其他稳定授权事实
- **THEN**store返回稳定idempotency conflict，旧credential record不被覆盖

#### Scenario: 权威 deadline 已经收紧
- **WHEN**相同issuance ID重试时current session、membership或assignment lease的权威deadline早于首次签发expiry
- **THEN**issuer返回idempotency conflict而不重放已经超出当前权威边界的资格
