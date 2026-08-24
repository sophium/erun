-- erun_current_user_id() is the sibling of erun_current_tenant_id() (schema/rls/context.sql):
-- it lets a column default to the authenticated caller the same way tenant_id
-- already defaults to the authenticated tenant, so a client cannot assert an
-- author for someone else.
CREATE FUNCTION erun_current_user_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('erun.user_id', true), '')::UUID
$$;

-- Add "author_user_id" to "reviews" (schema/tables/reviews.sql). Added nullable
-- first because existing reviews predate authorship and have nothing to default
-- from: erun_current_user_id() reads a per-transaction setting that is not
-- present during migration, so applying the eventual NOT NULL default while
-- backfilling would fail on any already-populated control plane.
ALTER TABLE "reviews" ADD COLUMN "author_user_id" uuid;

-- Explicit backfill: a pre-existing review's real author was never recorded, so
-- attribute it to its tenant's earliest user as a deterministic placeholder
-- rather than leaving the column unset. A tenant with any reviews already has
-- at least one user, so this always finds a row.
UPDATE "reviews" AS r
   SET "author_user_id" = (
     SELECT u."user_id"
       FROM "users" u
      WHERE u."tenant_id" = r."tenant_id"
      ORDER BY u."created_at" ASC, u."user_id" ASC
      LIMIT 1
   )
 WHERE r."author_user_id" IS NULL;

ALTER TABLE "reviews" ALTER COLUMN "author_user_id" SET NOT NULL;
ALTER TABLE "reviews" ALTER COLUMN "author_user_id" SET DEFAULT erun_current_user_id();
ALTER TABLE "reviews" ADD CONSTRAINT "reviews_tenant_id_author_user_id_fkey"
  FOREIGN KEY ("tenant_id", "author_user_id") REFERENCES "users" ("tenant_id", "user_id")
  ON UPDATE NO ACTION ON DELETE NO ACTION;

-- Discovery indexes for the new "authorUserId" / "sourceBranch" list filters
-- (schema/indexes/reviews.sql).
CREATE INDEX "reviews_tenant_author_idx" ON "reviews" ("tenant_id", "author_user_id");
CREATE INDEX "reviews_tenant_source_branch_idx" ON "reviews" ("tenant_id", "source_branch");

-- One live review per source/target branch pair (schema/indexes/reviews.sql).
-- Branch history stays unbounded — a recycled branch name may have many closed
-- reviews — but two live proposals of the same change are refused here rather
-- than discovered at merge time, where the second would merge a branch the
-- target already contains and mint a second release for one change.
CREATE UNIQUE INDEX "reviews_tenant_live_source_target_idx"
  ON "reviews" ("tenant_id", "source_branch", "target_branch")
  WHERE (status NOT IN ('MERGED', 'CLOSED'));

-- Create "review_reviewers" table (schema/tables/review_reviewers.sql).
CREATE TABLE "review_reviewers" (
  "tenant_id" uuid NOT NULL DEFAULT erun_current_tenant_id(),
  "review_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("tenant_id", "review_id", "user_id"),
  CONSTRAINT "review_reviewers_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("tenant_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "review_reviewers_tenant_id_review_id_fkey" FOREIGN KEY ("tenant_id", "review_id") REFERENCES "reviews" ("tenant_id", "review_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "review_reviewers_tenant_id_user_id_fkey" FOREIGN KEY ("tenant_id", "user_id") REFERENCES "users" ("tenant_id", "user_id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "review_reviewers_tenant_user_idx" to table: "review_reviewers"
CREATE INDEX "review_reviewers_tenant_user_idx" ON "review_reviewers" ("tenant_id", "user_id", "review_id");

-- Timestamp trigger for the new table (atlas migrate diff omits trigger objects
-- without an atlas login session; appended from the declarative schema;
-- references the existing erun_set_timestamps()).
CREATE TRIGGER review_reviewers_set_timestamps
  BEFORE INSERT OR UPDATE ON "review_reviewers"
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

-- Grant tenant-owned CRUD on the new table to the application roles (atlas
-- migrate diff omits permissions without an atlas login session; appended from
-- schema/roles.sql). erun_tenant gets RLS-scoped access; erun_operations
-- crosses tenants under its own policies.
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES
  ON "review_reviewers"
  TO erun_tenant, erun_operations;

-- Row-level security for the new tenant-owned table (atlas migrate diff omits
-- policies without an atlas login session; appended from
-- schema/rls/review_reviewers.sql). Two policies mirror the reviews template:
-- tenant_isolation scopes erun_tenant by erun_current_tenant_id();
-- operations_access lets erun_operations cross tenants.
ALTER TABLE "review_reviewers" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "review_reviewers" FORCE ROW LEVEL SECURITY;
CREATE POLICY review_reviewers_tenant_isolation
  ON "review_reviewers"
  FOR ALL
  TO erun_tenant
  USING (tenant_id = erun_current_tenant_id())
  WITH CHECK (tenant_id = erun_current_tenant_id());
CREATE POLICY review_reviewers_operations_access
  ON "review_reviewers"
  FOR ALL
  TO erun_operations
  USING (true)
  WITH CHECK (true);
