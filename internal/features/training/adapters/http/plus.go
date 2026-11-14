package traininghttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

func itoa(n int) string { return strconv.Itoa(n) }

// registerPlus monta las rutas autenticadas de asignaciones, rutas, insignias y catálogo offline.
func (h *Handler) registerPlus(r chi.Router, base string) {
	r.Post(base+"/assignments", h.createAssignment)
	r.Get(base+"/assignments", h.listAssignments)
	r.Post(base+"/assignments/{assignmentID}/cancel", h.cancelAssignment)
	r.Get(base+"/me/assignments", h.myAssignments)
	r.Get(base+"/me/achievements", h.myAchievements)
	r.Get(base+"/me/offline-catalog", h.offlineCatalog)
	r.Get(base+"/achievements", h.listAchievements)
	r.Put(base+"/achievements/{achievementID}", h.updateAchievement)
	r.Post(base+"/paths", h.createPath)
	r.Get(base+"/paths", h.listPaths)
	r.Get(base+"/paths/{pathID}", h.getPath)
	r.Put(base+"/paths/{pathID}", h.updatePath)
	r.Post(base+"/paths/{pathID}/publish", h.publishPath)
	r.Post(base+"/paths/{pathID}/archive", h.archivePath)
}

// RegisterPublic monta la verificación pública de constancias, sin autenticación
// y con el limitador de solicitudes recibido (por IP).
func (h *Handler) RegisterPublic(r chi.Router, limit func(http.Handler) http.Handler) {
	r.With(limit).Get("/public/training-certificates/{verificationCode}", h.verifyCertificate)
}

// failPlus traduce los errores nuevos; devuelve false si no son suyos.
func (h *Handler) failPlus(w http.ResponseWriter, id string, err error) bool {
	notFound := func(code, message string) {
		response.Error(w, 404, id, response.APIError{Code: code, Message: message})
	}
	switch {
	case errors.Is(err, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
	case errors.Is(err, domain.ErrCourseNotPublished):
		response.Error(w, 422, id, response.APIError{Code: "TRAINING_COURSE_NOT_PUBLISHED", Message: "Solo se asignan cursos publicados."})
	case errors.Is(err, domain.ErrAssigneeNotFound):
		notFound("TRAINING_ASSIGNEE_NOT_FOUND", "La persona no pertenece a esta empresa.")
	case errors.Is(err, domain.ErrLocationNotFound):
		notFound("TRAINING_LOCATION_NOT_FOUND", "La sede no existe en esta empresa.")
	case errors.Is(err, domain.ErrAssignmentNotFound):
		notFound("TRAINING_ASSIGNMENT_NOT_FOUND", "La asignación no existe en esta empresa.")
	case errors.Is(err, domain.ErrAssignmentExists):
		response.Error(w, 409, id, response.APIError{Code: "TRAINING_ASSIGNMENT_EXISTS", Message: "Ya existe una asignación activa igual para este curso y destinatario."})
	case errors.Is(err, domain.ErrCertificateNotFound):
		notFound("TRAINING_CERTIFICATE_NOT_FOUND", "No se encontró una constancia con ese código.")
	case errors.Is(err, domain.ErrAchievementNotFound):
		notFound("TRAINING_ACHIEVEMENT_NOT_FOUND", "La insignia no existe en esta empresa.")
	case errors.Is(err, domain.ErrPathNotFound):
		notFound("TRAINING_PATH_NOT_FOUND", "La ruta de aprendizaje no está disponible en esta empresa.")
	case errors.Is(err, domain.ErrPathConflict):
		response.Error(w, 409, id, response.APIError{Code: "TRAINING_PATH_CONFLICT", Message: "La ruta cambió. Recárgala antes de guardar."})
	case errors.Is(err, domain.ErrPathArchived):
		response.Error(w, 409, id, response.APIError{Code: "TRAINING_PATH_ARCHIVED", Message: "La ruta está archivada y no admite cambios."})
	default:
		return false
	}
	return true
}

// listQuery separa los filtros permitidos de los parámetros de paginación y rechaza
// cualquier parámetro repetido o desconocido.
func listQuery(r *http.Request, filters ...string) (map[string]string, pagination.Request, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, pagination.Request{}, pagination.ErrInvalid
	}
	out := map[string]string{}
	for _, name := range filters {
		if values, ok := query[name]; ok {
			if len(values) != 1 {
				return nil, pagination.Request{}, pagination.ErrInvalid
			}
			out[name] = values[0]
			query.Del(name)
		}
	}
	page, err := request.Pagination(query.Encode(), "pageSize", "pageToken")
	return out, page, err
}

func pageResponse(w http.ResponseWriter, items any, next string) {
	response.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"items": items}, "pagination": map[string]string{"nextPageToken": next}})
}

// createAssignment: POST /training/assignments
func (h *Handler) createAssignment(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body assignmentBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	out, err := h.service.CreateAssignment(r.Context(), user, org, body.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	data(w, status, map[string]any{"assignment": assignment(out.Assignment)})
}

// listAssignments: GET /training/assignments?status&courseId&pageSize&pageToken
func (h *Handler) listAssignments(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	filters, page, err := listQuery(r, "status", "courseId")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result, err := h.service.ListAssignments(r.Context(), user, org, domain.AssignmentFilter{Status: filters["status"], CourseID: filters["courseId"]}, page)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]assignmentDTO, 0, len(result.Items))
	for _, a := range result.Items {
		items = append(items, assignment(a))
	}
	pageResponse(w, items, result.NextToken)
}

// cancelAssignment: POST /training/assignments/{assignmentID}/cancel
func (h *Handler) cancelAssignment(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	out, err := h.service.CancelAssignment(r.Context(), user, org, chi.URLParam(r, "assignmentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"assignment": assignment(out)})
}

// myAssignments: GET /training/me/assignments
func (h *Handler) myAssignments(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	_, page, err := listQuery(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result, err := h.service.MyAssignments(r.Context(), user, org, page)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]myAssignmentDTO, 0, len(result.Items))
	for _, m := range result.Items {
		items = append(items, myAssignment(m))
	}
	pageResponse(w, items, result.NextToken)
}

// myAchievements: GET /training/me/achievements
func (h *Handler) myAchievements(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	out, err := h.service.MyAchievements(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, myAchievements(out))
}

// offlineCatalog: GET /training/me/offline-catalog (con If-None-Match → 304)
func (h *Handler) offlineCatalog(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	out, err := h.service.OfflineCatalog(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	etag := `"` + out.ETag + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	data(w, http.StatusOK, offlineCatalog(out))
}

// listAchievements: GET /training/achievements (administración)
func (h *Handler) listAchievements(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	items, err := h.service.AchievementCatalog(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"items": achievementList(items)})
}

// updateAchievement: PUT /training/achievements/{achievementID}
func (h *Handler) updateAchievement(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body achievementUpdateBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	if body.Active == nil {
		h.fail(w, r, &domain.ValidationError{Fields: map[string]string{"active": "Indica si la insignia está activa."}})
		return
	}
	update := domain.AchievementUpdate{Name: body.Name, Description: body.Description, Points: -1, Threshold: 0}
	if body.Points != nil {
		update.Points = *body.Points
	}
	if body.CriterionThreshold != nil {
		update.Threshold = *body.CriterionThreshold
	}
	if body.Active != nil {
		update.Active = *body.Active
	}
	out, err := h.service.UpdateAchievement(r.Context(), user, org, chi.URLParam(r, "achievementID"), update)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"achievement": achievement(out)})
}

func (b pathBody) domain() domain.PathInput {
	in := domain.PathInput{ID: b.ID, Title: b.Title, Description: b.Description, RowVersion: int(b.RowVersion)}
	if b.CourseIDs != nil {
		in.CourseIDs = append([]string{}, (*b.CourseIDs)...)
	}
	return in
}

// createPath: POST /training/paths
func (h *Handler) createPath(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body pathBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	out, err := h.service.CreatePath(r.Context(), user, org, body.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	data(w, status, map[string]any{"path": path(out)})
}

// updatePath: PUT /training/paths/{pathID}
func (h *Handler) updatePath(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body pathBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	out, err := h.service.UpdatePath(r.Context(), user, org, chi.URLParam(r, "pathID"), body.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"path": path(out)})
}

// publishPath y archivePath: POST /training/paths/{pathID}/publish|archive
func (h *Handler) publishPath(w http.ResponseWriter, r *http.Request) { h.pathTransition(w, r, true) }
func (h *Handler) archivePath(w http.ResponseWriter, r *http.Request) { h.pathTransition(w, r, false) }

func (h *Handler) pathTransition(w http.ResponseWriter, r *http.Request, publish bool) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	call := h.service.ArchivePath
	if publish {
		call = h.service.PublishPath
	}
	out, err := call(r.Context(), user, org, chi.URLParam(r, "pathID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"path": path(out)})
}

// getPath: GET /training/paths/{pathID}
func (h *Handler) getPath(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	out, err := h.service.Path(r.Context(), user, org, chi.URLParam(r, "pathID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"path": path(out)})
}

// listPaths: GET /training/paths?status&pageSize&pageToken
func (h *Handler) listPaths(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	filters, page, err := listQuery(r, "status")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result, err := h.service.ListPaths(r.Context(), user, org, filters["status"], page)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]pathDTO, 0, len(result.Items))
	for _, p := range result.Items {
		items = append(items, path(p))
	}
	pageResponse(w, items, result.NextToken)
}

// verifyCertificate: GET /public/training-certificates/{verificationCode}
func (h *Handler) verifyCertificate(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	out, err := h.service.VerifyCertificate(r.Context(), chi.URLParam(r, "verificationCode"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	noStore(w)
	response.JSON(w, http.StatusOK, map[string]any{"data": publicCertificate(out)})
}
