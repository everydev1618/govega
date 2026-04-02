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
	if err := store.InitDomainTablesV2(); err != nil {
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

// ── New domain table tests ──────────────────────────────────────

func TestCustomerLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a customer.
	id, err := store.InsertCustomer(Customer{
		Name:          "Anderson Residence",
		ContactName:   "Mike Anderson",
		Email:         "mike@anderson.com",
		Phone:         "555-0101",
		Address:       "142 Oak Lane",
		City:          "Springfield",
		State:         "IL",
		Zip:           "62701",
		Source:        "referral",
		Status:        "prospect",
		Tags:          "residential,hardscape",
		Notes:         "Referred by Henderson",
		PaymentMethod: "check",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get it back.
	c, err := store.GetCustomer(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Anderson Residence" {
		t.Errorf("got name %q, want Anderson Residence", c.Name)
	}
	if c.Status != "prospect" {
		t.Errorf("got status %q, want prospect", c.Status)
	}
	if c.Email != "mike@anderson.com" {
		t.Errorf("got email %q, want mike@anderson.com", c.Email)
	}

	// Update.
	c.Status = "active"
	c.Notes = "Signed first contract"
	if err := store.UpdateCustomer(*c); err != nil {
		t.Fatal(err)
	}
	c, _ = store.GetCustomer(id)
	if c.Status != "active" {
		t.Errorf("got status %q, want active", c.Status)
	}

	// Insert a second customer for list/search.
	_, err = store.InsertCustomer(Customer{
		Name:        "Henderson Estate",
		ContactName: "Jane Henderson",
		Email:       "jane@henderson.com",
		Status:      "active",
		Source:      "website",
	})
	if err != nil {
		t.Fatal(err)
	}

	// List.
	customers, err := store.ListCustomers(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(customers) != 2 {
		t.Errorf("got %d customers, want 2", len(customers))
	}

	// Search.
	customers, err = store.SearchCustomers("Anderson", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(customers) != 1 {
		t.Errorf("got %d customers for Anderson search, want 1", len(customers))
	}

	// Delete.
	if err := store.DeleteCustomer(id); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetCustomer(id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestPropertyLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a customer first.
	custID, err := store.InsertCustomer(Customer{
		Name:   "Anderson Residence",
		Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a property.
	id, err := store.InsertProperty(Property{
		CustomerID:   custID,
		Address:      "142 Oak Lane",
		City:         "Springfield",
		State:        "IL",
		Zip:          "62701",
		LotSizeSqft:  12000,
		LawnSqft:     8000,
		BedSqft:      1500,
		HardscapeSqft: 2500,
		Tags:         "front-yard,backyard",
		Notes:        "Corner lot, good drainage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get it back.
	p, err := store.GetProperty(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Address != "142 Oak Lane" {
		t.Errorf("got address %q, want 142 Oak Lane", p.Address)
	}
	if p.LotSizeSqft != 12000 {
		t.Errorf("got lot size %f, want 12000", p.LotSizeSqft)
	}

	// Update.
	p.LawnSqft = 7500
	p.Notes = "Updated after survey"
	if err := store.UpdateProperty(*p); err != nil {
		t.Fatal(err)
	}
	p, _ = store.GetProperty(id)
	if p.LawnSqft != 7500 {
		t.Errorf("got lawn sqft %f, want 7500", p.LawnSqft)
	}

	// Add a second property for the same customer.
	_, err = store.InsertProperty(Property{
		CustomerID: custID,
		Address:    "200 Elm St",
		City:       "Springfield",
		State:      "IL",
	})
	if err != nil {
		t.Fatal(err)
	}

	// List by customer.
	props, err := store.ListPropertiesByCustomer(custID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 2 {
		t.Errorf("got %d properties, want 2", len(props))
	}

	// Delete.
	if err := store.DeleteProperty(id); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetProperty(id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestCrewMemberLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create crew members.
	id, err := store.InsertCrewMember(CrewMember{
		Name:       "Carlos Rivera",
		Role:       "foreman",
		Phone:      "555-0201",
		Email:      "carlos@vega.com",
		HourlyRate: 35.00,
		Skills:     "hardscape,grading,equipment",
		Active:     true,
		Notes:      "10 years experience",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertCrewMember(CrewMember{
		Name:       "Jake Thompson",
		Role:       "laborer",
		Phone:      "555-0202",
		HourlyRate: 20.00,
		Skills:     "hardscape",
		Active:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertCrewMember(CrewMember{
		Name:   "Sam Wilson",
		Role:   "laborer",
		Active: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	cm, err := store.GetCrewMember(id)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Name != "Carlos Rivera" {
		t.Errorf("got name %q, want Carlos Rivera", cm.Name)
	}
	if cm.HourlyRate != 35.00 {
		t.Errorf("got rate %f, want 35.00", cm.HourlyRate)
	}

	// Update.
	cm.HourlyRate = 38.00
	if err := store.UpdateCrewMember(*cm); err != nil {
		t.Fatal(err)
	}
	cm, _ = store.GetCrewMember(id)
	if cm.HourlyRate != 38.00 {
		t.Errorf("got rate %f, want 38.00", cm.HourlyRate)
	}

	// List active only.
	members, err := store.ListCrewMembers("", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Errorf("got %d active members, want 2", len(members))
	}

	// List by role.
	members, err = store.ListCrewMembers("laborer", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 2 laborers total (1 active, 1 inactive)
	if len(members) != 2 {
		t.Errorf("got %d laborers, want 2", len(members))
	}

	// List all including inactive.
	members, err = store.ListCrewMembers("", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Errorf("got %d total members, want 3", len(members))
	}
}

func TestCrewLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a foreman first.
	foremanID, err := store.InsertCrewMember(CrewMember{
		Name:   "Carlos Rivera",
		Role:   "foreman",
		Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a crew.
	id, err := store.InsertCrew(Crew{
		Name:        "Alpha Crew",
		ForemanID:   foremanID,
		MemberIDs:   "[1,2,3]",
		Truck:       "F-350 #12",
		Specialties: "hardscape,grading",
		Active:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get.
	crew, err := store.GetCrew(id)
	if err != nil {
		t.Fatal(err)
	}
	if crew.Name != "Alpha Crew" {
		t.Errorf("got name %q, want Alpha Crew", crew.Name)
	}
	if crew.Truck != "F-350 #12" {
		t.Errorf("got truck %q, want F-350 #12", crew.Truck)
	}

	// Update.
	crew.MemberIDs = "[1,2,3,4]"
	if err := store.UpdateCrew(*crew); err != nil {
		t.Fatal(err)
	}
	crew, _ = store.GetCrew(id)
	if crew.MemberIDs != "[1,2,3,4]" {
		t.Errorf("got member_ids %q, want [1,2,3,4]", crew.MemberIDs)
	}

	// Add inactive crew.
	_, err = store.InsertCrew(Crew{
		Name:   "Bravo Crew",
		Active: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List active only.
	crews, err := store.ListCrews(true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(crews) != 1 {
		t.Errorf("got %d active crews, want 1", len(crews))
	}

	// List all.
	crews, err = store.ListCrews(false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(crews) != 2 {
		t.Errorf("got %d total crews, want 2", len(crews))
	}
}

func TestItemLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create items.
	id, err := store.InsertItem(Item{
		Name:     "Paver - Belgard Mega Arbel",
		Category: "material",
		Unit:     "sq_ft",
		Cost:     3.50,
		Price:    7.00,
		Supplier: "Belgard",
		SKU:      "BEL-MA-001",
		Taxable:  true,
		Active:   true,
		Notes:    "Standard paver",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertItem(Item{
		Name:     "Bobcat Rental",
		Category: "equipment",
		Unit:     "hour",
		Cost:     85.00,
		Price:    150.00,
		Active:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertItem(Item{
		Name:     "Discontinued Edging",
		Category: "material",
		Active:   false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	item, err := store.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "Paver - Belgard Mega Arbel" {
		t.Errorf("got name %q, want Paver - Belgard Mega Arbel", item.Name)
	}
	if item.Cost != 3.50 {
		t.Errorf("got cost %f, want 3.50", item.Cost)
	}

	// Update.
	item.Price = 7.50
	if err := store.UpdateItem(*item); err != nil {
		t.Fatal(err)
	}
	item, _ = store.GetItem(id)
	if item.Price != 7.50 {
		t.Errorf("got price %f, want 7.50", item.Price)
	}

	// List by category, active only.
	items, err := store.ListItems("material", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d active materials, want 1", len(items))
	}

	// List all categories, active only.
	items, err = store.ListItems("", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("got %d active items, want 2", len(items))
	}

	// Search.
	items, err = store.SearchItems("Belgard", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items for Belgard search, want 1", len(items))
	}
}

func TestEstimateLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a customer.
	custID, _ := store.InsertCustomer(Customer{Name: "Anderson", Status: "active"})
	propID, _ := store.InsertProperty(Property{CustomerID: custID, Address: "142 Oak Lane"})

	// Create an estimate.
	id, err := store.InsertEstimate(Estimate{
		CustomerID: custID,
		PropertyID: propID,
		Title:      "Patio Installation",
		Status:     "draft",
		LineItems:  `[{"item":"pavers","qty":500,"unit_price":7.00}]`,
		Subtotal:   3500.00,
		Tax:        280.00,
		Total:      3780.00,
		MarginPct:  45.0,
		DepositPct: 30.0,
		ValidUntil: "2026-05-01",
		Notes:      "Includes base prep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get.
	est, err := store.GetEstimate(id)
	if err != nil {
		t.Fatal(err)
	}
	if est.Title != "Patio Installation" {
		t.Errorf("got title %q, want Patio Installation", est.Title)
	}
	if est.Total != 3780.00 {
		t.Errorf("got total %f, want 3780.00", est.Total)
	}

	// Update.
	est.Total = 4000.00
	est.Notes = "Revised after site visit"
	if err := store.UpdateEstimate(*est); err != nil {
		t.Fatal(err)
	}
	est, _ = store.GetEstimate(id)
	if est.Total != 4000.00 {
		t.Errorf("got total %f, want 4000.00", est.Total)
	}

	// Update status.
	if err := store.UpdateEstimateStatus(id, "sent"); err != nil {
		t.Fatal(err)
	}
	est, _ = store.GetEstimate(id)
	if est.Status != "sent" {
		t.Errorf("got status %q, want sent", est.Status)
	}

	// Insert a second estimate.
	_, err = store.InsertEstimate(Estimate{
		CustomerID: custID,
		PropertyID: propID,
		Title:      "Retaining Wall",
		Status:     "draft",
		Total:      8500.00,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List by status.
	estimates, err := store.ListEstimates("draft", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(estimates) != 1 {
		t.Errorf("got %d draft estimates, want 1", len(estimates))
	}

	// List by customer.
	estimates, err = store.ListEstimatesByCustomer(custID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(estimates) != 2 {
		t.Errorf("got %d estimates for customer, want 2", len(estimates))
	}
}

func TestCalendarEventLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create events.
	id, err := store.InsertCalendarEvent(CalendarEvent{
		Title:     "Anderson Patio Install",
		EventType: "job",
		Date:      "2026-04-15",
		StartTime: "07:00",
		EndTime:   "16:00",
		CrewID:    1,
		JobID:     1,
		Status:    "scheduled",
		Notes:     "Day 1 of 3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertCalendarEvent(CalendarEvent{
		Title:     "Henderson Consultation",
		EventType: "consultation",
		Date:      "2026-04-15",
		StartTime: "10:00",
		EndTime:   "11:00",
		Status:    "scheduled",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertCalendarEvent(CalendarEvent{
		Title:     "Mulch Delivery",
		EventType: "delivery",
		Date:      "2026-04-16",
		Status:    "scheduled",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	ev, err := store.GetCalendarEvent(id)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Title != "Anderson Patio Install" {
		t.Errorf("got title %q, want Anderson Patio Install", ev.Title)
	}

	// Update.
	ev.Status = "in_progress"
	if err := store.UpdateCalendarEvent(*ev); err != nil {
		t.Fatal(err)
	}
	ev, _ = store.GetCalendarEvent(id)
	if ev.Status != "in_progress" {
		t.Errorf("got status %q, want in_progress", ev.Status)
	}

	// List by date.
	events, err := store.ListCalendarEvents("2026-04-15", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events on 4/15, want 2", len(events))
	}

	// List by range.
	events, err = store.ListCalendarEventsByRange("2026-04-15", "2026-04-16", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events in range, want 3", len(events))
	}

	// List by crew.
	events, err = store.ListCalendarEventsByCrew(1, "2026-04-15", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events for crew 1, want 1", len(events))
	}

	// Delete.
	if err := store.DeleteCalendarEvent(id); err != nil {
		t.Fatal(err)
	}
	_, err = store.GetCalendarEvent(id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestInvoiceLifecycle(t *testing.T) {
	store := newTestStore(t)

	custID, _ := store.InsertCustomer(Customer{Name: "Anderson", Status: "active"})

	// Create an invoice.
	id, err := store.InsertInvoice(Invoice{
		InvoiceNumber:  "INV-1001",
		CustomerID:     custID,
		JobID:          1,
		Status:         "draft",
		LineItems:      `[{"desc":"Patio pavers","qty":500,"price":7.00}]`,
		Subtotal:       3500.00,
		Tax:            280.00,
		Total:          3780.00,
		DepositApplied: 1134.00,
		AmountDue:      2646.00,
		IssuedDate:     "2026-04-01",
		DueDate:        "2026-04-30",
		Notes:          "Net 30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get.
	inv, err := store.GetInvoice(id)
	if err != nil {
		t.Fatal(err)
	}
	if inv.InvoiceNumber != "INV-1001" {
		t.Errorf("got number %q, want INV-1001", inv.InvoiceNumber)
	}
	if inv.AmountDue != 2646.00 {
		t.Errorf("got amount_due %f, want 2646.00", inv.AmountDue)
	}

	// Update.
	inv.Notes = "Payment reminder sent"
	if err := store.UpdateInvoice(*inv); err != nil {
		t.Fatal(err)
	}
	inv, _ = store.GetInvoice(id)
	if inv.Notes != "Payment reminder sent" {
		t.Errorf("got notes %q, want Payment reminder sent", inv.Notes)
	}

	// Update status.
	if err := store.UpdateInvoiceStatus(id, "sent", "", ""); err != nil {
		t.Fatal(err)
	}
	inv, _ = store.GetInvoice(id)
	if inv.Status != "sent" {
		t.Errorf("got status %q, want sent", inv.Status)
	}

	// Insert second invoice.
	_, err = store.InsertInvoice(Invoice{
		InvoiceNumber: "INV-1002",
		CustomerID:    custID,
		Status:        "paid",
		Total:         1500.00,
		AmountDue:     0,
		PaidDate:      "2026-03-15",
		PaymentMethod: "check",
	})
	if err != nil {
		t.Fatal(err)
	}

	// List by status.
	invoices, err := store.ListInvoices("sent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(invoices) != 1 {
		t.Errorf("got %d sent invoices, want 1", len(invoices))
	}

	// List by customer.
	invoices, err = store.ListInvoicesByCustomer(custID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(invoices) != 2 {
		t.Errorf("got %d invoices for customer, want 2", len(invoices))
	}
}

func TestVendorLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create vendors.
	id, err := store.InsertVendor(Vendor{
		Name:           "Belgard Supply",
		ContactName:    "Tom Peters",
		Phone:          "555-0301",
		Email:          "tom@belgard.com",
		Address:        "500 Industrial Blvd",
		Specialty:      "pavers,retaining walls",
		PaymentTerms:   "Net 30",
		AccountNumber:  "BEL-4521",
		Active:         true,
		Notes:          "Primary paver supplier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertVendor(Vendor{
		Name:      "Green Thumb Nursery",
		Specialty: "plants,mulch",
		Active:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertVendor(Vendor{
		Name:   "Old Stone Co",
		Active: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	v, err := store.GetVendor(id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Belgard Supply" {
		t.Errorf("got name %q, want Belgard Supply", v.Name)
	}

	// Update.
	v.PaymentTerms = "Net 15"
	if err := store.UpdateVendor(*v); err != nil {
		t.Fatal(err)
	}
	v, _ = store.GetVendor(id)
	if v.PaymentTerms != "Net 15" {
		t.Errorf("got terms %q, want Net 15", v.PaymentTerms)
	}

	// List active only.
	vendors, err := store.ListVendors(true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vendors) != 2 {
		t.Errorf("got %d active vendors, want 2", len(vendors))
	}

	// List all.
	vendors, err = store.ListVendors(false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vendors) != 3 {
		t.Errorf("got %d total vendors, want 3", len(vendors))
	}

	// Search.
	vendors, err = store.SearchVendors("Belgard", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vendors) != 1 {
		t.Errorf("got %d vendors for Belgard search, want 1", len(vendors))
	}
}

func TestSalesLeadLifecycle(t *testing.T) {
	store := newTestStore(t)

	custID, _ := store.InsertCustomer(Customer{Name: "New Prospect", Status: "prospect"})

	// Create leads.
	id, err := store.InsertSalesLead(SalesLead{
		CustomerID:      custID,
		Name:            "Richardson Patio",
		Phone:           "555-0401",
		Email:           "rich@email.com",
		Source:          "referral",
		Status:          "new",
		EstimatedValue:  15000.00,
		JobType:         "patio",
		PropertyAddress: "88 Maple Dr",
		AssignedTo:      "mara",
		Notes:           "Wants large paver patio",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertSalesLead(SalesLead{
		Name:       "Smith Retaining Wall",
		Status:     "contacted",
		AssignedTo: "mara",
		JobType:    "retaining_wall",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertSalesLead(SalesLead{
		Name:       "Jones Planting",
		Status:     "new",
		AssignedTo: "devin",
		JobType:    "planting",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	lead, err := store.GetSalesLead(id)
	if err != nil {
		t.Fatal(err)
	}
	if lead.Name != "Richardson Patio" {
		t.Errorf("got name %q, want Richardson Patio", lead.Name)
	}
	if lead.EstimatedValue != 15000.00 {
		t.Errorf("got value %f, want 15000.00", lead.EstimatedValue)
	}

	// Update.
	lead.Status = "qualified"
	lead.Notes = "Site visit completed"
	if err := store.UpdateSalesLead(*lead); err != nil {
		t.Fatal(err)
	}
	lead, _ = store.GetSalesLead(id)
	if lead.Status != "qualified" {
		t.Errorf("got status %q, want qualified", lead.Status)
	}

	// List by status.
	leads, err := store.ListSalesLeads("new", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 1 {
		t.Errorf("got %d new leads, want 1", len(leads))
	}

	// List by assignee.
	leads, err = store.ListSalesLeadsByAssignee("mara", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 2 {
		t.Errorf("got %d leads for mara, want 2", len(leads))
	}
}

func TestCostCodeLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create cost codes.
	id1, err := store.InsertCostCode(CostCode{
		Code:          "01-100",
		Name:          "Field Labor - Hardscape",
		Division:      "Labor",
		DivisionGroup: "Direct Costs",
		Active:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero id")
	}

	_, err = store.InsertCostCode(CostCode{
		Code:          "01-200",
		Name:          "Field Labor - Planting",
		Division:      "Labor",
		DivisionGroup: "Direct Costs",
		Active:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertCostCode(CostCode{
		Code:          "02-100",
		Name:          "Pavers",
		Division:      "Materials",
		DivisionGroup: "Direct Costs",
		Active:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List by division.
	codes, err := store.ListCostCodes("Labor", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 {
		t.Errorf("got %d labor codes, want 2", len(codes))
	}

	// List all (empty division).
	codes, err = store.ListCostCodes("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 3 {
		t.Errorf("got %d total codes, want 3", len(codes))
	}

	// List divisions.
	divisions, err := store.ListDivisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(divisions) != 2 {
		t.Errorf("got %d divisions, want 2 (Labor, Materials)", len(divisions))
	}
}

func TestBudgetLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a budget.
	id, err := store.InsertBudget(Budget{
		Year:            2026,
		Name:            "2026 Operating Budget",
		RevenueTarget:   750000.00,
		TotalOverhead:   180000.00,
		BillableHours:   4800,
		HourlyRate:      65.00,
		OwnerSalary:     120000.00,
		TargetMarginPct: 45.0,
		Notes:           "Conservative growth target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Get.
	b, err := store.GetBudget(id)
	if err != nil {
		t.Fatal(err)
	}
	if b.Year != 2026 {
		t.Errorf("got year %d, want 2026", b.Year)
	}
	if b.RevenueTarget != 750000.00 {
		t.Errorf("got revenue %f, want 750000.00", b.RevenueTarget)
	}

	// Update.
	b.RevenueTarget = 800000.00
	b.Notes = "Revised upward after Q1"
	if err := store.UpdateBudget(*b); err != nil {
		t.Fatal(err)
	}
	b, _ = store.GetBudget(id)
	if b.RevenueTarget != 800000.00 {
		t.Errorf("got revenue %f, want 800000.00", b.RevenueTarget)
	}

	// Get by year.
	b, err = store.GetBudgetByYear(2026)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "2026 Operating Budget" {
		t.Errorf("got name %q, want 2026 Operating Budget", b.Name)
	}

	// Create second budget year.
	_, err = store.InsertBudget(Budget{
		Year:          2027,
		Name:          "2027 Operating Budget",
		RevenueTarget: 900000.00,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List years.
	years, err := store.ListBudgetYears()
	if err != nil {
		t.Fatal(err)
	}
	if len(years) != 2 {
		t.Errorf("got %d budget years, want 2", len(years))
	}
}

func TestBudgetLineLifecycle(t *testing.T) {
	store := newTestStore(t)

	// Create a budget.
	budgetID, err := store.InsertBudget(Budget{
		Year: 2026,
		Name: "2026 Budget",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create budget lines.
	id1, err := store.InsertBudgetLine(BudgetLine{
		BudgetID:      budgetID,
		CostCode:      "01-100",
		Description:   "Field Labor - Hardscape",
		Category:      "labor",
		AnnualAmount:  240000.00,
		MonthlyAmount: 20000.00,
		Notes:         "3 crew members",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero id")
	}

	id2, err := store.InsertBudgetLine(BudgetLine{
		BudgetID:      budgetID,
		CostCode:      "02-100",
		Description:   "Pavers",
		Category:      "materials",
		AnnualAmount:  120000.00,
		MonthlyAmount: 10000.00,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertBudgetLine(BudgetLine{
		BudgetID:      budgetID,
		CostCode:      "05-100",
		Description:   "Truck Fuel",
		Category:      "equipment",
		AnnualAmount:  18000.00,
		MonthlyAmount: 1500.00,
	})
	if err != nil {
		t.Fatal(err)
	}

	// List by budget.
	lines, err := store.ListBudgetLines(budgetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Errorf("got %d budget lines, want 3", len(lines))
	}

	// Delete one.
	if err := store.DeleteBudgetLine(id2); err != nil {
		t.Fatal(err)
	}
	lines, err = store.ListBudgetLines(budgetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Errorf("got %d budget lines after delete, want 2", len(lines))
	}
}
