package acp

// BuildEnvForTest exposes buildEnv to the external test package.
func BuildEnvForTest(parent []string, scrub bool, extra ...[]string) []string {
	return buildEnv(parent, scrub, extra...)
}

// NewGuardForTest exposes the path guard to the external test package.
func NewGuardForTest(cwd string) (*GuardForTest, error) {
	g, err := newGuard(cwd)
	if err != nil {
		return nil, err
	}
	return &GuardForTest{g}, nil
}

// GuardForTest wraps guard for tests.
type GuardForTest struct{ g *guard }

func (t *GuardForTest) Close() { t.g.close() }
func (t *GuardForTest) Read(p string, line, limit *int) (string, error) {
	return t.g.readFile(p, line, limit)
}
func (t *GuardForTest) Write(p, c string) error      { return t.g.writeFile(p, c) }
func (t *GuardForTest) Dir(p string) (string, error) { return t.g.dir(p) }
