package core

import (
	"context"
	"strings"
	"testing"
)

// Audit 2026-10-16, M4: an agent turn outside a workflow (a room reply, a chat) ran when the budgets had not started. With no
// budget store the turn is refused.
func TestATurnOutsideAWorkflowIsRefusedWhenTheBudgetsDidNotStart(t *testing.T) {
	c := &Core{} // no economics extension ran: there is no budget store
	err := EconomicsBudgetBlock(context.Background(), c, "prj_1", "", "agt_1")
	if err == nil || !strings.Contains(err.Error(), "budgets did not start") {
		t.Fatalf("a turn was allowed with no budgets: %v", err)
	}
}
