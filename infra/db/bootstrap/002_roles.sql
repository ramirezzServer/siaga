-- Satu role pemilik per layanan. Dibuat NOLOGIN; login dan password diaktifkan terpisah
-- (Compose: 900_login_roles.sh, CloudNativePG: spec.managed.roles) supaya tidak ada password di SQL.
DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['siaga_geo', 'siaga_core', 'siaga_alert', 'siaga_ai', 'siaga_tiles'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', r);
    END IF;
  END LOOP;
END
$$;

-- Tidak ada yang boleh membuat objek di schema public selain superuser.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO siaga_geo, siaga_core, siaga_alert, siaga_ai, siaga_tiles;
