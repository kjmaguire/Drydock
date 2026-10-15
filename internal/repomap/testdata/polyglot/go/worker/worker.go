package worker

import "example.com/polyglot/go/store"

// RunWorker copies keys between stores.
func RunWorker(from, to *store.Store, keys []string) {
	for _, k := range keys {
		to.SaveValue(k, from.LookupValue(k))
	}
}

// ReportProgress is only used by the reporting package.
func ReportProgress(done, total int) float64 {
	return float64(done) / float64(total)
}
