-- Limited redemption quota reclaim schema patch
-- Target: SQLite
-- Baseline: redemptions does not contain reclaim_status.
-- IMPORTANT: this is a one-time upgrade script and must not be rerun.

BEGIN IMMEDIATE;

ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_status" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_remaining_quota" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaimed_quota" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_enabled_time" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaimed_time" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_error" text;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_reviewed_by" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_reviewed_time" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_review_note" text;

UPDATE "redemptions"
SET
  "reclaim_error" = COALESCE("reclaim_error", ''),
  "reclaim_review_note" = COALESCE("reclaim_review_note", '')
WHERE "reclaim_error" IS NULL OR "reclaim_review_note" IS NULL;

CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_due"
  ON "redemptions" ("reclaim_status", "expired_time");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_user"
  ON "redemptions" ("used_user_id", "reclaim_status");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_completed"
  ON "redemptions" ("reclaim_status", "reclaimed_time");

COMMIT;
