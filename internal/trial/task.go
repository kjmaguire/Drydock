// Package trial runs the head-to-head trial of docs/evals.md section 13: the same tasks done by Drydock (arm D) and by plain
// Claude Code (arm B1, "one premium agent, no team", section 5), from the same fixture and start commit, with the same model,
// and judged by hidden acceptance checks that neither arm ever sees. It is an evaluation harness (cmd/drydock-trial), not part
// of the product, and it drives the real CLI and the real core: nothing in a run record is estimated or typed by hand.
package trial

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task is one entry of the suite, evals/tasks/<id>.yaml (the format of docs/evals.md 6.1).
type Task struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	// Fixture is the directory, relative to the repository root, copied into a fresh repository for each run.
	Fixture       string     `yaml:"fixture" json:"fixture"`
	Class         string     `yaml:"class" json:"class"`
	Risk          string     `yaml:"risk" json:"risk"`
	Size          string     `yaml:"size" json:"size"`
	ExpectedRoles []string   `yaml:"expected_roles" json:"expectedRoles"`
	Prompt        string     `yaml:"prompt" json:"prompt"`
	Acceptance    Acceptance `yaml:"acceptance" json:"acceptance"`
}

// Acceptance is the hidden check of a task: Files (a directory, relative to the repository root) is laid over a copy of the
// arm's result, then the commands run there with `sh -c`, in order, and all must exit 0.
type Acceptance struct {
	Files          string   `yaml:"files" json:"files"`
	Commands       []string `yaml:"commands" json:"commands"`
	TimeoutSeconds int      `yaml:"timeout_seconds" json:"timeoutSeconds"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// LoadSuite reads every task in dir (*.yaml), sorted by id, and checks each against root, the repository root its paths are
// relative to. An unknown field is an error, so a typo cannot silently drop a hidden check.
func LoadSuite(root, dir string) ([]Task, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no tasks in %s", dir)
	}
	var out []Task
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var t Task
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&t); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if err := t.check(root); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if strings.TrimSuffix(filepath.Base(f), ".yaml") != t.ID {
			return nil, fmt.Errorf("%s: the file name must be the task id %q", filepath.Base(f), t.ID)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("task %s is defined twice", t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *Task) check(root string) error {
	var errs []error
	if !idRe.MatchString(t.ID) {
		errs = append(errs, fmt.Errorf("id %q: lower-case letters, digits and dashes", t.ID))
	}
	if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Prompt) == "" {
		errs = append(errs, errors.New("a task needs a title and a prompt"))
	}
	if st, err := os.Stat(filepath.Join(root, t.Fixture)); t.Fixture == "" || err != nil || !st.IsDir() {
		errs = append(errs, fmt.Errorf("fixture %q is not a directory under the repository", t.Fixture))
	}
	if st, err := os.Stat(filepath.Join(root, t.Acceptance.Files)); t.Acceptance.Files == "" || err != nil || !st.IsDir() {
		errs = append(errs, fmt.Errorf("acceptance files %q are not a directory under the repository", t.Acceptance.Files))
	}
	if len(t.Acceptance.Commands) == 0 {
		errs = append(errs, errors.New("acceptance needs at least one command"))
	}
	if t.Acceptance.TimeoutSeconds <= 0 {
		t.Acceptance.TimeoutSeconds = 300
	}
	return errors.Join(errs...)
}

// FindTasks picks the tasks named in ids (all of them when ids is empty), in suite order.
func FindTasks(all []Task, ids []string) ([]Task, error) {
	if len(ids) == 0 {
		return all, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []Task
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
			delete(want, t.ID)
		}
	}
	if len(want) > 0 {
		var missing []string
		for id := range want {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("no such task: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
