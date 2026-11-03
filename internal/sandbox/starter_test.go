package sandbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func files(m map[string]string) func(string) (string, bool) {
	return func(n string) (string, bool) { v, ok := m[n]; return v, ok }
}

// The starter reads what the project already says about its containers (X35): compose's image services with their environment
// and command, the environment of the service built here, a dev container's image (JSON with comments), else a Dockerfile's first
// FROM. A value naming a variable of the machine, a service built here and names that are no service name are left out.
func TestTheStarterReadsComposeDevcontainerAndDockerfile(t *testing.T) {
	compose := `
services:
  app:
    build: .
    environment:
      DATABASE_URL: postgres://app:app@db:5432/app
      API_KEY: ${API_KEY}
  db:
    image: postgres:17
    environment:
      - POSTGRES_USER=app
      - POSTGRES_PASSWORD=app
      - FROM_HOST
  cache:
    image: redis:7
    command: redis-server --save ""
  Bad_Name:
    image: nginx
  built:
    build: ./other
`
	devc := `{
  // the dev container
  "name": "app", /* a comment */
  "image": "mcr.microsoft.com/devcontainers/typescript-node:22",
  "features": {},
}`
	c, from := StarterFrom(files(map[string]string{"package.json": "{}", "package-lock.json": "{}", "docker-compose.yml": compose, ".devcontainer/devcontainer.json": devc, "Dockerfile": "FROM node:20\n"}))
	if c.Image != "mcr.microsoft.com/devcontainers/typescript-node:22" || c.Setup[0] != "npm ci" {
		t.Fatalf("image %q setup %v", c.Image, c.Setup)
	}
	if strings.Join(from, ",") != ".devcontainer/devcontainer.json,docker-compose.yml" {
		t.Fatalf("sources %v", from)
	}
	if len(c.Services) != 2 || c.Services["db"].Image != "postgres:17" || c.Services["db"].Env["POSTGRES_PASSWORD"] != "app" || len(c.Services["db"].Env) != 2 {
		t.Fatalf("services %+v", c.Services)
	}
	if strings.Join(c.Services["cache"].Command, " ") != `redis-server --save ""` {
		t.Fatalf("command %q", c.Services["cache"].Command)
	}
	if c.Env["DATABASE_URL"] != "postgres://app:app@db:5432/app" || len(c.Env) != 1 {
		t.Fatalf("env %v", c.Env)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the suggestion is not a valid definition: %v", err)
	}

	// no dev container: the Dockerfile's first FROM, past its flags
	c, from = StarterFrom(files(map[string]string{"go.mod": "module x", "Dockerfile": "# build\nFROM --platform=linux/amd64 golang:1.27 AS build\nFROM alpine\n"}))
	if c.Image != "golang:1.27" || strings.Join(from, ",") != "Dockerfile" || c.Setup[0] != "go mod download" {
		t.Fatalf("%q %v %v", c.Image, from, c.Setup)
	}
	// a FROM that names a build argument is no image; nothing else read leaves the language starter as it was
	c, from = StarterFrom(files(map[string]string{"go.mod": "module x", "Dockerfile": "ARG V=1\nFROM golang:${V}\n"}))
	if c.Image != Starter(func(n string) bool { return n == "go.mod" }).Image || len(from) != 0 {
		t.Fatalf("%q %v", c.Image, from)
	}
	// files that do not parse are ignored
	c, from = StarterFrom(files(map[string]string{"compose.yaml": "services: [", ".devcontainer.json": "{not json"}))
	if c.Image != "debian:trixie" || len(from) != 0 || c.Services != nil {
		t.Fatalf("%+v %v", c, from)
	}
}

func TestJSONCKeepsStringsAndDropsCommentsAndTrailingCommas(t *testing.T) {
	got := jsonc("{\"a\": \"http://x // not a comment\", /* c */ \"b\": [1, 2,], // end\n}")
	if got != "{\"a\": \"http://x // not a comment\",  \"b\": [1, 2] \n}" {
		t.Fatalf("%q", got)
	}
	// a commented-out last property: the comma before it is a trailing comma, so the image is read
	var dc struct{ Image string }
	text := jsonc("{\"name\":\"Go\",\"image\":\"mcr.microsoft.com/devcontainers/go:1\",\n  // \"features\": {},\n  /* \"x\": 1 */\n}")
	if err := json.Unmarshal([]byte(text), &dc); err != nil || dc.Image != "mcr.microsoft.com/devcontainers/go:1" {
		t.Fatalf("%q: %v", text, err)
	}
	// strings keep escaped quotes, commas and brackets
	if got := jsonc(`{"a": "x\",]", "b": ",}" ,}`); got != `{"a": "x\",]", "b": ",}" }` {
		t.Fatalf("%q", got)
	}
}
