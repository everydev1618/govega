package serve

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// RoutineFrequency mirrors the apex-host-mgmt frontend's RoutineFrequency
// enum (refs govega#52). Server-side conversion to cron lets the FE work
// with structured fields instead of parsing cron itself.
type RoutineFrequency string

const (
	FrequencyDaily    RoutineFrequency = "daily"
	FrequencyWeekdays RoutineFrequency = "weekdays"
	FrequencyWeekly   RoutineFrequency = "weekly"
	FrequencyBiweekly RoutineFrequency = "biweekly"
	FrequencyMonthly  RoutineFrequency = "monthly"
	FrequencyCustom   RoutineFrequency = "custom"
)

// Schedule is the structured representation of when a routine fires.
// The frontend models it directly; cron is derived server-side via ToCron.
//
// Fields are honored by frequency:
//   - daily / weekdays:   time, timezone
//   - weekly / biweekly:  time, timezone, days_of_week
//   - monthly:            time, timezone, day_of_month
//   - custom:             cron (raw) — the other fields are echoed for
//     display only.
//
// Biweekly degrades to weekly today — the cron grammar can't express
// "every other week" natively. Worker-side parity gating is left for a
// follow-up.
type Schedule struct {
	Frequency  RoutineFrequency `json:"frequency"`
	Time       string           `json:"time"`     // HH:mm 24h
	Timezone   string           `json:"timezone"` // IANA, e.g. America/New_York
	DaysOfWeek []int            `json:"days_of_week,omitempty"`
	DayOfMonth *int             `json:"day_of_month,omitempty"`
	// Cron is set when Frequency == custom; otherwise it's the derived
	// expression returned on read for debugging.
	Cron string `json:"cron,omitempty"`
}

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

// Validate checks the schedule shape matches the frequency. Returns a
// human-readable error so handlers can pass it straight to the caller.
func (s Schedule) Validate() error {
	if s.Frequency == FrequencyCustom {
		if strings.TrimSpace(s.Cron) == "" {
			return fmt.Errorf("custom frequency requires a cron expression")
		}
		if _, err := cron.ParseStandard(s.Cron); err != nil {
			return fmt.Errorf("invalid cron %q: %w", s.Cron, err)
		}
		return nil
	}
	if !hhmmRe.MatchString(s.Time) {
		return fmt.Errorf("time %q must be HH:mm (24h)", s.Time)
	}
	if s.Timezone == "" {
		return fmt.Errorf("timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", s.Timezone, err)
	}
	switch s.Frequency {
	case FrequencyDaily, FrequencyWeekdays:
		// OK — no extra fields needed.
	case FrequencyWeekly, FrequencyBiweekly:
		if len(s.DaysOfWeek) == 0 {
			return fmt.Errorf("%s frequency requires days_of_week", s.Frequency)
		}
		for _, d := range s.DaysOfWeek {
			if d < 0 || d > 6 {
				return fmt.Errorf("days_of_week %d must be 0..6", d)
			}
		}
	case FrequencyMonthly:
		if s.DayOfMonth == nil {
			return fmt.Errorf("monthly frequency requires day_of_month")
		}
		if *s.DayOfMonth < 1 || *s.DayOfMonth > 31 {
			return fmt.Errorf("day_of_month %d must be 1..31", *s.DayOfMonth)
		}
	default:
		return fmt.Errorf("unknown frequency %q", s.Frequency)
	}
	return nil
}

// ToCron returns the cron expression that matches this schedule. For
// custom frequency, returns s.Cron unchanged. The expression is prefixed
// with CRON_TZ=<timezone> so robfig/cron fires in the right zone
// regardless of process locale.
func (s Schedule) ToCron() (string, error) {
	if s.Frequency == FrequencyCustom {
		return s.Cron, nil
	}
	if err := s.Validate(); err != nil {
		return "", err
	}
	hh, mm := s.Time[:2], s.Time[3:5]
	minute, _ := strconv.Atoi(mm)
	hour, _ := strconv.Atoi(hh)
	var dom, month, dow string
	dom, month, dow = "*", "*", "*"
	switch s.Frequency {
	case FrequencyDaily:
		// nothing else
	case FrequencyWeekdays:
		dow = "1-5"
	case FrequencyWeekly, FrequencyBiweekly:
		// Biweekly degrades to weekly until a parity gate is added.
		days := append([]int(nil), s.DaysOfWeek...)
		sort.Ints(days)
		parts := make([]string, len(days))
		for i, d := range days {
			parts[i] = strconv.Itoa(d)
		}
		dow = strings.Join(parts, ",")
	case FrequencyMonthly:
		dom = strconv.Itoa(*s.DayOfMonth)
	}
	return fmt.Sprintf("CRON_TZ=%s %d %d %s %s %s", s.Timezone, minute, hour, dom, month, dow), nil
}

// NextRun returns the next fire time at or after `after` for the supplied
// cron expression, or nil if the expression cannot be parsed.
func NextRun(cronExpr string, after time.Time) *time.Time {
	if cronExpr == "" {
		return nil
	}
	sched, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil
	}
	next := sched.Next(after).UTC()
	return &next
}

// newRoutineID generates a server-side opaque id for a routine. Unique
// enough for the per-tenant scope; collisions would require a 64-bit
// guess. Prefix makes the id self-describing in logs.
func newRoutineID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return "sched_" + hex.EncodeToString(b[:])
}
