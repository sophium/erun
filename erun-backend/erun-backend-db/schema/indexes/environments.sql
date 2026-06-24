CREATE INDEX environments_tenant_type_idx
  ON environments (tenant_id, type);

CREATE INDEX environments_tenant_context_idx
  ON environments (tenant_id, context_id);
