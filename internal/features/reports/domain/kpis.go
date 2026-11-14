package domain

import "time"

// WindowStat es el cumplimiento en una ventana de días locales.
type WindowStat struct {
	Average *float64
	Runs    int
}

// PendingChecks cuenta controles diarios programados para hoy (fecha local de cada sede).
type PendingChecks struct{ Assigned, Pending, Overdue int }

// TrainingKPI resume el avance de capacitación de la empresa.
type TrainingKPI struct {
	PublishedCourses      int
	Learners              int
	CompletedEnrollments  int
	InProgressEnrollments int
	CompletionPercent     float64
	AverageBestScore      *float64
}

// KPIs es el panel de indicadores clave (RF-30). Training es nil para CHEF.
type KPIs struct {
	GeneratedAt   time.Time
	Today         WindowStat
	Last7Days     WindowStat
	Last30Days    WindowStat
	Pending       PendingChecks
	OpenAlerts    int
	OpenIncidents int
	Training      *TrainingKPI
}

// TrainingCourseStat es el avance de un curso publicado (RF-09).
type TrainingCourseStat struct {
	CourseID, Code, Title string
	Assignable            int
	Enrolled              int
	InProgress            int
	Completed             int
	CompletionPercent     float64
	AverageBestScore      *float64
}

// TrainingPerson es el avance de una persona sobre los cursos publicados.
type TrainingPerson struct {
	UserID, DisplayName, Role string
	PublishedCourses          int
	Completed                 int
	InProgress                int
	AverageBestScore          *float64
	LastActivityAt            *time.Time
}

// CompletionPercent calcula completados sobre asignables con dos decimales.
func CompletionPercent(completed, assignable int) float64 {
	if assignable <= 0 {
		return 0
	}
	return Round2(float64(completed) * 100 / float64(assignable))
}
