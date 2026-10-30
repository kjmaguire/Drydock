package pipeline

// What the steps that change something outside a run change (workflow.MetaEffect, decision P123). A replay never runs one of
// them without a fresh approval; the run page lists them in the replay's confirmation. Every graph that merges, pushes or
// creates tasks marks the node that does it (TestEveryStepThatChangesSomethingOutsideTheRunIsMarked).
const (
	EffectMerge       = "merges the task's branch into its target branch"
	EffectMergePR     = "merges the pull request on GitHub"
	EffectPush        = "pushes the branch and opens or updates the pull request"
	EffectCreateTasks = "creates the plan's tasks"
)
