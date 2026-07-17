-- Liandong payment schema upgrade
-- Target: SQLite
-- Baseline: the initial Liandong schema delivered on 2026-07-15.
-- IMPORTANT: this is a one-time upgrade script. Do not run it on a fresh
-- database or rerun it after a successful upgrade. See README-liandong-payment.md.

PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

CREATE TEMP TABLE "__liandong_patch_guard" ("value" integer);
CREATE TEMP TRIGGER "__liandong_patch_guard_trigger"
BEFORE INSERT ON "__liandong_patch_guard"
BEGIN
  SELECT RAISE(
    ABORT,
    'liandong patch requires the exact 2026-07-15 SQLite schema'
  )
  WHERE COALESCE((
    SELECT group_concat(signature, ',')
    FROM (
      SELECT
        "name" || ':' || lower("type") || ':' || "notnull" || ':' || "pk" || ':' || "hidden" AS signature
      FROM pragma_table_xinfo('liandong_products')
      ORDER BY "cid"
    )
  ), '') <> 'id:integer:0:1:0,business_type:varchar(32):1:0:0,name:varchar(128):1:0:0,goods_key:varchar(128):1:0:0,quota_amount:bigint:1:0:0,plan_id:integer:0:0:0,expected_amount_minor:bigint:1:0:0,currency:varchar(8):1:0:0,enabled:numeric:0:0:0,sort_order:integer:0:0:0,created_by:integer:0:0:0,updated_by:integer:0:0:0,created_at:bigint:0:0:0,updated_at:bigint:0:0:0'
  OR COALESCE((
    SELECT group_concat(signature, ',')
    FROM (
      SELECT
        "name" || ':' || lower("type") || ':' || "notnull" || ':' || "pk" || ':' || "hidden" AS signature
      FROM pragma_table_xinfo('liandong_orders')
      ORDER BY "cid"
    )
  ), '') <> 'id:integer:0:1:0,local_trade_no:varchar(128):1:0:0,provider_trade_no:varchar(128):0:0:0,user_id:integer:1:0:0,product_id:integer:1:0:0,product_name_snapshot:varchar(128):1:0:0,business_type:varchar(32):1:0:0,target_id:integer:0:0:0,goods_key_snapshot:varchar(128):1:0:0,contact_snapshot:varchar(12):1:0:0,j_uuid_snapshot:varchar(128):1:0:0,expected_amount_minor:bigint:1:0:0,currency_snapshot:varchar(8):1:0:0,fulfillment_snapshot:text:1:0:0,payment_status:varchar(32):1:0:0,fulfillment_status:varchar(32):1:0:0,last_check_at:bigint:0:0:0,next_check_at:bigint:0:0:0,check_deadline_at:bigint:0:0:0,check_count:integer:0:0:0,consecutive_error_count:integer:0:0:0,check_lock_until:bigint:0:0:0,provider_summary:text:0:0:0,last_error:text:0:0:0,paid_at:bigint:0:0:0,fulfilled_at:bigint:0:0:0,created_at:bigint:0:0:0,updated_at:bigint:0:0:0'
  OR COALESCE((
    SELECT group_concat("name", ',')
    FROM (
      SELECT "name"
      FROM sqlite_master
      WHERE "type" = 'index'
        AND "tbl_name" = 'liandong_products'
        AND "name" NOT LIKE 'sqlite_autoindex_%'
      ORDER BY "name"
    )
  ), '') <> 'idx_liandong_products_business_type,idx_liandong_products_goods_key,idx_liandong_products_plan_id'
  OR COALESCE((
    SELECT group_concat("name", ',')
    FROM (
      SELECT "name"
      FROM sqlite_master
      WHERE "type" = 'index'
        AND "tbl_name" = 'liandong_orders'
        AND "name" NOT LIKE 'sqlite_autoindex_%'
      ORDER BY "name"
    )
  ), '') <> 'idx_liandong_orders_business_type,idx_liandong_orders_check_deadline_at,idx_liandong_orders_check_lock_until,idx_liandong_orders_contact_snapshot,idx_liandong_orders_created_at,idx_liandong_orders_fulfillment_status,idx_liandong_orders_local_trade_no,idx_liandong_orders_next_check_at,idx_liandong_orders_payment_status,idx_liandong_orders_product_id,idx_liandong_orders_provider_trade_no,idx_liandong_orders_target_id,idx_liandong_orders_user_id'
  OR EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'table'
      AND "name" IN (
        'liandong_product_thumbnails',
        'liandong_product_inventory_codes',
        'liandong_user_operation_leases'
      )
  )
  OR EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'trigger'
      AND (
        "tbl_name" IN ('liandong_products', 'liandong_orders')
        OR lower(COALESCE("sql", '')) LIKE '%liandong_products%'
        OR lower(COALESCE("sql", '')) LIKE '%liandong_orders%'
      )
  )
  OR EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'view'
      AND (
        lower(COALESCE("sql", '')) LIKE '%liandong_products%'
        OR lower(COALESCE("sql", '')) LIKE '%liandong_orders%'
      )
  )
  OR EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'index'
      AND "tbl_name" IN ('liandong_products', 'liandong_orders')
      AND "name" LIKE 'sqlite_autoindex_%'
  )
  OR EXISTS (
    SELECT 1
    FROM pragma_foreign_key_list('liandong_products')
  )
  OR EXISTS (
    SELECT 1
    FROM pragma_foreign_key_list('liandong_orders')
  )
  OR EXISTS (
    SELECT 1
    FROM sqlite_master AS schema_object
    JOIN pragma_foreign_key_list(schema_object."name") AS foreign_key
    WHERE schema_object."type" = 'table'
      AND foreign_key."table" IN ('liandong_products', 'liandong_orders')
  )
  OR EXISTS (
    SELECT 1
    FROM sqlite_master
    WHERE "type" = 'table'
      AND "name" IN ('liandong_products', 'liandong_orders')
      AND (
        lower(COALESCE("sql", '')) LIKE '% check(%'
        OR lower(COALESCE("sql", '')) LIKE '% check (%'
        OR lower(COALESCE("sql", '')) LIKE '% generated %'
      )
  );
END;

INSERT INTO "__liandong_patch_guard" ("value") VALUES (1);
DROP TRIGGER "__liandong_patch_guard_trigger";
DROP TABLE "__liandong_patch_guard";

CREATE TEMP TABLE "__liandong_patch_sequence" (
  "name" text PRIMARY KEY,
  "seq" integer NOT NULL
);
INSERT INTO "__liandong_patch_sequence" ("name", "seq")
SELECT 'liandong_products', COALESCE((
  SELECT "seq" FROM "sqlite_sequence" WHERE "name" = 'liandong_products'
), 0);
INSERT INTO "__liandong_patch_sequence" ("name", "seq")
SELECT 'liandong_orders', COALESCE((
  SELECT "seq" FROM "sqlite_sequence" WHERE "name" = 'liandong_orders'
), 0);

CREATE TABLE "liandong_products__new_20260716" (
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

INSERT INTO "liandong_products__new_20260716" (
  "id",
  "business_type",
  "goods_type",
  "name",
  "goods_key",
  "quota_amount",
  "plan_id",
  "expected_amount_minor",
  "currency",
  "inventory_mode",
  "inventory_capacity",
  "thumbnail_version",
  "enabled",
  "sort_order",
  "created_by",
  "updated_by",
  "created_at",
  "updated_at"
)
SELECT
  "id",
  "business_type",
  'card',
  "name",
  "goods_key",
  "quota_amount",
  "plan_id",
  "expected_amount_minor",
  "currency",
  'unlimited',
  0,
  0,
  "enabled",
  "sort_order",
  "created_by",
  "updated_by",
  "created_at",
  "updated_at"
FROM "liandong_products";

DROP TABLE "liandong_products";
ALTER TABLE "liandong_products__new_20260716" RENAME TO "liandong_products";

CREATE UNIQUE INDEX "idx_liandong_products_goods_key"
  ON "liandong_products" ("goods_key");
CREATE INDEX "idx_liandong_products_business_type"
  ON "liandong_products" ("business_type");
CREATE INDEX "idx_liandong_products_goods_type"
  ON "liandong_products" ("goods_type");
CREATE INDEX "idx_liandong_products_plan_id"
  ON "liandong_products" ("plan_id");
CREATE INDEX "idx_liandong_products_inventory_mode"
  ON "liandong_products" ("inventory_mode");

CREATE TABLE "liandong_orders__new_20260716" (
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

INSERT INTO "liandong_orders__new_20260716" (
  "id",
  "local_trade_no",
  "provider_trade_no",
  "user_id",
  "product_id",
  "product_name_snapshot",
  "business_type",
  "target_id",
  "goods_key_snapshot",
  "contact_snapshot",
  "j_uuid_snapshot",
  "expected_amount_minor",
  "currency_snapshot",
  "fulfillment_snapshot",
  "inventory_code_id",
  "payment_status",
  "fulfillment_status",
  "last_check_at",
  "next_check_at",
  "check_deadline_at",
  "check_count",
  "consecutive_error_count",
  "check_lock_until",
  "provider_summary",
  "last_error",
  "expires_at",
  "closed_reason",
  "late_payment",
  "paid_at",
  "fulfilled_at",
  "created_at",
  "updated_at"
)
SELECT
  "id",
  "local_trade_no",
  "provider_trade_no",
  "user_id",
  "product_id",
  "product_name_snapshot",
  "business_type",
  "target_id",
  "goods_key_snapshot",
  "contact_snapshot",
  "j_uuid_snapshot",
  "expected_amount_minor",
  "currency_snapshot",
  "fulfillment_snapshot",
  0,
  "payment_status",
  "fulfillment_status",
  "last_check_at",
  "next_check_at",
  "check_deadline_at",
  "check_count",
  "consecutive_error_count",
  "check_lock_until",
  "provider_summary",
  "last_error",
  0,
  '',
  0,
  "paid_at",
  "fulfilled_at",
  "created_at",
  "updated_at"
FROM "liandong_orders";

DROP TABLE "liandong_orders";
ALTER TABLE "liandong_orders__new_20260716" RENAME TO "liandong_orders";

DELETE FROM "sqlite_sequence"
WHERE "name" IN ('liandong_products', 'liandong_orders');
INSERT INTO "sqlite_sequence" ("name", "seq")
SELECT
  'liandong_products',
  MAX(
    COALESCE((
      SELECT "seq"
      FROM "__liandong_patch_sequence"
      WHERE "name" = 'liandong_products'
    ), 0),
    COALESCE((SELECT MAX("id") FROM "liandong_products"), 0)
  );
INSERT INTO "sqlite_sequence" ("name", "seq")
SELECT
  'liandong_orders',
  MAX(
    COALESCE((
      SELECT "seq"
      FROM "__liandong_patch_sequence"
      WHERE "name" = 'liandong_orders'
    ), 0),
    COALESCE((SELECT MAX("id") FROM "liandong_orders"), 0)
  );
DROP TABLE "__liandong_patch_sequence";

CREATE UNIQUE INDEX "idx_liandong_orders_local_trade_no"
  ON "liandong_orders" ("local_trade_no");
CREATE UNIQUE INDEX "idx_liandong_orders_provider_trade_no"
  ON "liandong_orders" ("provider_trade_no");
CREATE UNIQUE INDEX "idx_liandong_orders_contact_snapshot"
  ON "liandong_orders" ("contact_snapshot");
CREATE INDEX "idx_liandong_orders_user_id"
  ON "liandong_orders" ("user_id");
CREATE INDEX "idx_liandong_orders_product_id"
  ON "liandong_orders" ("product_id");
CREATE INDEX "idx_liandong_orders_business_type"
  ON "liandong_orders" ("business_type");
CREATE INDEX "idx_liandong_orders_target_id"
  ON "liandong_orders" ("target_id");
CREATE INDEX "idx_liandong_orders_inventory_code_id"
  ON "liandong_orders" ("inventory_code_id");
CREATE INDEX "idx_liandong_orders_payment_status"
  ON "liandong_orders" ("payment_status");
CREATE INDEX "idx_liandong_orders_fulfillment_status"
  ON "liandong_orders" ("fulfillment_status");
CREATE INDEX "idx_liandong_orders_next_check_at"
  ON "liandong_orders" ("next_check_at");
CREATE INDEX "idx_liandong_orders_check_deadline_at"
  ON "liandong_orders" ("check_deadline_at");
CREATE INDEX "idx_liandong_orders_check_lock_until"
  ON "liandong_orders" ("check_lock_until");
CREATE INDEX "idx_liandong_orders_expires_at"
  ON "liandong_orders" ("expires_at");
CREATE INDEX "idx_liandong_orders_closed_reason"
  ON "liandong_orders" ("closed_reason");
CREATE INDEX "idx_liandong_orders_late_payment"
  ON "liandong_orders" ("late_payment");
CREATE INDEX "idx_liandong_orders_created_at"
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
