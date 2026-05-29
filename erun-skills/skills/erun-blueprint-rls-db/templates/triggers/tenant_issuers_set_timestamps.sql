CREATE TRIGGER tenant_issuers_set_timestamps
  BEFORE INSERT OR UPDATE ON tenant_issuers
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
