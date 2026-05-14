package dsl

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyDispatchOutcome covers the matrix of inputs DispatchToAgent
// hands the classifier when filing an inbox completion notification.
//
// Anchor case: Nadia thrashing in the Fly busybox sandbox produced a long
// stream-of-consciousness response that ended mid-tool-investigation with
// "wget is mangling ports... nc is running on PID 741. The wget port
// parsing issue is a busybox quirk with quoted p..." — clearly NOT done.
// The previous classifier filed it as a normal-priority "Task completed".
// This test pins the desired behavior: that pattern lands as urgent.
func TestClassifyDispatchOutcome(t *testing.T) {
	const nadiaThrash = `On it. Let me check the workspace and get the server back up with a session-persistent method.

Files confirmed intact. No services running. Starting with nohup + disown for session persistence.

disown isn't available in this shell — switching to setsid which creates a fully detached process session.

Process died immediately. Let me use start_service — the sandbox's native persistent service manager.

No python3 binary — let me find what's available.

No Python anywhere. BusyBox nc with -lk -e is the path forward — I'll write a tiny HTTP handler script and use that.

wget is mangling ports. Let me check nc is alive and test with a raw TCP approach.

nc is running on PID 741. The wget port parsing issue is a busybox quirk with quoted p`

	cases := []struct {
		name        string
		resp        string
		err         error
		wantSubject string
		wantPrio    string
	}{
		{
			name:        "explicit error wins regardless of resp",
			resp:        "task complete, dashboard is live",
			err:         errors.New("boom"),
			wantSubject: "Task failed for nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "empty response is urgent incomplete",
			resp:        "",
			err:         nil,
			wantSubject: "Task incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "whitespace-only response is urgent incomplete",
			resp:        "   \n\t  ",
			err:         nil,
			wantSubject: "Task incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "Nadia's thrash narrative lands as urgent may-be-incomplete",
			resp:        nadiaThrash,
			err:         nil,
			wantSubject: "Task may be incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "clean success summary stays normal",
			resp:        "Dashboard rebuilt. dashboard.html written. http://localhost:7842/dashboard.html returns 200. Posted completion to #portfolio-tracker.",
			err:         nil,
			wantSubject: "Task completed by nadia",
			wantPrio:    "normal",
		},
		{
			name:        "response ending mid-sentence with no terminator is urgent",
			resp:        "Reading portfolio.yaml. Calling GitHub. Got the repo list. Now writing dashboard.md and let me try to verify with",
			err:         nil,
			wantSubject: "Task may be incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "explicit failure verb late in response is urgent",
			resp:        "Ran a few approaches. None of them worked — the python3 binary doesn't exist in this image and start_service kept failing silently. Reporting back so you can pick another path.",
			err:         nil,
			wantSubject: "Task may be incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "process died phrase is urgent",
			resp:        "Tried three different supervisors. Process died immediately each time, even with setsid + nohup. I'm stuck.",
			err:         nil,
			wantSubject: "Task may be incomplete from nadia",
			wantPrio:    "urgent",
		},
		{
			name:        "success-emoji marker overrides any earlier exploration text",
			resp:        "First I tried setsid but the process died immediately. Then I switched to busybox httpd which worked. ✅ Dashboard live at http://localhost:7842/dashboard.html — 200 OK.",
			err:         nil,
			wantSubject: "Task completed by nadia",
			wantPrio:    "normal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, prio := classifyDispatchOutcome("nadia", "Restart the dashboard server.", tc.resp, tc.err)
			if subject != tc.wantSubject {
				t.Errorf("subject: got %q, want %q", subject, tc.wantSubject)
			}
			if prio != tc.wantPrio {
				t.Errorf("priority: got %q, want %q", prio, tc.wantPrio)
			}
			if body == "" {
				t.Errorf("body should never be empty")
			}
			// Sanity: body should always include the original request so the
			// orchestrator triaging the inbox knows what was asked.
			if !strings.Contains(body, "Restart the dashboard server") {
				t.Errorf("body missing original request: %q", body)
			}
		})
	}
}
