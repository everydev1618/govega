package serve

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := store.InitDomainTables(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		os.Remove(path)
	})
	return store
}

func TestJobLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a job.
	id, err := store.InsertJob(Job{
		CustomerName:    "Anderson",
		PropertyAddress: "142 Oak Lane",
		JobType:         "patio",
		Stage:           "lead_captured",
		OwnerAgent:      "mara",
		Notes:           "Referral from Henderson",
		EstimateTotal:   10975,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get it back.
	j, err := store.GetJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.CustomerName != "Anderson" {
		t.Errorf("got customer %q, want Anderson", j.CustomerName)
	}
	if j.Stage != "lead_captured" {
		t.Errorf("got stage %q, want lead_captured", j.Stage)
	}

	// Advance stage.
	if err := store.UpdateJobStage(id, "job_sold", "mara", "Contract signed, $3200 deposit"); err != nil {
		t.Fatal(err)
	}
	j, _ = store.GetJob(id)
	if j.Stage != "job_sold" {
		t.Errorf("got stage %q, want job_sold", j.Stage)
	}

	// List by stage.
	jobs, err := store.ListJobsByStage("job_sold", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Errorf("got %d jobs, want 1", len(jobs))
	}

	// Search.
	jobs, err = store.SearchJobs("Anderson", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Errorf("got %d jobs, want 1", len(jobs))
	}

	// Cost the job.
	if err := store.UpdateJobTotals(id, 10975, 11200); err != nil {
		t.Fatal(err)
	}
	j, _ = store.GetJob(id)
	if j.ActualTotal != 11200 {
		t.Errorf("got actual %f, want 11200", j.ActualTotal)
	}
}

func TestFollowUps(t *testing.T) {
	store := newTestStore(t)

	// Add follow-ups.
	id1, err := store.InsertFollowUp(FollowUp{
		Agent:      "mara",
		TargetType: "lead",
		TargetName: "Richardson",
		Action:     "call",
		DueDate:    "2026-03-30",
		Status:     "pending",
		Notes:      "First touch",
	})
	if err != nil {
		t.Fatal(err)
	}

	id2, err := store.InsertFollowUp(FollowUp{
		Agent:      "devin",
		TargetType: "invoice",
		TargetName: "Invoice #1047",
		Action:     "reminder",
		DueDate:    "2026-04-01",
		Status:     "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	// List all due by date.
	items, err := store.ListFollowUpsDue("", "2026-03-31", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d follow-ups due by 3/31, want 1", len(items))
	}

	// List by agent.
	items, err = store.ListFollowUpsByAgent("mara", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d follow-ups for mara, want 1", len(items))
	}

	// Complete one.
	if err := store.CompleteFollowUp(id1, "done"); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListFollowUpsByAgent("mara", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("got %d pending for mara after completing, want 0", len(items))
	}

	_ = id2 // used above in the due-date query
}

func TestProductionRates(t *testing.T) {
	store := newTestStore(t)

	// Record some patio rates.
	_, err := store.InsertProductionRate(ProductionRate{
		JobType:               "patio",
		Unit:                  "sq_ft",
		EstimatedHoursPerUnit: 0.08,
		ActualHoursPerUnit:    0.09,
		JobName:               "Anderson patio",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertProductionRate(ProductionRate{
		JobType:               "patio",
		Unit:                  "sq_ft",
		EstimatedHoursPerUnit: 0.08,
		ActualHoursPerUnit:    0.07,
		JobName:               "Henderson patio",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get rates.
	rates, err := store.GetProductionRates("patio", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Errorf("got %d rates, want 2", len(rates))
	}

	// Get averages.
	avgEst, avgAct, count, err := store.GetProductionRateAverage("patio")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("got count %d, want 2", count)
	}
	if avgEst != 0.08 {
		t.Errorf("got avg estimated %f, want 0.08", avgEst)
	}
	// Average of 0.09 and 0.07 = 0.08
	if avgAct != 0.08 {
		t.Errorf("got avg actual %f, want 0.08", avgAct)
	}

	// No data for a different type.
	rates, err = store.GetProductionRates("retaining_wall", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates for retaining_wall, want 0", len(rates))
	}
}
