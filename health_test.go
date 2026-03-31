package vega

import (
	"testing"
	"time"
)

func TestNewHealthMonitor(t *testing.T) {
	config := HealthConfig{
		CheckInterval:        10 * time.Second,
		StaleProgressMinutes: 5,
		MaxIterationsWarning: 100,
		ErrorLoopCount:       3,
		CostAlertUSD:         1.0,
	}

	hm := NewHealthMonitor(config)
	if hm == nil {
		t.Fatal("NewHealthMonitor() returned nil")
	}

	if hm.config.CheckInterval != 10*time.Second {
		t.Errorf("CheckInterval = %v, want 10s", hm.config.CheckInterval)
	}
}

func TestHealthMonitorAlertTypes(t *testing.T) {
	tests := []struct {
		at   AlertType
		want string
	}{
		{AlertStaleProgress, "stale_progress"},
		{AlertHighCost, "high_cost"},
		{AlertErrorLoop, "error_loop"},
		{AlertTimeoutWarning, "timeout_warning"},
		{AlertHighIterations, "high_iterations"},
	}

	for _, tt := range tests {
		if string(tt.at) != tt.want {
			t.Errorf("AlertType = %q, want %q", tt.at, tt.want)
		}
	}
}

func TestHealthMonitorCheckHealth_HighIterations(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		MaxIterationsWarning: 10,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		metrics: ProcessMetrics{
			Iterations: 15,
		},
	}

	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		if alert.Type != AlertHighIterations {
			t.Errorf("Alert type = %q, want %q", alert.Type, AlertHighIterations)
		}
		if alert.ProcessID != "proc-1" {
			t.Errorf("Alert ProcessID = %q, want %q", alert.ProcessID, "proc-1")
		}
	default:
		t.Error("Expected high iterations alert")
	}
}

func TestHealthMonitorCheckHealth_HighCost(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		CostAlertUSD: 0.5,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		metrics: ProcessMetrics{
			CostUSD: 1.0,
		},
	}

	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		if alert.Type != AlertHighCost {
			t.Errorf("Alert type = %q, want %q", alert.Type, AlertHighCost)
		}
	default:
		t.Error("Expected high cost alert")
	}
}

func TestHealthMonitorCheckHealth_ErrorLoop(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		ErrorLoopCount: 3,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		metrics: ProcessMetrics{
			Errors: 5,
		},
	}

	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		if alert.Type != AlertErrorLoop {
			t.Errorf("Alert type = %q, want %q", alert.Type, AlertErrorLoop)
		}
	default:
		t.Error("Expected error loop alert")
	}
}

func TestHealthMonitorCheckHealth_SkipsNonRunning(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		MaxIterationsWarning: 10,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusCompleted,
		metrics: ProcessMetrics{
			Iterations: 15,
		},
	}

	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		t.Errorf("Should not alert for completed process, got: %+v", alert)
	default:
		// Expected
	}
}

func TestHealthMonitorCheckHealth_CleansUpDeadProcesses(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		MaxIterationsWarning: 10,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		metrics: ProcessMetrics{
			Iterations: 15,
		},
	}

	// First check adds the monitor
	hm.checkHealth([]*Process{p})
	// Drain alert
	<-hm.alertCh

	if len(hm.monitors) != 1 {
		t.Fatalf("monitors count = %d, want 1", len(hm.monitors))
	}

	// Second check with no processes should clean up
	hm.checkHealth([]*Process{})

	if len(hm.monitors) != 0 {
		t.Error("Dead process monitors should be cleaned up")
	}
}

func TestHealthMonitorStartStop(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		CheckInterval: 10 * time.Millisecond,
	})

	processes := []*Process{}
	hm.Start(func() []*Process { return processes })

	// Should not panic on stop
	time.Sleep(20 * time.Millisecond)
	hm.Stop()
}

func TestHealthMonitorHighCostDedup(t *testing.T) {
	hm := NewHealthMonitor(HealthConfig{
		CostAlertUSD: 0.5,
	})

	p := &Process{
		ID:     "proc-1",
		Agent:  &Agent{Name: "TestAgent"},
		status: StatusRunning,
		metrics: ProcessMetrics{
			CostUSD: 1.0,
		},
	}

	// First check: should alert
	hm.checkHealth([]*Process{p})
	<-hm.alertCh

	// Second check with same cost: should NOT alert again
	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		t.Errorf("Should not re-alert for same cost level, got: %+v", alert)
	default:
		// Expected
	}

	// Third check with higher cost: should alert
	p.mu.Lock()
	p.metrics.CostUSD = 2.0
	p.mu.Unlock()

	hm.checkHealth([]*Process{p})

	select {
	case alert := <-hm.alertCh:
		if alert.Type != AlertHighCost {
			t.Errorf("Alert type = %q, want %q", alert.Type, AlertHighCost)
		}
	default:
		t.Error("Expected alert for increased cost")
	}
}
