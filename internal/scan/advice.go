package scan

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Plain words for a finding (decision P97): what it means and what to do, for the Security page's detail pane. A secret is
// explained by its gitleaks rule family (an AWS key, a GitHub token, a private key, a generic API key...), a vulnerable
// dependency by its advisories and the version that fixes them, and a code finding of a built-in rule by the rule's own
// message and metadata (its CWE). A rule Drydock does not know (one of a person's own rules) gets its finding's title and
// the general steps.

// Advice is what a finding means and what to do about it.
type Advice struct {
	Meaning string   `json:"meaning"`
	Steps   []string `json:"steps"`
	// Refs are what the advice rests on: a dependency's advisory ids, a code rule's weakness (CWE).
	Refs []string `json:"refs,omitempty"`
}

// BuiltinRule is a built-in Code rule as the advice reads it.
type BuiltinRule struct {
	ID       string
	Set      string
	Severity string
	Message  string
	CWE      []string
}

var builtinIndex struct {
	once  sync.Once
	rules map[string]BuiltinRule
	ids   []string // longest first, for suffix matches
}

func loadBuiltinIndex() {
	builtinIndex.rules = map[string]BuiltinRule{}
	_ = fs.WalkDir(builtinFS, "rules", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isRuleFile(p) {
			return nil
		}
		b, err := fs.ReadFile(builtinFS, p)
		if err != nil {
			return nil
		}
		var rf struct {
			Rules []struct {
				ID       string `yaml:"id"`
				Severity string `yaml:"severity"`
				Message  string `yaml:"message"`
				Metadata struct {
					CWE any `yaml:"cwe"`
				} `yaml:"metadata"`
			} `yaml:"rules"`
		}
		if yaml.Unmarshal(b, &rf) != nil {
			return nil
		}
		set := strings.TrimPrefix(path.Dir(p), "rules/")
		for _, r := range rf.Rules {
			if r.ID == "" {
				continue
			}
			br := BuiltinRule{ID: r.ID, Set: set, Severity: r.Severity, Message: strings.Join(strings.Fields(r.Message), " ")}
			switch v := r.Metadata.CWE.(type) {
			case string:
				br.CWE = []string{v}
			case []any:
				for _, x := range v {
					if s, ok := x.(string); ok {
						br.CWE = append(br.CWE, s)
					}
				}
			}
			builtinIndex.rules[r.ID] = br
			builtinIndex.ids = append(builtinIndex.ids, r.ID)
		}
		return nil
	})
	sort.Slice(builtinIndex.ids, func(i, j int) bool { return len(builtinIndex.ids[i]) > len(builtinIndex.ids[j]) })
}

// builtinRule finds the built-in rule a Code finding's rule names: its id, or Opengrep's name for it (the rules folder's path
// in dots, then the id).
func builtinRule(rule string) (BuiltinRule, bool) {
	builtinIndex.once.Do(loadBuiltinIndex)
	if r, ok := builtinIndex.rules[rule]; ok {
		return r, true
	}
	for _, id := range builtinIndex.ids {
		if strings.HasSuffix(rule, "."+id) {
			return builtinIndex.rules[id], true
		}
	}
	return BuiltinRule{}, false
}

// AdviceFor is the plain words for a finding.
func AdviceFor(f Finding) Advice {
	switch f.Engine {
	case Secrets:
		return secretAdvice(f)
	case Deps:
		return depsAdvice(f)
	}
	return codeAdvice(f)
}

// secretKind is a gitleaks rule family: what the value is, where it is revoked, and whether it is often a false alarm.
type secretKind struct {
	what, revoke string
}

// secretKinds are the families by the start of the gitleaks rule id (the longest match wins).
var secretKinds = map[string]secretKind{
	"aws":             {"an AWS access key", "Deactivate the key in the AWS console (IAM, the user's Security credentials) and make a new one if it is still needed."},
	"github":          {"a GitHub token", "Revoke the token on GitHub (Settings, Developer settings, Personal access tokens, or the app or OAuth app it belongs to) and make a new one if it is still needed."},
	"gitlab":          {"a GitLab token", "Revoke the token in GitLab (User settings, Access tokens, or the project's or group's tokens) and make a new one if it is still needed."},
	"private-key":     {"a private key", "Make a new key pair, put the new public key wherever the old one is trusted (servers, deploy keys, certificates), and remove the old public key there."},
	"generic-api-key": {"a value that looks like a key, token or password (a long random value given to a name like key, token or secret)", "If it is a real credential, revoke it where it was issued and make a new one."},
	"jwt":             {"a JSON Web Token", "A token stays valid until it expires: if it can still be used, revoke the session or rotate the key it was signed with."},
	"slack":           {"a Slack token or webhook", "Revoke it in the Slack app's settings (OAuth & Permissions, or Incoming Webhooks) and make a new one."},
	"stripe":          {"a Stripe API key", "Roll the key in the Stripe dashboard (Developers, API keys)."},
	"openai":          {"an OpenAI API key", "Revoke the key on the OpenAI platform (API keys) and make a new one."},
	"anthropic":       {"an Anthropic API key", "Revoke the key in the Anthropic Console (API keys) and make a new one."},
	"gcp":             {"a Google Cloud key", "Delete the key in the Google Cloud console (APIs & Services, Credentials, or the service account's keys) and make a new one."},
	"azure":           {"an Azure credential", "Rotate the secret in the Azure portal (the app registration's Certificates & secrets, or the resource's keys)."},
	"npm":             {"an npm access token", "Revoke the token on npmjs.com (Access Tokens) and make a new one."},
	"pypi":            {"a PyPI upload token", "Revoke the token on pypi.org (Account settings, API tokens) and make a new one."},
	"huggingface":     {"a Hugging Face token", "Revoke the token on huggingface.co (Settings, Access Tokens) and make a new one."},
	"generic":         {"a value that looks like a credential", "If it is a real credential, revoke it where it was issued and make a new one."},
}

// secretFamily finds the family of a gitleaks rule id ("github-pat" is github's, "private-key" its own).
func secretFamily(rule string) (secretKind, bool) {
	best := ""
	for prefix := range secretKinds {
		if (rule == prefix || strings.HasPrefix(rule, prefix+"-")) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return secretKind{}, false
	}
	return secretKinds[best], true
}

func secretAdvice(f Finding) Advice {
	where := f.File
	if where == "" {
		where = "the file"
	}
	kind, ok := secretFamily(f.Rule)
	if !ok {
		kind = secretKind{what: "a credential (" + cleanLine(f.Title, 160) + ")", revoke: "Revoke it where it was issued and make a new one if it is still needed."}
	}
	a := Advice{Meaning: fmt.Sprintf("%s is written in %s. Anyone who can read the file, a copy of it, or the repository's history can use it.", upperFirst(kind.what), where)}
	if f.Rule == "generic-api-key" || strings.HasPrefix(f.Rule, "generic") {
		a.Meaning = fmt.Sprintf("%s is written in %s. This rule also finds test values and examples, so check it first: if it is real, anyone who can read the file, a copy of it, or the repository's history can use it.", upperFirst(kind.what), where)
	}
	a.Steps = []string{
		kind.revoke,
		fmt.Sprintf("Take it out of %s: read it from an environment variable or a secret store instead, and keep files that hold secrets (like .env) out of the repository.", where),
		"If it was ever committed, it stays in the git history: revoking it is what makes it safe; rewriting the history is optional.",
		"If it is a test value or an example, dismiss the finding as \"Not a real key\".",
	}
	if f.Rule == "private-key" {
		a.Steps[1] = fmt.Sprintf("Delete the key from %s and keep key files out of the repository (add them to .gitignore).", where)
	}
	return a
}

func depsAdvice(f Finding) Advice {
	pkg := f.Package
	if pkg == "" {
		pkg = "a package"
	}
	eco := ""
	if f.Rule != "" {
		eco = " (" + f.Rule + ")"
	}
	a := Advice{Refs: append([]string(nil), f.IDs...)}
	a.Meaning = fmt.Sprintf("%s %s%s has known vulnerabilities (%s). Code that uses the affected part of it can be attacked.", pkg, f.Version, eco, strings.Join(f.IDs, ", "))
	where := ""
	if f.File != "" {
		where = " in " + f.File
	}
	switch {
	case f.Fixed != "" && len(f.Unfixed) == 0:
		a.Steps = append(a.Steps, fmt.Sprintf("Upgrade %s to %s or later%s: osv.dev lists it as the version that fixes these advisories.", pkg, f.Fixed, where))
	case f.Fixed != "":
		a.Steps = append(a.Steps, fmt.Sprintf("Upgrade %s to %s or later%s: it fixes the advisories osv.dev names a fix for.", pkg, f.Fixed, where),
			fmt.Sprintf("%s name no fixed version yet: read them to see whether your code uses the affected part, and whether there is a workaround.", strings.Join(f.Unfixed, ", ")))
	default:
		a.Steps = append(a.Steps, fmt.Sprintf("osv.dev names no fixed version of %s: read the advisories to see whether your code uses the affected part, and whether there is a workaround or a replacement.", pkg))
	}
	a.Steps = append(a.Steps, "Run the tests after the upgrade, then scan again.",
		"If the code never uses the affected part, you may dismiss the finding as an accepted risk; it comes back if the version changes.")
	for _, id := range f.IDs {
		a.Steps = append(a.Steps, "Read "+id+" at https://osv.dev/vulnerability/"+id+".")
		if len(a.Steps) >= 8 {
			break
		}
	}
	return a
}

func codeAdvice(f Finding) Advice {
	where := f.File
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	generic := []string{
		fmt.Sprintf("Look at the code at %s and change it so the pattern is gone.", where),
		"If the code is safe here (for example the value never comes from outside), dismiss the finding as a false positive.",
	}
	r, ok := builtinRule(f.Rule)
	if !ok || r.Message == "" {
		meaning := f.Title
		if meaning == "" {
			meaning = "The rule " + f.Rule + " matched this code."
		}
		return Advice{Meaning: meaning, Steps: generic}
	}
	sentences := splitSentences(r.Message)
	a := Advice{Meaning: sentences[0], Refs: r.CWE}
	a.Steps = append(a.Steps, sentences[1:]...)
	if len(a.Steps) == 0 {
		a.Steps = append(a.Steps, generic[0])
	}
	a.Steps = append(a.Steps, generic[1])
	return a
}

// splitSentences cuts a rule's message at the end of each sentence (". " followed by a capital letter).
func splitSentences(s string) []string {
	var out []string
	for {
		i := -1
		for j := 0; j+2 < len(s); j++ {
			if s[j] == '.' && s[j+1] == ' ' && s[j+2] >= 'A' && s[j+2] <= 'Z' {
				i = j
				break
			}
		}
		if i < 0 {
			break
		}
		out = append(out, strings.TrimSpace(s[:i+1]))
		s = s[i+2:]
	}
	if s = strings.TrimSpace(s); s != "" {
		out = append(out, s)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
