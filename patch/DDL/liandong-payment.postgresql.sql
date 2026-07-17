-- Liandong payment schema patch
-- Target: PostgreSQL >= 9.6
-- Baseline: the initial Liandong schema delivered on 2026-07-15.
-- The script supports both a fresh schema and an upgrade from that baseline.

CREATE TABLE IF NOT EXISTS "liandong_products" (
  "id" bigserial PRIMARY KEY,
  "business_type" varchar(32) NOT NULL,
  "goods_type" varchar(32) NOT NULL,
  "name" varchar(128) NOT NULL,
  "goods_key" varchar(128) NOT NULL,
  "quota_amount" bigint NOT NULL,
  "plan_id" bigint,
  "expected_amount_minor" bigint NOT NULL,
  "currency" varchar(8) NOT NULL,
  "inventory_mode" varchar(32) NOT NULL,
  "inventory_capacity" bigint NOT NULL,
  "thumbnail_version" bigint NOT NULL,
  "enabled" boolean,
  "sort_order" bigint,
  "created_by" bigint,
  "updated_by" bigint,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE TABLE IF NOT EXISTS "liandong_orders" (
  "id" bigserial PRIMARY KEY,
  "local_trade_no" varchar(128) NOT NULL,
  "provider_trade_no" varchar(128),
  "user_id" bigint NOT NULL,
  "product_id" bigint NOT NULL,
  "product_name_snapshot" varchar(128) NOT NULL,
  "business_type" varchar(32) NOT NULL,
  "target_id" bigint,
  "goods_key_snapshot" varchar(128) NOT NULL,
  "contact_snapshot" varchar(12) NOT NULL,
  "j_uuid_snapshot" varchar(128) NOT NULL,
  "expected_amount_minor" bigint NOT NULL,
  "currency_snapshot" varchar(8) NOT NULL,
  "fulfillment_snapshot" text NOT NULL,
  "inventory_code_id" bigint NOT NULL,
  "payment_status" varchar(32) NOT NULL,
  "fulfillment_status" varchar(32) NOT NULL,
  "last_check_at" bigint,
  "next_check_at" bigint,
  "check_deadline_at" bigint,
  "check_count" bigint,
  "consecutive_error_count" bigint,
  "check_lock_until" bigint,
  "provider_summary" text,
  "last_error" text,
  "expires_at" bigint NOT NULL,
  "closed_reason" varchar(64) NOT NULL,
  "late_payment" boolean NOT NULL,
  "paid_at" bigint,
  "fulfilled_at" bigint,
  "created_at" bigint,
  "updated_at" bigint
);

ALTER TABLE "liandong_products"
  ADD COLUMN IF NOT EXISTS "goods_type" varchar(32),
  ADD COLUMN IF NOT EXISTS "inventory_mode" varchar(32),
  ADD COLUMN IF NOT EXISTS "inventory_capacity" bigint,
  ADD COLUMN IF NOT EXISTS "thumbnail_version" bigint;

UPDATE "liandong_products"
SET "goods_type" = 'card'
WHERE "goods_type" IS NULL OR "goods_type" = '';

UPDATE "liandong_products"
SET "inventory_mode" = 'unlimited'
WHERE "inventory_mode" IS NULL OR "inventory_mode" = '';

UPDATE "liandong_products"
SET "inventory_capacity" = 0
WHERE "inventory_capacity" IS NULL;

UPDATE "liandong_products"
SET "thumbnail_version" = 0
WHERE "thumbnail_version" IS NULL;

ALTER TABLE "liandong_products"
  ALTER COLUMN "goods_type" SET NOT NULL,
  ALTER COLUMN "inventory_mode" SET NOT NULL,
  ALTER COLUMN "inventory_capacity" SET NOT NULL,
  ALTER COLUMN "thumbnail_version" SET NOT NULL;

ALTER TABLE "liandong_orders"
  ADD COLUMN IF NOT EXISTS "inventory_code_id" bigint,
  ADD COLUMN IF NOT EXISTS "expires_at" bigint,
  ADD COLUMN IF NOT EXISTS "closed_reason" varchar(64),
  ADD COLUMN IF NOT EXISTS "late_payment" boolean;

UPDATE "liandong_orders"
SET "inventory_code_id" = 0
WHERE "inventory_code_id" IS NULL;

UPDATE "liandong_orders"
SET "expires_at" = 0
WHERE "expires_at" IS NULL;

UPDATE "liandong_orders"
SET "closed_reason" = ''
WHERE "closed_reason" IS NULL;

UPDATE "liandong_orders"
SET "late_payment" = FALSE
WHERE "late_payment" IS NULL;

ALTER TABLE "liandong_orders"
  ALTER COLUMN "inventory_code_id" SET NOT NULL,
  ALTER COLUMN "expires_at" SET NOT NULL,
  ALTER COLUMN "closed_reason" SET NOT NULL,
  ALTER COLUMN "late_payment" SET NOT NULL;

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
  "product_id" bigint PRIMARY KEY,
  "content_type" varchar(32) NOT NULL,
  "data" bytea,
  "width" bigint,
  "height" bigint,
  "size" bigint,
  "version" bigint NOT NULL,
  "created_at" bigint,
  "updated_at" bigint
);

CREATE TABLE IF NOT EXISTS "liandong_product_inventory_codes" (
  "id" bigserial PRIMARY KEY,
  "product_id" bigint NOT NULL,
  "code" char(32) NOT NULL,
  "name" varchar(128) NOT NULL,
  "status" varchar(32) NOT NULL,
  "reserved_order_id" bigint,
  "reserved_trade_no" varchar(128),
  "reserved_user_id" bigint,
  "reserved_at" bigint,
  "consumed_at" bigint,
  "disabled_at" bigint,
  "created_by" bigint,
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
  "user_id" bigint PRIMARY KEY,
  "token" char(32) NOT NULL,
  "expires_at" bigint NOT NULL,
  "updated_at" bigint
);

CREATE INDEX IF NOT EXISTS "idx_liandong_user_operation_leases_expires_at"
  ON "liandong_user_operation_leases" ("expires_at");
