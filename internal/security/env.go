package security

import (
	"sort"
	"strings"
)

// EnvPolicy is what an adapter asks for when building an agent process environment (docs/security.md s8, T9).
type EnvPolicy struct {
	// Allow names (exact, case-insensitive) or prefixes ("NAME*") that survive the secret-name heuristic, for example
	// ANTHROPIC_API_KEY for an API-key Claude connection. Allow never overrides NeverEnv.
	Allow []string
	// Set injects variables the adapter supplies itself (for example the provider key read from the credential store).
	// A name in NeverEnv is dropped even here.
	Set map[string]string
	// Deny adds names or prefixes to remove on top of the defaults.
	Deny []string
	// OnlyBase keeps only BaseEnv names (plus Allow and Set) instead of everything not denied.
	OnlyBase bool
}

// BaseEnv is the allowlist for OnlyBase mode: PATH, system roots, locale, home and temp locations. Illustrative.
var BaseEnv = []string{
	"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS",
	"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)",
	"PROGRAMW6432", "COMMONPROGRAMFILES", "TEMP", "TMP", "TMPDIR", "USERNAME", "USER", "LOGNAME", "COMPUTERNAME",
	"LANG", "LANGUAGE", "LC_*", "TZ", "TERM", "COLORTERM", "SHELL", "PWD", "NO_COLOR", "FORCE_COLOR", "ComSpec",
	"GOPATH", "GOROOT", "GOCACHE", "NODE_OPTIONS", "NODE_ENV", "CI",
}

// NeverEnv is removed from every agent environment no matter what an adapter allows: Drydock-internal variables and
// production-capable credentials (the doc's strip list). Names are upper case; a trailing "*" is a prefix.
var NeverEnv = []string{
	"DRYDOCK_*", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "CLOUDFLARE_*", "CF_API_*", "CF_ACCOUNT_ID",
	"AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AZURE_CLIENT_SECRET", "ARM_CLIENT_SECRET", "GOOGLE_APPLICATION_CREDENTIALS",
	"NPM_TOKEN", "NODE_AUTH_TOKEN",
}

// defaultDeny lists credential-bearing names that are removed unless the adapter allows them (provider keys the
// doc names, cloud credential families).
var defaultDeny = []string{
	"ANTHROPIC_*", "OPENAI_*", "OPENROUTER_*", "GEMINI_*", "GOOGLE_API_KEY", "GOOGLE_GENAI_*", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_*TOKEN*",
	"AWS_*", "AZURE_*", "ARM_*", "GCP_*", "GOOGLE_CLOUD_*", "GCLOUD_*", "DIGITALOCEAN_*", "HEROKU_*", "VERCEL_*", "NETLIFY_*", "FLY_*",
	"SENTRY_*", "LINEAR_*", "NOTION_*", "AIKIDO_*", "OTEL_EXPORTER_OTLP_HEADERS",
}

// secretFragments: any name containing one of these is removed unless allowed (the doc's default deny).
var secretFragments = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "KEY", "CREDENTIAL", "PRIVATE"}

// ScrubEnv returns the environment for an agent process from environ (os.Environ() form "NAME=value"). It removes
// NeverEnv and the defaults/Deny, and every name containing TOKEN, SECRET, PASSWORD, KEY (and a few siblings) unless
// the policy allows it explicitly. Order of environ is kept; Set values follow. Names compare case-insensitively
// (Windows semantics) on every OS, which can only remove more.
func ScrubEnv(environ []string, p EnvPolicy) []string {
	allow := upperAll(p.Allow)
	deny := append(upperAll(defaultDeny), upperAll(p.Deny)...)
	never := upperAll(NeverEnv)
	base := upperAll(BaseEnv)

	out := make([]string, 0, len(environ))
	seen := map[string]bool{}
	for _, kv := range environ {
		name, _, ok := splitEnv(kv)
		if !ok {
			continue
		}
		u := strings.ToUpper(name)
		if seen[u] {
			continue
		}
		if strings.HasPrefix(name, "=") { // Windows per-drive cwd entries such as =C:
			out = append(out, kv)
			seen[u] = true
			continue
		}
		if matchName(never, u) {
			continue
		}
		allowed := matchName(allow, u)
		if !allowed {
			if matchName(deny, u) || containsAny(u, secretFragments) {
				continue
			}
			if p.OnlyBase && !matchName(base, u) {
				continue
			}
		}
		seen[u] = true
		out = append(out, kv)
	}
	names := make([]string, 0, len(p.Set))
	for k := range p.Set {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		u := strings.ToUpper(k)
		if k == "" || strings.ContainsAny(k, "=\x00") || matchName(never, u) {
			continue
		}
		// An adapter-supplied value replaces any inherited one.
		for i := 0; i < len(out); i++ {
			if n, _, ok := splitEnv(out[i]); ok && strings.ToUpper(n) == u {
				out = append(out[:i], out[i+1:]...)
				i--
			}
		}
		out = append(out, k+"="+p.Set[k])
	}
	return out
}

// EnvNames returns the sorted names in an environment block (what an ActionDescriptor records).
func EnvNames(environ []string) []string {
	var ns []string
	for _, kv := range environ {
		if n, _, ok := splitEnv(kv); ok {
			ns = append(ns, n)
		}
	}
	sort.Strings(ns)
	return ns
}

func splitEnv(kv string) (name, val string, ok bool) {
	i := strings.Index(kv[min(1, len(kv)):], "=") // a leading '=' belongs to the name (=C:)
	if i < 0 {
		return "", "", false
	}
	i += min(1, len(kv))
	return kv[:i], kv[i+1:], true
}

func upperAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToUpper(s)
	}
	return out
}

// matchName: pattern is an exact name, or contains '*' wildcards (any run of characters).
func matchName(patterns []string, name string) bool {
	for _, p := range patterns {
		if !strings.Contains(p, "*") {
			if p == name {
				return true
			}
			continue
		}
		if wildMatch(p, name) {
			return true
		}
	}
	return false
}

func wildMatch(pat, s string) bool {
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

func containsAny(s string, frags []string) bool {
	for _, f := range frags {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}
