-- Liandong payment schema patch
-- Target: MySQL >= 5.7.8
-- Baseline: the initial Liandong schema delivered on 2026-07-15.
-- MySQL DDL auto-commits. Back up the database before execution.

SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS `liandong_products` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `business_type` varchar(32) NOT NULL,
  `goods_type` varchar(32) NOT NULL,
  `name` varchar(128) NOT NULL,
  `goods_key` varchar(128) NOT NULL,
  `quota_amount` bigint NOT NULL,
  `plan_id` bigint NULL,
  `expected_amount_minor` bigint NOT NULL,
  `currency` varchar(8) NOT NULL,
  `inventory_mode` varchar(32) NOT NULL,
  `inventory_capacity` bigint NOT NULL,
  `thumbnail_version` bigint NOT NULL,
  `enabled` boolean NULL,
  `sort_order` bigint NULL,
  `created_by` bigint NULL,
  `updated_by` bigint NULL,
  `created_at` bigint NULL,
  `updated_at` bigint NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_liandong_products_goods_key` (`goods_key`),
  KEY `idx_liandong_products_business_type` (`business_type`),
  KEY `idx_liandong_products_goods_type` (`goods_type`),
  KEY `idx_liandong_products_plan_id` (`plan_id`),
  KEY `idx_liandong_products_inventory_mode` (`inventory_mode`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `liandong_orders` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `local_trade_no` varchar(128) NOT NULL,
  `provider_trade_no` varchar(128) NULL,
  `user_id` bigint NOT NULL,
  `product_id` bigint NOT NULL,
  `product_name_snapshot` varchar(128) NOT NULL,
  `business_type` varchar(32) NOT NULL,
  `target_id` bigint NULL,
  `goods_key_snapshot` varchar(128) NOT NULL,
  `contact_snapshot` varchar(12) NOT NULL,
  `j_uuid_snapshot` varchar(128) NOT NULL,
  `expected_amount_minor` bigint NOT NULL,
  `currency_snapshot` varchar(8) NOT NULL,
  `fulfillment_snapshot` text NOT NULL,
  `inventory_code_id` bigint NOT NULL,
  `payment_status` varchar(32) NOT NULL,
  `fulfillment_status` varchar(32) NOT NULL,
  `last_check_at` bigint NULL,
  `next_check_at` bigint NULL,
  `check_deadline_at` bigint NULL,
  `check_count` bigint NULL,
  `consecutive_error_count` bigint NULL,
  `check_lock_until` bigint NULL,
  `provider_summary` text NULL,
  `last_error` text NULL,
  `expires_at` bigint NOT NULL,
  `closed_reason` varchar(64) NOT NULL,
  `late_payment` boolean NOT NULL,
  `paid_at` bigint NULL,
  `fulfilled_at` bigint NULL,
  `created_at` bigint NULL,
  `updated_at` bigint NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_liandong_orders_local_trade_no` (`local_trade_no`),
  UNIQUE KEY `idx_liandong_orders_provider_trade_no` (`provider_trade_no`),
  UNIQUE KEY `idx_liandong_orders_contact_snapshot` (`contact_snapshot`),
  KEY `idx_liandong_orders_user_id` (`user_id`),
  KEY `idx_liandong_orders_product_id` (`product_id`),
  KEY `idx_liandong_orders_business_type` (`business_type`),
  KEY `idx_liandong_orders_target_id` (`target_id`),
  KEY `idx_liandong_orders_inventory_code_id` (`inventory_code_id`),
  KEY `idx_liandong_orders_payment_status` (`payment_status`),
  KEY `idx_liandong_orders_fulfillment_status` (`fulfillment_status`),
  KEY `idx_liandong_orders_next_check_at` (`next_check_at`),
  KEY `idx_liandong_orders_check_deadline_at` (`check_deadline_at`),
  KEY `idx_liandong_orders_check_lock_until` (`check_lock_until`),
  KEY `idx_liandong_orders_expires_at` (`expires_at`),
  KEY `idx_liandong_orders_closed_reason` (`closed_reason`),
  KEY `idx_liandong_orders_late_payment` (`late_payment`),
  KEY `idx_liandong_orders_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Add product columns when upgrading from the 2026-07-15 schema.
SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_products` ADD COLUMN `goods_type` varchar(32) NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND COLUMN_NAME = 'goods_type'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_products` ADD COLUMN `inventory_mode` varchar(32) NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND COLUMN_NAME = 'inventory_mode'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_products` ADD COLUMN `inventory_capacity` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND COLUMN_NAME = 'inventory_capacity'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_products` ADD COLUMN `thumbnail_version` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND COLUMN_NAME = 'thumbnail_version'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

UPDATE `liandong_products`
SET `goods_type` = 'card'
WHERE `goods_type` IS NULL OR `goods_type` = '';

UPDATE `liandong_products`
SET `inventory_mode` = 'unlimited'
WHERE `inventory_mode` IS NULL OR `inventory_mode` = '';

UPDATE `liandong_products`
SET `inventory_capacity` = 0
WHERE `inventory_capacity` IS NULL;

UPDATE `liandong_products`
SET `thumbnail_version` = 0
WHERE `thumbnail_version` IS NULL;

ALTER TABLE `liandong_products`
  MODIFY COLUMN `goods_type` varchar(32) NOT NULL,
  MODIFY COLUMN `inventory_mode` varchar(32) NOT NULL,
  MODIFY COLUMN `inventory_capacity` bigint NOT NULL,
  MODIFY COLUMN `thumbnail_version` bigint NOT NULL;

-- Add order columns used by inventory reservation, timeout closure and late-payment review.
SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_orders` ADD COLUMN `inventory_code_id` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND COLUMN_NAME = 'inventory_code_id'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_orders` ADD COLUMN `expires_at` bigint NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND COLUMN_NAME = 'expires_at'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_orders` ADD COLUMN `closed_reason` varchar(64) NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND COLUMN_NAME = 'closed_reason'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'ALTER TABLE `liandong_orders` ADD COLUMN `late_payment` boolean NULL',
    'SELECT 1'
  )
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND COLUMN_NAME = 'late_payment'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

UPDATE `liandong_orders`
SET `inventory_code_id` = 0
WHERE `inventory_code_id` IS NULL;

UPDATE `liandong_orders`
SET `expires_at` = 0
WHERE `expires_at` IS NULL;

UPDATE `liandong_orders`
SET `closed_reason` = ''
WHERE `closed_reason` IS NULL;

UPDATE `liandong_orders`
SET `late_payment` = FALSE
WHERE `late_payment` IS NULL;

ALTER TABLE `liandong_orders`
  MODIFY COLUMN `inventory_code_id` bigint NOT NULL,
  MODIFY COLUMN `expires_at` bigint NOT NULL,
  MODIFY COLUMN `closed_reason` varchar(64) NOT NULL,
  MODIFY COLUMN `late_payment` boolean NOT NULL;

-- Add indexes for the newly introduced product and order columns.
SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_products_goods_type` ON `liandong_products` (`goods_type`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND INDEX_NAME = 'idx_liandong_products_goods_type'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_products_inventory_mode` ON `liandong_products` (`inventory_mode`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_products'
    AND INDEX_NAME = 'idx_liandong_products_inventory_mode'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_orders_inventory_code_id` ON `liandong_orders` (`inventory_code_id`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND INDEX_NAME = 'idx_liandong_orders_inventory_code_id'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_orders_expires_at` ON `liandong_orders` (`expires_at`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND INDEX_NAME = 'idx_liandong_orders_expires_at'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_orders_closed_reason` ON `liandong_orders` (`closed_reason`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND INDEX_NAME = 'idx_liandong_orders_closed_reason'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

SET @liandong_ddl = (
  SELECT IF(
    COUNT(*) = 0,
    'CREATE INDEX `idx_liandong_orders_late_payment` ON `liandong_orders` (`late_payment`)',
    'SELECT 1'
  )
  FROM information_schema.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'liandong_orders'
    AND INDEX_NAME = 'idx_liandong_orders_late_payment'
);
PREPARE liandong_stmt FROM @liandong_ddl;
EXECUTE liandong_stmt;
DEALLOCATE PREPARE liandong_stmt;

CREATE TABLE IF NOT EXISTS `liandong_product_thumbnails` (
  `product_id` bigint NOT NULL,
  `content_type` varchar(32) NOT NULL,
  `data` longblob NULL,
  `width` bigint NULL,
  `height` bigint NULL,
  `size` bigint NULL,
  `version` bigint NOT NULL,
  `created_at` bigint NULL,
  `updated_at` bigint NULL,
  PRIMARY KEY (`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `liandong_product_inventory_codes` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `product_id` bigint NOT NULL,
  `code` char(32) NOT NULL,
  `name` varchar(128) NOT NULL,
  `status` varchar(32) NOT NULL,
  `reserved_order_id` bigint NULL,
  `reserved_trade_no` varchar(128) NULL,
  `reserved_user_id` bigint NULL,
  `reserved_at` bigint NULL,
  `consumed_at` bigint NULL,
  `disabled_at` bigint NULL,
  `created_by` bigint NULL,
  `created_at` bigint NULL,
  `updated_at` bigint NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_liandong_product_inventory_codes_code` (`code`),
  KEY `idx_liandong_inventory_product_status` (`product_id`, `status`),
  KEY `idx_liandong_product_inventory_codes_reserved_order_id` (`reserved_order_id`),
  KEY `idx_liandong_product_inventory_codes_reserved_trade_no` (`reserved_trade_no`),
  KEY `idx_liandong_product_inventory_codes_reserved_user_id` (`reserved_user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `liandong_user_operation_leases` (
  `user_id` bigint NOT NULL,
  `token` char(32) NOT NULL,
  `expires_at` bigint NOT NULL,
  `updated_at` bigint NULL,
  PRIMARY KEY (`user_id`),
  KEY `idx_liandong_user_operation_leases_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @liandong_ddl = NULL;
