package sandbox

import (
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// StarterFrom suggests a definition from the files on the default branch (effort review X35): the language files Starter knows,
// and what the project already says about its containers. A compose file's services that run an image (not one built here: that
// is the project itself) become services, with their environment and command; the environment of the service built here becomes
// the definition's env. A dev container's image, else a Dockerfile's first FROM, becomes the image. read returns a file's text and
// whether it is there. sources names the files the suggestion took something from.
//
// It is still a suggestion the person reads and commits; the values are the repository's text copied as they are, and what lands in
// env is visible to the agent, so the settings say so beside it. A value that names a variable of the machine (${...}) is left out:
// it would mean nothing in the sandbox, or carry a secret from elsewhere.
func StarterFrom(read func(name string) (string, bool)) (Config, []string) {
	c := Starter(func(name string) bool { _, ok := read(name); return ok })
	var sources []string
	for _, name := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
		if text, ok := read(name); ok {
			if img := devcontainerImage(text); img != "" {
				c.Image = img
				sources = append(sources, name)
			}
			break
		}
	}
	if len(sources) == 0 {
		if text, ok := read("Dockerfile"); ok {
			if img := dockerfileImage(text); img != "" {
				c.Image = img
				sources = append(sources, "Dockerfile")
			}
		}
	}
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		text, ok := read(name)
		if !ok {
			continue
		}
		services, env := composeServices(text)
		if len(services) > 0 || len(env) > 0 {
			c.Services, c.Env = services, env
			sources = append(sources, name)
		}
		break
	}
	return c, sources
}

// devcontainerImage is the "image" of a devcontainer.json, which is JSON with comments and trailing commas.
func devcontainerImage(text string) string {
	var d struct {
		Image string `json:"image"`
	}
	if json.Unmarshal([]byte(jsonc(text)), &d) != nil {
		return ""
	}
	return validImage(d.Image)
}

// jsonc turns JSON with comments (// and /* */) and trailing commas into JSON. Text inside strings is kept as it is.
func jsonc(text string) string {
	// comments first, then trailing commas over the text without them: a comma followed by a comment and then a closing bracket
	// (a commented-out last property) is a trailing comma too
	return dropTrailingCommas(dropComments(text))
}

// skipString copies the JSON string that starts at text[i] (a quote) to b and returns the index of its closing quote.
func skipString(b *strings.Builder, text string, i int) int {
	b.WriteByte(text[i])
	for i++; i < len(text); i++ {
		ch := text[i]
		b.WriteByte(ch)
		switch ch {
		case '\\':
			if i+1 < len(text) {
				i++
				b.WriteByte(text[i])
			}
		case '"':
			return i
		}
	}
	return i
}

// dropComments removes // and /* */ comments outside strings; a line comment leaves its line's end.
func dropComments(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case ch == '"':
			i = skipString(&b, text, i)
		case ch == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case ch == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 3
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// dropTrailingCommas removes, outside strings, a comma followed only by space before a closing bracket.
func dropTrailingCommas(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch ch {
		case '"':
			i = skipString(&b, text, i)
		case ',':
			j := i + 1
			for j < len(text) && strings.ContainsRune(" \t\r\n", rune(text[j])) {
				j++
			}
			if j < len(text) && (text[j] == '}' || text[j] == ']') {
				continue
			}
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// dockerfileImage is the image of a Dockerfile's first FROM: one that names a build argument or starts from nothing is no image to
// suggest.
func dockerfileImage(text string) string {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "FROM") {
			continue
		}
		args := f[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:]
		}
		if len(args) == 0 || strings.Contains(args[0], "$") || strings.EqualFold(args[0], "scratch") {
			return ""
		}
		return validImage(args[0])
	}
	return ""
}

func validImage(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "$") || !imageRe.MatchString(s) {
		return ""
	}
	return s
}

// composeServices reads a compose file: the services that run an image become services (at most 8, by name), and the environment
// of the one built here (build:) becomes the env. What does not fit a definition (a name, an image, a variable) is left out.
func composeServices(text string) (map[string]Service, map[string]string) {
	var f struct {
		Services map[string]struct {
			Image       string    `yaml:"image"`
			Build       yaml.Node `yaml:"build"`
			Environment yaml.Node `yaml:"environment"`
			Command     yaml.Node `yaml:"command"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(text), &f) != nil {
		return nil, nil
	}
	names := make([]string, 0, len(f.Services))
	for n := range f.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var services map[string]Service
	var env map[string]string
	for _, n := range names {
		s := f.Services[n]
		if !s.Build.IsZero() {
			if env == nil {
				if e := composeEnv(&s.Environment); len(e) > 0 && checkEnv(e) == nil {
					env = e
				}
			}
			continue
		}
		name := strings.ToLower(n)
		img := validImage(s.Image)
		if img == "" || !serviceRe.MatchString(name) || name == "workspace" || name == "egress" || len(services) >= 8 {
			continue
		}
		svc := Service{Image: img, Env: composeEnv(&s.Environment), Command: composeCommand(&s.Command)}
		if checkEnv(svc.Env) != nil {
			svc.Env = nil
		}
		if services == nil {
			services = map[string]Service{}
		}
		services[name] = svc
	}
	return services, env
}

// composeEnv reads environment: as a map (KEY: value) or a list (KEY=value); a key with no value or a value naming a variable of
// the machine is left out.
func composeEnv(n *yaml.Node) map[string]string {
	out := map[string]string{}
	add := func(k, v string) {
		k = strings.TrimSpace(k)
		if k != "" && !strings.Contains(v, "${") && !strings.HasPrefix(v, "$") {
			out[k] = v
		}
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			if v.Kind != yaml.ScalarNode || v.Tag == "!!null" {
				continue
			}
			add(n.Content[i].Value, v.Value)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if k, v, ok := strings.Cut(item.Value, "="); ok {
				add(k, v)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// composeCommand reads command: a list as it is, a string split on spaces (compose splits it like a shell; quotes are rare in a
// service's command).
func composeCommand(n *yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.ScalarNode:
		out = strings.Fields(n.Value)
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				out = append(out, item.Value)
			}
		}
	}
	if len(out) > 64 {
		return nil
	}
	return out
}
