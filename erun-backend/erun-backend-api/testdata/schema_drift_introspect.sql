\pset format unaligned
\pset tuples_only on
\pset fieldsep '|'

SELECT '### ROLES' AS marker;
SELECT rolname, rolcanlogin FROM pg_roles WHERE rolname IN ('erun_tenant','erun_operations') ORDER BY 1;

SELECT '### SCHEMA USAGE' AS marker;
SELECT rolname, has_schema_privilege(rolname, 'public', 'USAGE')
FROM pg_roles WHERE rolname IN ('erun_tenant','erun_operations') ORDER BY 1;

SELECT '### COLUMNS' AS marker;
SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default,''), is_identity, coalesce(identity_generation,'')
FROM information_schema.columns
WHERE table_schema='public'
ORDER BY table_name, column_name;

SELECT '### CONSTRAINTS' AS marker;
SELECT c.conrelid::regclass::text, c.conname, pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_namespace n ON n.oid = c.connamespace
WHERE n.nspname = 'public'
ORDER BY 1, 2;

SELECT '### INDEXES' AS marker;
SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname='public' ORDER BY tablename, indexname;

SELECT '### TRIGGERS' AS marker;
SELECT event_object_table, trigger_name, action_timing, event_manipulation, action_statement
FROM information_schema.triggers
WHERE trigger_schema='public'
ORDER BY event_object_table, trigger_name, event_manipulation;

SELECT '### RLS FLAGS' AS marker;
SELECT relname, relrowsecurity, relforcerowsecurity
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND relkind='r'
ORDER BY relname;

SELECT '### RLS POLICIES' AS marker;
SELECT tablename, policyname, permissive, roles::text, cmd, coalesce(qual,''), coalesce(with_check,'')
FROM pg_policies WHERE schemaname='public' ORDER BY tablename, policyname;

SELECT '### TABLE GRANTS' AS marker;
SELECT table_name, grantee, privilege_type FROM information_schema.role_table_grants
WHERE table_schema='public' AND grantee IN ('erun_tenant','erun_operations')
ORDER BY table_name, grantee, privilege_type;

SELECT '### COLUMN GRANTS' AS marker;
SELECT table_name, column_name, grantee, privilege_type FROM information_schema.column_privileges
WHERE table_schema='public' AND grantee IN ('erun_tenant','erun_operations')
ORDER BY table_name, column_name, grantee, privilege_type;

SELECT '### FUNCTIONS' AS marker;
SELECT p.proname, pg_get_functiondef(p.oid)
FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='public'
ORDER BY p.proname;
