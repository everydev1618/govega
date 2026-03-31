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

// RegisterDomainTools registers job tracking, follow-up, and production rate
// tools on the interpreter. Follows the same pattern as RegisterMemoryTools.
func RegisterDomainTools(interp *dsl.Interpreter) {
	t := interp.Tools()
	registerJobTools(t)
	registerFollowUpTools(t)
	registerProductionRateTools(t)
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
