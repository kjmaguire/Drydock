package proc

import (
	"os"
	"slices"
	"strings"
)

// BaseEnvNames is the environment a scrubbed agent process keeps: enough to run a command-line program and reach the network
// through a corporate proxy, and nothing that carries a credential for another service. Names are matched case-insensitively
// (Windows environment names are).
var BaseEnvNames = []string{
	"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "HOMEDRIVE", "HOMEPATH", "USERNAME", "USER", "LOGNAME",
	"LANG", "LC_ALL", "TERM", "TZ", "SHELL", "NO_COLOR", NoCwdSearchVar, "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
	// the network: proxies and the certificate authorities that go with them
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
}

// ScrubEnv returns the entries of parent ("KEY=value") whose name is in names or starts with one of prefixes. Nothing else is
// kept: not the app's own variables, not other tools' tokens. The result is never nil, because a nil Spec.Env means "inherit the
// whole environment" and a scrub that found nothing must not turn into that.
func ScrubEnv(parent, names, prefixes []string) []string {
	allow := make(map[string]bool, len(names))
	for _, n := range names {
		allow[strings.ToUpper(n)] = true
	}
	out := []string{}
	for _, kv := range parent {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		u := strings.ToUpper(k)
		if allow[u] {
			out = append(out, kv)
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(u, strings.ToUpper(p)) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// ToolEnvNames are, besides BaseEnvNames, the variables a project's build, test, lint or preview command reads to find its
// toolchain (Go, Node, Python, Java, Rust, .NET, the Windows SDK and C compilers) and the Windows system facts build tools
// ask for. None of them carries a credential. Everything else of the app's environment (tokens for GitHub, cloud providers,
// npm registries) stays out of a command that runs code an agent just wrote (docs/security.md s8).
var ToolEnvNames = []string{
	"GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE", "GOFLAGS", "GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOSUMDB",
	"GOTOOLCHAIN", "GOENV", "GOTMPDIR", "GOEXPERIMENT", "GOAMD64", "CGO_ENABLED", "CGO_CFLAGS", "CGO_LDFLAGS", "CC", "CXX", "AR", "PKG_CONFIG_PATH",
	"NODE_PATH", "NODE_OPTIONS", "NVM_HOME", "NVM_SYMLINK", "NVM_DIR", "VOLTA_HOME", "PNPM_HOME", "YARN_CACHE_FOLDER", "COREPACK_HOME",
	"PLAYWRIGHT_BROWSERS_PATH", "PUPPETEER_EXECUTABLE_PATH", "CHROME_PATH",
	"PYTHONPATH", "PYTHONHOME", "PYTHONUTF8", "VIRTUAL_ENV", "CONDA_PREFIX", "CONDA_DEFAULT_ENV", "PIPX_HOME", "UV_CACHE_DIR", "POETRY_HOME",
	"JAVA_HOME", "MAVEN_HOME", "M2_HOME", "GRADLE_HOME", "GRADLE_USER_HOME", "CARGO_HOME", "RUSTUP_HOME", "RUSTUP_TOOLCHAIN",
	"DOTNET_ROOT", "DOTNET_CLI_TELEMETRY_OPTOUT", "NUGET_PACKAGES", "VCPKG_ROOT", "INCLUDE", "LIB", "LIBPATH", "VCINSTALLDIR", "VSINSTALLDIR",
	"VCTOOLSINSTALLDIR", "WINDOWSSDKDIR", "WINDOWSSDKVERSION", "UCRTVERSION", "UNIVERSALCRTSDKDIR",
	"PROGRAMW6432", "COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
	"PROCESSOR_IDENTIFIER", "OS", "SYSTEMDRIVE", "ALLUSERSPROFILE", "PUBLIC", "COMPUTERNAME", "PSMODULEPATH", "DRIVERDATA",
	"CHOCOLATEYINSTALL", "CHOCOLATEYTOOLSLOCATION", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
}

// ToolEnv is the environment of a project command (a check, a preview, a dependency scan): parent filtered to BaseEnvNames,
// ToolEnvNames and the extra names the person chose to pass (the same list agents get), with the user name and password taken
// out of a proxy address unless the person passed that variable on by name (WithoutProxyCredentials).
func ToolEnv(parent, extra []string) []string {
	names := slices.Concat(BaseEnvNames, ToolEnvNames, extra)
	return WithoutProxyCredentials(ScrubEnv(parent, names, nil), extra)
}

// proxyEnvNames are the variables that hold the address of a proxy (compared ignoring case: Windows does, and Unix tools read
// both spellings).
var proxyEnvNames = map[string]bool{"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true}

// WithoutProxyCredentials returns env with the user name and password taken out of the address in HTTP_PROXY, HTTPS_PROXY and
// ALL_PROXY ("http://user:pass@proxy:8080" becomes "http://proxy:8080"; an address without a scheme, "user:pass@proxy:8080",
// is cut the same way). The allowlist passes proxy settings so an agent's CLI and a build can reach the network, and a program
// an agent steers can read its own environment; the address is what it needs, the password is a credential for another service
// (audit 2026-10-16, L8). The names in keep are left as they are: a person who passes a proxy variable on by name chose to hand
// its credentials over.
func WithoutProxyCredentials(env, keep []string) []string {
	kept := make(map[string]bool, len(keep))
	for _, k := range keep {
		kept[strings.ToUpper(k)] = true
	}
	out := make([]string, len(env))
	for i, kv := range env {
		out[i] = kv
		k, v, ok := strings.Cut(kv, "=")
		if ok && proxyEnvNames[strings.ToUpper(k)] && !kept[strings.ToUpper(k)] {
			out[i] = k + "=" + stripUserinfo(v)
		}
	}
	return out
}

// stripUserinfo removes "user:pass@" from the authority of an address that may or may not have a scheme.
func stripUserinfo(addr string) string {
	scheme, rest := "", addr
	if i := strings.Index(addr, "://"); i >= 0 {
		scheme, rest = addr[:i+3], addr[i+3:]
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = authority[i+1:]
	}
	return scheme + authority + tail
}

// NoCwdSearchVar is the Windows setting that stops cmd.exe and CreateProcess from looking for a bare program name in the
// current directory before PATH.
const NoCwdSearchVar = "NoDefaultCurrentDirectoryInExePath"

// WithSafeExeSearch returns env (nil: this process's environment) with NoDefaultCurrentDirectoryInExePath=1. An agent's
// working directory is its worktree, which the agent writes; the npm launchers of agent CLIs on Windows (claude.cmd and the
// like) are batch files that run a bare `node`, and without this setting cmd.exe finds a `node.cmd` or `node.exe` planted in the
// worktree before the real one. The setting is always 1 in the result, whatever env held (audit 2026-10-16, H2).
func WithSafeExeSearch(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, NoCwdSearchVar) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, NoCwdSearchVar+"=1")
}

// HardenProcess sets NoDefaultCurrentDirectoryInExePath=1 in this process's own environment, so every program the app starts
// without an environment of its own (git, gh, the container engine, npm) inherits it, and Go's own program lookup skips the
// current directory. Call it first thing in main.
func HardenProcess() {
	_ = os.Setenv(NoCwdSearchVar, "1")
}
