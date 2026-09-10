-- Limited redemption quota reclaim schema patch
-- Target: MySQL >= 5.7.8
-- Baseline: an existing redemptions table, with or without earlier reclaim columns.
-- MySQL DDL auto-commits. Back up the database and stop all writers first.

SET NAMES utf8mb4;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_status` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_status'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_remaining_quota` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_remaining_quota'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaimed_quota` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaimed_quota'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_enabled_time` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_enabled_time'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaimed_time` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaimed_time'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_error` text NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_error'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_reviewed_by` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_reviewed_by'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_reviewed_time` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_reviewed_time'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `redemptions` ADD COLUMN `reclaim_review_note` text NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND COLUMN_NAME = 'reclaim_review_note'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

UPDATE `redemptions`
SET
  `reclaim_status` = COALESCE(`reclaim_status`, 0),
  `reclaim_remaining_quota` = COALESCE(`reclaim_remaining_quota`, 0),
  `reclaimed_quota` = COALESCE(`reclaimed_quota`, 0),
  `reclaim_enabled_time` = COALESCE(`reclaim_enabled_time`, 0),
  `reclaimed_time` = COALESCE(`reclaimed_time`, 0),
  `reclaim_error` = COALESCE(`reclaim_error`, ''),
  `reclaim_reviewed_by` = COALESCE(`reclaim_reviewed_by`, 0),
  `reclaim_reviewed_time` = COALESCE(`reclaim_reviewed_time`, 0),
  `reclaim_review_note` = COALESCE(`reclaim_review_note`, '')
WHERE
  `reclaim_status` IS NULL
  OR `reclaim_remaining_quota` IS NULL
  OR `reclaimed_quota` IS NULL
  OR `reclaim_enabled_time` IS NULL
  OR `reclaimed_time` IS NULL
  OR `reclaim_error` IS NULL
  OR `reclaim_reviewed_by` IS NULL
  OR `reclaim_reviewed_time` IS NULL
  OR `reclaim_review_note` IS NULL;

ALTER TABLE `redemptions`
  MODIFY COLUMN `reclaim_status` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaim_remaining_quota` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaimed_quota` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaim_enabled_time` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaimed_time` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaim_reviewed_by` bigint NOT NULL DEFAULT 0,
  MODIFY COLUMN `reclaim_reviewed_time` bigint NOT NULL DEFAULT 0;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_redemption_reclaim_due` ON `redemptions` (`reclaim_status`, `expired_time`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND INDEX_NAME = 'idx_redemption_reclaim_due'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_redemption_reclaim_user` ON `redemptions` (`used_user_id`, `reclaim_status`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND INDEX_NAME = 'idx_redemption_reclaim_user'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_redemption_reclaim_completed` ON `redemptions` (`reclaim_status`, `reclaimed_time`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'redemptions'
    AND INDEX_NAME = 'idx_redemption_reclaim_completed'
);
PREPARE redemption_reclaim_stmt FROM @redemption_reclaim_ddl;
EXECUTE redemption_reclaim_stmt;
DEALLOCATE PREPARE redemption_reclaim_stmt;

SET @redemption_reclaim_ddl = NULL;
