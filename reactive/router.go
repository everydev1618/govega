package reactive

import (
	"bytes"
	"context"
	"log/slog"
	"text/template"

	"github.com/everydev1618/govega/events"
)

// Trigger is one reactive subscription declared on an agent: when an event of
// type On (a glob) arrives and its optional Where predicate holds, the agent
// wakes with Prompt (a template) rendered against the event. Gate selects the
// salience tier ("" = rules only, "model" = add the cheap classifier, §5.2).
//
// This is the canonical type; the Agent blueprint carries a []Trigger (D1).
type Trigger struct {
	On     string
	Where  string
	Gate   string
	Prompt string
}

// Dispatcher wakes an agent with a message. The dsl.Interpreter satisfies this
// via SendToAgent; the router depends only on the method, not the interpreter.
type Dispatcher interface {
	SendToAgent(ctx context.Context, agentName, message string) (string, error)
}

// TriggerRegistry provides the current agent→triggers map. Backed by the agent
// registry in production so there is no separate subscription store (D1).
type TriggerRegistry interface {
	ReactiveTriggers() map[string][]Trigger
}

// Record is one audit entry: what the spine made an agent consider, and whether
// it acted or was gated out (and why). Silent suppression is a bug — §7.
type Record struct {
	Agent   string
	EventID string
	Type    string
	Fired   bool
	Reason  string // "" when fired; otherwise gate/guard/error reason
}

// Config wires a Router. Only Triggers is required for evaluate(); Bus and
// Dispatch are required for a live Start().
type Config struct {
	Bus      *events.Bus
	Dispatch Dispatcher
	Triggers TriggerRegistry
	Guard    *LoopGuard
	// Prepare decorates the dispatch context per agent — e.g. attaching memory
	// via serve.ContextWithMemory. Nil means identity.
	Prepare func(ctx context.Context, agentName string) context.Context
	// Audit, if set, receives one Record per (agent, event) considered.
	Audit func(Record)
	// AfterWake, if set, runs when a reactive wake completes, with the agent's
	// result. serve uses it for end-of-wake consolidation — the self-learning
	// write-back that makes a short reactive burst leave a trace in memory
	// regardless of length (§3.4). Runs even if the wake returned an error
	// (result may be empty), so a failed reaction is still remembered.
	AfterWake func(ctx context.Context, agentName string, e events.Event, result string)
}

// Router subscribes to the event spine and turns matching events into agent
// wakes, filtered by the salience gate and loop guard.
type Router struct {
	cfg Config
	sub *events.Subscription
}

// NewRouter builds a Router from cfg. It does not subscribe until Start.
func NewRouter(cfg Config) *Router { return &Router{cfg: cfg} }

// decision is the outcome of evaluating one agent against one event.
type decision struct {
	Agent   string
	Message string
	Fired   bool
	Reason  string
}

// evaluate computes, purely, what each agent should do with an event. It is the
// testable core of the router: no dispatch, no goroutines, no clock.
func (r *Router) evaluate(e events.Event) []decision {
	reg := r.cfg.Triggers.ReactiveTriggers()
	out := make([]decision, 0, len(reg))

	for agent, triggers := range reg {
		d := decision{Agent: agent}
		t, reason, matched := firstMatch(triggers, e)
		if !matched {
			d.Reason = reason // "" if simply no type/predicate match; set on predicate error
			out = append(out, d)
			continue
		}
		// One wake per agent per event: the guard runs once, here.
		if r.cfg.Guard != nil {
			if ok, gr := r.cfg.Guard.Allow(agent, e); !ok {
				d.Reason = gr
				out = append(out, d)
				continue
			}
		}
		msg, err := renderPrompt(t.Prompt, e)
		if err != nil {
			d.Reason = "template: " + err.Error()
			out = append(out, d)
			continue
		}
		d.Fired = true
		d.Message = msg
		out = append(out, d)
	}
	return out
}

// firstMatch returns the first trigger whose rules pass for e. If a trigger's
// predicate is malformed, matching stops and the error reason is returned so it
// can be audited rather than silently ignored.
func firstMatch(triggers []Trigger, e events.Event) (Trigger, string, bool) {
	for _, t := range triggers {
		ok, err := passRules(t.On, t.Where, e)
		if err != nil {
			return Trigger{}, "predicate: " + err.Error(), false
		}
		if ok {
			return t, "", true
		}
	}
	return Trigger{}, "", false
}

// Start subscribes to the bus (durable — a reactive trigger must not miss a
// creak) and dispatches wakes until ctx is cancelled.
func (r *Router) Start(ctx context.Context) {
	if r.cfg.Bus == nil {
		return
	}
	r.sub = r.cfg.Bus.Subscribe(events.Durable, nil)
	go func() {
		for {
			select {
			case <-ctx.Done():
				r.sub.Close()
				return
			case e, ok := <-r.sub.C:
				if !ok {
					return
				}
				r.handle(ctx, e)
			}
		}
	}()
}

func (r *Router) handle(ctx context.Context, e events.Event) {
	for _, d := range r.evaluate(e) {
		if r.cfg.Audit != nil {
			r.cfg.Audit(Record{Agent: d.Agent, EventID: e.ID, Type: e.Type, Fired: d.Fired, Reason: d.Reason})
		}
		if !d.Fired {
			continue
		}
		// Dispatch async so one slow wake does not stall the drain loop.
		agent, msg := d.Agent, d.Message
		dctx := ctx
		if r.cfg.Prepare != nil {
			dctx = r.cfg.Prepare(ctx, agent)
		}
		go func() {
			result, err := r.cfg.Dispatch.SendToAgent(dctx, agent, msg)
			if err != nil {
				slog.Warn("reactive wake failed", "agent", agent, "event", e.Type, "error", err)
			}
			if r.cfg.AfterWake != nil {
				r.cfg.AfterWake(dctx, agent, e, result)
			}
		}()
	}
}

// renderPrompt renders a trigger's prompt template against an event. Template
// context exposes .Type, .ID, .Data (the payload map), and .Origin.
func renderPrompt(tmpl string, e events.Event) (string, error) {
	t, err := template.New("wake").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	data := struct {
		Type   string
		ID     string
		Data   map[string]any
		Origin *events.Origin
	}{Type: e.Type, ID: e.ID, Data: e.Data, Origin: e.Origin}
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
