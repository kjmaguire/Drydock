//go:build tools

// Package tools pins dependencies that are approved in docs/decisions.md but not yet imported by product code, so
// `go mod tidy` keeps them in go.mod at the decided versions.
package tools

import (
	_ "cel.dev/cel-go/cel"
	_ "github.com/anthropics/anthropic-sdk-go"
	_ "github.com/aymanbagabas/go-pty"
	_ "github.com/charmbracelet/x/ansi"
	_ "github.com/coder/acp-go-sdk"
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "github.com/openai/openai-go/v3"
	_ "github.com/zalando/go-keyring"
	_ "github.com/zricethezav/gitleaks/v8/detect"
	_ "gopkg.in/yaml.v3"
	_ "mvdan.cc/sh/v3/syntax"
)
