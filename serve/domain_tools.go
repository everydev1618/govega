package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/tools"
)

// domainContextKey is a type for domain tool context keys.
type domainContextKey string

const domainCtxStore domainContextKey = "domain.store"

// ContextWithDomainStore returns a context carrying the domain store.
func ContextWithDomainStore(ctx context.Context, store *SQLiteStore) context.Context {
	return context.WithValue(ctx, domainCtxStore, store)
}

// domainStoreFromContext extracts the SQLiteStore from context.
func domainStoreFromContext(ctx context.Context) (*SQLiteStore, error) {
	store, _ := ctx.Value(domainCtxStore).(*SQLiteStore)
	if store == nil {
		return nil, fmt.Errorf("domain store not set in context")
	}
	return store, nil
}

// RegisterDomainTools registers job tracking, follow-up, production rate,
// and all V2 domain tools on the interpreter.
func RegisterDomainTools(interp *dsl.Interpreter) {
	t := interp.Tools()
	registerJobTools(t)
	registerFollowUpTools(t)
	registerProductionRateTools(t)
	registerCustomerTools(t)
	registerPropertyTools(t)
	registerCrewMemberTools(t)
	registerCrewTools(t)
	registerItemTools(t)
	registerEstimateTools(t)
	registerCalendarEventTools(t)
	registerInvoiceTools(t)
	registerVendorTools(t)
	registerSalesLeadTools(t)
	registerCostCodeTools(t)
	registerBudgetTools(t)
	registerBudgetLineTools(t)
}

// ────────────────────────────────────────────────────────────────
// Job lifecycle tools
// ────────────────────────────────────────────────────────────────

func registerJobTools(t *tools.Tools) {

	t.Register("track_job", tools.ToolDef{
		Description: "Create or update a job in the lifecycle tracker. Use when a new lead comes in, a job is sold, work starts, or any stage transition happens.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}

			// If id is provided, update existing job's stage.
			if idVal, ok := params["id"]; ok {
				id := toInt64(idVal)
				if id == 0 {
					return "", fmt.Errorf("invalid id")
				}
				stage, _ := params["stage"].(string)
				owner, _ := params["owner_agent"].(string)
				notes, _ := params["notes"].(string)
				if stage == "" {
					return "", fmt.Errorf("stage is required for updates")
				}
				if err := store.UpdateJobStage(id, stage, owner, notes); err != nil {
					return "", fmt.Errorf("update job: %w", err)
				}
				return fmt.Sprintf("Job %d updated to stage %q.", id, stage), nil
			}

			// Create new job.
			customer, _ := params["customer_name"].(string)
			if customer == "" {
				return "", fmt.Errorf("customer_name is required")
			}
			j := Job{
				CustomerName:    customer,
				PropertyAddress: strOrDefault(params, "property_address", ""),
				JobType:         strOrDefault(params, "job_type", ""),
				Stage:           strOrDefault(params, "stage", "lead_captured"),
				OwnerAgent:      strOrDefault(params, "owner_agent", ""),
				Notes:           strOrDefault(params, "notes", ""),
				EstimateTotal:   toFloat64(params["estimate_total"]),
				ActualTotal:     toFloat64(params["actual_total"]),
				ExternalID:      strOrDefault(params, "external_id", ""),
			}
			id, err := store.InsertJob(j)
			if err != nil {
				return "", fmt.Errorf("create job: %w", err)
			}
			return fmt.Sprintf("Job created (id=%d, customer=%q, stage=%q).", id, customer, j.Stage), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":               {Type: "number", Description: "Job ID to update (omit to create new)"},
			"customer_name":    {Type: "string", Description: "Customer name (required for new jobs)"},
			"property_address": {Type: "string", Description: "Property address"},
			"job_type":         {Type: "string", Description: "Job type: patio, retaining_wall, planting, irrigation, maintenance, mulch, cleanup, drainage, lighting, etc."},
			"stage":            {Type: "string", Description: "Lifecycle stage: lead_captured, lead_qualified, estimate_created, proposal_sent, job_sold, materials_ordered, job_scheduled, site_ready, crew_dispatched, job_in_progress, job_completed, materials_returned, invoice_sent, payment_received, job_costed, pnl_updated"},
			"owner_agent":      {Type: "string", Description: "Agent currently owning this stage"},
			"notes":            {Type: "string", Description: "Notes about this stage transition"},
			"estimate_total":   {Type: "number", Description: "Estimated dollar amount"},
			"actual_total":     {Type: "number", Description: "Actual dollar amount (set after job costing)"},
			"external_id":      {Type: "string", Description: "SynkedUp project ID for cross-reference"},
		},
	})

	t.Register("get_job", tools.ToolDef{
		Description: "Get details of a specific job by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			j, err := store.GetJob(id)
			if err != nil {
				return "", fmt.Errorf("get job: %w", err)
			}
			out, _ := json.MarshalIndent(j, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Job ID", Required: true},
		},
	})

	t.Register("list_jobs", tools.ToolDef{
		Description: "List jobs, optionally filtered by lifecycle stage or search query. Use to see pipeline status, find jobs by customer, or check what's at a specific stage.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var jobs []Job
			if stage, ok := params["stage"].(string); ok && stage != "" {
				jobs, err = store.ListJobsByStage(stage, limit)
			} else if query, ok := params["query"].(string); ok && query != "" {
				jobs, err = store.SearchJobs(query, limit)
			} else {
				jobs, err = store.ListJobs(limit)
			}
			if err != nil {
				return "", fmt.Errorf("list jobs: %w", err)
			}
			if len(jobs) == 0 {
				return "No jobs found.", nil
			}
			out, _ := json.MarshalIndent(jobs, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"stage": {Type: "string", Description: "Filter by lifecycle stage (e.g. job_sold, job_in_progress, invoice_sent)"},
			"query": {Type: "string", Description: "Search by customer name, job type, or notes"},
			"limit": {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("cost_job", tools.ToolDef{
		Description: "Record the estimate and actual totals for a completed job. This is how you track profitability and feed the production rate flywheel.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			est := toFloat64(params["estimate_total"])
			act := toFloat64(params["actual_total"])
			if err := store.UpdateJobTotals(id, est, act); err != nil {
				return "", fmt.Errorf("cost job: %w", err)
			}
			var margin float64
			if est > 0 {
				margin = ((est - act) / est) * 100
			}
			return fmt.Sprintf("Job %d costed: estimate $%.2f, actual $%.2f, margin %.1f%%.", id, est, act, margin), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":             {Type: "number", Description: "Job ID", Required: true},
			"estimate_total": {Type: "number", Description: "Estimated dollar amount", Required: true},
			"actual_total":   {Type: "number", Description: "Actual dollar amount", Required: true},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Follow-up tools
// ────────────────────────────────────────────────────────────────

func registerFollowUpTools(t *tools.Tools) {

	t.Register("add_follow_up", tools.ToolDef{
		Description: "Schedule a follow-up action — a call, text, email, visit, or payment reminder. The follow-up queue is your system for making sure nothing falls through the cracks.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			_, _, agent, memErr := memoryFromContext(ctx)
			if memErr != nil {
				agent = ""
			}
			if a, ok := params["agent"].(string); ok && a != "" {
				agent = a
			}

			f := FollowUp{
				Agent:      agent,
				TargetType: strOrDefault(params, "target_type", ""),
				TargetName: strOrDefault(params, "target_name", ""),
				Action:     strOrDefault(params, "action", ""),
				DueDate:    strOrDefault(params, "due_date", ""),
				Status:     "pending",
				Notes:      strOrDefault(params, "notes", ""),
			}
			if f.TargetName == "" {
				return "", fmt.Errorf("target_name is required")
			}
			if f.DueDate == "" {
				return "", fmt.Errorf("due_date is required")
			}
			id, err := store.InsertFollowUp(f)
			if err != nil {
				return "", fmt.Errorf("add follow-up: %w", err)
			}
			return fmt.Sprintf("Follow-up scheduled (id=%d): %s %s on %s.", id, f.Action, f.TargetName, f.DueDate), nil
		}),
		Params: map[string]tools.ParamDef{
			"target_type": {Type: "string", Description: "What you're following up on: lead, invoice, customer, job"},
			"target_name": {Type: "string", Description: "Name of the target (e.g. 'Anderson', 'Invoice #1047')", Required: true},
			"action":      {Type: "string", Description: "Action to take: call, text, email, visit, reminder"},
			"due_date":    {Type: "string", Description: "When to follow up (YYYY-MM-DD)", Required: true},
			"notes":       {Type: "string", Description: "Context for the follow-up"},
			"agent":       {Type: "string", Description: "Agent responsible (defaults to current agent)"},
		},
	})

	t.Register("list_follow_ups", tools.ToolDef{
		Description: "List pending follow-ups. Use to see what's due today, what's overdue, or what a specific agent has on their plate.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			agent, _ := params["agent"].(string)
			status, _ := params["status"].(string)

			var items []FollowUp
			if dueBy, ok := params["due_by"].(string); ok && dueBy != "" {
				items, err = store.ListFollowUpsDue(agent, dueBy, limit)
			} else {
				if status == "" {
					status = "pending"
				}
				items, err = store.ListFollowUpsByAgent(agent, status, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list follow-ups: %w", err)
			}
			if len(items) == 0 {
				return "No follow-ups found.", nil
			}
			out, _ := json.MarshalIndent(items, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"agent":  {Type: "string", Description: "Filter by agent name (empty = all agents)"},
			"due_by": {Type: "string", Description: "Show follow-ups due on or before this date (YYYY-MM-DD)"},
			"status": {Type: "string", Description: "Filter by status: pending, done, skipped (default: pending)"},
			"limit":  {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("complete_follow_up", tools.ToolDef{
		Description: "Mark a follow-up as done or skipped.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			status := strOrDefault(params, "status", "done")
			if status != "done" && status != "skipped" {
				return "", fmt.Errorf("status must be 'done' or 'skipped'")
			}
			if err := store.CompleteFollowUp(id, status); err != nil {
				return "", fmt.Errorf("complete follow-up: %w", err)
			}
			return fmt.Sprintf("Follow-up %d marked as %s.", id, status), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":     {Type: "number", Description: "Follow-up ID", Required: true},
			"status": {Type: "string", Description: "New status: done or skipped (default: done)"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Production rate tools
// ────────────────────────────────────────────────────────────────

func registerProductionRateTools(t *tools.Tools) {

	t.Register("record_production_rate", tools.ToolDef{
		Description: "Record estimated vs. actual hours per unit after a job is costed. This feeds the estimating flywheel — more data points mean better future estimates.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			jobType, _ := params["job_type"].(string)
			if jobType == "" {
				return "", fmt.Errorf("job_type is required")
			}
			p := ProductionRate{
				JobType:               jobType,
				Unit:                  strOrDefault(params, "unit", ""),
				EstimatedHoursPerUnit: toFloat64(params["estimated_hours_per_unit"]),
				ActualHoursPerUnit:    toFloat64(params["actual_hours_per_unit"]),
				JobName:               strOrDefault(params, "job_name", ""),
				Notes:                 strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertProductionRate(p)
			if err != nil {
				return "", fmt.Errorf("record rate: %w", err)
			}

			var drift string
			if p.EstimatedHoursPerUnit > 0 {
				pct := ((p.ActualHoursPerUnit - p.EstimatedHoursPerUnit) / p.EstimatedHoursPerUnit) * 100
				if pct > 0 {
					drift = fmt.Sprintf(" Actual ran %.0f%% over estimate.", pct)
				} else if pct < 0 {
					drift = fmt.Sprintf(" Actual ran %.0f%% under estimate.", -pct)
				}
			}
			return fmt.Sprintf("Production rate recorded (id=%d, %s, %.2f est → %.2f actual hrs/%s).%s", id, jobType, p.EstimatedHoursPerUnit, p.ActualHoursPerUnit, p.Unit, drift), nil
		}),
		Params: map[string]tools.ParamDef{
			"job_type":                {Type: "string", Description: "Job type: patio, retaining_wall, planting, irrigation, maintenance, mulch, etc.", Required: true},
			"unit":                    {Type: "string", Description: "Unit of measure: sq_ft, lin_ft, cu_yd, each"},
			"estimated_hours_per_unit": {Type: "number", Description: "Estimated hours per unit from the original estimate", Required: true},
			"actual_hours_per_unit":    {Type: "number", Description: "Actual hours per unit from job costing", Required: true},
			"job_name":                {Type: "string", Description: "Job name for reference (e.g. 'Anderson patio')"},
			"notes":                   {Type: "string", Description: "Notes about why actuals differed"},
		},
	})

	t.Register("get_production_rates", tools.ToolDef{
		Description: "Look up production rate history for a job type. Shows historical accuracy and average rates. Use before estimating to get the best hours-per-unit figure.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			jobType, _ := params["job_type"].(string)
			if jobType == "" {
				return "", fmt.Errorf("job_type is required")
			}
			limit := 10
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			rates, err := store.GetProductionRates(jobType, limit)
			if err != nil {
				return "", fmt.Errorf("get rates: %w", err)
			}

			avgEst, avgAct, count, err := store.GetProductionRateAverage(jobType)
			if err != nil {
				return "", fmt.Errorf("get average: %w", err)
			}

			type response struct {
				JobType        string           `json:"job_type"`
				TotalDataPoints int             `json:"total_data_points"`
				AvgEstimated   float64          `json:"avg_estimated_hours_per_unit"`
				AvgActual      float64          `json:"avg_actual_hours_per_unit"`
				AccuracyPct    float64          `json:"accuracy_pct"`
				RecentRates    []ProductionRate `json:"recent_rates"`
			}

			var accuracy float64
			if avgEst > 0 {
				accuracy = (1 - (avgAct-avgEst)/avgEst) * 100
			}

			resp := response{
				JobType:         jobType,
				TotalDataPoints: count,
				AvgEstimated:    avgEst,
				AvgActual:       avgAct,
				AccuracyPct:     accuracy,
				RecentRates:     rates,
			}
			out, _ := json.MarshalIndent(resp, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"job_type": {Type: "string", Description: "Job type to look up rates for", Required: true},
			"limit":    {Type: "number", Description: "Max recent data points to show (default 10)"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────

func strOrDefault(params map[string]any, key, def string) string {
	if v, ok := params[key].(string); ok && v != "" {
		return v
	}
	return def
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func toBool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

// ────────────────────────────────────────────────────────────────
// Customer tools
// ────────────────────────────────────────────────────────────────

func registerCustomerTools(t *tools.Tools) {

	t.Register("list_customers", tools.ToolDef{
		Description: "List or search customers. Use to find customers by name, email, phone, or tags, or to browse the customer list.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var customers []Customer
			if query, ok := params["query"].(string); ok && query != "" {
				customers, err = store.SearchCustomers(query, limit)
			} else if status, ok := params["status"].(string); ok && status != "" {
				customers, err = store.SearchCustomers(status, limit)
			} else {
				customers, err = store.ListCustomers(limit)
			}
			if err != nil {
				return "", fmt.Errorf("list customers: %w", err)
			}
			if len(customers) == 0 {
				return "No customers found.", nil
			}
			out, _ := json.MarshalIndent(customers, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"query":  {Type: "string", Description: "Search by name, email, phone, or tags"},
			"status": {Type: "string", Description: "Filter by status (e.g. prospect, active, inactive)"},
			"limit":  {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_customer", tools.ToolDef{
		Description: "Get full details of a specific customer by ID. Use when you need all info about one customer.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			c, err := store.GetCustomer(id)
			if err != nil {
				return "", fmt.Errorf("get customer: %w", err)
			}
			out, _ := json.MarshalIndent(c, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Customer ID", Required: true},
		},
	})

	t.Register("create_customer", tools.ToolDef{
		Description: "Create a new customer record in the CRM. Use when a new customer or prospect is identified.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			c := Customer{
				Name:          name,
				ContactName:   strOrDefault(params, "contact_name", ""),
				Email:         strOrDefault(params, "email", ""),
				Phone:         strOrDefault(params, "phone", ""),
				Address:       strOrDefault(params, "address", ""),
				City:          strOrDefault(params, "city", ""),
				State:         strOrDefault(params, "state", ""),
				Zip:           strOrDefault(params, "zip", ""),
				Source:        strOrDefault(params, "source", ""),
				Status:        strOrDefault(params, "status", "prospect"),
				Tags:          strOrDefault(params, "tags", ""),
				Notes:         strOrDefault(params, "notes", ""),
				PaymentMethod: strOrDefault(params, "payment_method", ""),
			}
			id, err := store.InsertCustomer(c)
			if err != nil {
				return "", fmt.Errorf("create customer: %w", err)
			}
			return fmt.Sprintf("Customer created (id=%d, name=%q, status=%q).", id, c.Name, c.Status), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":           {Type: "string", Description: "Customer or company name", Required: true},
			"contact_name":   {Type: "string", Description: "Primary contact person name"},
			"email":          {Type: "string", Description: "Email address"},
			"phone":          {Type: "string", Description: "Phone number"},
			"address":        {Type: "string", Description: "Street address"},
			"city":           {Type: "string", Description: "City"},
			"state":          {Type: "string", Description: "State"},
			"zip":            {Type: "string", Description: "ZIP code"},
			"source":         {Type: "string", Description: "Lead source (referral, website, social, etc.)"},
			"status":         {Type: "string", Description: "Customer status (default: prospect)"},
			"tags":           {Type: "string", Description: "Comma-separated tags"},
			"notes":          {Type: "string", Description: "Notes about the customer"},
			"payment_method": {Type: "string", Description: "Preferred payment method"},
		},
	})

	t.Register("update_customer", tools.ToolDef{
		Description: "Update an existing customer record. Only the fields you provide will be changed — omit fields to keep their current values.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			c, err := store.GetCustomer(id)
			if err != nil {
				return "", fmt.Errorf("get customer for update: %w", err)
			}
			if v, ok := params["name"].(string); ok && v != "" {
				c.Name = v
			}
			if v, ok := params["contact_name"].(string); ok {
				c.ContactName = v
			}
			if v, ok := params["email"].(string); ok {
				c.Email = v
			}
			if v, ok := params["phone"].(string); ok {
				c.Phone = v
			}
			if v, ok := params["address"].(string); ok {
				c.Address = v
			}
			if v, ok := params["city"].(string); ok {
				c.City = v
			}
			if v, ok := params["state"].(string); ok {
				c.State = v
			}
			if v, ok := params["zip"].(string); ok {
				c.Zip = v
			}
			if v, ok := params["source"].(string); ok {
				c.Source = v
			}
			if v, ok := params["status"].(string); ok {
				c.Status = v
			}
			if v, ok := params["tags"].(string); ok {
				c.Tags = v
			}
			if v, ok := params["notes"].(string); ok {
				c.Notes = v
			}
			if v, ok := params["payment_method"].(string); ok {
				c.PaymentMethod = v
			}
			if err := store.UpdateCustomer(*c); err != nil {
				return "", fmt.Errorf("update customer: %w", err)
			}
			return fmt.Sprintf("Customer %d (%q) updated.", c.ID, c.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":             {Type: "number", Description: "Customer ID", Required: true},
			"name":           {Type: "string", Description: "Customer or company name"},
			"contact_name":   {Type: "string", Description: "Primary contact person name"},
			"email":          {Type: "string", Description: "Email address"},
			"phone":          {Type: "string", Description: "Phone number"},
			"address":        {Type: "string", Description: "Street address"},
			"city":           {Type: "string", Description: "City"},
			"state":          {Type: "string", Description: "State"},
			"zip":            {Type: "string", Description: "ZIP code"},
			"source":         {Type: "string", Description: "Lead source"},
			"status":         {Type: "string", Description: "Customer status"},
			"tags":           {Type: "string", Description: "Comma-separated tags"},
			"notes":          {Type: "string", Description: "Notes"},
			"payment_method": {Type: "string", Description: "Preferred payment method"},
		},
	})

	t.Register("delete_customer", tools.ToolDef{
		Description: "Delete a customer record. Use with caution — this is permanent.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			if err := store.DeleteCustomer(id); err != nil {
				return "", fmt.Errorf("delete customer: %w", err)
			}
			return fmt.Sprintf("Customer %d deleted.", id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Customer ID", Required: true},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Property tools
// ────────────────────────────────────────────────────────────────

func registerPropertyTools(t *tools.Tools) {

	t.Register("list_properties", tools.ToolDef{
		Description: "List properties for a customer. Use to see all service addresses for a given customer.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			customerID := toInt64(params["customer_id"])
			if customerID == 0 {
				return "", fmt.Errorf("customer_id is required")
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			props, err := store.ListPropertiesByCustomer(customerID, limit)
			if err != nil {
				return "", fmt.Errorf("list properties: %w", err)
			}
			if len(props) == 0 {
				return "No properties found for this customer.", nil
			}
			out, _ := json.MarshalIndent(props, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"customer_id": {Type: "number", Description: "Customer ID to list properties for", Required: true},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_property", tools.ToolDef{
		Description: "Get full details of a specific property by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			p, err := store.GetProperty(id)
			if err != nil {
				return "", fmt.Errorf("get property: %w", err)
			}
			out, _ := json.MarshalIndent(p, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Property ID", Required: true},
		},
	})

	t.Register("create_property", tools.ToolDef{
		Description: "Create a new property record for a customer. Use when a customer has a new service address.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			customerID := toInt64(params["customer_id"])
			if customerID == 0 {
				return "", fmt.Errorf("customer_id is required")
			}
			address, _ := params["address"].(string)
			if address == "" {
				return "", fmt.Errorf("address is required")
			}
			p := Property{
				CustomerID:    customerID,
				Address:       address,
				City:          strOrDefault(params, "city", ""),
				State:         strOrDefault(params, "state", ""),
				Zip:           strOrDefault(params, "zip", ""),
				LotSizeSqft:   toFloat64(params["lot_size_sqft"]),
				LawnSqft:      toFloat64(params["lawn_sqft"]),
				BedSqft:       toFloat64(params["bed_sqft"]),
				HardscapeSqft: toFloat64(params["hardscape_sqft"]),
				Tags:          strOrDefault(params, "tags", ""),
				Notes:         strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertProperty(p)
			if err != nil {
				return "", fmt.Errorf("create property: %w", err)
			}
			return fmt.Sprintf("Property created (id=%d, address=%q, customer_id=%d).", id, p.Address, p.CustomerID), nil
		}),
		Params: map[string]tools.ParamDef{
			"customer_id":    {Type: "number", Description: "Customer ID this property belongs to", Required: true},
			"address":        {Type: "string", Description: "Street address", Required: true},
			"city":           {Type: "string", Description: "City"},
			"state":          {Type: "string", Description: "State"},
			"zip":            {Type: "string", Description: "ZIP code"},
			"lot_size_sqft":  {Type: "number", Description: "Total lot size in square feet"},
			"lawn_sqft":      {Type: "number", Description: "Lawn area in square feet"},
			"bed_sqft":       {Type: "number", Description: "Bed area in square feet"},
			"hardscape_sqft": {Type: "number", Description: "Hardscape area in square feet"},
			"tags":           {Type: "string", Description: "Comma-separated tags"},
			"notes":          {Type: "string", Description: "Notes about the property"},
		},
	})

	t.Register("update_property", tools.ToolDef{
		Description: "Update an existing property record. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			p, err := store.GetProperty(id)
			if err != nil {
				return "", fmt.Errorf("get property for update: %w", err)
			}
			if v := toInt64(params["customer_id"]); v != 0 {
				p.CustomerID = v
			}
			if v, ok := params["address"].(string); ok && v != "" {
				p.Address = v
			}
			if v, ok := params["city"].(string); ok {
				p.City = v
			}
			if v, ok := params["state"].(string); ok {
				p.State = v
			}
			if v, ok := params["zip"].(string); ok {
				p.Zip = v
			}
			if _, ok := params["lot_size_sqft"]; ok {
				p.LotSizeSqft = toFloat64(params["lot_size_sqft"])
			}
			if _, ok := params["lawn_sqft"]; ok {
				p.LawnSqft = toFloat64(params["lawn_sqft"])
			}
			if _, ok := params["bed_sqft"]; ok {
				p.BedSqft = toFloat64(params["bed_sqft"])
			}
			if _, ok := params["hardscape_sqft"]; ok {
				p.HardscapeSqft = toFloat64(params["hardscape_sqft"])
			}
			if v, ok := params["tags"].(string); ok {
				p.Tags = v
			}
			if v, ok := params["notes"].(string); ok {
				p.Notes = v
			}
			if err := store.UpdateProperty(*p); err != nil {
				return "", fmt.Errorf("update property: %w", err)
			}
			return fmt.Sprintf("Property %d (%q) updated.", p.ID, p.Address), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":             {Type: "number", Description: "Property ID", Required: true},
			"customer_id":    {Type: "number", Description: "Customer ID this property belongs to"},
			"address":        {Type: "string", Description: "Street address"},
			"city":           {Type: "string", Description: "City"},
			"state":          {Type: "string", Description: "State"},
			"zip":            {Type: "string", Description: "ZIP code"},
			"lot_size_sqft":  {Type: "number", Description: "Total lot size in square feet"},
			"lawn_sqft":      {Type: "number", Description: "Lawn area in square feet"},
			"bed_sqft":       {Type: "number", Description: "Bed area in square feet"},
			"hardscape_sqft": {Type: "number", Description: "Hardscape area in square feet"},
			"tags":           {Type: "string", Description: "Comma-separated tags"},
			"notes":          {Type: "string", Description: "Notes about the property"},
		},
	})

	t.Register("delete_property", tools.ToolDef{
		Description: "Delete a property record. Use with caution — this is permanent.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			if err := store.DeleteProperty(id); err != nil {
				return "", fmt.Errorf("delete property: %w", err)
			}
			return fmt.Sprintf("Property %d deleted.", id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Property ID", Required: true},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Crew Member tools
// ────────────────────────────────────────────────────────────────

func registerCrewMemberTools(t *tools.Tools) {

	t.Register("list_crew", tools.ToolDef{
		Description: "List crew members. Use to see who's on the team, filter by role, or find active crew members.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			role, _ := params["role"].(string)
			activeOnly := toBool(params["active_only"], true)

			members, err := store.ListCrewMembers(role, activeOnly, limit)
			if err != nil {
				return "", fmt.Errorf("list crew: %w", err)
			}
			if len(members) == 0 {
				return "No crew members found.", nil
			}
			out, _ := json.MarshalIndent(members, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"role":        {Type: "string", Description: "Filter by role (e.g. foreman, laborer, operator)"},
			"active_only": {Type: "boolean", Description: "Only show active crew members (default: true)"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_crew_member", tools.ToolDef{
		Description: "Get full details of a specific crew member by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			m, err := store.GetCrewMember(id)
			if err != nil {
				return "", fmt.Errorf("get crew member: %w", err)
			}
			out, _ := json.MarshalIndent(m, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Crew member ID", Required: true},
		},
	})

	t.Register("create_crew_member", tools.ToolDef{
		Description: "Create a new crew member record. Use when hiring or onboarding a new team member.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			m := CrewMember{
				Name:       name,
				Role:       strOrDefault(params, "role", ""),
				Phone:      strOrDefault(params, "phone", ""),
				Email:      strOrDefault(params, "email", ""),
				HourlyRate: toFloat64(params["hourly_rate"]),
				Skills:     strOrDefault(params, "skills", ""),
				Active:     toBool(params["active"], true),
				Notes:      strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertCrewMember(m)
			if err != nil {
				return "", fmt.Errorf("create crew member: %w", err)
			}
			return fmt.Sprintf("Crew member created (id=%d, name=%q, role=%q).", id, m.Name, m.Role), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":        {Type: "string", Description: "Full name", Required: true},
			"role":        {Type: "string", Description: "Role (e.g. foreman, laborer, operator)"},
			"phone":       {Type: "string", Description: "Phone number"},
			"email":       {Type: "string", Description: "Email address"},
			"hourly_rate": {Type: "number", Description: "Hourly pay rate"},
			"skills":      {Type: "string", Description: "Comma-separated skills"},
			"active":      {Type: "boolean", Description: "Whether the crew member is active (default: true)"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_crew_member", tools.ToolDef{
		Description: "Update an existing crew member record. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			m, err := store.GetCrewMember(id)
			if err != nil {
				return "", fmt.Errorf("get crew member for update: %w", err)
			}
			if v, ok := params["name"].(string); ok && v != "" {
				m.Name = v
			}
			if v, ok := params["role"].(string); ok {
				m.Role = v
			}
			if v, ok := params["phone"].(string); ok {
				m.Phone = v
			}
			if v, ok := params["email"].(string); ok {
				m.Email = v
			}
			if _, ok := params["hourly_rate"]; ok {
				m.HourlyRate = toFloat64(params["hourly_rate"])
			}
			if v, ok := params["skills"].(string); ok {
				m.Skills = v
			}
			if v, ok := params["active"].(bool); ok {
				m.Active = v
			}
			if v, ok := params["notes"].(string); ok {
				m.Notes = v
			}
			if err := store.UpdateCrewMember(*m); err != nil {
				return "", fmt.Errorf("update crew member: %w", err)
			}
			return fmt.Sprintf("Crew member %d (%q) updated.", m.ID, m.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":          {Type: "number", Description: "Crew member ID", Required: true},
			"name":        {Type: "string", Description: "Full name"},
			"role":        {Type: "string", Description: "Role"},
			"phone":       {Type: "string", Description: "Phone number"},
			"email":       {Type: "string", Description: "Email address"},
			"hourly_rate": {Type: "number", Description: "Hourly pay rate"},
			"skills":      {Type: "string", Description: "Comma-separated skills"},
			"active":      {Type: "boolean", Description: "Whether the crew member is active"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Crew tools
// ────────────────────────────────────────────────────────────────

func registerCrewTools(t *tools.Tools) {

	t.Register("list_crews", tools.ToolDef{
		Description: "List work crews. Use to see available crews, their foremen, and specialties.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			activeOnly := toBool(params["active_only"], true)

			crews, err := store.ListCrews(activeOnly, limit)
			if err != nil {
				return "", fmt.Errorf("list crews: %w", err)
			}
			if len(crews) == 0 {
				return "No crews found.", nil
			}
			out, _ := json.MarshalIndent(crews, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"active_only": {Type: "boolean", Description: "Only show active crews (default: true)"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_crew", tools.ToolDef{
		Description: "Get full details of a specific crew by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			c, err := store.GetCrew(id)
			if err != nil {
				return "", fmt.Errorf("get crew: %w", err)
			}
			out, _ := json.MarshalIndent(c, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Crew ID", Required: true},
		},
	})

	t.Register("create_crew", tools.ToolDef{
		Description: "Create a new work crew. Use when forming a new crew with a foreman and members.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			c := Crew{
				Name:        name,
				ForemanID:   toInt64(params["foreman_id"]),
				MemberIDs:   strOrDefault(params, "member_ids", "[]"),
				Truck:       strOrDefault(params, "truck", ""),
				Specialties: strOrDefault(params, "specialties", ""),
				Active:      toBool(params["active"], true),
			}
			id, err := store.InsertCrew(c)
			if err != nil {
				return "", fmt.Errorf("create crew: %w", err)
			}
			return fmt.Sprintf("Crew created (id=%d, name=%q).", id, c.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":        {Type: "string", Description: "Crew name", Required: true},
			"foreman_id":  {Type: "number", Description: "Crew member ID of the foreman"},
			"member_ids":  {Type: "string", Description: "JSON array of crew member IDs (e.g. [1,2,3])"},
			"truck":       {Type: "string", Description: "Truck/vehicle assigned"},
			"specialties": {Type: "string", Description: "Crew specialties"},
			"active":      {Type: "boolean", Description: "Whether the crew is active (default: true)"},
		},
	})

	t.Register("update_crew", tools.ToolDef{
		Description: "Update an existing crew record. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			c, err := store.GetCrew(id)
			if err != nil {
				return "", fmt.Errorf("get crew for update: %w", err)
			}
			if v, ok := params["name"].(string); ok && v != "" {
				c.Name = v
			}
			if _, ok := params["foreman_id"]; ok {
				c.ForemanID = toInt64(params["foreman_id"])
			}
			if v, ok := params["member_ids"].(string); ok {
				c.MemberIDs = v
			}
			if v, ok := params["truck"].(string); ok {
				c.Truck = v
			}
			if v, ok := params["specialties"].(string); ok {
				c.Specialties = v
			}
			if v, ok := params["active"].(bool); ok {
				c.Active = v
			}
			if err := store.UpdateCrew(*c); err != nil {
				return "", fmt.Errorf("update crew: %w", err)
			}
			return fmt.Sprintf("Crew %d (%q) updated.", c.ID, c.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":          {Type: "number", Description: "Crew ID", Required: true},
			"name":        {Type: "string", Description: "Crew name"},
			"foreman_id":  {Type: "number", Description: "Crew member ID of the foreman"},
			"member_ids":  {Type: "string", Description: "JSON array of crew member IDs"},
			"truck":       {Type: "string", Description: "Truck/vehicle assigned"},
			"specialties": {Type: "string", Description: "Crew specialties"},
			"active":      {Type: "boolean", Description: "Whether the crew is active"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Item/Catalog tools
// ────────────────────────────────────────────────────────────────

func registerItemTools(t *tools.Tools) {

	t.Register("list_items", tools.ToolDef{
		Description: "List or search catalog items (materials, services). Use to find items for estimates, check pricing, or browse the catalog.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			activeOnly := toBool(params["active_only"], true)

			var items []Item
			if query, ok := params["query"].(string); ok && query != "" {
				items, err = store.SearchItems(query, limit)
			} else {
				category, _ := params["category"].(string)
				items, err = store.ListItems(category, activeOnly, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list items: %w", err)
			}
			if len(items) == 0 {
				return "No items found.", nil
			}
			out, _ := json.MarshalIndent(items, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"category":    {Type: "string", Description: "Filter by category (e.g. material, labor, equipment)"},
			"active_only": {Type: "boolean", Description: "Only show active items (default: true)"},
			"query":       {Type: "string", Description: "Search by name, category, supplier, SKU, or notes"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_item", tools.ToolDef{
		Description: "Get full details of a specific catalog item by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			item, err := store.GetItem(id)
			if err != nil {
				return "", fmt.Errorf("get item: %w", err)
			}
			out, _ := json.MarshalIndent(item, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Item ID", Required: true},
		},
	})

	t.Register("create_item", tools.ToolDef{
		Description: "Create a new catalog item. Use when adding a new material, service, or product to the catalog.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			i := Item{
				Name:     name,
				Category: strOrDefault(params, "category", ""),
				Unit:     strOrDefault(params, "unit", "each"),
				Cost:     toFloat64(params["cost"]),
				Price:    toFloat64(params["price"]),
				Supplier: strOrDefault(params, "supplier", ""),
				SKU:      strOrDefault(params, "sku", ""),
				Taxable:  toBool(params["taxable"], true),
				Active:   toBool(params["active"], true),
				Notes:    strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertItem(i)
			if err != nil {
				return "", fmt.Errorf("create item: %w", err)
			}
			return fmt.Sprintf("Item created (id=%d, name=%q, category=%q).", id, i.Name, i.Category), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":     {Type: "string", Description: "Item name", Required: true},
			"category": {Type: "string", Description: "Category (e.g. material, labor, equipment)"},
			"unit":     {Type: "string", Description: "Unit of measure (default: each)"},
			"cost":     {Type: "number", Description: "Cost per unit"},
			"price":    {Type: "number", Description: "Sell price per unit"},
			"supplier": {Type: "string", Description: "Supplier name"},
			"sku":      {Type: "string", Description: "SKU or part number"},
			"taxable":  {Type: "boolean", Description: "Whether the item is taxable (default: true)"},
			"active":   {Type: "boolean", Description: "Whether the item is active (default: true)"},
			"notes":    {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_item", tools.ToolDef{
		Description: "Update an existing catalog item. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			i, err := store.GetItem(id)
			if err != nil {
				return "", fmt.Errorf("get item for update: %w", err)
			}
			if v, ok := params["name"].(string); ok && v != "" {
				i.Name = v
			}
			if v, ok := params["category"].(string); ok {
				i.Category = v
			}
			if v, ok := params["unit"].(string); ok {
				i.Unit = v
			}
			if _, ok := params["cost"]; ok {
				i.Cost = toFloat64(params["cost"])
			}
			if _, ok := params["price"]; ok {
				i.Price = toFloat64(params["price"])
			}
			if v, ok := params["supplier"].(string); ok {
				i.Supplier = v
			}
			if v, ok := params["sku"].(string); ok {
				i.SKU = v
			}
			if v, ok := params["taxable"].(bool); ok {
				i.Taxable = v
			}
			if v, ok := params["active"].(bool); ok {
				i.Active = v
			}
			if v, ok := params["notes"].(string); ok {
				i.Notes = v
			}
			if err := store.UpdateItem(*i); err != nil {
				return "", fmt.Errorf("update item: %w", err)
			}
			return fmt.Sprintf("Item %d (%q) updated.", i.ID, i.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":       {Type: "number", Description: "Item ID", Required: true},
			"name":     {Type: "string", Description: "Item name"},
			"category": {Type: "string", Description: "Category"},
			"unit":     {Type: "string", Description: "Unit of measure"},
			"cost":     {Type: "number", Description: "Cost per unit"},
			"price":    {Type: "number", Description: "Sell price per unit"},
			"supplier": {Type: "string", Description: "Supplier name"},
			"sku":      {Type: "string", Description: "SKU or part number"},
			"taxable":  {Type: "boolean", Description: "Whether the item is taxable"},
			"active":   {Type: "boolean", Description: "Whether the item is active"},
			"notes":    {Type: "string", Description: "Notes"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Estimate tools
// ────────────────────────────────────────────────────────────────

func registerEstimateTools(t *tools.Tools) {

	t.Register("list_estimates", tools.ToolDef{
		Description: "List estimates/quotes. Use to see pending proposals, filter by status, or find estimates for a specific customer.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var estimates []Estimate
			if customerID := toInt64(params["customer_id"]); customerID != 0 {
				estimates, err = store.ListEstimatesByCustomer(customerID, limit)
			} else {
				status, _ := params["status"].(string)
				estimates, err = store.ListEstimates(status, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list estimates: %w", err)
			}
			if len(estimates) == 0 {
				return "No estimates found.", nil
			}
			out, _ := json.MarshalIndent(estimates, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"status":      {Type: "string", Description: "Filter by status (draft, sent, accepted, declined, expired)"},
			"customer_id": {Type: "number", Description: "Filter by customer ID"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_estimate", tools.ToolDef{
		Description: "Get full details of a specific estimate by ID, including line items.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			e, err := store.GetEstimate(id)
			if err != nil {
				return "", fmt.Errorf("get estimate: %w", err)
			}
			out, _ := json.MarshalIndent(e, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Estimate ID", Required: true},
		},
	})

	t.Register("create_estimate", tools.ToolDef{
		Description: "Create a new estimate/quote. Use when building a proposal for a customer.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			customerID := toInt64(params["customer_id"])
			if customerID == 0 {
				return "", fmt.Errorf("customer_id is required")
			}
			title, _ := params["title"].(string)
			if title == "" {
				return "", fmt.Errorf("title is required")
			}
			e := Estimate{
				CustomerID: customerID,
				PropertyID: toInt64(params["property_id"]),
				JobID:      toInt64(params["job_id"]),
				Title:      title,
				Status:     strOrDefault(params, "status", "draft"),
				LineItems:  strOrDefault(params, "line_items", "[]"),
				Subtotal:   toFloat64(params["subtotal"]),
				Tax:        toFloat64(params["tax"]),
				Total:      toFloat64(params["total"]),
				MarginPct:  toFloat64(params["margin_pct"]),
				DepositPct: toFloat64(params["deposit_pct"]),
				ValidUntil: strOrDefault(params, "valid_until", ""),
				Notes:      strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertEstimate(e)
			if err != nil {
				return "", fmt.Errorf("create estimate: %w", err)
			}
			return fmt.Sprintf("Estimate created (id=%d, title=%q, total=$%.2f).", id, e.Title, e.Total), nil
		}),
		Params: map[string]tools.ParamDef{
			"customer_id": {Type: "number", Description: "Customer ID", Required: true},
			"property_id": {Type: "number", Description: "Property ID"},
			"job_id":      {Type: "number", Description: "Job ID to link this estimate to"},
			"title":       {Type: "string", Description: "Estimate title", Required: true},
			"status":      {Type: "string", Description: "Status (default: draft)"},
			"line_items":  {Type: "string", Description: "JSON array of line items"},
			"subtotal":    {Type: "number", Description: "Subtotal before tax"},
			"tax":         {Type: "number", Description: "Tax amount"},
			"total":       {Type: "number", Description: "Total including tax"},
			"margin_pct":  {Type: "number", Description: "Target margin percentage"},
			"deposit_pct": {Type: "number", Description: "Deposit percentage"},
			"valid_until": {Type: "string", Description: "Expiration date (YYYY-MM-DD)"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_estimate", tools.ToolDef{
		Description: "Update an existing estimate. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			e, err := store.GetEstimate(id)
			if err != nil {
				return "", fmt.Errorf("get estimate for update: %w", err)
			}
			if v := toInt64(params["customer_id"]); v != 0 {
				e.CustomerID = v
			}
			if _, ok := params["property_id"]; ok {
				e.PropertyID = toInt64(params["property_id"])
			}
			if _, ok := params["job_id"]; ok {
				e.JobID = toInt64(params["job_id"])
			}
			if v, ok := params["title"].(string); ok && v != "" {
				e.Title = v
			}
			if v, ok := params["status"].(string); ok && v != "" {
				e.Status = v
			}
			if v, ok := params["line_items"].(string); ok {
				e.LineItems = v
			}
			if _, ok := params["subtotal"]; ok {
				e.Subtotal = toFloat64(params["subtotal"])
			}
			if _, ok := params["tax"]; ok {
				e.Tax = toFloat64(params["tax"])
			}
			if _, ok := params["total"]; ok {
				e.Total = toFloat64(params["total"])
			}
			if _, ok := params["margin_pct"]; ok {
				e.MarginPct = toFloat64(params["margin_pct"])
			}
			if _, ok := params["deposit_pct"]; ok {
				e.DepositPct = toFloat64(params["deposit_pct"])
			}
			if v, ok := params["valid_until"].(string); ok {
				e.ValidUntil = v
			}
			if v, ok := params["notes"].(string); ok {
				e.Notes = v
			}
			if err := store.UpdateEstimate(*e); err != nil {
				return "", fmt.Errorf("update estimate: %w", err)
			}
			return fmt.Sprintf("Estimate %d (%q) updated.", e.ID, e.Title), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":          {Type: "number", Description: "Estimate ID", Required: true},
			"customer_id": {Type: "number", Description: "Customer ID"},
			"property_id": {Type: "number", Description: "Property ID"},
			"job_id":      {Type: "number", Description: "Job ID"},
			"title":       {Type: "string", Description: "Estimate title"},
			"status":      {Type: "string", Description: "Status"},
			"line_items":  {Type: "string", Description: "JSON array of line items"},
			"subtotal":    {Type: "number", Description: "Subtotal before tax"},
			"tax":         {Type: "number", Description: "Tax amount"},
			"total":       {Type: "number", Description: "Total including tax"},
			"margin_pct":  {Type: "number", Description: "Target margin percentage"},
			"deposit_pct": {Type: "number", Description: "Deposit percentage"},
			"valid_until": {Type: "string", Description: "Expiration date (YYYY-MM-DD)"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})

	t.Register("send_estimate", tools.ToolDef{
		Description: "Update the status of an estimate. Use to mark it as sent, accepted, declined, or expired.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			status, _ := params["status"].(string)
			if status == "" {
				return "", fmt.Errorf("status is required (draft, sent, accepted, declined, expired)")
			}
			if err := store.UpdateEstimateStatus(id, status); err != nil {
				return "", fmt.Errorf("send estimate: %w", err)
			}
			return fmt.Sprintf("Estimate %d status updated to %q.", id, status), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":     {Type: "number", Description: "Estimate ID", Required: true},
			"status": {Type: "string", Description: "New status: draft, sent, accepted, declined, expired", Required: true},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Calendar Event tools
// ────────────────────────────────────────────────────────────────

func registerCalendarEventTools(t *tools.Tools) {

	t.Register("list_calendar", tools.ToolDef{
		Description: "List calendar events. Provide a date to see one day, start_date+end_date for a range, or crew_id+date for a crew's schedule. Use to check scheduling conflicts or plan the week.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 50
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			crewID := toInt64(params["crew_id"])
			date, _ := params["date"].(string)
			startDate, _ := params["start_date"].(string)
			endDate, _ := params["end_date"].(string)

			var events []CalendarEvent
			if crewID != 0 && date != "" {
				events, err = store.ListCalendarEventsByCrew(crewID, date, limit)
			} else if startDate != "" && endDate != "" {
				events, err = store.ListCalendarEventsByRange(startDate, endDate, limit)
			} else if date != "" {
				events, err = store.ListCalendarEvents(date, limit)
			} else {
				return "", fmt.Errorf("provide date, start_date+end_date, or crew_id+date")
			}
			if err != nil {
				return "", fmt.Errorf("list calendar: %w", err)
			}
			if len(events) == 0 {
				return "No calendar events found.", nil
			}
			out, _ := json.MarshalIndent(events, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"date":       {Type: "string", Description: "Date to show (YYYY-MM-DD)"},
			"start_date": {Type: "string", Description: "Range start date (YYYY-MM-DD)"},
			"end_date":   {Type: "string", Description: "Range end date (YYYY-MM-DD)"},
			"crew_id":    {Type: "number", Description: "Filter by crew ID"},
			"limit":      {Type: "number", Description: "Max results (default 50)"},
		},
	})

	t.Register("get_calendar_event", tools.ToolDef{
		Description: "Get full details of a specific calendar event by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			e, err := store.GetCalendarEvent(id)
			if err != nil {
				return "", fmt.Errorf("get calendar event: %w", err)
			}
			out, _ := json.MarshalIndent(e, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Calendar event ID", Required: true},
		},
	})

	t.Register("create_calendar_event", tools.ToolDef{
		Description: "Create a new calendar event. Use to schedule jobs, crew assignments, meetings, or deadlines.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			title, _ := params["title"].(string)
			if title == "" {
				return "", fmt.Errorf("title is required")
			}
			date, _ := params["date"].(string)
			if date == "" {
				return "", fmt.Errorf("date is required")
			}
			e := CalendarEvent{
				Title:      title,
				EventType:  strOrDefault(params, "event_type", "job"),
				Date:       date,
				StartTime:  strOrDefault(params, "start_time", ""),
				EndTime:    strOrDefault(params, "end_time", ""),
				CrewID:     toInt64(params["crew_id"]),
				JobID:      toInt64(params["job_id"]),
				CustomerID: toInt64(params["customer_id"]),
				PropertyID: toInt64(params["property_id"]),
				Status:     strOrDefault(params, "status", "scheduled"),
				Notes:      strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertCalendarEvent(e)
			if err != nil {
				return "", fmt.Errorf("create calendar event: %w", err)
			}
			return fmt.Sprintf("Calendar event created (id=%d, title=%q, date=%s).", id, e.Title, e.Date), nil
		}),
		Params: map[string]tools.ParamDef{
			"title":       {Type: "string", Description: "Event title", Required: true},
			"event_type":  {Type: "string", Description: "Event type (job, meeting, deadline, etc.)"},
			"date":        {Type: "string", Description: "Date (YYYY-MM-DD)", Required: true},
			"start_time":  {Type: "string", Description: "Start time (HH:MM)"},
			"end_time":    {Type: "string", Description: "End time (HH:MM)"},
			"crew_id":     {Type: "number", Description: "Crew ID to assign"},
			"job_id":      {Type: "number", Description: "Job ID to link"},
			"customer_id": {Type: "number", Description: "Customer ID"},
			"property_id": {Type: "number", Description: "Property ID"},
			"status":      {Type: "string", Description: "Status (default: scheduled)"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_calendar_event", tools.ToolDef{
		Description: "Update an existing calendar event. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			e, err := store.GetCalendarEvent(id)
			if err != nil {
				return "", fmt.Errorf("get calendar event for update: %w", err)
			}
			if v, ok := params["title"].(string); ok && v != "" {
				e.Title = v
			}
			if v, ok := params["event_type"].(string); ok {
				e.EventType = v
			}
			if v, ok := params["date"].(string); ok && v != "" {
				e.Date = v
			}
			if v, ok := params["start_time"].(string); ok {
				e.StartTime = v
			}
			if v, ok := params["end_time"].(string); ok {
				e.EndTime = v
			}
			if _, ok := params["crew_id"]; ok {
				e.CrewID = toInt64(params["crew_id"])
			}
			if _, ok := params["job_id"]; ok {
				e.JobID = toInt64(params["job_id"])
			}
			if _, ok := params["customer_id"]; ok {
				e.CustomerID = toInt64(params["customer_id"])
			}
			if _, ok := params["property_id"]; ok {
				e.PropertyID = toInt64(params["property_id"])
			}
			if v, ok := params["status"].(string); ok {
				e.Status = v
			}
			if v, ok := params["notes"].(string); ok {
				e.Notes = v
			}
			if err := store.UpdateCalendarEvent(*e); err != nil {
				return "", fmt.Errorf("update calendar event: %w", err)
			}
			return fmt.Sprintf("Calendar event %d (%q) updated.", e.ID, e.Title), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":          {Type: "number", Description: "Calendar event ID", Required: true},
			"title":       {Type: "string", Description: "Event title"},
			"event_type":  {Type: "string", Description: "Event type"},
			"date":        {Type: "string", Description: "Date (YYYY-MM-DD)"},
			"start_time":  {Type: "string", Description: "Start time (HH:MM)"},
			"end_time":    {Type: "string", Description: "End time (HH:MM)"},
			"crew_id":     {Type: "number", Description: "Crew ID"},
			"job_id":      {Type: "number", Description: "Job ID"},
			"customer_id": {Type: "number", Description: "Customer ID"},
			"property_id": {Type: "number", Description: "Property ID"},
			"status":      {Type: "string", Description: "Status"},
			"notes":       {Type: "string", Description: "Notes"},
		},
	})

	t.Register("delete_calendar_event", tools.ToolDef{
		Description: "Delete a calendar event. Use to remove cancelled or incorrect events.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			if err := store.DeleteCalendarEvent(id); err != nil {
				return "", fmt.Errorf("delete calendar event: %w", err)
			}
			return fmt.Sprintf("Calendar event %d deleted.", id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Calendar event ID", Required: true},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Invoice tools
// ────────────────────────────────────────────────────────────────

func registerInvoiceTools(t *tools.Tools) {

	t.Register("list_invoices", tools.ToolDef{
		Description: "List invoices. Use to check outstanding balances, filter by status (draft, sent, paid, overdue), or see a customer's invoice history.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var invoices []Invoice
			if customerID := toInt64(params["customer_id"]); customerID != 0 {
				invoices, err = store.ListInvoicesByCustomer(customerID, limit)
			} else {
				status, _ := params["status"].(string)
				invoices, err = store.ListInvoices(status, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list invoices: %w", err)
			}
			if len(invoices) == 0 {
				return "No invoices found.", nil
			}
			out, _ := json.MarshalIndent(invoices, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"status":      {Type: "string", Description: "Filter by status (draft, sent, paid, overdue)"},
			"customer_id": {Type: "number", Description: "Filter by customer ID"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_invoice", tools.ToolDef{
		Description: "Get full details of a specific invoice by ID, including line items and payment info.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			inv, err := store.GetInvoice(id)
			if err != nil {
				return "", fmt.Errorf("get invoice: %w", err)
			}
			out, _ := json.MarshalIndent(inv, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Invoice ID", Required: true},
		},
	})

	t.Register("create_invoice", tools.ToolDef{
		Description: "Create a new invoice. Use when billing a customer for completed work.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			invoiceNumber, _ := params["invoice_number"].(string)
			if invoiceNumber == "" {
				return "", fmt.Errorf("invoice_number is required")
			}
			customerID := toInt64(params["customer_id"])
			if customerID == 0 {
				return "", fmt.Errorf("customer_id is required")
			}
			inv := Invoice{
				InvoiceNumber:  invoiceNumber,
				CustomerID:     customerID,
				JobID:          toInt64(params["job_id"]),
				EstimateID:     toInt64(params["estimate_id"]),
				Status:         strOrDefault(params, "status", "draft"),
				LineItems:      strOrDefault(params, "line_items", "[]"),
				Subtotal:       toFloat64(params["subtotal"]),
				Tax:            toFloat64(params["tax"]),
				Total:          toFloat64(params["total"]),
				DepositApplied: toFloat64(params["deposit_applied"]),
				AmountDue:      toFloat64(params["amount_due"]),
				IssuedDate:     strOrDefault(params, "issued_date", ""),
				DueDate:        strOrDefault(params, "due_date", ""),
				Notes:          strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertInvoice(inv)
			if err != nil {
				return "", fmt.Errorf("create invoice: %w", err)
			}
			return fmt.Sprintf("Invoice created (id=%d, number=%q, total=$%.2f).", id, inv.InvoiceNumber, inv.Total), nil
		}),
		Params: map[string]tools.ParamDef{
			"invoice_number":  {Type: "string", Description: "Invoice number (e.g. INV-1001)", Required: true},
			"customer_id":     {Type: "number", Description: "Customer ID", Required: true},
			"job_id":          {Type: "number", Description: "Job ID to link"},
			"estimate_id":     {Type: "number", Description: "Estimate ID to link"},
			"status":          {Type: "string", Description: "Status (default: draft)"},
			"line_items":      {Type: "string", Description: "JSON array of line items"},
			"subtotal":        {Type: "number", Description: "Subtotal before tax"},
			"tax":             {Type: "number", Description: "Tax amount"},
			"total":           {Type: "number", Description: "Total including tax"},
			"deposit_applied": {Type: "number", Description: "Deposit amount applied"},
			"amount_due":      {Type: "number", Description: "Amount due after deposit"},
			"issued_date":     {Type: "string", Description: "Date issued (YYYY-MM-DD)"},
			"due_date":        {Type: "string", Description: "Payment due date (YYYY-MM-DD)"},
			"notes":           {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_invoice", tools.ToolDef{
		Description: "Update an existing invoice. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			inv, err := store.GetInvoice(id)
			if err != nil {
				return "", fmt.Errorf("get invoice for update: %w", err)
			}
			if v, ok := params["invoice_number"].(string); ok && v != "" {
				inv.InvoiceNumber = v
			}
			if v := toInt64(params["customer_id"]); v != 0 {
				inv.CustomerID = v
			}
			if _, ok := params["job_id"]; ok {
				inv.JobID = toInt64(params["job_id"])
			}
			if _, ok := params["estimate_id"]; ok {
				inv.EstimateID = toInt64(params["estimate_id"])
			}
			if v, ok := params["status"].(string); ok && v != "" {
				inv.Status = v
			}
			if v, ok := params["line_items"].(string); ok {
				inv.LineItems = v
			}
			if _, ok := params["subtotal"]; ok {
				inv.Subtotal = toFloat64(params["subtotal"])
			}
			if _, ok := params["tax"]; ok {
				inv.Tax = toFloat64(params["tax"])
			}
			if _, ok := params["total"]; ok {
				inv.Total = toFloat64(params["total"])
			}
			if _, ok := params["deposit_applied"]; ok {
				inv.DepositApplied = toFloat64(params["deposit_applied"])
			}
			if _, ok := params["amount_due"]; ok {
				inv.AmountDue = toFloat64(params["amount_due"])
			}
			if v, ok := params["issued_date"].(string); ok {
				inv.IssuedDate = v
			}
			if v, ok := params["due_date"].(string); ok {
				inv.DueDate = v
			}
			if v, ok := params["notes"].(string); ok {
				inv.Notes = v
			}
			if err := store.UpdateInvoice(*inv); err != nil {
				return "", fmt.Errorf("update invoice: %w", err)
			}
			return fmt.Sprintf("Invoice %d (%q) updated.", inv.ID, inv.InvoiceNumber), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":              {Type: "number", Description: "Invoice ID", Required: true},
			"invoice_number":  {Type: "string", Description: "Invoice number"},
			"customer_id":     {Type: "number", Description: "Customer ID"},
			"job_id":          {Type: "number", Description: "Job ID"},
			"estimate_id":     {Type: "number", Description: "Estimate ID"},
			"status":          {Type: "string", Description: "Status"},
			"line_items":      {Type: "string", Description: "JSON array of line items"},
			"subtotal":        {Type: "number", Description: "Subtotal before tax"},
			"tax":             {Type: "number", Description: "Tax amount"},
			"total":           {Type: "number", Description: "Total including tax"},
			"deposit_applied": {Type: "number", Description: "Deposit amount applied"},
			"amount_due":      {Type: "number", Description: "Amount due after deposit"},
			"issued_date":     {Type: "string", Description: "Date issued (YYYY-MM-DD)"},
			"due_date":        {Type: "string", Description: "Payment due date (YYYY-MM-DD)"},
			"notes":           {Type: "string", Description: "Notes"},
		},
	})

	t.Register("mark_invoice_paid", tools.ToolDef{
		Description: "Mark an invoice as paid. Use when payment is received to close out the invoice.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			status := strOrDefault(params, "status", "paid")
			paidDate := strOrDefault(params, "paid_date", "")
			paymentMethod := strOrDefault(params, "payment_method", "")
			if err := store.UpdateInvoiceStatus(id, status, paidDate, paymentMethod); err != nil {
				return "", fmt.Errorf("mark invoice paid: %w", err)
			}
			return fmt.Sprintf("Invoice %d marked as %q.", id, status), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":             {Type: "number", Description: "Invoice ID", Required: true},
			"status":         {Type: "string", Description: "Status (default: paid)"},
			"paid_date":      {Type: "string", Description: "Date payment received (YYYY-MM-DD)"},
			"payment_method": {Type: "string", Description: "Payment method (check, card, cash, etc.)"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Vendor tools
// ────────────────────────────────────────────────────────────────

func registerVendorTools(t *tools.Tools) {

	t.Register("list_vendors", tools.ToolDef{
		Description: "List or search vendors/suppliers. Use to find material suppliers, subcontractors, or browse the vendor list.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			activeOnly := toBool(params["active_only"], true)

			var vendors []Vendor
			if query, ok := params["query"].(string); ok && query != "" {
				vendors, err = store.SearchVendors(query, limit)
			} else {
				vendors, err = store.ListVendors(activeOnly, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list vendors: %w", err)
			}
			if len(vendors) == 0 {
				return "No vendors found.", nil
			}
			out, _ := json.MarshalIndent(vendors, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"active_only": {Type: "boolean", Description: "Only show active vendors (default: true)"},
			"query":       {Type: "string", Description: "Search by name, contact, specialty, or notes"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_vendor", tools.ToolDef{
		Description: "Get full details of a specific vendor by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			v, err := store.GetVendor(id)
			if err != nil {
				return "", fmt.Errorf("get vendor: %w", err)
			}
			out, _ := json.MarshalIndent(v, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Vendor ID", Required: true},
		},
	})

	t.Register("create_vendor", tools.ToolDef{
		Description: "Create a new vendor/supplier record. Use when adding a new material supplier or subcontractor.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			v := Vendor{
				Name:          name,
				ContactName:   strOrDefault(params, "contact_name", ""),
				Phone:         strOrDefault(params, "phone", ""),
				Email:         strOrDefault(params, "email", ""),
				Address:       strOrDefault(params, "address", ""),
				Specialty:     strOrDefault(params, "specialty", ""),
				PaymentTerms:  strOrDefault(params, "payment_terms", ""),
				AccountNumber: strOrDefault(params, "account_number", ""),
				Active:        toBool(params["active"], true),
				Notes:         strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertVendor(v)
			if err != nil {
				return "", fmt.Errorf("create vendor: %w", err)
			}
			return fmt.Sprintf("Vendor created (id=%d, name=%q).", id, v.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":           {Type: "string", Description: "Vendor/company name", Required: true},
			"contact_name":   {Type: "string", Description: "Primary contact person"},
			"phone":          {Type: "string", Description: "Phone number"},
			"email":          {Type: "string", Description: "Email address"},
			"address":        {Type: "string", Description: "Street address"},
			"specialty":      {Type: "string", Description: "Specialty (e.g. hardscape, nursery, equipment)"},
			"payment_terms":  {Type: "string", Description: "Payment terms (e.g. Net 30)"},
			"account_number": {Type: "string", Description: "Account number with vendor"},
			"active":         {Type: "boolean", Description: "Whether the vendor is active (default: true)"},
			"notes":          {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_vendor", tools.ToolDef{
		Description: "Update an existing vendor record. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			v, err := store.GetVendor(id)
			if err != nil {
				return "", fmt.Errorf("get vendor for update: %w", err)
			}
			if val, ok := params["name"].(string); ok && val != "" {
				v.Name = val
			}
			if val, ok := params["contact_name"].(string); ok {
				v.ContactName = val
			}
			if val, ok := params["phone"].(string); ok {
				v.Phone = val
			}
			if val, ok := params["email"].(string); ok {
				v.Email = val
			}
			if val, ok := params["address"].(string); ok {
				v.Address = val
			}
			if val, ok := params["specialty"].(string); ok {
				v.Specialty = val
			}
			if val, ok := params["payment_terms"].(string); ok {
				v.PaymentTerms = val
			}
			if val, ok := params["account_number"].(string); ok {
				v.AccountNumber = val
			}
			if val, ok := params["active"].(bool); ok {
				v.Active = val
			}
			if val, ok := params["notes"].(string); ok {
				v.Notes = val
			}
			if err := store.UpdateVendor(*v); err != nil {
				return "", fmt.Errorf("update vendor: %w", err)
			}
			return fmt.Sprintf("Vendor %d (%q) updated.", v.ID, v.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":             {Type: "number", Description: "Vendor ID", Required: true},
			"name":           {Type: "string", Description: "Vendor/company name"},
			"contact_name":   {Type: "string", Description: "Primary contact person"},
			"phone":          {Type: "string", Description: "Phone number"},
			"email":          {Type: "string", Description: "Email address"},
			"address":        {Type: "string", Description: "Street address"},
			"specialty":      {Type: "string", Description: "Specialty"},
			"payment_terms":  {Type: "string", Description: "Payment terms"},
			"account_number": {Type: "string", Description: "Account number"},
			"active":         {Type: "boolean", Description: "Whether the vendor is active"},
			"notes":          {Type: "string", Description: "Notes"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Sales Lead tools
// ────────────────────────────────────────────────────────────────

func registerSalesLeadTools(t *tools.Tools) {

	t.Register("list_sales_leads", tools.ToolDef{
		Description: "List sales leads. Use to see the sales pipeline, filter by status (new, contacted, qualified, proposal, won, lost), or see leads assigned to a specific person.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var leads []SalesLead
			if assignee, ok := params["assigned_to"].(string); ok && assignee != "" {
				leads, err = store.ListSalesLeadsByAssignee(assignee, limit)
			} else {
				status, _ := params["status"].(string)
				leads, err = store.ListSalesLeads(status, limit)
			}
			if err != nil {
				return "", fmt.Errorf("list sales leads: %w", err)
			}
			if len(leads) == 0 {
				return "No sales leads found.", nil
			}
			out, _ := json.MarshalIndent(leads, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"status":      {Type: "string", Description: "Filter by status (new, contacted, qualified, proposal, won, lost)"},
			"assigned_to": {Type: "string", Description: "Filter by assigned person/agent"},
			"limit":       {Type: "number", Description: "Max results (default 20)"},
		},
	})

	t.Register("get_sales_lead", tools.ToolDef{
		Description: "Get full details of a specific sales lead by ID.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			l, err := store.GetSalesLead(id)
			if err != nil {
				return "", fmt.Errorf("get sales lead: %w", err)
			}
			out, _ := json.MarshalIndent(l, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Sales lead ID", Required: true},
		},
	})

	t.Register("create_sales_lead", tools.ToolDef{
		Description: "Create a new sales lead. Use when a potential customer inquiry comes in.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			l := SalesLead{
				Name:            name,
				CustomerID:      toInt64(params["customer_id"]),
				Phone:           strOrDefault(params, "phone", ""),
				Email:           strOrDefault(params, "email", ""),
				Source:          strOrDefault(params, "source", ""),
				Status:          strOrDefault(params, "status", "new"),
				EstimatedValue:  toFloat64(params["estimated_value"]),
				JobType:         strOrDefault(params, "job_type", ""),
				PropertyAddress: strOrDefault(params, "property_address", ""),
				AssignedTo:      strOrDefault(params, "assigned_to", ""),
				Notes:           strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertSalesLead(l)
			if err != nil {
				return "", fmt.Errorf("create sales lead: %w", err)
			}
			return fmt.Sprintf("Sales lead created (id=%d, name=%q, status=%q).", id, l.Name, l.Status), nil
		}),
		Params: map[string]tools.ParamDef{
			"name":             {Type: "string", Description: "Lead name", Required: true},
			"customer_id":      {Type: "number", Description: "Existing customer ID (if applicable)"},
			"phone":            {Type: "string", Description: "Phone number"},
			"email":            {Type: "string", Description: "Email address"},
			"source":           {Type: "string", Description: "Lead source (referral, website, social, etc.)"},
			"status":           {Type: "string", Description: "Status (default: new)"},
			"estimated_value":  {Type: "number", Description: "Estimated dollar value of the opportunity"},
			"job_type":         {Type: "string", Description: "Type of work requested"},
			"property_address": {Type: "string", Description: "Property address for the potential work"},
			"assigned_to":      {Type: "string", Description: "Person or agent assigned to this lead"},
			"notes":            {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_sales_lead", tools.ToolDef{
		Description: "Update an existing sales lead. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			l, err := store.GetSalesLead(id)
			if err != nil {
				return "", fmt.Errorf("get sales lead for update: %w", err)
			}
			if v, ok := params["name"].(string); ok && v != "" {
				l.Name = v
			}
			if _, ok := params["customer_id"]; ok {
				l.CustomerID = toInt64(params["customer_id"])
			}
			if v, ok := params["phone"].(string); ok {
				l.Phone = v
			}
			if v, ok := params["email"].(string); ok {
				l.Email = v
			}
			if v, ok := params["source"].(string); ok {
				l.Source = v
			}
			if v, ok := params["status"].(string); ok && v != "" {
				l.Status = v
			}
			if _, ok := params["estimated_value"]; ok {
				l.EstimatedValue = toFloat64(params["estimated_value"])
			}
			if v, ok := params["job_type"].(string); ok {
				l.JobType = v
			}
			if v, ok := params["property_address"].(string); ok {
				l.PropertyAddress = v
			}
			if v, ok := params["assigned_to"].(string); ok {
				l.AssignedTo = v
			}
			if v, ok := params["lost_reason"].(string); ok {
				l.LostReason = v
			}
			if v, ok := params["notes"].(string); ok {
				l.Notes = v
			}
			if err := store.UpdateSalesLead(*l); err != nil {
				return "", fmt.Errorf("update sales lead: %w", err)
			}
			return fmt.Sprintf("Sales lead %d (%q) updated.", l.ID, l.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":               {Type: "number", Description: "Sales lead ID", Required: true},
			"name":             {Type: "string", Description: "Lead name"},
			"customer_id":      {Type: "number", Description: "Customer ID"},
			"phone":            {Type: "string", Description: "Phone number"},
			"email":            {Type: "string", Description: "Email address"},
			"source":           {Type: "string", Description: "Lead source"},
			"status":           {Type: "string", Description: "Status"},
			"estimated_value":  {Type: "number", Description: "Estimated dollar value"},
			"job_type":         {Type: "string", Description: "Type of work"},
			"property_address": {Type: "string", Description: "Property address"},
			"assigned_to":      {Type: "string", Description: "Assigned person/agent"},
			"lost_reason":      {Type: "string", Description: "Reason the lead was lost (if applicable)"},
			"notes":            {Type: "string", Description: "Notes"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Cost Code tools
// ────────────────────────────────────────────────────────────────

func registerCostCodeTools(t *tools.Tools) {

	t.Register("list_cost_codes", tools.ToolDef{
		Description: "List cost codes, optionally filtered by division. Use to find the right cost code for budgeting or job costing.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			limit := 100
			if l, ok := params["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}
			division, _ := params["division"].(string)

			codes, err := store.ListCostCodes(division, limit)
			if err != nil {
				return "", fmt.Errorf("list cost codes: %w", err)
			}
			if len(codes) == 0 {
				return "No cost codes found.", nil
			}
			out, _ := json.MarshalIndent(codes, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"division": {Type: "string", Description: "Filter by division (e.g. overhead, labor, materials)"},
			"limit":    {Type: "number", Description: "Max results (default 100)"},
		},
	})

	t.Register("list_divisions", tools.ToolDef{
		Description: "List all distinct cost code divisions. Use to see what budget categories exist.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			divisions, err := store.ListDivisions()
			if err != nil {
				return "", fmt.Errorf("list divisions: %w", err)
			}
			if len(divisions) == 0 {
				return "No divisions found.", nil
			}
			out, _ := json.MarshalIndent(divisions, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	})

	t.Register("create_cost_code", tools.ToolDef{
		Description: "Create a new cost code for budgeting and job costing. Use when adding a new expense category.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			code, _ := params["code"].(string)
			if code == "" {
				return "", fmt.Errorf("code is required")
			}
			name, _ := params["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			c := CostCode{
				Code:          code,
				Name:          name,
				Division:      strOrDefault(params, "division", ""),
				DivisionGroup: strOrDefault(params, "division_group", ""),
				Active:        toBool(params["active"], true),
			}
			id, err := store.InsertCostCode(c)
			if err != nil {
				return "", fmt.Errorf("create cost code: %w", err)
			}
			return fmt.Sprintf("Cost code created (id=%d, code=%q, name=%q).", id, c.Code, c.Name), nil
		}),
		Params: map[string]tools.ParamDef{
			"code":           {Type: "string", Description: "Cost code (e.g. 5010, OH-001)", Required: true},
			"name":           {Type: "string", Description: "Cost code name", Required: true},
			"division":       {Type: "string", Description: "Division (e.g. overhead, labor, materials)"},
			"division_group": {Type: "string", Description: "Division group for sub-categorization"},
			"active":         {Type: "boolean", Description: "Whether the cost code is active (default: true)"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Budget tools
// ────────────────────────────────────────────────────────────────

func registerBudgetTools(t *tools.Tools) {

	t.Register("list_budget_years", tools.ToolDef{
		Description: "List available budget years. Use to see which years have budgets defined.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			years, err := store.ListBudgetYears()
			if err != nil {
				return "", fmt.Errorf("list budget years: %w", err)
			}
			if len(years) == 0 {
				return "No budgets found.", nil
			}
			out, _ := json.MarshalIndent(years, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{},
	})

	t.Register("get_budget", tools.ToolDef{
		Description: "Get a budget by ID or year. Use to see revenue targets, overhead, hourly rate, and margin goals.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			var b *Budget
			if id := toInt64(params["id"]); id != 0 {
				b, err = store.GetBudget(id)
			} else if year := toInt64(params["year"]); year != 0 {
				b, err = store.GetBudgetByYear(int(year))
			} else {
				return "", fmt.Errorf("id or year is required")
			}
			if err != nil {
				return "", fmt.Errorf("get budget: %w", err)
			}
			out, _ := json.MarshalIndent(b, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":   {Type: "number", Description: "Budget ID"},
			"year": {Type: "number", Description: "Budget year (e.g. 2026)"},
		},
	})

	t.Register("create_budget", tools.ToolDef{
		Description: "Create a new annual budget. Use when setting up financial targets for a year.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			year := toInt64(params["year"])
			if year == 0 {
				return "", fmt.Errorf("year is required")
			}
			b := Budget{
				Year:            int(year),
				Name:            strOrDefault(params, "name", ""),
				RevenueTarget:   toFloat64(params["revenue_target"]),
				TotalOverhead:   toFloat64(params["total_overhead"]),
				BillableHours:   toFloat64(params["billable_hours"]),
				HourlyRate:      toFloat64(params["hourly_rate"]),
				OwnerSalary:     toFloat64(params["owner_salary"]),
				TargetMarginPct: toFloat64(params["target_margin_pct"]),
				Notes:           strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertBudget(b)
			if err != nil {
				return "", fmt.Errorf("create budget: %w", err)
			}
			return fmt.Sprintf("Budget created (id=%d, year=%d, revenue_target=$%.2f).", id, b.Year, b.RevenueTarget), nil
		}),
		Params: map[string]tools.ParamDef{
			"year":              {Type: "number", Description: "Budget year (e.g. 2026)", Required: true},
			"name":              {Type: "string", Description: "Budget name"},
			"revenue_target":    {Type: "number", Description: "Annual revenue target"},
			"total_overhead":    {Type: "number", Description: "Total annual overhead"},
			"billable_hours":    {Type: "number", Description: "Total billable hours available"},
			"hourly_rate":       {Type: "number", Description: "Target hourly rate"},
			"owner_salary":      {Type: "number", Description: "Owner salary"},
			"target_margin_pct": {Type: "number", Description: "Target profit margin percentage"},
			"notes":             {Type: "string", Description: "Notes"},
		},
	})

	t.Register("update_budget", tools.ToolDef{
		Description: "Update an existing budget. Only the fields you provide will be changed.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			b, err := store.GetBudget(id)
			if err != nil {
				return "", fmt.Errorf("get budget for update: %w", err)
			}
			if _, ok := params["year"]; ok {
				b.Year = int(toInt64(params["year"]))
			}
			if v, ok := params["name"].(string); ok {
				b.Name = v
			}
			if _, ok := params["revenue_target"]; ok {
				b.RevenueTarget = toFloat64(params["revenue_target"])
			}
			if _, ok := params["total_overhead"]; ok {
				b.TotalOverhead = toFloat64(params["total_overhead"])
			}
			if _, ok := params["billable_hours"]; ok {
				b.BillableHours = toFloat64(params["billable_hours"])
			}
			if _, ok := params["hourly_rate"]; ok {
				b.HourlyRate = toFloat64(params["hourly_rate"])
			}
			if _, ok := params["owner_salary"]; ok {
				b.OwnerSalary = toFloat64(params["owner_salary"])
			}
			if _, ok := params["target_margin_pct"]; ok {
				b.TargetMarginPct = toFloat64(params["target_margin_pct"])
			}
			if v, ok := params["notes"].(string); ok {
				b.Notes = v
			}
			if err := store.UpdateBudget(*b); err != nil {
				return "", fmt.Errorf("update budget: %w", err)
			}
			return fmt.Sprintf("Budget %d (year=%d) updated.", b.ID, b.Year), nil
		}),
		Params: map[string]tools.ParamDef{
			"id":                {Type: "number", Description: "Budget ID", Required: true},
			"year":              {Type: "number", Description: "Budget year"},
			"name":              {Type: "string", Description: "Budget name"},
			"revenue_target":    {Type: "number", Description: "Annual revenue target"},
			"total_overhead":    {Type: "number", Description: "Total annual overhead"},
			"billable_hours":    {Type: "number", Description: "Total billable hours"},
			"hourly_rate":       {Type: "number", Description: "Target hourly rate"},
			"owner_salary":      {Type: "number", Description: "Owner salary"},
			"target_margin_pct": {Type: "number", Description: "Target profit margin percentage"},
			"notes":             {Type: "string", Description: "Notes"},
		},
	})
}

// ────────────────────────────────────────────────────────────────
// Budget Line tools
// ────────────────────────────────────────────────────────────────

func registerBudgetLineTools(t *tools.Tools) {

	t.Register("list_budget_lines", tools.ToolDef{
		Description: "List all line items for a budget. Use to see the detailed breakdown of a budget's expenses.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			budgetID := toInt64(params["budget_id"])
			if budgetID == 0 {
				return "", fmt.Errorf("budget_id is required")
			}
			lines, err := store.ListBudgetLines(budgetID)
			if err != nil {
				return "", fmt.Errorf("list budget lines: %w", err)
			}
			if len(lines) == 0 {
				return "No budget lines found.", nil
			}
			out, _ := json.MarshalIndent(lines, "", "  ")
			return string(out), nil
		}),
		Params: map[string]tools.ParamDef{
			"budget_id": {Type: "number", Description: "Budget ID to list lines for", Required: true},
		},
	})

	t.Register("create_budget_line", tools.ToolDef{
		Description: "Create a new budget line item. Use to add an expense category to a budget.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			budgetID := toInt64(params["budget_id"])
			if budgetID == 0 {
				return "", fmt.Errorf("budget_id is required")
			}
			description, _ := params["description"].(string)
			if description == "" {
				return "", fmt.Errorf("description is required")
			}
			l := BudgetLine{
				BudgetID:      budgetID,
				CostCode:      strOrDefault(params, "cost_code", ""),
				Description:   description,
				Category:      strOrDefault(params, "category", ""),
				AnnualAmount:  toFloat64(params["annual_amount"]),
				MonthlyAmount: toFloat64(params["monthly_amount"]),
				Notes:         strOrDefault(params, "notes", ""),
			}
			id, err := store.InsertBudgetLine(l)
			if err != nil {
				return "", fmt.Errorf("create budget line: %w", err)
			}
			return fmt.Sprintf("Budget line created (id=%d, description=%q, annual=$%.2f).", id, l.Description, l.AnnualAmount), nil
		}),
		Params: map[string]tools.ParamDef{
			"budget_id":      {Type: "number", Description: "Budget ID this line belongs to", Required: true},
			"cost_code":      {Type: "string", Description: "Cost code reference"},
			"description":    {Type: "string", Description: "Line item description", Required: true},
			"category":       {Type: "string", Description: "Category (e.g. fixed, variable)"},
			"annual_amount":  {Type: "number", Description: "Annual amount"},
			"monthly_amount": {Type: "number", Description: "Monthly amount"},
			"notes":          {Type: "string", Description: "Notes"},
		},
	})

	t.Register("delete_budget_line", tools.ToolDef{
		Description: "Delete a budget line item. Use to remove an expense line from a budget.",
		Fn: tools.ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			store, err := domainStoreFromContext(ctx)
			if err != nil {
				return "", err
			}
			id := toInt64(params["id"])
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			if err := store.DeleteBudgetLine(id); err != nil {
				return "", fmt.Errorf("delete budget line: %w", err)
			}
			return fmt.Sprintf("Budget line %d deleted.", id), nil
		}),
		Params: map[string]tools.ParamDef{
			"id": {Type: "number", Description: "Budget line ID", Required: true},
		},
	})
}
