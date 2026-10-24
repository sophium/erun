-- Shared by every <table>_set_timestamps trigger; do not inline per table.
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
