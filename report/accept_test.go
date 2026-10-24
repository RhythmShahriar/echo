package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/compare"
)

func TestVerboseShowsChangeKeys(t *testing.T) {
	r := Result{Run: comparedRun(), Comparison: sampleComparison()}
	for _, plain := range []bool{true, false} {
		for _, verbose := range []bool{true, false} {
			var buf bytes.Buffer
			NewPretty(&buf, &buf, PrettyOptions{Plain: plain, Verbose: verbose}).Summary(r, "")
			for _, ch := range r.Comparison.Changes {
				want := "key: " + compare.Key(ch)
				if got := strings.Contains(buf.String(), want); got != verbose {
					t.Errorf("plain %v, verbose %v: %q shown = %v:\n%s", plain, verbose, want, got, buf.String())
				}
			}
		}
	}
}
