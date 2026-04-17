package dsl

import (
	"context"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// mockChannelBackend implements ChannelBackend for testing.
type mockChannelBackend struct {
	channels []ChannelInfo
}

func (m *mockChannelBackend) CreateChannel(id, name, description, createdBy string, team []string, mode string) error {
	return nil
}

func (m *mockChannelBackend) GetChannelByName(name string) (*ChannelInfo, error) {
	for i := range m.channels {
		if m.channels[i].Name == name {
			return &m.channels[i], nil
		}
	}
	return nil, nil
}

func (m *mockChannelBackend) ListChannelsForAgent(agent string) ([]ChannelInfo, error) {
	var result []ChannelInfo
	for _, ch := range m.channels {
		for _, member := range ch.Team {
			if member == agent {
				result = append(result, ch)
				break
			}
		}
	}
	return result, nil
}

func (m *mockChannelBackend) ListAllChannels() ([]ChannelInfo, error) {
	return m.channels, nil
}

func (m *mockChannelBackend) FindChannelForAgents(agent1, agent2 string) (string, string, error) {
	return "", "", nil
}

func (m *mockChannelBackend) InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string) (int64, error) {
	return 0, nil
}

func (m *mockChannelBackend) RecentChannelMessages(channelID string, limit int) ([]ChannelMessage, error) {
	return nil, nil
}

// callListMyChannels registers channel tools on a minimal interpreter and
// invokes list_my_channels as the given agent.
func callListMyChannels(t *testing.T, backend *mockChannelBackend, agentName string) string {
	t.Helper()

	interp := &Interpreter{
		doc:    &Document{Agents: map[string]*Agent{}},
		agents: map[string]*vega.Process{},
		tools:  tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterChannelTools(interp, backend, nil, nil)

	proc := &vega.Process{
		ID:    "test-proc",
		Agent: &vega.Agent{Name: agentName},
	}
	ctx := vega.ContextWithProcess(context.Background(), proc)

	result, err := interp.Tools().Execute(ctx, "list_my_channels", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

func TestListMyChannels_AgentIsMember(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "finance", Team: []string{"grace", "walt", "devin"}},
			{ID: "ch_2", Name: "operations", Team: []string{"hank", "june"}},
			{ID: "ch_3", Name: "general", Team: []string{"grace", "hank", "walt"}},
		},
	}

	result := callListMyChannels(t, backend, "grace")

	// Should show ALL channels.
	if !strings.Contains(result, "#finance") {
		t.Errorf("expected #finance in result, got:\n%s", result)
	}
	if !strings.Contains(result, "#operations") {
		t.Errorf("expected #operations in result, got:\n%s", result)
	}
	if !strings.Contains(result, "#general") {
		t.Errorf("expected #general in result, got:\n%s", result)
	}

	// Should mark channels grace belongs to.
	for _, line := range strings.Split(strings.TrimSpace(result), "\n") {
		if strings.Contains(line, "#finance") || strings.Contains(line, "#general") {
			if !strings.Contains(line, "you are here") {
				t.Errorf("expected membership marker on line: %s", line)
			}
		}
		if strings.Contains(line, "#operations") {
			if strings.Contains(line, "you are here") {
				t.Errorf("unexpected membership marker on operations line: %s", line)
			}
		}
	}
}

func TestListMyChannels_AgentNotMember(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "finance", Team: []string{"grace", "walt"}},
			{ID: "ch_2", Name: "operations", Team: []string{"hank", "june"}},
		},
	}

	result := callListMyChannels(t, backend, "iris")

	// Should still show all channels even though iris isn't in any.
	if !strings.Contains(result, "#finance") {
		t.Errorf("expected #finance in result, got:\n%s", result)
	}
	if !strings.Contains(result, "#operations") {
		t.Errorf("expected #operations in result, got:\n%s", result)
	}

	// No membership markers.
	if strings.Contains(result, "you are here") {
		t.Errorf("expected no membership markers for iris, got:\n%s", result)
	}
}

func TestListMyChannels_NoChannels(t *testing.T) {
	backend := &mockChannelBackend{channels: nil}

	result := callListMyChannels(t, backend, "iris")

	if !strings.Contains(result, "No channels exist") {
		t.Errorf("expected 'No channels exist' message, got:\n%s", result)
	}
}

func TestListMyChannels_StripsUserSuffix(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "finance", Team: []string{"grace", "walt"}},
		},
	}

	// Simulate a per-user clone agent name.
	result := callListMyChannels(t, backend, "grace:Etienne")

	if !strings.Contains(result, "#finance") {
		t.Errorf("expected #finance in result, got:\n%s", result)
	}
	if !strings.Contains(result, "you are here") {
		t.Errorf("expected membership marker after stripping user suffix, got:\n%s", result)
	}
}
