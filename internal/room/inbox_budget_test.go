package room

import (
	"strings"
	"testing"
)

// A budget's warning and its limit are inbox items of their own, with what to do (decision P118, room-protocol s9): the budget
// gate's alerts (budget_alerts) and its pause request (an approvals row whose payload says gate "budget").
func TestBudgetWarningsLimitsAndPausesAreBudgetItems(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('task_1','proj_1',1,'Parser','IN_PROGRESS',1,1)`)
	e.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('proj_2','other','/tmp/o',1)`)
	e.exec(`INSERT INTO budgets(id,scope,scope_id,project_id,limit_usd,created_at,updated_at,updated_by) VALUES
		('bud_p','project','proj_1','',10,1,1,'human:kyle'),
		('bud_t','task','*','proj_1',2,1,1,'human:kyle'),
		('bud_w','workflow','iterate-until-verified','proj_1',5,1,1,'human:kyle'),
		('bud_o','project','proj_2','',1,1,1,'human:kyle')`)
	e.exec(`INSERT INTO budget_alerts(budget_id,instance_id,level,ts) VALUES
		('bud_p','proj_1','warning',10),
		('bud_t','task_1','warning',11),('bud_t','task_1','exceeded',12),
		('bud_w','proj_1/iterate-until-verified','warning',13),
		('bud_o','proj_2','exceeded',14)`)

	items, err := e.svc.NeedsYou(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]NeedsYouItem{}
	for _, it := range items {
		if it.Kind != NeedsBudget {
			t.Fatalf("only budget items here: %+v", it)
		}
		byKey[it.Key] = it
	}
	if len(items) != 3 {
		t.Fatalf("the project's warning, the task's limit (its warning gives way) and the workflow's warning; not the other project's: %+v", items)
	}
	p := byKey["budget:bud_p:proj_1:warning:10"]
	if p.BudgetState != BudgetWarning || p.Severity != 2 || p.Title != "Budget warning: the project budget passed 80%" || !strings.Contains(p.Detail, "raise the limit on the Budgets page") {
		t.Fatalf("project warning %+v", p)
	}
	tk := byKey["budget:bud_t:task_1:exceeded:12"]
	if tk.BudgetState != BudgetExceeded || tk.Severity != 0 || tk.TaskID != "task_1" || tk.TaskSeq != 1 || tk.Title != "Budget reached: the task budget is at its limit" ||
		!strings.Contains(tk.Detail, "do not start") {
		t.Fatalf("task limit %+v", tk)
	}
	if w := byKey["budget:bud_w:proj_1/iterate-until-verified:warning:13"]; w.Title != "Budget warning: the budget of the workflow iterate-until-verified passed 80%" || w.BudgetID != "bud_w" {
		t.Fatalf("workflow warning %+v", w)
	}
	if items[0].Key != "budget:bud_t:task_1:exceeded:12" {
		t.Fatalf("a budget at its limit comes before warnings: %+v", items)
	}

	// the run waiting on the task budget's pause: its request is the budget item, the limit's own item gives way to it
	e.exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,payload,status,created_at) VALUES('apr_b','run_1','task_1','proj_1','approval',
		'Budget limit reached: task task_1 budget at 120%',?,'REQUESTED',20)`, `{"gate":"budget","message":"m","exceeded":[{"budget":{"id":"bud_t"}}]}`)
	items, _ = e.svc.NeedsYou(e.ctx, e.project)
	var pause *NeedsYouItem
	for i := range items {
		if items[i].Key == "budget:bud_t:task_1:exceeded:12" {
			t.Fatal("the limit's item stays while its pause is the item")
		}
		if items[i].ApprovalID == "apr_b" {
			pause = &items[i]
		}
	}
	if pause == nil || pause.Kind != NeedsBudget || pause.BudgetState != BudgetPaused || pause.BudgetID != "bud_t" || pause.Severity != 0 ||
		!strings.Contains(pause.Detail, "approve one more turn") || pause.Key != "approval:apr_b" {
		t.Fatalf("pause %+v", pause)
	}

	// a person dismisses the warning; changing the limit clears the alerts (the budget store does) and with them the items
	if err := e.svc.AckNeedsYou(e.ctx, "budget:bud_p:proj_1:warning:10", kyle); err != nil {
		t.Fatal(err)
	}
	e.exec(`DELETE FROM budget_alerts WHERE budget_id='bud_w'`)
	e.exec(`UPDATE approvals SET status='APPROVED' WHERE id='apr_b'`)
	items, _ = e.svc.NeedsYou(e.ctx, e.project)
	if len(items) != 1 || items[0].Key != "budget:bud_t:task_1:exceeded:12" {
		t.Fatalf("after: %+v", items)
	}
	// a budget switched off is no item
	e.exec(`UPDATE budgets SET enabled=0 WHERE id='bud_t'`)
	if items, _ = e.svc.NeedsYou(e.ctx, e.project); len(items) != 0 {
		t.Fatalf("disabled: %+v", items)
	}
	// every project's inbox has the other project's limit
	all, _ := e.svc.NeedsYou(e.ctx, "")
	if len(all) != 1 || all[0].ProjectID != "proj_2" {
		t.Fatalf("all projects: %+v", all)
	}
	// the dismissed warning fires again after a limit change: the dismissal was of the earlier one
	e.exec(`DELETE FROM budget_alerts WHERE budget_id='bud_p'`)
	e.exec(`INSERT INTO budget_alerts(budget_id,instance_id,level,ts) VALUES('bud_p','proj_1','warning',30)`)
	if items, _ = e.svc.NeedsYou(e.ctx, e.project); len(items) != 1 || items[0].Key != "budget:bud_p:proj_1:warning:30" {
		t.Fatalf("fired again: %+v", items)
	}
}
