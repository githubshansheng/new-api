-- Liandong payment schema creation
-- Target: a fresh SQLite database without any Liandong tables.
-- Do not use this file to upgrade the 2026-07-15 Liandong schema.

PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

CREATE TEMP TABLE "__liandong_fresh_guard" ("value" integer);
CREATE TEMP TRIGGER "__liandong_fresh_guard_trigger"
BEFORE INSERT ON "__liandong_fresh_guard"
BEGIN
  SELECT RAISE(
    ABORT,
    'liandong fresh schema requires all Liandong tables to be absent'
  )
  WHERE EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'table'
      AND "name" IN (
        'liandong_products',
        'liandong_orders',
        'liandong_product_thumbnails',
        'liandong_product_inventory_codes',
        'liandong_user_operation_leases'
      )
  );
END;

INSERT INTO "__liandong_fresh_guard" ("value") VALUES (1);
DROP TRIGGER "__liandong_fresh_guard_trigger";
DROP TABLE "__liandong_fresh_guard";

CREATE TABLE IF NOT EXISTS "liandong_products" (
  "id" integer PRIMARY KEY AUTOINCREMENT,
  "business_type" varchar(32) NOT NULL,
  "goods_type" varchar(32) NOT NULL,
  "name" varchar(128) NOT NULL,
  "goods_key" varchar(128) NOT NULL,
  "quota_amount" bigint NOT NULL,
  "plan_id" integer,
  "expected_amount_minor" bigint NOT NULL,
  "currency" varchar(8) NOT NULL,
  "inventory_mode" varchar(32) NOT NULL,
  "inventory_capacity" integer NOT NULL,
  "thumbnail_version" bigint NOT NULL,
  "enabled" numeric,
  "sort_order" integer,
  "created_by" integer,
  "updated_by" integer,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE UNIQUE INDEX IF NOT EXISTS "idx_liandong_products_goods_key"
  ON "liandong_products" ("goods_key");
CREATE INDEX IF NOT EXISTS "idx_liandong_products_business_type"
  ON "liandong_products" ("business_type");
CREATE INDEX IF NOT EXISTS "idx_liandong_products_goods_type"
  ON "liandong_products" ("goods_type");
CREATE INDEX IF NOT EXISTS "idx_liandong_products_plan_id"
  ON "liandong_products" ("plan_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_products_inventory_mode"
  ON "liandong_products" ("inventory_mode");

CREATE TABLE IF NOT EXISTS "liandong_orders" (
  "id" integer PRIMARY KEY AUTOINCREMENT,
  "local_trade_no" varchar(128) NOT NULL,
  "provider_trade_no" varchar(128),
  "user_id" integer NOT NULL,
  "product_id" integer NOT NULL,
  "product_name_snapshot" varchar(128) NOT NULL,
  "business_type" varchar(32) NOT NULL,
  "target_id" integer,
  "goods_key_snapshot" varchar(128) NOT NULL,
  "contact_snapshot" varchar(12) NOT NULL,
  "j_uuid_snapshot" varchar(128) NOT NULL,
  "expected_amount_minor" bigint NOT NULL,
  "currency_snapshot" varchar(8) NOT NULL,
  "fulfillment_snapshot" text NOT NULL,
  "inventory_code_id" integer NOT NULL,
  "payment_status" varchar(32) NOT NULL,
  "fulfillment_status" varchar(32) NOT NULL,
  "last_check_at" bigint,
  "next_check_at" bigint,
  "check_deadline_at" bigint,
  "check_count" integer,
  "consecutive_error_count" integer,
  "check_lock_until" bigint,
  "provider_summary" text,
  "last_error" text,
  "expires_at" bigint NOT NULL,
  "closed_reason" varchar(64) NOT NULL,
  "late_payment" numeric NOT NULL,
  "paid_at" bigint,
  "fulfilled_at" bigint,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE UNIQUE INDEX IF NOT EXISTS "idx_liandong_orders_local_trade_no"
  ON "liandong_orders" ("local_trade_no");
CREATE UNIQUE INDEX IF NOT EXISTS "idx_liandong_orders_provider_trade_no"
  ON "liandong_orders" ("provider_trade_no");
CREATE UNIQUE INDEX IF NOT EXISTS "idx_liandong_orders_contact_snapshot"
  ON "liandong_orders" ("contact_snapshot");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_user_id"
  ON "liandong_orders" ("user_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_product_id"
  ON "liandong_orders" ("product_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_business_type"
  ON "liandong_orders" ("business_type");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_target_id"
  ON "liandong_orders" ("target_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_inventory_code_id"
  ON "liandong_orders" ("inventory_code_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_payment_status"
  ON "liandong_orders" ("payment_status");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_fulfillment_status"
  ON "liandong_orders" ("fulfillment_status");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_next_check_at"
  ON "liandong_orders" ("next_check_at");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_check_deadline_at"
  ON "liandong_orders" ("check_deadline_at");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_check_lock_until"
  ON "liandong_orders" ("check_lock_until");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_expires_at"
  ON "liandong_orders" ("expires_at");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_closed_reason"
  ON "liandong_orders" ("closed_reason");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_late_payment"
  ON "liandong_orders" ("late_payment");
CREATE INDEX IF NOT EXISTS "idx_liandong_orders_created_at"
  ON "liandong_orders" ("created_at");

CREATE TABLE IF NOT EXISTS "liandong_product_thumbnails" (
  "product_id" integer PRIMARY KEY,
  "content_type" varchar(32) NOT NULL,
  "data" blob,
  "width" integer,
  "height" integer,
  "size" integer,
  "version" bigint NOT NULL,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE TABLE IF NOT EXISTS "liandong_product_inventory_codes" (
  "id" integer PRIMARY KEY AUTOINCREMENT,
  "product_id" integer NOT NULL,
  "code" char(32) NOT NULL,
  "name" varchar(128) NOT NULL,
  "status" varchar(32) NOT NULL,
  "reserved_order_id" integer,
  "reserved_trade_no" varchar(128),
  "reserved_user_id" integer,
  "reserved_at" bigint,
  "consumed_at" bigint,
  "disabled_at" bigint,
  "created_by" integer,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE UNIQUE INDEX IF NOT EXISTS "idx_liandong_product_inventory_codes_code"
  ON "liandong_product_inventory_codes" ("code");
CREATE INDEX IF NOT EXISTS "idx_liandong_inventory_product_status"
  ON "liandong_product_inventory_codes" ("product_id", "status");
CREATE INDEX IF NOT EXISTS "idx_liandong_product_inventory_codes_reserved_order_id"
  ON "liandong_product_inventory_codes" ("reserved_order_id");
CREATE INDEX IF NOT EXISTS "idx_liandong_product_inventory_codes_reserved_trade_no"
  ON "liandong_product_inventory_codes" ("reserved_trade_no");
CREATE INDEX IF NOT EXISTS "idx_liandong_product_inventory_codes_reserved_user_id"
  ON "liandong_product_inventory_codes" ("reserved_user_id");

CREATE TABLE IF NOT EXISTS "liandong_user_operation_leases" (
  "user_id" integer PRIMARY KEY,
  "token" char(32) NOT NULL,
  "expires_at" bigint NOT NULL,
  "updated_at" bigint
);

CREATE INDEX IF NOT EXISTS "idx_liandong_user_operation_leases_expires_at"
  ON "liandong_user_operation_leases" ("expires_at");

COMMIT;
PRAGMA foreign_keys = ON;
