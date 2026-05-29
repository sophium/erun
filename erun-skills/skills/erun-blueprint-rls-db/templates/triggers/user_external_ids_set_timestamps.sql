CREATE TRIGGER user_external_ids_set_timestamps
  BEFORE INSERT OR UPDATE ON user_external_ids
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
