CREATE INDEX user_external_ids_tenant_user_idx
  ON user_external_ids (tenant_id, user_id);

