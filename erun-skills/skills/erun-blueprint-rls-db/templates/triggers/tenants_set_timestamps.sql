CREATE TRIGGER tenants_set_timestamps
  BEFORE INSERT OR UPDATE ON tenants
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
