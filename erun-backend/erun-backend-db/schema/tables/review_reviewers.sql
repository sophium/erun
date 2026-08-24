CREATE TABLE review_reviewers (
  tenant_id UUID NOT NULL DEFAULT erun_current_tenant_id(),
  review_id UUID NOT NULL,
  user_id UUID NOT NULL,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, review_id, user_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
  FOREIGN KEY (tenant_id, review_id) REFERENCES reviews (tenant_id, review_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id)
);
