-- iHomeland 本地 MySQL 开发命令。
--
-- 用途：
-- 1. 创建本地开发库。
-- 2. 创建或修复服务端应用账号。
-- 3. 授权应用账号访问本地开发库。
-- 4. 执行当前第一阶段 MySQL migration。
--
-- 执行方式：
--   cd server
--   mysql -u root -p -h 127.0.0.1 -P 3306 < scripts/mysql-local-dev.sql
--
-- SQL IDE 注意：
-- - 本文件末尾的 SOURCE 是 mysql 命令行客户端语法。
-- - 如果所用 SQL IDE 不支持 SOURCE，请选中 ihomeland schema 后按顺序执行 migrations/*.up.sql。
--
-- 约束：
-- - 只用于本地开发或一次性本地修复。
-- - 不得在生产数据库上直接执行。
-- - 生产环境必须使用正式 migration/DBA 流程。
-- - MySQL migration 中的正式表和字段必须带中文 COMMENT。

CREATE DATABASE IF NOT EXISTS `ihomeland`
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS 'ihomeland'@'localhost'
  IDENTIFIED BY 'ihomeland';

ALTER USER 'ihomeland'@'localhost'
  IDENTIFIED BY 'ihomeland';

CREATE USER IF NOT EXISTS 'ihomeland'@'127.0.0.1'
  IDENTIFIED BY 'ihomeland';

ALTER USER 'ihomeland'@'127.0.0.1'
  IDENTIFIED BY 'ihomeland';

GRANT ALL PRIVILEGES ON `ihomeland`.* TO 'ihomeland'@'localhost';
GRANT ALL PRIVILEGES ON `ihomeland`.* TO 'ihomeland'@'127.0.0.1';

FLUSH PRIVILEGES;

USE `ihomeland`;

SOURCE internal/storage/migrations/0001_room_lobby_summary.up.sql;
SOURCE internal/storage/migrations/0002_account_session.up.sql;

SHOW TABLES;
SHOW GRANTS FOR 'ihomeland'@'localhost';
SHOW GRANTS FOR 'ihomeland'@'127.0.0.1';
