package synth

import (
	"os"
	"testing"
)

// On the real aif-with-llm data (skipped when it is not checked out next to this module), the
// import must find the §1.4 eligible set; its size is logged (expected ≈ 40–60).
func TestImportQT30Real(t *testing.T) {
	dir := os.Getenv("GCJ_AIFDB") // e.g. the main checkout's ../aif-with-llm/data/aifdb
	if dir == "" {
		dir = "../../../aif-with-llm/data/aifdb"
	}
	srcs, f, err := ImportQT30(dir)
	if err != nil || f.Files == 0 {
		t.Skip("aif-with-llm data not available")
	}
	t.Logf("filter %+v", f)
	unanchored := 0
	for _, s := range srcs {
		unanchored += s.Unanchored
		if st := Stats(s.Discussion); st.Props < 8 || st.Props > 20 || st.Edges < 4 {
			t.Errorf("%s: %+v", s.NodesetID, st)
		}
	}
	t.Logf("unanchored I-nodes: %d", unanchored)
}
