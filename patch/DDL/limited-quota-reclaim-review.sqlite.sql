-- Limited redemption quota manual-review schema patch
-- Target: SQLite
-- Baseline: the first limited-quota reclaim schema contains reclaim_status,
--           but does not contain reclaim_reviewed_by.
-- IMPORTANT: this is a one-time upgrade script and must not be rerun.

BEGIN IMMEDIATE;

ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_reviewed_by" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_reviewed_time" integer NOT NULL DEFAULT 0;
ALTER TABLE "redemptions"
  ADD COLUMN "reclaim_review_note" text;

UPDATE "redemptions"
SET "reclaim_review_note" = COALESCE("reclaim_review_note", '')
WHERE "reclaim_review_note" IS NULL;

COMMIT;
