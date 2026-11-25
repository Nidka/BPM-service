package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/catalog"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// correctPicks devuelve, para una evaluación sorteada, la posición mostrada de la alternativa correcta de cada pregunta.
func correctPicks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, drawID string) []domain.Answer {
	t.Helper()
	var answers []domain.Answer
	err := New(pool).Within(ctx, func(db application.Store) error {
		draw, found, err := db.FindDraw(ctx, org, drawID)
		if err != nil || !found {
			t.Fatal("sorteo no encontrado", err)
		}
		questions, err := db.Questions(ctx, org, draw.VersionID)
		if err != nil {
			return err
		}
		byID := map[string]domain.Question{}
		for _, q := range questions {
			byID[q.ID] = q
		}
		for _, item := range draw.Items {
			for shown, original := range item.OptionOrder {
				if original == byID[item.QuestionID].CorrectOption {
					answers = append(answers, domain.Answer{QuestionID: item.QuestionID, SelectedOption: shown})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return answers
}

func bankAttempt(version, id, op, kind string, answers []domain.Answer) domain.AttemptInput {
	return domain.AttemptInput{ID: id, ClientOperationID: op, VersionID: version, AttemptType: kind, StartedAt: time.Now().Add(-time.Minute), ClientCreatedAt: time.Now(), Answers: answers}
}

func codesOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, versionID string, ids ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT id::text,item_code||'|'||bloom_level FROM training_questions WHERE course_version_id=$1 AND id::text=ANY($2)`, versionID, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, v string
		if err = rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		out[id] = v
	}
	return out
}

func TestBancoOficialInstalacionPracticaEvaluacionYRotacion(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})

	// Simulación: no escribe nada.
	if report, err := s.InstallBPMCatalog(ctx, org, "admin", true); err != nil || len(report) != 5 {
		t.Fatal(report, err)
	}
	var courses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_courses`).Scan(&courses); err != nil || courses != 0 {
		t.Fatal("la simulación escribió cursos", courses, err)
	}
	report, err := s.InstallBPMCatalog(ctx, org, "admin", false)
	if err != nil || len(report) != 5 {
		t.Fatal(report, err)
	}
	for _, r := range report {
		if !r.Created || r.Skipped || r.Items != 20 || r.Units < 3 || r.Units > 4 {
			t.Fatalf("instalación inesperada %+v", r)
		}
	}
	// Idempotente: repetirla no cambia nada.
	again, err := s.InstallBPMCatalog(ctx, org, "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if !r.Skipped {
			t.Fatalf("la segunda instalación debe omitirse: %+v", r)
		}
	}
	var questions, bank, foreignCourses int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM training_questions),(SELECT count(*) FROM training_questions WHERE item_code IS NOT NULL AND bank_version=1 AND explanation IS NOT NULL),(SELECT count(*) FROM training_courses WHERE organization_id=$1)`, foreign).Scan(&questions, &bank, &foreignCourses); err != nil || questions != 100 || bank != 100 || foreignCourses != 0 {
		t.Fatal("conteos del banco", questions, bank, foreignCourses, err)
	}

	list, err := s.ListCourses(ctx, "admin", org, "")
	if err != nil || len(list) != 5 {
		t.Fatal(list, err)
	}
	var m01 string
	for _, c := range list {
		if c.Code == "BPM-M01" {
			m01 = c.ID
		}
	}
	view, err := s.Course(ctx, "worker", org, m01)
	if err != nil || view.Bank == nil || view.Bank.Version != 1 || view.Bank.PoolSize != 20 || len(view.Lessons) != 4 || len(view.Questions) != 20 || view.Version.PassingScore != 80 {
		t.Fatal(view.Bank, len(view.Lessons), len(view.Questions), err)
	}
	if view.Review.DueCount != 0 {
		t.Fatal("sin práctica no hay repaso", view.Review)
	}

	// Práctica: un fallo deja el ítem en repaso y da retroalimentación con fuentes; el primer acierto da XP una sola vez.
	var target domain.PublicQuestion
	for _, q := range view.Questions {
		target = q
		break
	}
	var wrongOption, rightOption int
	err = pool.QueryRow(ctx, `SELECT correct_option,(correct_option+1)%5 FROM training_questions WHERE id=$1`, target.ID).Scan(&rightOption, &wrongOption)
	if err != nil {
		t.Fatal(err)
	}
	miss, err := s.StudyAnswer(ctx, "worker", org, m01, target.ID, wrongOption)
	if err != nil || miss.Correct || miss.ItemCode == "" || len(miss.Sources) == 0 || miss.XP != 0 {
		t.Fatal(miss, err)
	}
	view, _ = s.Course(ctx, "worker", org, m01)
	if view.Review.DueCount != 1 || len(view.Review.QuestionIDs) != 1 || view.Review.QuestionIDs[0] != target.ID {
		t.Fatal("el ítem fallado debe quedar en repaso", view.Review)
	}
	hit, err := s.StudyAnswer(ctx, "worker", org, m01, target.ID, rightOption)
	if err != nil || !hit.Correct || hit.XP != domain.PracticeXP {
		t.Fatal(hit, err)
	}
	if hit, _ = s.StudyAnswer(ctx, "worker", org, m01, target.ID, rightOption); hit.XP != 0 {
		t.Fatal("el XP de práctica se da una sola vez por ítem", hit.XP)
	}
	view, _ = s.Course(ctx, "worker", org, m01)
	if view.Review.DueCount != 0 {
		t.Fatal("acertar saca el ítem del repaso", view.Review)
	}
	var practiceXP int
	if err = pool.QueryRow(ctx, `SELECT COALESCE(sum(experience_points),0) FROM training_practice_results WHERE user_id='worker'`).Scan(&practiceXP); err != nil || practiceXP != domain.PracticeXP {
		t.Fatal("XP de práctica", practiceXP, err)
	}
	// SUPER_ADMIN sin membresía practica sin dejar rastro.
	if _, err = s.StudyAnswer(ctx, "super", org, m01, target.ID, rightOption); err != nil {
		t.Fatal(err)
	}

	// Sin sorteo no hay intento; el sorteo de otra persona u organización tampoco sirve.
	if _, err = s.SubmitAttempt(ctx, "worker", org, m01, bankAttempt(view.Version.ID, uid(7, 1), uid(8, 1), "FINAL", nil)); !errors.Is(err, domain.ErrEvaluationNotStarted) {
		t.Fatal("un curso con banco exige iniciar la evaluación", err)
	}
	if _, err = s.StartEvaluation(ctx, "other", foreign, m01, application.EvaluationInput{ID: uid(7, 1), AttemptType: "FINAL"}); !errors.Is(err, domain.ErrCourseNotFound) {
		t.Fatal("otra organización no sortea el curso", err)
	}
	if _, err = s.StartEvaluation(ctx, "worker", org, m01, application.EvaluationInput{ID: uid(7, 1), AttemptType: "MODULE"}); err == nil {
		t.Fatal("MODULE no se sortea")
	}

	// Primera evaluación: la selección recomendada del documento, con alternativas barajadas.
	first, err := s.StartEvaluation(ctx, "worker", org, m01, application.EvaluationInput{ID: uid(7, 1), AttemptType: "FINAL"})
	if err != nil || len(first.Questions) != 5 || first.AttemptNumber != 1 || first.BankVersion != 1 || first.Replayed {
		t.Fatal(first, err)
	}
	ids := make([]string, len(first.Questions))
	for i, q := range first.Questions {
		ids[i] = q.ID
	}
	got := map[string]bool{}
	for _, v := range codesOf(t, ctx, pool, first.VersionID, ids...) {
		got[v[:6]] = true
	}
	bankData, _ := catalog.MustLoad()
	for _, code := range bankData.Recommended("M01") {
		if !got[code] {
			t.Fatalf("la primera evaluación debe incluir %s: %v", code, got)
		}
	}
	replayed, err := s.StartEvaluation(ctx, "worker", org, m01, application.EvaluationInput{ID: uid(7, 1), AttemptType: "FINAL"})
	if err != nil || !replayed.Replayed || replayed.Questions[0].ID != first.Questions[0].ID || replayed.Questions[0].Options[0] != first.Questions[0].Options[0] {
		t.Fatal("repetir el inicio devuelve el mismo sorteo", err)
	}
	if _, err = s.StartEvaluation(ctx, "chef", org, m01, application.EvaluationInput{ID: uid(7, 1), AttemptType: "FINAL"}); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatal("otra persona no reutiliza el sorteo", err)
	}

	right := correctPicks(t, ctx, pool, uid(7, 1))
	submission := bankAttempt(first.VersionID, uid(7, 1), uid(8, 1), "FINAL", right)
	outcome, err := s.SubmitAttempt(ctx, "worker", org, m01, submission)
	if err != nil || !outcome.Passed || outcome.Score != 100 || outcome.Certificate == nil || len(outcome.Results) != 5 {
		t.Fatal(outcome, err)
	}
	for _, r := range outcome.Results {
		if r.ItemCode == "" || r.BankVersion != 1 || len(r.Sources) == 0 || len(r.Options) != 5 || r.SelectedOption != r.CorrectOption || r.QuestionText == "" {
			t.Fatalf("resultado del banco incompleto: %+v", r)
		}
	}
	var items, versions, orders int
	if err = pool.QueryRow(ctx, `SELECT count(item_code),count(bank_version),count(option_order) FROM training_attempt_answers WHERE attempt_id=$1`, uid(7, 1)).Scan(&items, &versions, &orders); err != nil || items != 5 || versions != 5 || orders != 5 {
		t.Fatal("cada respuesta guarda ítem, versión y orden", items, versions, orders, err)
	}
	// La selección guardada usa el índice original, no la posición mostrada.
	var storedOK int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM training_attempt_answers a JOIN training_questions q ON q.id=a.question_id WHERE a.attempt_id=$1 AND a.selected_option=q.correct_option AND a.is_correct`, uid(7, 1)).Scan(&storedOK); err != nil || storedOK != 5 {
		t.Fatal("selected_option debe ser el índice original", storedOK, err)
	}
	replay, err := s.SubmitAttempt(ctx, "worker", org, m01, submission)
	if err != nil || !replay.Replayed || replay.Score != 100 {
		t.Fatal(replay, err)
	}
	for i, r := range replay.Results {
		if r.ItemCode != outcome.Results[i].ItemCode || r.SelectedOption != outcome.Results[i].SelectedOption || r.CorrectOption != outcome.Results[i].CorrectOption || r.Options[r.CorrectOption] != outcome.Results[i].Options[outcome.Results[i].CorrectOption] {
			t.Fatalf("la repetición reconstruye otro orden: %+v vs %+v", r, outcome.Results[i])
		}
	}

	// Segunda evaluación: rota (ningún ítem repetido) y balancea por Bloom; 3/5 no aprueba.
	second, err := s.StartEvaluation(ctx, "worker", org, m01, application.EvaluationInput{ID: uid(7, 2), AttemptType: "FINAL"})
	if err != nil || len(second.Questions) != 5 || second.AttemptNumber != 2 {
		t.Fatal(second, err)
	}
	ids = ids[:0]
	for _, q := range second.Questions {
		ids = append(ids, q.ID)
	}
	levels := map[string]int{}
	for _, v := range codesOf(t, ctx, pool, second.VersionID, ids...) {
		for _, code := range bankData.Recommended("M01") {
			if v[:6] == code {
				t.Fatalf("la rotación repitió %s", code)
			}
		}
		levels[v[7:]]++
	}
	if levels["Aplicar"] != 2 || levels["Analizar"] != 1 || levels["Evaluar"] != 1 || levels["Comprender"] != 1 {
		t.Fatalf("mezcla de Bloom %v", levels)
	}
	picks := correctPicks(t, ctx, pool, uid(7, 2))
	picks[0].SelectedOption, picks[1].SelectedOption = (picks[0].SelectedOption+1)%5, (picks[1].SelectedOption+1)%5
	failed, err := s.SubmitAttempt(ctx, "worker", org, m01, bankAttempt(second.VersionID, uid(7, 2), uid(8, 2), "FINAL", picks))
	if err != nil || failed.Passed || failed.Score != 60 {
		t.Fatal(failed, err)
	}
	// Una respuesta ajena al sorteo o faltante se rechaza completa.
	if _, err = s.StartEvaluation(ctx, "worker", org, m01, application.EvaluationInput{ID: uid(7, 3), AttemptType: "DIAGNOSTIC"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitAttempt(ctx, "worker", org, m01, bankAttempt(second.VersionID, uid(7, 3), uid(8, 3), "DIAGNOSTIC", correctPicks(t, ctx, pool, uid(7, 3))[:4])); err == nil {
		t.Fatal("faltan respuestas")
	}
	var answersOfRejected int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM training_attempt_answers WHERE attempt_id=$1`, uid(7, 3)).Scan(&answersOfRejected); err != nil || answersOfRejected != 0 {
		t.Fatal("un intento rechazado no guarda respuestas", answersOfRejected, err)
	}

	// Banco para administración: solo ADMIN, con clave y justificación.
	if _, err = s.Bank(ctx, "worker", org, m01); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("el personal no ve el banco", err)
	}
	adminBank, err := s.Bank(ctx, "admin", org, m01)
	if err != nil || len(adminBank.Items) != 20 || adminBank.Module != "M01" {
		t.Fatal(adminBank.Module, len(adminBank.Items), err)
	}
	recommended := 0
	for _, item := range adminBank.Items {
		if item.Recommended {
			recommended++
		}
		if item.Question.Explanation == "" || len(item.Question.Sources) == 0 || item.LessonTitle == "" {
			t.Fatalf("ítem de banco incompleto %+v", item)
		}
	}
	if recommended != 5 {
		t.Fatal("recomendados", recommended)
	}

	// El borrador no permite editar ítems oficiales: se conservan texto, clave y metadatos.
	draft, err := s.Draft(ctx, "admin", org, m01)
	if err != nil {
		t.Fatal(err)
	}
	tampered := domain.DraftInput{PassingScore: draft.Version.PassingScore, RowVersion: draft.Version.RowVersion, Lessons: draft.Lessons, Questions: append([]domain.Question(nil), draft.Questions...)}
	original := tampered.Questions[0]
	tampered.Questions[0].Text, tampered.Questions[0].CorrectOption, tampered.Questions[0].ItemCode = "Texto alterado", (original.CorrectOption+1)%5, ""
	saved, err := s.SaveDraft(ctx, "admin", org, m01, tampered)
	if err != nil {
		t.Fatal(err)
	}
	if q := saved.Questions[0]; q.Text != original.Text || q.CorrectOption != original.CorrectOption || q.ItemCode != original.ItemCode || q.Bloom != original.Bloom || len(q.Sources) == 0 {
		t.Fatalf("el ítem oficial cambió desde el borrador: %+v", q)
	}
	var fromDraft int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM training_questions WHERE course_version_id=$1 AND item_code IS NOT NULL`, saved.Version.ID).Scan(&fromDraft); err != nil || fromDraft != 20 {
		t.Fatal("el borrador conserva los 20 ítems", fromDraft, err)
	}
}

func TestInstalarCatalogoConservaElVideoYArchivaLaVersionAnterior(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	video := uid(5, 1)
	if _, err := pool.Exec(ctx, `INSERT INTO media_files(id,organization_id,object_key,storage_url,original_name,mime_type,size_bytes,sha256,status) VALUES($1,$2,$3,'x','v.mp4','video/mp4',10,repeat('a',64),'AVAILABLE')`,
		video, org, "organizations/"+org+"/training/videos/"+video+".mp4"); err != nil {
		t.Fatal(err)
	}
	courseID, oldVersion, _ := publish(t, ctx, s, "BPM-M01", video)
	// Un borrador pendiente del curso se reemplaza por el contenido oficial al instalar.
	if _, err := s.Draft(ctx, "admin", org, courseID); err != nil {
		t.Fatal(err)
	}
	report, err := s.InstallBPMCatalog(ctx, org, "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	var m01 application.InstalledCourse
	for _, r := range report {
		if r.CourseCode == "BPM-M01" {
			m01 = r
		}
	}
	if m01.Created || m01.Skipped || !m01.VideoKept || m01.VersionNumber < 2 {
		t.Fatalf("instalación sobre curso existente: %+v", m01)
	}
	view, err := s.Course(ctx, "worker", org, courseID)
	if err != nil || view.Version.ID == oldVersion || len(view.Lessons) != 4 || view.Lessons[0].Video == nil || view.Lessons[0].Video.MediaID != video || view.Lessons[1].Video != nil {
		t.Fatal("el video queda en la primera unidad", view.Lessons, err)
	}
	var archived string
	if err = pool.QueryRow(ctx, `SELECT status FROM training_course_versions WHERE id=$1`, oldVersion).Scan(&archived); err != nil || archived != "ARCHIVED" {
		t.Fatal("la versión anterior queda archivada", archived, err)
	}
	// El banco se instala solo en la organización pedida.
	var other int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM training_courses WHERE organization_id=$1`, foreign).Scan(&other); err != nil || other != 0 {
		t.Fatal("otra organización quedó afectada", other, err)
	}
}

// catalogVideos simula R2: devuelve, para la clave pedida, exactamente el objeto aprobado del catálogo (salvo los
// que se marcan como ausentes o alterados).
type catalogVideos struct {
	videos
	missing, altered map[string]bool
}

func (c catalogVideos) InspectVideo(_ context.Context, key string) (contracts.VideoInspection, error) {
	_, courses := catalog.MustLoad()
	for _, m := range courses.Modules {
		if m.Video.ObjectKey != key {
			continue
		}
		if c.missing[key] {
			return contracts.VideoInspection{}, contracts.ErrObjectNotFound
		}
		in := contracts.VideoInspection{ContentType: m.Video.MIMEType, ContentLength: m.Video.SizeBytes, SHA256Hex: m.Video.SHA256, Header: []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p'}}
		if c.altered[key] {
			in.SHA256Hex = strings.Repeat("0", 64)
		}
		return in, nil
	}
	return contracts.VideoInspection{}, contracts.ErrObjectNotFound
}

func TestCatalogoBaseComparteVideosDeLaPlataformaYMantieneElTexto(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	_, courses := catalog.MustLoad()
	storage := catalogVideos{missing: map[string]bool{courses.Modules[4].Video.ObjectKey: true}, altered: map[string]bool{courses.Modules[1].Video.ObjectKey: true}}
	s := application.New(New(pool), storage, domain.VideoPolicy{MaxBytes: 1 << 20})

	// Dos organizaciones comparten los mismos 5 videos de la plataforma.
	for _, id := range []string{org, foreign} {
		if _, err := s.InstallBPMCatalog(ctx, id, "admin", false); err != nil {
			t.Fatal(id, err)
		}
	}
	var videosRows, lessonsWithVideo int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM platform_training_videos),(SELECT count(*) FROM training_lessons WHERE platform_video_id IS NOT NULL)`).Scan(&videosRows, &lessonsWithVideo); err != nil || videosRows != 5 || lessonsWithVideo != 10 {
		t.Fatal("5 videos compartidos por 10 lecciones", videosRows, lessonsWithVideo, err)
	}
	var m01 string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM training_courses WHERE organization_id=$1 AND code='BPM-M01'`, org).Scan(&m01); err != nil {
		t.Fatal(err)
	}
	// Hasta verificar el objeto, el video no se muestra ni se firma.
	view, err := s.Course(ctx, "worker", org, m01)
	if err != nil || view.Lessons[0].Video != nil {
		t.Fatal("un video sin verificar no se muestra", err)
	}
	if _, err = s.LessonVideo(ctx, "worker", org, m01, view.Lessons[0].ID); !errors.Is(err, domain.ErrVideoNotFound) {
		t.Fatal(err)
	}
	results, err := s.VerifyPlatformVideos(ctx)
	if err != nil || len(results) != 5 {
		t.Fatal(results, err)
	}
	status := map[string]string{}
	for _, r := range results {
		status[r.Code] = r.Status
	}
	if status["BPM-M01-V1"] != "AVAILABLE" || status["BPM-M02-V1"] != "FAILED" || status["BPM-M05-V1"] != "PENDING_UPLOAD" {
		t.Fatalf("verificación inesperada %v", status)
	}
	view, err = s.Course(ctx, "worker", org, m01)
	if err != nil || view.Lessons[0].Video == nil || view.Lessons[0].Video.MIMEType != "video/mp4" || view.Lessons[1].Video != nil {
		t.Fatal("el video verificado aparece solo en la primera unidad", err)
	}
	access, err := s.LessonVideo(ctx, "worker", org, m01, view.Lessons[0].ID)
	if err != nil || access.URL == "" || access.MIMEType != "video/mp4" {
		t.Fatal(access, err)
	}
	// La otra organización ve el mismo video; el módulo con el objeto alterado no se muestra.
	var otherM01, otherM02 string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM training_courses WHERE organization_id=$1 AND code='BPM-M01'`, foreign).Scan(&otherM01)
	_ = pool.QueryRow(ctx, `SELECT id::text FROM training_courses WHERE organization_id=$1 AND code='BPM-M02'`, org).Scan(&otherM02)
	if v, e := s.Course(ctx, "other", foreign, otherM01); e != nil || v.Lessons[0].Video == nil || v.Lessons[0].Video.MediaID != view.Lessons[0].Video.MediaID {
		t.Fatal("el video de plataforma es el mismo para todas las organizaciones", e)
	}
	if v, e := s.Course(ctx, "worker", org, otherM02); e != nil || v.Lessons[0].Video != nil {
		t.Fatal("un objeto alterado no se muestra", e)
	}
	// Verificar de nuevo es idempotente.
	if _, err = s.VerifyPlatformVideos(ctx); err != nil {
		t.Fatal(err)
	}

	// El texto del catálogo se mantiene al día aunque el contenido ya esté instalado, sin republicar.
	if _, err = pool.Exec(ctx, `UPDATE training_courses SET description='antigua' WHERE organization_id=$1 AND code='BPM-M01'`, org); err != nil {
		t.Fatal(err)
	}
	var before int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM training_course_versions WHERE organization_id=$1`, org).Scan(&before)
	report, err := s.InstallBPMCatalog(ctx, org, "admin", false)
	if err != nil || !report[0].Skipped {
		t.Fatal(report, err)
	}
	var description string
	var after int
	_ = pool.QueryRow(ctx, `SELECT description FROM training_courses WHERE organization_id=$1 AND code='BPM-M01'`, org).Scan(&description)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM training_course_versions WHERE organization_id=$1`, org).Scan(&after)
	if description != courses.Modules[0].Description || before != after {
		t.Fatal("la descripción se actualiza sin crear versiones", description, before, after)
	}

	// El borrador de un curso con video de plataforma lo conserva si el cliente no envía otro video.
	draft, err := s.Draft(ctx, "admin", org, m01)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Lessons[0].PlatformVideoID == "" {
		t.Fatal("el borrador copia el video de plataforma")
	}
	input := domain.DraftInput{PassingScore: draft.Version.PassingScore, RowVersion: draft.Version.RowVersion, Questions: draft.Questions}
	for _, l := range draft.Lessons {
		l.PlatformVideoID = ""
		input.Lessons = append(input.Lessons, l)
	}
	saved, err := s.SaveDraft(ctx, "admin", org, m01, input)
	if err != nil || saved.Lessons[0].PlatformVideoID == "" || saved.Lessons[1].PlatformVideoID != "" {
		t.Fatal("el servidor conserva el video de plataforma de la lección", err)
	}
}
