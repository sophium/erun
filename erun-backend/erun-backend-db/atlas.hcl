variable "database_url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "default" {
  src = [
    "file://schema/rls/context.sql",
    "file://schema/tables/tenants.sql",
    "file://schema/tables/tenant_issuers.sql",
    "file://schema/tables/users.sql",
    "file://schema/tables/user_external_ids.sql",
    "file://schema/tables/roles.sql",
    "file://schema/tables/role_permissions.sql",
    "file://schema/tables/user_roles.sql",
    "file://schema/tables/reviews.sql",
    "file://schema/tables/review_merge_queue.sql",
    "file://schema/tables/builds.sql",
    "file://schema/tables/comments.sql",
    "file://schema/tables/audit_events.sql",
    "file://schema/indexes/users.sql",
    "file://schema/indexes/user_external_ids.sql",
    "file://schema/indexes/role_permissions.sql",
    "file://schema/indexes/user_roles.sql",
    "file://schema/indexes/reviews.sql",
    "file://schema/indexes/review_merge_queue.sql",
    "file://schema/indexes/builds.sql",
    "file://schema/indexes/comments.sql",
    "file://schema/indexes/audit_events.sql",
    "file://schema/triggers/comments.sql",
    "file://schema/triggers/timestamps.sql",
    "file://schema/fks/review_builds.sql",
    "file://schema/roles.sql",
    "file://schema/rls/users.sql",
    "file://schema/rls/user_external_ids.sql",
    "file://schema/rls/roles.sql",
    "file://schema/rls/role_permissions.sql",
    "file://schema/rls/user_roles.sql",
    "file://schema/rls/reviews.sql",
    "file://schema/rls/review_merge_queue.sql",
    "file://schema/rls/builds.sql",
    "file://schema/rls/comments.sql",
    "file://schema/rls/audit_events.sql",
  ]
  url = var.database_url
  dev = "docker://postgres/18/dev?search_path=public"

  migration {
    dir = "file://migrations/default"
  }
}
