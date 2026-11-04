// Package domain contiene reglas puras de alertas y sus valores.
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrDenied = errors.New("alert access denied")
var ErrNotFound = errors.New("alert not found")
var ErrConflict = errors.New("alert conflict")
var ErrInvalid = errors.New("invalid alert input")
var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var organization = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidID(v string) bool {
	return uuid.MatchString(v) && v != "00000000-0000-0000-0000-000000000000"
}
func ValidOrganization(v string) bool { return organization.MatchString(v) }
func ValidUID(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) > 0 && utf8.RuneCountInString(v) <= 128 && strings.IndexFunc(v, unicode.IsControl) < 0
}

type Rule struct {
	ID                                string
	LocationID                        *string
	LocationName, Name, Type          string
	Configuration                     json.RawMessage
	Active, EmailEnabled, PushEnabled bool
	RowVersion                        int
	CreatedAt                         time.Time
}
type Alert struct {
	ID, Type, Title, Message, Status             string
	CreatedAt                                    time.Time
	ViewedAt, AttendedAt                         *time.Time
	LocationID                                   *string
	LocationName, Timezone, SourceType, SourceID string
	SourceParentID                               *string
	SourceAvailable                              bool
}

// StrictJSON valida también duplicados anidados; un documento JSON es un solo objeto UTF8.
func StrictJSON(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(bool) error
	walk = func(root bool) error {
		tok, e := d.Token()
		if e != nil {
			return ErrInvalid
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			if root {
				return ErrInvalid
			}
			return nil
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return ErrInvalid
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if e = walk(false); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return ErrInvalid
			}
			return nil
		}
		if delim == '[' && !root {
			for d.More() {
				if e = walk(false); e != nil {
					return e
				}
			}
			_, e = d.Token()
			return e
		}
		return ErrInvalid
	}
	if e := walk(true); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return ErrInvalid
	}
	return nil
}
func ValidateConfiguration(kind string, raw json.RawMessage) error {
	switch kind {
	case "CHECKLIST_MISSING":
		var v struct {
			After *string `json:"afterLocalTime"`
		}
		if StrictJSON(raw, &v) != nil || v.After == nil {
			return ErrInvalid
		}
		if _, e := time.Parse("15:04", *v.After); e != nil || len(*v.After) != 5 {
			return ErrInvalid
		}
	case "TEMPERATURE_DANGER":
		var v struct {
			Min *float64 `json:"minimumTemperatureCelsius"`
			Max *float64 `json:"maximumTemperatureCelsius"`
		}
		if StrictJSON(raw, &v) != nil || v.Min == nil || v.Max == nil {
			return ErrInvalid
		}
		for _, n := range []float64{*v.Min, *v.Max} {
			if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 9999.99 || math.Abs(n*100-math.Round(n*100)) > 0.000001 {
				return ErrInvalid
			}
		}
		if *v.Min >= *v.Max {
			return ErrInvalid
		}
	case "ORGANIZATION_INACTIVE":
		var v struct {
			Days *int `json:"inactivityDays"`
		}
		if StrictJSON(raw, &v) != nil || v.Days == nil || *v.Days < 1 || *v.Days > 365 {
			return ErrInvalid
		}
	case "TRAINING_ASSIGNED":
		if StrictJSON(raw, &struct{}{}) != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func NormalizeRule(v Rule, create bool) (Rule, error) {
	v.ID = strings.ToLower(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	if !ValidID(v.ID) || !utf8.ValidString(v.Name) || utf8.RuneCountInString(v.Name) < 2 || utf8.RuneCountInString(v.Name) > 160 || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 || (!create && v.RowVersion < 1) {
		return v, ErrInvalid
	}
	if v.LocationID != nil {
		if !ValidID(*v.LocationID) || v.Type == "ORGANIZATION_INACTIVE" {
			return v, ErrInvalid
		}
		id := strings.ToLower(*v.LocationID)
		v.LocationID = &id
	}
	if e := ValidateConfiguration(v.Type, v.Configuration); e != nil {
		return v, e
	}
	var canonical any
	json.Unmarshal(v.Configuration, &canonical)
	v.Configuration, _ = json.Marshal(canonical)
	return v, nil
}
func LocalDay(now time.Time, z *time.Location) (time.Time, time.Time) {
	local := now.In(z)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, z)
	return start, start.AddDate(0, 0, 1)
}
