package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cel.dev/cel-go/cel"
	"github.com/charmbracelet/x/ansi"
	"github.com/zricethezav/gitleaks/v8/detect"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	// gitleaks
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		panic(err)
	}
	fs := d.DetectString("aws_access_key_id = AKIA" + "IOSFODNN7ABCDEFG\n" + "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYzxk3Fv8Qp9\n")
	for _, f := range fs {
		fmt.Println("gitleaks finding:", f.RuleID, "line", f.StartLine)
	}
	fmt.Println("gitleaks findings:", len(fs), "rules loaded:", len(d.Config.Rules))
	// cel
	env, _ := cel.NewEnv(cel.Variable("tool", cel.StringType), cel.Variable("path", cel.StringType))
	ast, iss := env.Compile(`tool == "write_file" && path.startsWith("/etc") ? "deny" : "allow"`)
	if iss.Err() != nil {
		panic(iss.Err())
	}
	prg, _ := env.Program(ast)
	out, _, err := prg.Eval(map[string]any{"tool": "write_file", "path": "/etc/passwd"})
	fmt.Println("cel:", out, err)
	// sh
	f, err := syntax.NewParser().Parse(strings.NewReader(`echo hi | wc -c; rm -rf "$HOME"/x && git push --force`), "")
	if err != nil {
		panic(err)
	}
	var cmds []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			cmds = append(cmds, c.Args[0].Lit())
		}
		return true
	})
	fmt.Println("sh parsed commands:", cmds)
	r, _ := interp.New(interp.StdIO(nil, os.Stdout, os.Stderr))
	f2, _ := syntax.NewParser().Parse(strings.NewReader(`echo from-interp; echo $((1+2))`), "")
	fmt.Println("interp err:", r.Run(context.Background(), f2))
	// ansi
	fmt.Printf("ansi.Strip: %q  width: %d\n", ansi.Strip("\x1b[31mred\x1b[0m text"), ansi.StringWidth("\x1b[31mred\x1b[0m"))
}
