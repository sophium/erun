-- A review's name is its eventual squash-merge message, and the uniqueness it
-- needed was for two changes that land: two landed changes must not claim the
-- same message. The old table-level UNIQUE (tenant_id, name) reserved the name
-- for every review that was ever created instead, so a review closed without
-- merging — which never landed, and whose name was therefore never used as a
-- merge message — held it forever. Re-reviewing that branch after a rebase, a
-- redo, or an abandoned review picked back up was refused with a 409 whose body
-- said only "Conflict", and the only way forward was to reword the name, which
-- then no longer matched the change's own commit subject.
--
-- The replacement is a partial unique index over exactly the reviews that
-- reserve the name: everything except CLOSED (schema/indexes/reviews.sql).
ALTER TABLE "reviews" DROP CONSTRAINT "reviews_tenant_name_key";

CREATE UNIQUE INDEX "reviews_tenant_name_idx"
  ON "reviews" ("tenant_id", "name")
  WHERE status <> 'CLOSED';
