CREATE TRIGGER users_set_timestamps
  BEFORE INSERT OR UPDATE ON users
  FOR EACH ROW
  EXECUTE FUNCTION erun_set_timestamps();
