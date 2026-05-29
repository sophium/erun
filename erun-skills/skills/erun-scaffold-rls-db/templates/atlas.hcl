variable "database_url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "default" {
  src = [
    # erun_current_tenant_id() must be loaded before any table default
    # references it.
    "file://schema/rls/context.sql",

    # Tables — root tenant resolution first, then tenant-owned domain
    # tables. Add new tables to this list in dependency order.
    "file://schema/tables/tenants.sql",
    "file://schema/tables/tenant_issuers.sql",
    "file://schema/tables/users.sql",
    "file://schema/tables/user_external_ids.sql",

    # Indexes — keep secondary indexes separate from table files.
    "file://schema/indexes/users.sql",
    "file://schema/indexes/user_external_ids.sql",

    # Triggers — shared function + per-table BEFORE INSERT OR UPDATE.
    "file://schema/triggers/erun_set_timestamps.sql",
    "file://schema/triggers/tenants_set_timestamps.sql",
    "file://schema/triggers/tenant_issuers_set_timestamps.sql",
    "file://schema/triggers/users_set_timestamps.sql",
    "file://schema/triggers/user_external_ids_set_timestamps.sql",

    # Cross-table foreign keys that are clearer outside table files.
    # Add files to schema/fks/ here when needed.

    # Roles — defined after tables so GRANTs reference existing relations.
    "file://schema/roles.sql",

    # RLS policies — last so they reference both tables and roles.
    "file://schema/rls/users.sql",
    "file://schema/rls/user_external_ids.sql",
  ]
  url = var.database_url
  dev = "docker://postgres/18/dev?search_path=public"

  migration {
    dir = "file://migrations/default"
  }
}
