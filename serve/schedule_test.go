package serve

import (
	"strings"
	"testing"
	"time"
)

// TestSchedule_ToCron pins the structured-schedule → cron mapping that
// the apex-host-mgmt Routines UI relies on (refs govega#52). Each case
// fixes the wire format so a FE update to the cron string is a deliberate
// change, not a regression.
func TestSchedule_ToCron(t *testing.T) {
	dom := 15
	cases := []struct {
		name string
		s    Schedule
		want string
	}{
		{
			name: "daily 9am NY",
			s:    Schedule{Frequency: FrequencyDaily, Time: "09:00", Timezone: "America/New_York"},
			want: "CRON_TZ=America/New_York 0 9 * * *",
		},
		{
			name: "daily 17:30 London",
			s:    Schedule{Frequency: FrequencyDaily, Time: "17:30", Timezone: "Europe/London"},
			want: "CRON_TZ=Europe/London 30 17 * * *",
		},
		{
			name: "weekdays 8am UTC",
			s:    Schedule{Frequency: FrequencyWeekdays, Time: "08:00", Timezone: "UTC"},
			want: "CRON_TZ=UTC 0 8 * * 1-5",
		},
		{
			name: "weekly mon+wed 14:15 NY",
			s:    Schedule{Frequency: FrequencyWeekly, Time: "14:15", Timezone: "America/New_York", DaysOfWeek: []int{3, 1}},
			want: "CRON_TZ=America/New_York 15 14 * * 1,3",
		},
		{
			name: "monthly 15th 10am UTC",
			s:    Schedule{Frequency: FrequencyMonthly, Time: "10:00", Timezone: "UTC", DayOfMonth: &dom},
			want: "CRON_TZ=UTC 0 10 15 * *",
		},
		{
			name: "custom passes through",
			s:    Schedule{Frequency: FrequencyCustom, Cron: "*/15 * * * *"},
			want: "*/15 * * * *",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.s.ToCron()
			if err != nil {
				t.Fatalf("ToCron: %v", err)
			}
			if got != tc.want {
				t.Errorf("ToCron() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSchedule_Validate makes sure bad payloads fail fast at the API
// boundary rather than silently converting to a malformed cron expression.
func TestSchedule_Validate(t *testing.T) {
	cases := []struct {
		name   string
		s      Schedule
		errSub string
	}{
		{"missing time", Schedule{Frequency: FrequencyDaily, Timezone: "UTC"}, "time"},
		{"bad time", Schedule{Frequency: FrequencyDaily, Time: "25:00", Timezone: "UTC"}, "time"},
		{"missing tz", Schedule{Frequency: FrequencyDaily, Time: "09:00"}, "timezone"},
		{"bad tz", Schedule{Frequency: FrequencyDaily, Time: "09:00", Timezone: "Mars/Olympus"}, "timezone"},
		{"weekly without days", Schedule{Frequency: FrequencyWeekly, Time: "09:00", Timezone: "UTC"}, "days_of_week"},
		{"weekly bad day", Schedule{Frequency: FrequencyWeekly, Time: "09:00", Timezone: "UTC", DaysOfWeek: []int{7}}, "0..6"},
		{"monthly without day", Schedule{Frequency: FrequencyMonthly, Time: "09:00", Timezone: "UTC"}, "day_of_month"},
		{"custom without cron", Schedule{Frequency: FrequencyCustom}, "cron"},
		{"custom bad cron", Schedule{Frequency: FrequencyCustom, Cron: "not a cron"}, "invalid cron"},
		{"unknown frequency", Schedule{Frequency: "yearly", Time: "09:00", Timezone: "UTC"}, "unknown frequency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.errSub)
			}
		})
	}
}

// TestNextRun makes sure the helper produces a forward-looking time and
// honors CRON_TZ.
func TestNextRun(t *testing.T) {
	now := time.Date(2026, 5, 11, 18, 0, 0, 0, time.UTC)
	next := NextRun("CRON_TZ=UTC 0 9 * * *", now)
	if next == nil {
		t.Fatal("NextRun nil")
	}
	want := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun = %v, want %v", next, want)
	}

	if NextRun("not a cron", now) != nil {
		t.Error("invalid cron should return nil")
	}
	if NextRun("", now) != nil {
		t.Error("empty cron should return nil")
	}
}

// TestNewRoutineID returns prefixed unique ids — collisions are a guess at
// 64 bits and shouldn't happen in routine tests.
func TestNewRoutineID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newRoutineID()
		if !strings.HasPrefix(id, "sched_") {
			t.Fatalf("id %q missing prefix", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
