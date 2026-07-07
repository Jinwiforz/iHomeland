-- rollback: 第一阶段尚无生产账号数据依赖时可删除新增表。
-- compatibility: 若已发布到共享环境，回滚前必须先停用账号注册、登录和会话恢复写入路径并导出 account_player。

DROP TABLE IF EXISTS account_player;
