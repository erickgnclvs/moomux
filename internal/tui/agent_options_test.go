package tui

import (
	"reflect"
	"testing"

	"github.com/erickgnclvs/moomux/internal/app"
)

// TestAgentOptionsMatchesTUIFixture guards testAgentOptions (used to
// construct every test Model) against drifting from what App.AgentOptions
// actually serves in production — the whole point of sourcing the TUI's
// agent/model/thinking selectors from the backend instead of a copy of their
// own is that there is exactly one table; this pins that there really is
// only one. claude's and codex's models and thinking levels come from
// agent-usage, so the core reads uishot's sample models.json.
func TestAgentOptionsMatchesTUIFixture(t *testing.T) {
	a := &app.App{ModelsPath: "../../cmd/uishot/samplemodels.json"}
	if got := a.AgentOptions(); !reflect.DeepEqual(got, testAgentOptions) {
		t.Fatalf("app.AgentOptions() = %+v, want testAgentOptions %+v", got, testAgentOptions)
	}
}
