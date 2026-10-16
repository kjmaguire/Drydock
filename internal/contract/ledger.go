package contract

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Spend is one ledger line: what one paid run of a contract test cost, as the CLI reported it.
type Spend struct {
	Time     string  `json:"time"`
	Test     string  `json:"test"` // t.Name(): Test or Test/Subtest
	Contract string  `json:"contract,omitempty"`
	USD      float64 `json:"usd"`
}

// AppendSpend adds one line to the ledger file. Each line is written with a single append, so test processes running at the
// same time do not interleave.
func AppendSpend(path string, s Spend) error {
	if path == "" {
		return nil
	}
	if s.Time == "" {
		s.Time = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	return errors.Join(werr, f.Close())
}

// ReadLedger returns every line of the ledger (none when the file does not exist yet). A line that does not parse is an error:
// the budget must never be computed from a ledger that is partly unreadable.
func ReadLedger(path string) ([]Spend, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Spend
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s Spend
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		out = append(out, s)
	}
	return out, sc.Err()
}

// LedgerTotal adds up the ledger.
func LedgerTotal(path string) (float64, error) {
	ss, err := ReadLedger(path)
	var total float64
	for _, s := range ss {
		total += s.USD
	}
	return total, err
}

// SpendByTest adds up the ledger per test name; a subtest's spend also counts for its parent.
func SpendByTest(ss []Spend) map[string]float64 {
	out := map[string]float64{}
	for _, s := range ss {
		out[s.Test] += s.USD
		if parent, _, ok := strings.Cut(s.Test, "/"); ok {
			out[parent] += s.USD
		}
	}
	return out
}

// Budget reads the cap from the environment: ok is false when no budget is set (no cap).
func Budget() (usd float64, ok bool, err error) {
	v := strings.TrimSpace(os.Getenv(EnvBudget))
	if v == "" {
		return 0, false, nil
	}
	usd, err = strconv.ParseFloat(v, 64)
	if err != nil || usd < 0 {
		return 0, false, fmt.Errorf("%s=%q is not an amount in dollars", EnvBudget, v)
	}
	return usd, true, nil
}
