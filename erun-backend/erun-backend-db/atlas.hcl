variable "sqlite_url" {
  type    = string
  default = "sqlite://erun-backend.db?_fk=1"
}

variable "postgres_url" {
  type    = string
  default = getenv("DATABASE_URL")
}

variable "clickhouse_url" {
  type    = string
  default = getenv("CLICKHOUSE_URL")
}

locals {
  schema_files = [
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
    "file://schema/sqlite/tables/audit_events.sql",
    "file://schema/indexes/users.sql",
    "file://schema/indexes/user_external_ids.sql",
    "file://schema/indexes/role_permissions.sql",
    "file://schema/indexes/user_roles.sql",
    "file://schema/indexes/reviews.sql",
    "file://schema/indexes/review_merge_queue.sql",
    "file://schema/indexes/builds.sql",
    "file://schema/indexes/comments.sql",
    "file://schema/sqlite/indexes/audit_events.sql",
    "file://schema/sqlite/triggers/reviews.sql",
    "file://schema/sqlite/triggers/comments.sql",
    "file://schema/sqlite/triggers/audit_events.sql",
    "file://schema/sqlite/triggers/primary_keys.sql",
    "file://schema/sqlite/triggers/timestamps.sql",
  ]
}

env "sqlite" {
  src = local.schema_files
  url = var.sqlite_url
  dev = "sqlite://file?mode=memory&_fk=1"

  migration {
    dir = "file://migrations/sqlite"
  }
}

env "postgres" {
  src = [
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
    "file://schema/indexes/users.sql",
    "file://schema/indexes/user_external_ids.sql",
    "file://schema/indexes/role_permissions.sql",
    "file://schema/indexes/user_roles.sql",
    "file://schema/indexes/reviews.sql",
    "file://schema/indexes/review_merge_queue.sql",
    "file://schema/indexes/builds.sql",
    "file://schema/indexes/comments.sql",
    "file://schema/postgres/triggers/comments.sql",
    "file://schema/postgres/triggers/primary_keys.sql",
    "file://schema/postgres/triggers/timestamps.sql",
    "file://schema/postgres/fks/review_builds.sql",
    "file://schema/postgres/roles.sql",
    "file://schema/postgres/rls/context.sql",
    "file://schema/postgres/rls/users.sql",
    "file://schema/postgres/rls/user_external_ids.sql",
    "file://schema/postgres/rls/roles.sql",
    "file://schema/postgres/rls/role_permissions.sql",
    "file://schema/postgres/rls/user_roles.sql",
    "file://schema/postgres/rls/reviews.sql",
    "file://schema/postgres/rls/review_merge_queue.sql",
    "file://schema/postgres/rls/builds.sql",
    "file://schema/postgres/rls/comments.sql",
  ]
  url = var.postgres_url
  dev = "docker://postgres/18/dev?search_path=public"

  migration {
    dir = "file://migrations/postgres"
  }
}

env "clickhouse" {
  src = [
    "file://schema/clickhouse/tables/audit_events.sql",
  ]
  url = var.clickhouse_url
  dev = "docker://clickhouse/24.8/dev"

  migration {
    dir = "file://migrations/clickhouse"
  }
}
