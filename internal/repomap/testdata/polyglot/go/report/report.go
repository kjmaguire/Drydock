package report

import (
	"fmt"

	"example.com/polyglot/go/worker"
)

// Summary describes progress.
func Summary(done, total int) string {
	return fmt.Sprintf("%.0f%%", worker.ReportProgress(done, total)*100)
}
