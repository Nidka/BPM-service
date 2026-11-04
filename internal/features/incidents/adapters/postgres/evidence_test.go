package postgres

import (
	"testing"
)

func TestEvidenceSharedWithChecklistOrCleaningIsExcluded(t *testing.T) {
	for _, target := range []string{"checklist", "cleaning"} {
		t.Run(target, func(t *testing.T) {
			ctx, pool, s, r := setupIncident(t)
			execSQL(t, ctx, pool, `INSERT INTO media_files(id,organization_id,location_id,object_key,storage_url,original_name,mime_type,size_bytes,status) VALUES($1,$2,$3,'test/photo','r2://test/photo','foto.jpg','image/jpeg',32,'AVAILABLE')`, uid(8, 1), org, site)
			execSQL(t, ctx, pool, `INSERT INTO incident_media(organization_id,incident_id,media_file_id,stage) VALUES($1,$2,$3,'BEFORE')`, org, r.ID, uid(8, 1))
			v, e := s.Detail(ctx, "chef", org, r.ID)
			if e != nil || v.BeforeCount != 1 {
				t.Fatal(v, e)
			}
			if target == "checklist" {
				execSQL(t, ctx, pool, `INSERT INTO checklist_templates(id,organization_id,name,area) VALUES($1,$2,'Plantilla','Cocina')`, uid(11, 1), org)
				execSQL(t, ctx, pool, `INSERT INTO checklist_template_versions(id,organization_id,template_id,version_number) VALUES($1,$2,$3,1)`, uid(12, 1), org, uid(11, 1))
				execSQL(t, ctx, pool, `INSERT INTO checklist_runs(id,organization_id,location_id,template_version_id,performed_by,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'chef',$5,NOW())`, uid(13, 1), org, site, uid(12, 1), uid(14, 1))
				execSQL(t, ctx, pool, `INSERT INTO checklist_media(organization_id,checklist_run_id,media_file_id) VALUES($1,$2,$3)`, org, uid(13, 1), uid(8, 1))
			} else {
				execSQL(t, ctx, pool, `INSERT INTO cleaning_plans(id,organization_id,location_id,area,task,frequency) VALUES($1,$2,$3,'Cocina','Limpiar','DAILY')`, uid(11, 1), org, site)
				execSQL(t, ctx, pool, `INSERT INTO cleaning_executions(id,organization_id,location_id,cleaning_plan_id,responsible_user_id,executed_at,result,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'worker',NOW(),'COMPLETED',$5,NOW())`, uid(12, 1), org, site, uid(11, 1), uid(13, 1))
				execSQL(t, ctx, pool, `INSERT INTO cleaning_media(organization_id,cleaning_execution_id,media_file_id) VALUES($1,$2,$3)`, org, uid(12, 1), uid(8, 1))
			}
			v, e = s.Detail(ctx, "chef", org, r.ID)
			if e != nil || v.BeforeCount != 0 {
				t.Fatal("foto compartida se contó", v, e)
			}
		})
	}
}
