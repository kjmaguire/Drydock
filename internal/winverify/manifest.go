package winverify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Manifest describes a bundle: where it was built from, for which system, and how each test binary is run. The builder
// writes it as bundle.json; drydock-verify reads it from the folder it runs in.
type Manifest struct {
	Format    int       `json:"format"`
	Commit    string    `json:"commit"`
	Dirty     bool      `json:"dirty,omitempty"`
	Built     time.Time `json:"built"`
	GOOS      string    `json:"goos"`
	GOARCH    string    `json:"goarch"`
	GoVersion string    `json:"goVersion"`
	Verify    string    `json:"verify"`  // drydock-verify[.exe]
	E2E       string    `json:"e2e"`     // drydock-e2e[.exe], the scripted core the smoke steps drive
	Fixture   string    `json:"fixture"` // the fixture project the smoke copies (slash-separated, relative)
	// GoBundled is the version of the Go toolchain copied into go/ ("" when the bundle has none). drydock-verify uses it
	// for the tests that run go themselves when no Go is installed.
	GoBundled string    `json:"goBundled,omitempty"`
	Packages  []Package `json:"packages"`
	// Untested lists the packages with Windows-specific code that have no tests on this system (compiled, not run).
	Untested []string `json:"untested,omitempty"`
}

// manifestFormat changes when drydock-verify can no longer read an older bundle.json.
const manifestFormat = 1

// Package is one test binary in the bundle.
type Package struct {
	Path   string `json:"path"`   // slash-separated, relative to the module (internal/proc)
	Binary string `json:"binary"` // slash-separated, relative to the bundle (tests/internal/proc/proc.test.exe)
	Proves string `json:"proves"`
	// Why says why the package is in the bundle: Windows-specific code, asked for by name, or real-Claude tests.
	Why     []string `json:"why"`
	Timeout string   `json:"timeout"`
	// NeedsGo are the top-level tests that run the Go toolchain themselves (they are skipped when go is not on PATH).
	NeedsGo []string `json:"needsGo,omitempty"`
	// RealClaude are the top-level tests that use the real Claude Code CLI. They never run in the unit-test step; the
	// real-Claude step runs only them, and only with --with-claude.
	RealClaude []string `json:"realClaude,omitempty"`
}

// Dir is the package folder inside the bundle (the working folder its binary runs in).
func (p Package) Dir() string { return filepath.Dir(filepath.FromSlash(p.Binary)) }

// StepID is the id of the package's unit-test step.
func (p Package) StepID() string { return "tests:" + p.Path }

// ReadManifest reads bundle.json from a bundle folder.
func ReadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("this folder is not a verification bundle (%s is missing): %w", ManifestName, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", ManifestName, err)
	}
	if m.Format != manifestFormat {
		return nil, fmt.Errorf("%s has format %d; this drydock-verify reads format %d (rebuild the bundle)", ManifestName, m.Format, manifestFormat)
	}
	return &m, nil
}

// WriteManifest writes bundle.json into a bundle folder.
func WriteManifest(dir string, m *Manifest) error {
	m.Format = manifestFormat
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestName), append(b, '\n'), 0o644)
}

// pkgInfo is what the builder knows about a package beyond what it can discover: the plain sentence for the report, how
// long its tests may take, and which tests need the Go toolchain at test time.
type pkgInfo struct {
	Proves  string
	Timeout time.Duration
	// NeedsGo lists top-level tests that run `go` themselves (a check on a Go fixture, the core's pipeline running the
	// fixture project's go build/test/vet). Found by running the suites with a logging go wrapper first on PATH.
	NeedsGo []string
}

// Requested are the packages the bundle always carries, Windows-specific code or not.
var Requested = []string{
	"internal/storage",        // SQLite (pure Go) on this machine's file system
	"internal/agents/claude",  // the Claude Code adapter: the instructions file and the argv
	"internal/connections",    // CLI detection
	"internal/git",            // repositories and task worktrees
	"internal/proc",           // process trees (Job Objects on Windows)
	"internal/agents/runtime", // the agent runtime, and real-Claude tests
	"internal/pipeline",       // the task loop, and real-Claude tests
	"internal/checks",         // the project's own tools (go, npm, gitleaks, osv-scanner) run as Windows child processes
}

// extraRealClaude are real-Claude tests whose names do not start with TestRealClaude (the builder finds those itself).
var extraRealClaude = map[string][]string{
	"internal/core":               {"TestRealTestConnectionPromptsAndIsKeptAcrossARestart"},
	"internal/agents/conformance": {"TestClaudeAdapterAgainstTheRealCLI"},
}

const defaultTimeout = 10 * time.Minute

var known = map[string]pkgInfo{
	"app": {
		Proves: "The window's icons are sized for this machine's display scale (the system's small and big icon sizes at a DPI), and a build without Drydock's icon resource keeps Wails' own.",
	},
	"internal/proc": {
		Proves: "Agent processes start, stop and are killed as a whole tree: on Windows each one runs in a Job Object, so killing an agent also kills everything it started.",
	},
	"internal/storage": {
		Proves: "The SQLite database (pure Go, no CGO) opens, migrates and reads back correctly on this machine's disk.",
	},
	"internal/agents/claude": {
		Proves: "The Claude Code adapter builds its command line correctly and passes the instructions in a file, which keeps claude.cmd on Windows from cutting a multi-line argument short.",
	},
	"internal/connections": {
		Proves: "Drydock finds the agent CLIs installed here (including Windows .cmd launchers) and reports their version and sign-in state without inventing anything.",
	},
	"internal/git": {
		Proves: "Repositories and task worktrees are created, committed to and merged with the git installed on this machine.",
	},
	"internal/providers": {
		Proves: "Provider keys are kept in the system's credential store (the Windows Credential Manager on Windows), with an encrypted fallback for long secrets.",
	},
	"internal/terminal": {
		Proves: "The human's terminal starts a shell on a pseudo-terminal (ConPTY on Windows) and reads and writes it.",
	},
	"internal/security": {
		Proves: "Paths that agents use stay inside the project, with this system's path rules (drive letters, backslashes and case on Windows).",
	},
	"internal/backup": {
		Proves: "Backups are written to, listed in and restored from this system's default backup folder.",
	},
	"internal/supportbundle": {
		Proves: "The support bundle reads free disk space with this system's API and leaves secrets out.",
	},
	"internal/mcp": {
		Proves:  "The MCP gateway and the stdio servers it starts work here, and stopping a server also stops the processes it started.",
		Timeout: 15 * time.Minute,
	},
	"internal/checks": {
		Proves: "Project checks (build, test, lint, secrets) run as child processes, and a check that runs too long is killed with everything it started.",
		NeedsGo: []string{"TestPassingTestRun", "TestFailingTestIsReportedWithNamesNotAsError", "TestBuildFailureOutputIsCapturedFromStderr",
			"TestTimeoutKillsTheCheck"},
	},
	"internal/agents/acp": {
		Proves: "The ACP client talks to agents over stdio and refuses file paths outside the project, with this system's path rules.",
		// the scripted ACP agent asks for a terminal that runs `go version`
		NeedsGo: []string{"TestAgentTerminalIsOffByDefaultAndConfinedWhenOn"},
	},
	"internal/agents/conformance": {
		Proves: "The Claude and ACP adapters pass the shared conformance suite against the scripted test-double agents (process ids read the Windows way).",
	},
	"internal/agents/runtime": {
		Proves: "The agent runtime starts, streams, interrupts and stops agent processes (the scripted test-double agent) on this machine.",
	},
	"internal/agents/responder": {
		Proves: "Agents answer in the Engineering Room through the agent runtime (the scripted test-double agent).",
	},
	"internal/pipeline": {
		Proves:  "The task loop (worker, checks, independent review, human approval, merge) runs end to end with the scripted test-double agent.",
		Timeout: 20 * time.Minute,
		// these run the gocalc fixture's checks (go build, go test, go vet) through the real Verify step
		NeedsGo: []string{"TestAgentCannotApproveTheMerge", "TestBugTemplatePullRequestVariant", "TestBugTemplateReproducesWithAFailingTestFirst",
			"TestCollisionGuardCanBeSwitchedOff", "TestContinueGivesTheWholeLoopBudgetAgain", "TestFeatureTemplateSpecGateRedGreenAndHumanMerge",
			"TestFullStackSaaSRunsDesignE2EScanAndPreviewBeforeTheHumanApproves", "TestHumanRejectionLoopsBackThenMerges",
			"TestHumanRejectsTheSpecThenAcceptsWithEditedCriteria", "TestIterateUntilVerifiedHappyPathEndsInHumanMerge",
			"TestIterationLimitHumanContinueResetsTheBudgetAndDeliversGuidance", "TestIterationLimitStopCancelsTheTaskCleanly",
			"TestLeadEscalationReachesTheHuman", "TestLeadHandlesAWatchdogTrip", "TestLeadHandlesAnErrorEcho", "TestLeadHandlesRepeatedReviewFeedback",
			"TestLeadInterventionsAreBoundedPerRun", "TestLeadInvalidAnswerRetriesOnceThenEscalates", "TestLeadNudgesAtTheIterationLimit",
			"TestLeadReassignsTheTask", "TestLeadSplitsOffFollowUps", "TestOnlyAHumanContinuesPastALimit",
			"TestPRFlowAGitHubFailureAsksTheHumanAndRetriesOnlyThatStep", "TestPRFlowCIFailureLoopsBackThenHumanMergesThroughGitHub",
			"TestPRFlowNeverMergesACommitPushedAfterTheReview", "TestPRFlowReviewFeedbackTrustedVersusUntrusted",
			"TestParallelTasksUseSeparateWorktreesRespectTheLimitAndNeedHumanMerges", "TestPipelineIterationLimitParksForHumanAndKeepsWork",
			"TestPipelineLoopsOnFailingChecksWithMinimalContext", "TestRegisterTemplatesIsRepeatableOnARunningEngine",
			"TestRepeatedReviseFeedbackEscalatesToTheHuman", "TestReviewerMustDifferFromAssignee", "TestSaaSE2EAndPreviewAreSkippedHonestlyWhenOff",
			"TestSecurityGateLoopsBackWhileAFindingRemains", "TestSecurityReviewerCanSendWorkBack", "TestSecurityTemplateRunsAPluggedInScanByName",
			"TestSecurityTemplateScansFixesRescansAndNeedsAHumanMerge", "TestTaskApprovedBeforeTheBaseMovedMergesWithoutAskingAgain",
			"TestTestWritingStepIsScopedToTestsAndTracesTheCriteria", "TestTestsThatPassBeforeTheImplementationAreRejected",
			"TestVerifyHonoursTheProjectSettingToTurnSecretsOff", "TestVerifyReportsDepsAsSkippedWhenOSVScannerIsMissing",
			"TestVerifySendsAnAddedSecretBackToTheAgentWithoutTheValue", "TestWaitingTaskCanBeReleasedByAHumanOnly", "TestWithoutALeadNothingChanges",
			"TestRealClaudeImplementsTheFixtureTask", "TestRealClaudeWorkerAndReviewerFullLoop"},
	},
	"internal/core": {
		Proves:  "The whole core works through the methods the UI calls (projects, tasks, rooms, chats, terminals, backups), with the scripted test-double agent.",
		Timeout: 30 * time.Minute,
		// these run a task through the pipeline, whose checks on the gocalc fixture are go build, go test and go vet
		NeedsGo: []string{"TestAgentsWithoutRouteModelsKeepTheirModel", "TestBudgetPausesTheRunAndTheHumanResumesByRaisingTheLimit",
			"TestDiffViewerShowsBothVersionsOfEveryFile", "TestE2EAndPreviewChecksThroughTheUIMethods",
			"TestExpiryStopsRunningWorkflowsAndTheyResumeAfterRenewal", "TestGoldenPathFeatureTemplateEndToEnd",
			"TestHumanCanAllowTheGatewayForAnUnverifiedACPCLIAndItWorksEndToEnd", "TestLeadInterventionThroughTheApp",
			"TestPipelineTurnsGetOneGatewayEntryAndTheRunEndsWithTheTurn", "TestPlanAcceptSchedulerAndHumanMergesThroughTheUIMethods",
			"TestProjectWithoutRepositoryThenAttach", "TestProviderMaxConcurrentSerializesAgentsOnThatProvider", "TestRejectingABudgetPauseFailsTheRun",
			"TestRestartKeepsTheRunWaitingForTheHuman", "TestRoomRepliesThroughTheUIMethods", "TestRunExplanationExplainsEveryTurn",
			"TestSkillsAndPackInstructionsReachTheWorktreeInTheRealLoop", "TestTaskLogsListAndReadByRange",
			"TestTheRouterChoosesTheModelOfAgentTurnsAndEscalates", "TestUIMethodsDriveATaskFromCreationToHumanMerge",
			"TestUnverifiedAdapterGetsNoMCPAndTheManifestSaysWhy", "TestUserMCPServerEndToEnd",
			"TestWorkerChecksAreExactCommandsTheWorkerIsToldAboutAndNeverForTheReviewer", "TestWorkersGetNoCommandsUnlessTheProjectAllowsIt",
			"TestRealClaudeLoopThroughTheCoreAndTheEvalReport"},
	},
	"internal/winverify": {
		Proves: "drydock-verify's own parsing, reports and process checks work on this machine.",
	},
}

// infoFor returns what is known about a package, with defaults for one the table does not list.
func infoFor(path string) pkgInfo {
	i, ok := known[path]
	if !ok {
		i.Proves = "Runs this package's own tests here; the package has code that differs on Windows."
	}
	if i.Timeout == 0 {
		i.Timeout = defaultTimeout
	}
	return i
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
