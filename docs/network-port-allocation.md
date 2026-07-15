# iHomeland 网络端口规划

## 目标

端口号用于提供可识别的默认配置，不作为跨环境不可变契约。开发机、CI、容器、测试环境和生产环境可能因端口占用、网络策略或托管平台要求使用不同端口；服务发现、连接配置和 ticket 才是运行时端点事实。

本规划区分三类端口：

- 标准或产品默认端口：保留行业语义，例如 HTTPS `443`、MySQL `3306`、Redis `6379`。
- 项目推荐默认端口：用于仓库示例和本地首次启动，可以被显式覆盖。
- 环境实际端口：由本机配置、容器映射、编排平台或生产部署确定。

端口范围遵循通用划分：`0-1023` 主要保留给系统与标准服务，`1024-49151` 可用于已登记服务和稳定应用 listener，`49152-65535` 主要用于动态或私有分配。项目选择固定推荐值时仍需检查目标操作系统、IANA 登记和同机部署清单；任何号码都不能保证在所有开发机上空闲。

## 推荐端口表

| Owner | 用途 | 协议 | 推荐默认值 | 暴露范围 | 约束 |
| --- | --- | --- | ---: | --- | --- |
| Edge/Public API | 生产 HTTPS 与 WSS | TCP | `443` | 公网 | HTTPS 与 WSS 优先复用同一 TLS 入口 |
| Public API | 本地 HTTP | TCP | `8080` | 本机或开发网 | 仅用于不要求 TLS 的本地开发 |
| Public API | 本地 HTTPS 与 WSS | TCP | `8443` | 本机或开发网 | 项目推荐值，不是正式 HTTPS 端口 |
| Realtime | 本地 TLS/TCP | TCP | `8444` | 本机或开发网 | 仓库 local 配置的可覆盖推荐值，客户端仍以 endpoint/ticket 为准 |
| Server Runtime | 健康、就绪、版本与 metrics | TCP | `8081` | 默认仅 loopback | 不承载公开业务；生产环境限制在管理网络 |
| Realtime | 部署 TLS/TCP | TCP | 不预留固定值 | 按部署配置 | 由 endpoint/ticket 下发，客户端不得硬编码 |
| Battle | 裸 UDP | UDP | 尚未分配 | 按部署配置 | 等 battle simulation model 与 network profile |
| Battle | KCP | UDP | 尚未分配 | 按部署配置 | listener、ticket 与 QoS 关系由后续 change 决定 |
| MySQL | 持久化数据库 | TCP | `3306` | 内网 | 实际连接端口可由环境配置或端口映射覆盖 |
| Redis | 可恢复运行态 | TCP | `6379` | 内网 | 不得暴露公网 |
| OpenTelemetry Collector | OTLP/gRPC | TCP | `4317` | 内网 | Collector 产品默认端口 |
| OpenTelemetry Collector | OTLP/HTTP | TCP | `4318` | 内网 | Collector 产品默认端口 |
| Prometheus | Prometheus Web/API | TCP | `9090` | 管理网络 | 属于 Prometheus 自身，不是应用 metrics 的固定端口 |
| Node Exporter | 主机 metrics | TCP | `9100` | 管理网络 | 仅部署该组件时使用 |
| Grafana | 可观测界面 | TCP | `3000` | 管理网络 | 生产环境应经过认证入口 |

`8080` 是通用的备用 HTTP 端口；`8081`、`8443`、`8444` 等属于易识别的工程约定，不具有不可覆盖的协议含义。自定义 TLS/TCP、UDP 和 KCP 没有适合本项目直接继承的行业默认端口，因此不得为了形式统一过早冻结生产号码。

公开 HTTP 与 WSS control 复用同一个实际 listener：WSS 不是第二个端口，而是该入口的精确 `/v1/control` upgrade path。`publicApi.address` 决定进程 bind，`publicApi.endpoints.wss` 决定客户端可见且写入 ticket 的 advertised endpoint；两者可以因 ingress 或 port mapping 不同，但必须由部署配置显式对应，服务端不得从不受信 Host header 重建 advertised endpoint。

## 覆盖与映射

推荐默认值被占用时必须显式覆盖，不能要求开发者释放系统或其他软件已经使用的端口。例如本机 MySQL 无法绑定产品默认端口时，可以采用：

```text
应用连接端点: 读取当前环境配置
宿主机端口: 选择当前机器上的可用端口
容器内端口: 保持 MySQL 产品默认值
```

这不会改变 MySQL 协议或 repository 设计，只改变当前环境的连接地址。其他服务同样遵循以下优先级：

```text
代码中的安全默认值
  < 已选择的环境配置文件
  < 白名单环境变量或 secret/config mount
  < 部署平台生成的运行时端点
```

本地可提交配置只提供无密钥示例；个人端口通过白名单环境变量或 `.local/` 下由 `--config` 显式选择的本机文件覆盖，密码和机器路径不得写入已跟踪配置。容器内部优先保留产品默认端口，通过 host/service/ingress 映射解决冲突，避免修改镜像内所有消费者。

## 冲突处理

- 固定 listener 绑定失败时，进程必须报告 component、IP、端口和可诊断原因，回滚已启动组件并以非零退出码结束。
- 服务端不得在端口冲突后自动递增或随机选择另一个生产端口，否则日志、探针、客户端和编排配置会指向不同端点。
- 启动前配置校验负责发现同一进程内的重复 listener 地址；操作系统绑定负责发现与其他进程的冲突。
- 本地端口变更必须通过配置完成，不得修改业务代码、协议 schema 或散落常量。
- 公网端点由 HTTPS bootstrap、服务发现或连接 ticket 提供；Unity 客户端不得把开发机端口编译为业务事实。
- 诊断、MySQL、Redis、OTLP、Prometheus 和 Grafana 默认不得暴露到公网；端口不替代 TLS、认证、授权或防火墙。

## 测试规则

- 单元测试优先使用 `httptest`、fake adapter 或无 listener 测试。
- 必须打开真实 listener 的进程内自动化测试使用 `127.0.0.1:0` 请求操作系统分配空闲端口，并从实际 listener 读取分配结果。
- `0` 只能用于测试或临时工具，不能进入可部署服务配置，也不能下发给客户端。
- 跨进程测试优先通过 ready file、日志或其他测试通道回传实际地址；暂时只能“分配后释放再由子进程绑定”时，必须限制在测试代码中并对地址被抢占执行有界重试。
- 端到端环境使用环境清单记录实际映射，并在启动阶段执行连通性和冲突检查。

## 变更规则

仅调整某台机器或某个部署环境的端口属于配置变更，不需要修改协议或发起架构 change。新增公网 listener、改变通道归属、合并或拆分 listener、改变安全边界以及固定新的项目级推荐端口，必须通过对应 OpenSpec change 更新本规划、部署配置、探针和测试。
