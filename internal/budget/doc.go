// Package budget enforces spending limits (brief s7 rules 12 and 14, docs/token-control.md s7).
//
// A budget limits one scope (project, task, run or agent) in USD, tokens and/or agent turns. Usage is never copied into
// the budget: every evaluation sums model_usage (and agent_runs for turns), so the figure shown is always the figure in the
// rows. Cost is split by source: reported (the provider or CLI returned it), estimated (computed from the price table),
// and unknown (nothing was reported and no price is known). Unknown is never counted as zero: it is shown as a count of
// rows next to the dollar figure, and a dollar limit therefore only sees the known part of the spend. A token or turn limit
// does not depend on prices and is the reliable guard for CLIs that do not report cost.
//
// At the warn percentage (default 80%) a budget.warning event and a SYSTEM room message are raised once. At 100% the next
// agent turn does not start: the Gate middleware parks the workflow run on an approval request (the run is WAITING_HUMAN
// and the request is in the human's Needs-you list). Only a human resumes it, by raising the limit or approving. A turn
// that is already running when the limit is crossed completes; the pause takes effect at the next turn boundary.
package budget
