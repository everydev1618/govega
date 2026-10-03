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
	messages map[string][]ChannelMessage // channelID → messages
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

func (m *mockChannelBackend) InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string, activities []vega.ToolActivity) (int64, error) {
	return 0, nil
}

func (m *mockChannelBackend) RecentChannelMessages(channelID string, limit int) ([]ChannelMessage, error) {
	msgs := m.messages[channelID]
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

// callListMyChannels registers channel tools on a minimal interpreter and
// invokes list_my_channels as the given agent.
func callListMyChannels(t *testing.T, backend *mockChannelBackend, agentName string) string {
	t.Helper()

	interp := &Interpreter{
		doc:               &Document{Agents: map[string]*Agent{}},
		agents:            map[string]*vega.Process{},
		tools:             tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterChannelTools(interp, backend, nil, nil, nil)

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

// callReadChannel registers channel tools on a minimal interpreter and
// invokes read_channel with the given params as the given agent.
func callReadChannel(t *testing.T, backend *mockChannelBackend, agentName string, params map[string]any) (string, error) {
	t.Helper()

	interp := &Interpreter{
		doc:               &Document{Agents: map[string]*Agent{}},
		agents:            map[string]*vega.Process{},
		tools:             tools.NewTools(),
		delegationConfigs: map[string]*DelegationDef{},
	}
	RegisterChannelTools(interp, backend, nil, nil, nil)

	proc := &vega.Process{
		ID:    "test-proc",
		Agent: &vega.Agent{Name: agentName},
	}
	ctx := vega.ContextWithProcess(context.Background(), proc)

	return interp.Tools().Execute(ctx, "read_channel", params)
}

func TestReadChannel_ReturnsFullUntruncatedContent(t *testing.T) {
	// 300-char body — longer than check_status's 150-char preview cap.
	// The whole point of this tool is to NOT truncate.
	longBody := strings.Repeat("a", 300)
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "synkedup-cto", Team: []string{"scout", "tony"}},
		},
		messages: map[string][]ChannelMessage{
			"ch_1": {{Agent: "scout", Sender: "scout", Content: longBody}},
		},
	}

	result, err := callReadChannel(t, backend, "tony", map[string]any{"channel": "synkedup-cto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, longBody) {
		t.Errorf("expected full %d-char body in result, got %d chars:\n%s", len(longBody), len(result), result)
	}
	if strings.Contains(result, "...") {
		t.Errorf("expected no truncation ellipsis, got:\n%s", result)
	}
}

func TestReadChannel_IncludesSenderAndOrdering(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "synkedup-cto", Team: []string{"scout", "tony"}},
		},
		messages: map[string][]ChannelMessage{
			"ch_1": {
				{Agent: "scout", Sender: "scout", Content: "first message"},
				{Agent: "tony", Sender: "tony", Content: "second message"},
			},
		},
	}

	result, err := callReadChannel(t, backend, "tony", map[string]any{"channel": "synkedup-cto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "scout") || !strings.Contains(result, "first message") {
		t.Errorf("expected scout's message in result, got:\n%s", result)
	}
	if !strings.Contains(result, "tony") || !strings.Contains(result, "second message") {
		t.Errorf("expected tony's message in result, got:\n%s", result)
	}
	firstIdx := strings.Index(result, "first message")
	secondIdx := strings.Index(result, "second message")
	if firstIdx < 0 || secondIdx < 0 || firstIdx >= secondIdx {
		t.Errorf("expected first message before second in result, got:\n%s", result)
	}
}

func TestReadChannel_RespectsLimit(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "synkedup-cto", Team: []string{"scout"}},
		},
		messages: map[string][]ChannelMessage{
			"ch_1": {
				{Agent: "scout", Content: "msg-1"},
				{Agent: "scout", Content: "msg-2"},
				{Agent: "scout", Content: "msg-3"},
			},
		},
	}

	result, err := callReadChannel(t, backend, "tony", map[string]any{
		"channel": "synkedup-cto",
		"limit":   float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "msg-1") {
		t.Errorf("limit=2 should drop the oldest message, got:\n%s", result)
	}
	if !strings.Contains(result, "msg-2") || !strings.Contains(result, "msg-3") {
		t.Errorf("expected msg-2 and msg-3 in result, got:\n%s", result)
	}
}

func TestReadChannel_MissingChannelParam(t *testing.T) {
	backend := &mockChannelBackend{}
	_, err := callReadChannel(t, backend, "tony", map[string]any{})
	if err == nil {
		t.Fatal("expected error when channel param is missing")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Errorf("expected error message to mention channel, got: %v", err)
	}
}

func TestReadChannel_ChannelNotFound(t *testing.T) {
	backend := &mockChannelBackend{channels: nil}
	_, err := callReadChannel(t, backend, "tony", map[string]any{"channel": "ghost"})
	if err == nil {
		t.Fatal("expected error when channel doesn't exist")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected error message to name the missing channel, got: %v", err)
	}
}

func TestReadChannel_EmptyChannel(t *testing.T) {
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "quiet", Team: []string{"tony"}},
		},
		messages: map[string][]ChannelMessage{"ch_1": nil},
	}

	result, err := callReadChannel(t, backend, "tony", map[string]any{"channel": "quiet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No messages") {
		t.Errorf("expected 'No messages' notice, got:\n%s", result)
	}
}

func TestReadChannel_StripsLeadingHash(t *testing.T) {
	// Agents (and humans) often type channel names with a leading '#'.
	// The tool should accept it.
	backend := &mockChannelBackend{
		channels: []ChannelInfo{
			{ID: "ch_1", Name: "synkedup-cto", Team: []string{"tony"}},
		},
		messages: map[string][]ChannelMessage{
			"ch_1": {{Agent: "scout", Content: "hello"}},
		},
	}
	result, err := callReadChannel(t, backend, "tony", map[string]any{"channel": "#synkedup-cto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected message in result, got:\n%s", result)
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
