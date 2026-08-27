CREATE FUNCTION erun_set_timestamps()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.created_at = COALESCE(NEW.created_at, NOW());
    NEW.updated_at = COALESCE(NEW.updated_at, NEW.created_at);
  ELSE
    NEW.created_at = OLD.created_at;
    NEW.updated_at = NOW();
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_set_timestamps
  BEFORE INSERT OR UPDATE ON tenants
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER issuers_set_timestamps
  BEFORE INSERT OR UPDATE ON issuers
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER tenant_issuers_set_timestamps
  BEFORE INSERT OR UPDATE ON tenant_issuers
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER users_set_timestamps
  BEFORE INSERT OR UPDATE ON users
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER user_external_ids_set_timestamps
  BEFORE INSERT OR UPDATE ON user_external_ids
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER roles_set_timestamps
  BEFORE INSERT OR UPDATE ON roles
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER role_permissions_set_timestamps
  BEFORE INSERT OR UPDATE ON role_permissions
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER user_roles_set_timestamps
  BEFORE INSERT OR UPDATE ON user_roles
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER reviews_set_timestamps
  BEFORE INSERT OR UPDATE ON reviews
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER review_merge_queue_set_timestamps
  BEFORE INSERT OR UPDATE ON review_merge_queue
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER review_reviewers_set_timestamps
  BEFORE INSERT OR UPDATE ON review_reviewers
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER builds_set_timestamps
  BEFORE INSERT OR UPDATE ON builds
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER releases_set_timestamps
  BEFORE INSERT OR UPDATE ON releases
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER comments_set_timestamps
  BEFORE INSERT OR UPDATE ON comments
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER contexts_set_timestamps
  BEFORE INSERT OR UPDATE ON contexts
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER environments_set_timestamps
  BEFORE INSERT OR UPDATE ON environments
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER tenant_quotas_set_timestamps
  BEFORE INSERT OR UPDATE ON tenant_quotas
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER cloud_provider_aliases_set_timestamps
  BEFORE INSERT OR UPDATE ON cloud_provider_aliases
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER context_credentials_set_timestamps
  BEFORE INSERT OR UPDATE ON context_credentials
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();

CREATE TRIGGER invites_set_timestamps
  BEFORE INSERT OR UPDATE ON invites
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
