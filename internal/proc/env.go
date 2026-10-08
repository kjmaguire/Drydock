package proc

import "strings"

// BaseEnvNames is the environment a scrubbed agent process keeps: enough to run a command-line program and reach the network
// through a corporate proxy, and nothing that carries a credential for another service. Names are matched case-insensitively
// (Windows environment names are).
var BaseEnvNames = []string{
	"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "HOMEDRIVE", "HOMEPATH", "USERNAME", "USER", "LOGNAME",
	"LANG", "LC_ALL", "TERM", "TZ", "SHELL", "NO_COLOR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
	// the network: proxies and the certificate authorities that go with them
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
}

// ScrubEnv returns the entries of parent ("KEY=value") whose name is in names or starts with one of prefixes. Nothing else is
// kept: not the app's own variables, not other tools' tokens.
func ScrubEnv(parent, names, prefixes []string) []string {
	allow := make(map[string]bool, len(names))
	for _, n := range names {
		allow[strings.ToUpper(n)] = true
	}
	var out []string
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
