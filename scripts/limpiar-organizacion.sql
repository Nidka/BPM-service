-- Elimina por completo una organización de prueba y todo lo que depende de ella.
-- Uso: psql "$DATABASE_URL" -v org_name='Prueba BPM' -v emails='a@x.com,b@y.com' -v confirmar=SI -f scripts/limpiar-organizacion.sql
--   org_name  nombre legal o comercial EXACTO de la organización (debe coincidir con una sola).
--   emails    correos de cuentas a borrar si, tras la limpieza, no pertenecen a otra organización.
--   confirmar SI para guardar; cualquier otro valor solo muestra lo que se borraría (ROLLBACK).
-- Todo ocurre en una sola transacción. Los disparadores de inmutabilidad (constancias, movimientos de stock)
-- se suspenden solo dentro de esta transacción con session_replication_role = replica.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL session_replication_role = replica;
SELECT set_config('bpm.org_name', :'org_name', true), set_config('bpm.emails', :'emails', true);

DO $$
DECLARE
    v_org   text;
    v_n     int;
    v_total bigint := 0;
    v_round int := 0;
    r       record;
    v_del   bigint;
BEGIN
    SELECT count(*) INTO v_n FROM organizations
     WHERE legal_name = current_setting('bpm.org_name') OR trade_name = current_setting('bpm.org_name');
    IF v_n <> 1 THEN RAISE EXCEPTION 'Se esperaba 1 organización llamada %, hay %', current_setting('bpm.org_name'), v_n; END IF;
    SELECT id INTO v_org FROM organizations
     WHERE legal_name = current_setting('bpm.org_name') OR trade_name = current_setting('bpm.org_name');
    RAISE NOTICE 'Organización: % (%)', current_setting('bpm.org_name'), v_org;

    -- Usuarios candidatos: los correos indicados que solo pertenecen a esta organización.
    CREATE TEMP TABLE bpm_users_to_delete ON COMMIT DROP AS
    SELECT u.id FROM application_users u
     WHERE lower(u.email) = ANY (string_to_array(lower(replace(current_setting('bpm.emails'), ' ', '')), ','))
       AND u.platform_role <> 'SUPER_ADMIN'
       AND NOT EXISTS (SELECT 1 FROM organization_memberships m WHERE m.user_id = u.id AND m.organization_id <> v_org);

    -- 1) Filas con organization_id de esta organización.
    FOR r IN SELECT c.table_name FROM information_schema.columns c
              JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
             WHERE c.table_schema = current_schema() AND c.column_name = 'organization_id' AND c.table_name <> 'organizations'
    LOOP
        EXECUTE format('DELETE FROM %I WHERE organization_id = $1', r.table_name) USING v_org;
        GET DIAGNOSTICS v_del = ROW_COUNT;
        IF v_del > 0 THEN RAISE NOTICE '  % : %', r.table_name, v_del; v_total := v_total + v_del; END IF;
    END LOOP;
    DELETE FROM organizations WHERE id = v_org;
    v_total := v_total + 1;

    -- 2) Usuarios de prueba.
    DELETE FROM application_users WHERE id IN (SELECT id FROM bpm_users_to_delete);
    GET DIAGNOSTICS v_del = ROW_COUNT;
    RAISE NOTICE '  application_users : %', v_del; v_total := v_total + v_del;

    -- 3) Hijos que quedaron sin padre (tablas sin organization_id), en rondas hasta que no quede nada.
    LOOP
        v_round := v_round + 1; v_n := 0;
        FOR r IN
            SELECT con.conrelid::regclass AS child, con.confrelid::regclass AS parent, con.conkey, con.confkey, con.conname
              FROM pg_constraint con JOIN pg_namespace ns ON ns.oid = con.connamespace
             WHERE con.contype = 'f' AND ns.nspname = current_schema()
        LOOP
            EXECUTE format(
                'DELETE FROM %s c WHERE %s AND NOT EXISTS (SELECT 1 FROM %s p WHERE %s)',
                r.child,
                (SELECT string_agg(format('c.%I IS NOT NULL', ca.attname), ' AND ')
                   FROM unnest(r.conkey) AS k(ck) JOIN pg_attribute ca ON ca.attrelid = r.child AND ca.attnum = k.ck),
                r.parent,
                (SELECT string_agg(format('p.%I = c.%I', pa.attname, ca.attname), ' AND ')
                   FROM unnest(r.conkey, r.confkey) AS k(ck, pk)
                   JOIN pg_attribute ca ON ca.attrelid = r.child AND ca.attnum = k.ck
                   JOIN pg_attribute pa ON pa.attrelid = r.parent AND pa.attnum = k.pk));
            GET DIAGNOSTICS v_del = ROW_COUNT;
            IF v_del > 0 THEN RAISE NOTICE '  % (huérfanos de %) : %', r.child, r.parent, v_del; v_n := v_n + v_del; END IF;
        END LOOP;
        v_total := v_total + v_n;
        EXIT WHEN v_n = 0 OR v_round > 20;
    END LOOP;
    RAISE NOTICE 'Total de filas borradas: %', v_total;
END $$;

SET LOCAL session_replication_role = origin;
\if :{?confirmar}
  SELECT :'confirmar' = 'SI' AS guardar \gset
\else
  \set guardar f
\endif
\if :guardar
  COMMIT;
  \echo 'Cambios GUARDADOS.'
\else
  ROLLBACK;
  \echo 'Simulación: no se guardó nada. Repite con -v confirmar=SI para borrar.'
\endif
