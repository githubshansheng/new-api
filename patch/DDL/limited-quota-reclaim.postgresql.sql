-- Limited redemption quota reclaim schema patch
-- Target: PostgreSQL >= 9.6
-- Baseline: an existing redemptions table, with or without earlier reclaim columns.

BEGIN;

ALTER TABLE "redemptions"
  ADD COLUMN IF NOT EXISTS "reclaim_status" bigint,
  ADD COLUMN IF NOT EXISTS "reclaim_remaining_quota" bigint,
  ADD COLUMN IF NOT EXISTS "reclaimed_quota" bigint,
  ADD COLUMN IF NOT EXISTS "reclaim_enabled_time" bigint,
  ADD COLUMN IF NOT EXISTS "reclaimed_time" bigint,
  ADD COLUMN IF NOT EXISTS "reclaim_error" text,
  ADD COLUMN IF NOT EXISTS "reclaim_reviewed_by" bigint,
  ADD COLUMN IF NOT EXISTS "reclaim_reviewed_time" bigint,
  ADD COLUMN IF NOT EXISTS "reclaim_review_note" text;

UPDATE "redemptions"
SET
  "reclaim_status" = COALESCE("reclaim_status", 0),
  "reclaim_remaining_quota" = COALESCE("reclaim_remaining_quota", 0),
  "reclaimed_quota" = COALESCE("reclaimed_quota", 0),
  "reclaim_enabled_time" = COALESCE("reclaim_enabled_time", 0),
  "reclaimed_time" = COALESCE("reclaimed_time", 0),
  "reclaim_error" = COALESCE("reclaim_error", ''),
  "reclaim_reviewed_by" = COALESCE("reclaim_reviewed_by", 0),
  "reclaim_reviewed_time" = COALESCE("reclaim_reviewed_time", 0),
  "reclaim_review_note" = COALESCE("reclaim_review_note", '')
WHERE
  "reclaim_status" IS NULL
  OR "reclaim_remaining_quota" IS NULL
  OR "reclaimed_quota" IS NULL
  OR "reclaim_enabled_time" IS NULL
  OR "reclaimed_time" IS NULL
  OR "reclaim_error" IS NULL
  OR "reclaim_reviewed_by" IS NULL
  OR "reclaim_reviewed_time" IS NULL
  OR "reclaim_review_note" IS NULL;

ALTER TABLE "redemptions"
  ALTER COLUMN "reclaim_status" TYPE bigint USING "reclaim_status"::bigint,
  ALTER COLUMN "reclaim_status" SET DEFAULT 0,
  ALTER COLUMN "reclaim_status" SET NOT NULL,
  ALTER COLUMN "reclaim_remaining_quota" TYPE bigint USING "reclaim_remaining_quota"::bigint,
  ALTER COLUMN "reclaim_remaining_quota" SET DEFAULT 0,
  ALTER COLUMN "reclaim_remaining_quota" SET NOT NULL,
  ALTER COLUMN "reclaimed_quota" TYPE bigint USING "reclaimed_quota"::bigint,
  ALTER COLUMN "reclaimed_quota" SET DEFAULT 0,
  ALTER COLUMN "reclaimed_quota" SET NOT NULL,
  ALTER COLUMN "reclaim_enabled_time" TYPE bigint USING "reclaim_enabled_time"::bigint,
  ALTER COLUMN "reclaim_enabled_time" SET DEFAULT 0,
  ALTER COLUMN "reclaim_enabled_time" SET NOT NULL,
  ALTER COLUMN "reclaimed_time" TYPE bigint USING "reclaimed_time"::bigint,
  ALTER COLUMN "reclaimed_time" SET DEFAULT 0,
  ALTER COLUMN "reclaimed_time" SET NOT NULL,
  ALTER COLUMN "reclaim_reviewed_by" TYPE bigint USING "reclaim_reviewed_by"::bigint,
  ALTER COLUMN "reclaim_reviewed_by" SET DEFAULT 0,
  ALTER COLUMN "reclaim_reviewed_by" SET NOT NULL,
  ALTER COLUMN "reclaim_reviewed_time" TYPE bigint USING "reclaim_reviewed_time"::bigint,
  ALTER COLUMN "reclaim_reviewed_time" SET DEFAULT 0,
  ALTER COLUMN "reclaim_reviewed_time" SET NOT NULL;

CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_due"
  ON "redemptions" ("reclaim_status", "expired_time");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_user"
  ON "redemptions" ("used_user_id", "reclaim_status");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_completed"
  ON "redemptions" ("reclaim_status", "reclaimed_time");

COMMIT;
