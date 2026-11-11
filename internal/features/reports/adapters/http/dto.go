package reportshttp

import (
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
)

// iso formatea instantes en ISO-8601 UTC.
func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func date(t time.Time) string { return t.Format(domain.DateLayout) }

type dayDTO struct {
	Date              string   `json:"date"`
	Runs              int      `json:"runs"`
	AverageCompliance *float64 `json:"averageCompliance"`
}

type locationComplianceDTO struct {
	LocationID        string   `json:"locationId"`
	LocationName      string   `json:"locationName"`
	Runs              int      `json:"runs"`
	AverageCompliance *float64 `json:"averageCompliance"`
	Days              []dayDTO `json:"days"`
}

type complianceDTO struct {
	From              string                  `json:"from"`
	To                string                  `json:"to"`
	Runs              int                     `json:"runs"`
	AverageCompliance *float64                `json:"averageCompliance"`
	Days              []dayDTO                `json:"days"`
	Locations         []locationComplianceDTO `json:"locations"`
}

func days(in []domain.DayStat) []dayDTO {
	out := make([]dayDTO, 0, len(in))
	for _, d := range in {
		out = append(out, dayDTO{d.Date, d.Runs, d.Average})
	}
	return out
}

func complianceOf(r domain.ComplianceReport) complianceDTO {
	out := complianceDTO{From: r.From, To: r.To, Runs: r.Runs, AverageCompliance: r.Average, Days: days(r.Days), Locations: []locationComplianceDTO{}}
	for _, l := range r.Locations {
		out.Locations = append(out.Locations, locationComplianceDTO{l.LocationID, l.LocationName, l.Runs, l.Average, days(l.Days)})
	}
	return out
}

type summaryLocationDTO struct {
	LocationID              string   `json:"locationId"`
	LocationName            string   `json:"locationName"`
	Runs                    int      `json:"runs"`
	AverageCompliance       *float64 `json:"averageCompliance"`
	IncidentsReported       int      `json:"incidentsReported"`
	IncidentsClosed         int      `json:"incidentsClosed"`
	IncidentsOpenNow        int      `json:"incidentsOpenNow"`
	TemperatureMeasurements int      `json:"temperatureMeasurements"`
	TemperatureOutOfRange   int      `json:"temperatureOutOfRange"`
}

type areaDTO struct {
	Area              string   `json:"area"`
	Runs              int      `json:"runs"`
	AverageCompliance *float64 `json:"averageCompliance"`
}

type templateDTO struct {
	TemplateID        string   `json:"templateId"`
	Name              string   `json:"name"`
	Area              string   `json:"area"`
	Runs              int      `json:"runs"`
	AverageCompliance *float64 `json:"averageCompliance"`
}

type summaryDTO struct {
	From              string               `json:"from"`
	To                string               `json:"to"`
	Runs              int                  `json:"runs"`
	AverageCompliance *float64             `json:"averageCompliance"`
	Locations         []summaryLocationDTO `json:"locations"`
	Areas             []areaDTO            `json:"areas"`
	Templates         []templateDTO        `json:"templates"`
	Incidents         struct {
		Reported int `json:"reported"`
		Closed   int `json:"closed"`
		OpenNow  int `json:"openNow"`
	} `json:"incidents"`
	Temperatures struct {
		Measurements int `json:"measurements"`
		OutOfRange   int `json:"outOfRange"`
	} `json:"temperatures"`
}

func summaryOf(s domain.Summary) summaryDTO {
	out := summaryDTO{From: s.From, To: s.To, Runs: s.Runs, AverageCompliance: s.Average,
		Locations: []summaryLocationDTO{}, Areas: []areaDTO{}, Templates: []templateDTO{}}
	for _, l := range s.Locations {
		out.Locations = append(out.Locations, summaryLocationDTO{l.LocationID, l.LocationName, l.Runs, l.Average, l.IncidentsReported, l.IncidentsClosed, l.IncidentsOpenNow, l.TemperatureMeasurements, l.TemperatureOutOfRange})
	}
	for _, a := range s.Areas {
		out.Areas = append(out.Areas, areaDTO{a.Area, a.Runs, a.Average})
	}
	for _, t := range s.Templates {
		out.Templates = append(out.Templates, templateDTO{t.TemplateID, t.Name, t.Area, t.Runs, t.Average})
	}
	out.Incidents.Reported, out.Incidents.Closed, out.Incidents.OpenNow = s.Incidents.Reported, s.Incidents.Closed, s.Incidents.OpenNow
	out.Temperatures.Measurements, out.Temperatures.OutOfRange = s.Temperatures.Measurements, s.Temperatures.OutOfRange
	return out
}

type windowDTO struct {
	AverageCompliance *float64 `json:"averageCompliance"`
	Runs              int      `json:"runs"`
}

type trainingKPIDTO struct {
	PublishedCourses      int      `json:"publishedCourses"`
	Learners              int      `json:"learners"`
	CompletedEnrollments  int      `json:"completedEnrollments"`
	InProgressEnrollments int      `json:"inProgressEnrollments"`
	CompletionPercent     float64  `json:"completionPercent"`
	AverageBestScore      *float64 `json:"averageBestScore"`
}

type kpisDTO struct {
	GeneratedAt string `json:"generatedAt"`
	Compliance  struct {
		Today      windowDTO `json:"today"`
		Last7Days  windowDTO `json:"last7Days"`
		Last30Days windowDTO `json:"last30Days"`
	} `json:"compliance"`
	PendingChecks struct {
		Assigned int `json:"assigned"`
		Pending  int `json:"pending"`
		Overdue  int `json:"overdue"`
	} `json:"pendingChecks"`
	OpenAlerts    int             `json:"openAlerts"`
	OpenIncidents int             `json:"openIncidents"`
	Training      *trainingKPIDTO `json:"training"`
}

func kpisOf(k domain.KPIs) kpisDTO {
	var out kpisDTO
	out.GeneratedAt = iso(k.GeneratedAt)
	out.Compliance.Today = windowDTO{k.Today.Average, k.Today.Runs}
	out.Compliance.Last7Days = windowDTO{k.Last7Days.Average, k.Last7Days.Runs}
	out.Compliance.Last30Days = windowDTO{k.Last30Days.Average, k.Last30Days.Runs}
	out.PendingChecks.Assigned, out.PendingChecks.Pending, out.PendingChecks.Overdue = k.Pending.Assigned, k.Pending.Pending, k.Pending.Overdue
	out.OpenAlerts, out.OpenIncidents = k.OpenAlerts, k.OpenIncidents
	if k.Training != nil {
		t := trainingKPIDTO(*k.Training)
		out.Training = &t
	}
	return out
}

type trainingCourseDTO struct {
	CourseID          string   `json:"courseId"`
	Code              string   `json:"code"`
	Title             string   `json:"title"`
	Assignable        int      `json:"assignable"`
	Enrolled          int      `json:"enrolled"`
	InProgress        int      `json:"inProgress"`
	Completed         int      `json:"completed"`
	CompletionPercent float64  `json:"completionPercent"`
	AverageBestScore  *float64 `json:"averageBestScore"`
}

type trainingPersonDTO struct {
	UserID           string   `json:"userId"`
	DisplayName      string   `json:"displayName"`
	Role             string   `json:"role"`
	PublishedCourses int      `json:"publishedCourses"`
	Completed        int      `json:"completed"`
	InProgress       int      `json:"inProgress"`
	AverageBestScore *float64 `json:"averageBestScore"`
	LastActivityAt   *string  `json:"lastActivityAt"`
}

func personOf(p domain.TrainingPerson) trainingPersonDTO {
	out := trainingPersonDTO{p.UserID, p.DisplayName, p.Role, p.PublishedCourses, p.Completed, p.InProgress, p.AverageBestScore, nil}
	if p.LastActivityAt != nil {
		v := iso(*p.LastActivityAt)
		out.LastActivityAt = &v
	}
	return out
}

type settingsDTO struct {
	WeeklyEmailEnabled  bool    `json:"weeklyEmailEnabled"`
	ComplianceThreshold float64 `json:"complianceThreshold"`
}

type settingsRequest struct {
	WeeklyEmailEnabled  *bool    `json:"weeklyEmailEnabled"`
	ComplianceThreshold *float64 `json:"complianceThreshold"`
}

type certificateDTO struct {
	ID                string  `json:"id"`
	PeriodStart       string  `json:"periodStart"`
	PeriodEnd         string  `json:"periodEnd"`
	AverageCompliance float64 `json:"averageCompliance"`
	Threshold         float64 `json:"threshold"`
	Runs              int     `json:"runs"`
	VerificationCode  string  `json:"verificationCode"`
	IssuedAt          string  `json:"issuedAt"`
}

func certificateOf(c domain.Certificate) certificateDTO {
	return certificateDTO{c.ID, date(c.PeriodStart), date(c.PeriodEnd), c.AverageCompliance, c.Threshold, c.Runs, c.VerificationCode, iso(c.IssuedAt)}
}

type certificateRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type publicCertificateDTO struct {
	Valid             bool    `json:"valid"`
	OrganizationName  string  `json:"organizationName"`
	PeriodStart       string  `json:"periodStart"`
	PeriodEnd         string  `json:"periodEnd"`
	AverageCompliance float64 `json:"averageCompliance"`
	Threshold         float64 `json:"threshold"`
	IssuedAt          string  `json:"issuedAt"`
	VerificationCode  string  `json:"verificationCode"`
}
