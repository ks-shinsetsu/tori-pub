package simulate

import (
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

func small(c Config) Config {
	c.Sources = 120
	return c
}

func TestGenerateDeterministic(t *testing.T) {
	_, js1, m1, err := Generate(small(Default()))
	if err != nil {
		t.Fatal(err)
	}
	_, js2, m2, err := Generate(small(Default()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(js1, js2) || !reflect.DeepEqual(m1, m2) {
		t.Fatal("Generate is not deterministic under a seed")
	}
}

func TestSourceSpecShape(t *testing.T) {
	w := &world{rng: newRng(3), base: map[[2]string]float64{}}
	for s := range 300 {
		si := w.sourceSpec(s)
		n := len(si.g.Props)
		if n < 8 || n > 20 {
			t.Fatalf("n_P = %d", n)
		}
		deg := map[string]int{}
		ca := 0
		for _, e := range si.g.Edges {
			deg[e.From]++
			deg[e.To]++
			if e.Kind == ged.CA {
				ca++
			}
		}
		iso := 0
		for _, p := range si.g.Props {
			if deg[p.ID] == 0 {
				iso++
			}
		}
		if iso < max(2, int(math.Round(0.12*float64(n)))) {
			t.Fatalf("source with %d isolated of %d", iso, n)
		}
		if ca == 0 {
			t.Fatal("source without CA")
		}
		perUtt := map[int]int{}
		for _, u := range si.utt {
			perUtt[u]++
			if perUtt[u] > 2 {
				t.Fatal("more than 2 propositions in an utterance")
			}
		}
	}
}

func TestMetaAndCounts(t *testing.T) {
	trips, js, metas, err := Generate(small(Default()))
	if err != nil {
		t.Fatal(err)
	}
	if len(trips) != 480 || len(js) != 480*6 {
		t.Fatalf("%d triplets, %d judgments", len(trips), len(js))
	}
	ident, diag, x := 0, 0, 0
	for _, m := range metas {
		if m.Info.T3Ident {
			ident++
			if m.Type != perturb.T3Contrast || m.Target != "" {
				t.Fatal("t3_ident outside T3 or Δ-targeted")
			}
		}
		if m.Info.T3Diag {
			diag++
			if m.Type != perturb.T3Contrast || m.Target != "" || !m.Info.DiagCAType || m.InFit() {
				t.Fatal("t3_diag outside T3, Δ-targeted, unflagged or in the fit")
			}
		}
		if !m.InFit() {
			x++
		}
		if m.Info.EditsA != len(m.EditsA) || m.Info.EditsB != len(m.EditsB) {
			t.Fatal("edit counts out of sync")
		}
		if m.Type == perturb.T1Monotone && m.T1Stratum == "" {
			t.Fatal("T1 without stratum")
		}
	}
	if ident == 0 || diag == 0 || x < diag {
		t.Fatalf("t3_ident %d, t3_diag %d, excluded %d", ident, diag, x)
	}
	c, err := Count(trips, metas, ged.Current())
	if err != nil {
		t.Fatal(err)
	}
	if c.FitSet != len(trips)-x || c.Cells[CellIdent] != ident || c.Cells[CellDiag] != diag || c.Cells[CellCooccur] != 0 {
		t.Fatalf("counts %+v", c.Cells)
	}
	for _, name := range []string{CellIMissing, CellIExtra, CellEMissing, CellEExtra, CellDirection, CellTypeRAMA, CellNeg, CellF09, CellF03} {
		if c.Cells[name] == 0 || c.AllCells[name] < c.Cells[name] {
			t.Errorf("cell %s: fit %d, all %d", name, c.Cells[name], c.AllCells[name])
		}
	}
	ft, fj, fm := Subset(trips, js, metas, Meta.InFit)
	if len(ft) != c.FitSet || len(fm) != c.FitSet || len(fj) != 6*c.FitSet {
		t.Fatalf("subset %d / %d / %d", len(ft), len(fj), len(fm))
	}
	for _, j := range fj {
		if j.Triplet >= len(ft) {
			t.Fatal("judgment index out of range")
		}
	}
}

func TestMisspecified(t *testing.T) {
	c := small(Misspecified())
	c.Random = 0
	_, js, metas, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	// with heterogeneity the true Δ differs from Δ at θ* for some triplets
	ref := small(Default())
	ref.Random = 0
	_, jr, mr, err := Generate(ref)
	if err != nil {
		t.Fatal(err)
	}
	diff := 0
	for i := range metas {
		if math.Abs(metas[i].TrueDelta-mr[i].TrueDelta) > 1e-9 {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("heterogeneity changed no true Δ")
	}
	if len(js) != len(jr) {
		t.Fatal("judgment counts differ")
	}
	var n [3]int
	for _, j := range js {
		n[j.Answer]++
	}
	if n[fit.AnswerA] == 0 || n[fit.AnswerB] == 0 || n[fit.AnswerTie] == 0 {
		t.Fatalf("answers %v", n)
	}
}

func newRng(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 0x5eed)) }
