package manifests

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// How a person's own CLI is given the MCP gateway (decision P117). A custom manifest may carry an `mcp` section that says how its
// CLI takes an MCP configuration; without one the CLI gets no MCP. The configuration is always the mcpServers JSON the other CLIs
// read (`{"mcpServers": {"drydock": {...}}}`, internal/mcp.ConfigFor) holding the run's single gateway entry:
//
//	mcp: {via: arg, format: mcpServers, args: [--mcp-config, "{{mcp_config}}"]}   # a flag that names the file
//	mcp: {via: file, format: mcpServers, file: .my-cli/mcp.json}                   # a file it reads from its working folder
//
// Like every non-Claude CLI's mechanism it is unproven until a real run uses it, so the gateway reaches the CLI only when a person
// allows the gateway for it (decision P20, mcp.try.set), and then under the role's row of the tool table (decision P115).

// Custom MCP routes and the one format.
const (
	CustomMCPViaArg  = "arg"
	CustomMCPViaFile = "file"
	// CustomMCPFormat is the mcpServers JSON of Claude Code and the ACP bridges.
	CustomMCPFormat = "mcpServers"
	// CustomMCPPlaceholder is where the config file's path goes in mcp.args.
	CustomMCPPlaceholder = "{{mcp_config}}"
)

// CustomMCP is a custom manifest's `mcp` section, validated.
type CustomMCP struct {
	Via    string   // arg or file
	Format string   // mcpServers
	Args   []string // via arg: the flags that name the config file, {{mcp_config}} exactly once
	File   string   // via file: the path the CLI reads, relative to its working folder, with / separators
}

// ArgsFor is Args with the config file's path in place of {{mcp_config}}.
func (c *CustomMCP) ArgsFor(configPath string) []string {
	out := make([]string, len(c.Args))
	for i, a := range c.Args {
		out[i] = strings.ReplaceAll(a, CustomMCPPlaceholder, configPath)
	}
	return out
}

// CustomMCP returns the mcp section of a custom manifest that declares one (nil, nil for any other manifest or none).
func (m *Manifest) CustomMCP() (*CustomMCP, error) {
	if !m.Meta.Custom || m.MCP == nil {
		return nil, nil
	}
	c, p := parseCustomMCP(m.MCP)
	if len(p) > 0 {
		return nil, &ValidationError{ID: m.Meta.ID, Problems: p}
	}
	return c, nil
}

// parseCustomMCP reads a custom manifest's mcp section strictly: known keys only, the one format, a route with exactly what it
// needs. A file route stays inside the working folder and out of .git, and names a .json file.
func parseCustomMCP(raw map[string]any) (*CustomMCP, []string) {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	c := &CustomMCP{}
	for k, v := range raw {
		switch k {
		case "via", "format", "file":
			s, ok := v.(string)
			if !ok {
				add("mcp.%s must be a string", k)
				continue
			}
			switch k {
			case "via":
				c.Via = s
			case "format":
				c.Format = s
			default:
				c.File = s
			}
		case "args":
			list, ok := v.([]any)
			if !ok {
				add("mcp.args must be a list of strings")
				continue
			}
			for _, a := range list {
				s, ok := a.(string)
				if !ok {
					add("mcp.args must be a list of strings")
					break
				}
				c.Args = append(c.Args, s)
			}
		default:
			add("mcp: unknown key %q (a custom manifest's mcp has via, format, args and file)", k)
		}
	}
	if c.Format != CustomMCPFormat {
		add("mcp.format must be %s (the {\"mcpServers\": {...}} JSON the other CLIs read), got %q", CustomMCPFormat, c.Format)
	}
	switch c.Via {
	case CustomMCPViaArg:
		if c.File != "" {
			add("mcp.file is for via: file; via: arg names the file in mcp.args")
		}
		n := 0
		for _, a := range c.Args {
			n += strings.Count(a, CustomMCPPlaceholder)
			for _, v := range placeholderNames(a) {
				if v != "mcp_config" {
					add("mcp.args may use only {{mcp_config}}, not {{%s}}", v)
				}
			}
			if strings.TrimSpace(a) == "" {
				add("mcp.args: an empty argument")
			}
		}
		if n != 1 {
			add("mcp.args must hold {{mcp_config}} exactly once (where the config file's path goes), found %d", n)
		}
	case CustomMCPViaFile:
		if len(c.Args) > 0 {
			add("mcp.args is for via: arg; via: file names the file in mcp.file")
		}
		f := c.File
		clean := path.Clean(f)
		first, _, _ := strings.Cut(clean, "/")
		switch {
		case f == "" || len(f) > 128:
			add("mcp.file must be a path of 1 to 128 characters")
		case strings.HasPrefix(f, "/") || strings.ContainsAny(f, `\:`) || clean != f || clean == "." || clean == ".." || strings.HasPrefix(clean, "../"):
			add("mcp.file %q must be a clean relative path with / separators inside the working folder", f)
		case strings.EqualFold(first, ".git"):
			add("mcp.file %q cannot be inside .git", f)
		case !strings.HasSuffix(strings.ToLower(f), ".json"):
			add("mcp.file %q must name a .json file", f)
		}
	default:
		add("mcp.via must be arg or file, got %q", c.Via)
	}
	if len(p) > 0 {
		sort.Strings(p)
		return nil, p
	}
	return c, nil
}
