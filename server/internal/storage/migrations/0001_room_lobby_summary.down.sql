-- rollback: 第一阶段尚无生产数据依赖时可删除新增表。
-- compatibility: 若已发布到共享环境，回滚前必须先停用写入路径并导出 room_summary / match_summary_stub。

DROP TABLE IF EXISTS match_summary_stub;
DROP TABLE IF EXISTS room_summary;
DROP TABLE IF EXISTS player_profile_stub;
