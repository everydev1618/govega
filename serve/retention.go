package serve

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// retentionSweepInterval is how often the background sweep runs. The first
// sweep fires shortly after boot so a long-lived database shrinks without
// waiting a full interval.
const retentionSweepInterval = 6 * time.Hour

// retentionPolicyFromEnv builds the retention policy. Defaults: events and
// process snapshots kept 30 days (operational telemetry — unbounded growth
// is the audit finding, govega#114); chat messages kept forever (they are
// the user's conversation record — deletion is opt-in).
//
//	VEGA_RETENTION_EVENTS_DAYS     days to keep events (0 = forever, default 30)
//	VEGA_RETENTION_SNAPSHOTS_DAYS  days to keep process snapshots (0 = forever, default 30)
//	VEGA_RETENTION_CHAT_DAYS       days to keep chat messages (0 = forever, default 0)
func retentionPolicyFromEnv() RetentionPolicy {
	days := func(env string, def int) time.Duration {
		if v := os.Getenv(env); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				return time.Duration(n) * 24 * time.Hour
			}
			slog.Warn("invalid retention setting, using default", "env", env, "value", v, "default_days", def)
		}
		return time.Duration(def) * 24 * time.Hour
	}
	return RetentionPolicy{
		Events:       days("VEGA_RETENTION_EVENTS_DAYS", 30),
		Snapshots:    days("VEGA_RETENTION_SNAPSHOTS_DAYS", 30),
		ChatMessages: days("VEGA_RETENTION_CHAT_DAYS", 0),
	}
}

// startRetentionSweeper runs SweepRetention periodically until ctx is done.
// No-op when every window is zero.
func (s *Server) startRetentionSweeper(ctx context.Context) {
	policy := retentionPolicyFromEnv()
	if policy.Events == 0 && policy.Snapshots == 0 && policy.ChatMessages == 0 {
		slog.Info("retention sweeper disabled (all windows zero)")
		return
	}

	go func() {
		// First sweep shortly after boot, then on the interval.
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			res, err := s.store.SweepRetention(policy)
			if err != nil {
				slog.Warn("retention sweep failed", "error", err)
			} else if res.Events+res.Snapshots+res.ChatMessages > 0 {
				slog.Info("retention sweep",
					"events", res.Events,
					"snapshots", res.Snapshots,
					"chat_messages", res.ChatMessages)
			}

			timer.Reset(retentionSweepInterval)
		}
	}()
}
