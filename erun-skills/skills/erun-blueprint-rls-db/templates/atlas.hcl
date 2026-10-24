variable "database_url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "default" {
  src = [
    # Table defaults reference erun_current_tenant_id().
    "file://schema/rls/context.sql",

    # Tables in dependency order.
    "file://schema/tables/tenants.sql",
    "file://schema/tables/tenant_issuers.sql",
    "file://schema/tables/users.sql",
    "file://schema/tables/user_external_ids.sql",

    "file://schema/indexes/users.sql",
    "file://schema/indexes/user_external_ids.sql",

    "file://schema/triggers/erun_set_timestamps.sql",
    "file://schema/triggers/tenants_set_timestamps.sql",
    "file://schema/triggers/tenant_issuers_set_timestamps.sql",
    "file://schema/triggers/users_set_timestamps.sql",
    "file://schema/triggers/user_external_ids_set_timestamps.sql",

    # Cross-table foreign keys (schema/fks/) go here when needed.

    # Roles after tables so GRANTs reference existing relations.
    "file://schema/roles.sql",

    # RLS policies last: they reference both tables and roles.
    "file://schema/rls/users.sql",
    "file://schema/rls/user_external_ids.sql",
  ]
  url = var.database_url
  dev = "docker://postgres/18/dev?search_path=public"

  migration {
    dir = "file://migrations/default"
  }
}
