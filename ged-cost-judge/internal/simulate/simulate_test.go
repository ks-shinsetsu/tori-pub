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

// TestFitSetMatchesFilter: fit.Filter with the default options keeps exactly the triplets with Meta.InFit, so the
// counts of §15.3 and the fit are computed on the same set; the triplet fields mirror the metadata.
func TestFitSetMatchesFilter(t *testing.T) {
	trips, js, metas, err := Generate(small(Default()))
	if err != nil {
		t.Fatal(err)
	}
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), Tie: fit.TypeTie()}
	ft, fj, kept := fit.Filter(trips, js, opts)
	var want []int
	for i, m := range metas {
		if m.InFit() {
			want = append(want, i)
		}
		if m.Info.DelAddCooccur {
			t.Fatal("del_add_cooccur produced (the default fit filter would keep it)")
		}
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("fit.Filter keeps %d triplets, Meta.InFit %d", len(kept), len(want))
	}
	st, sj, _ := Subset(trips, js, metas, Meta.InFit)
	if len(st) != len(ft) || !reflect.DeepEqual(sj, fj) {
		t.Fatal("Subset(Meta.InFit) and fit.Filter disagree on the judgments")
	}
	gens := map[string]map[int]bool{}
	for i, tr := range trips {
		m := metas[i]
		if tr.Type != string(m.Type) || tr.Generator != m.Generator || tr.Generator != GeneratorOf(tr.Source) {
			t.Fatalf("triplet %d: type %q / %q, generator %q / %q", i, tr.Type, m.Type, tr.Generator, m.Generator)
		}
		if !reflect.DeepEqual(tr.Flags, m.Flags()) {
			t.Fatalf("triplet %d: flags %v, meta %v", i, tr.Flags, m.Flags())
		}
		if tr.HasFlag("x_spk") != m.Info.XSpk || tr.HasFlag("diag_ca_type") != m.Info.DiagCAType ||
			tr.HasFlag("t3_ident") != m.Info.T3Ident || tr.HasFlag("t3_diag") != m.Info.T3Diag {
			t.Fatalf("triplet %d: flags %v out of sync with %+v", i, tr.Flags, m.Info)
		}
		if m.Type == perturb.T1Monotone {
			if tr.T1Delta0 != m.Delta0 {
				t.Fatalf("T1 triplet %d: T1Delta0 %v, Delta0 %v", i, tr.T1Delta0, m.Delta0)
			}
			if tr.HasFlag("t1_gate") != (m.Delta0 >= 2) {
				t.Fatalf("T1 triplet %d: gate flag %v at Δ(θ₀) = %.3f", i, tr.HasFlag("t1_gate"), m.Delta0)
			}
		} else if tr.T1Delta0 != 0 || tr.HasFlag("t1_gate") {
			t.Fatalf("non-T1 triplet %d with T1 fields", i)
		}
		if tr.Weight != 0 {
			t.Fatal("weight set")
		}
		if gens[tr.Generator] == nil {
			gens[tr.Generator] = map[int]bool{}
		}
		gens[tr.Generator][tr.Source] = true
	}
	if len(gens) != 2 || len(gens["gen-a"]) != len(gens["gen-b"]) {
		t.Fatalf("generator strata unbalanced: %d / %d sources", len(gens["gen-a"]), len(gens["gen-b"]))
	}
	for _, j := range js {
		if j.Family != JudgeFamily(j.Judge) {
			t.Fatalf("judgment family %q for judge %d", j.Family, j.Judge)
		}
	}
}

// TestWorlds pins the true costs of the five A.5 worlds (decisions-stats Appendix A.5 and preamble) and their position
// on the sum-normalized decision scale.
func TestWorlds(t *testing.T) {
	th0 := ged.Current()
	const m = 0.25
	type want struct {
		anchored map[int]float64 // differences from θ₀ elsewhere are errors
		sHat     float64
		// normalized values of the six margin-judged parameters, in fit.MarginJudged order
		norm [6]float64
	}
	wants := map[string]want{
		WorldNull: {map[int]float64{}, 1, [6]float64{1, 1, 1, 1, 0.75, 1}},
		WorldEdgeN: {map[int]float64{
			ged.PNodeMissing: 5.0 / 3, ged.PNodeExtra: 4.0 / 3, ged.PSub09: 0.4 / 3, ged.PSub07: 0.8 / 3, ged.PSub05: 0.8 / 3,
			ged.PSub03: 0.8 / 3, ged.PEdgeMissing: 4.0 / 3, ged.PTypeRACA: 1, ged.PTypeRAMA: 1, ged.PTypeCAMA: 1, ged.PDirection: 4.0 / 3,
		}, 4.0 / 3, [6]float64{1.25, 1, 1, 0.75, 0.75, 1}},
		WorldAlt: {map[int]float64{
			ged.PNodeMissing: 1.3, ged.PNodeExtra: 0.7, ged.PEdgeMissing: 1.2, ged.PTypeRAMA: 1.05, ged.PTypeRACA: 1.5, ged.PDirection: 1.2,
		}, 6.45 / 5.75, [6]float64{1.3 / 1.1217391, 0.7 / 1.1217391, 1.2 / 1.1217391, 1 / 1.1217391, 1.05 / 1.1217391, 1.2 / 1.1217391}},
		WorldSingle: {map[int]float64{ged.PNodeMissing: 1.3}, 6.05 / 5.75, [6]float64{1.3 / 1.0521739, 1 / 1.0521739, 1 / 1.0521739, 1 / 1.0521739, 0.75 / 1.0521739, 1 / 1.0521739}},
		WorldMild: {map[int]float64{
			ged.PNodeMissing: 1.15, ged.PNodeExtra: 1.15, ged.PEdgeMissing: 1.15, ged.PTypeRACA: 0.9, ged.PTypeRAMA: 0.9, ged.PTypeCAMA: 0.9, ged.PDirection: 1.15,
		}, 6.5 / 5.75, [6]float64{1.15 / 1.1304348, 1.15 / 1.1304348, 1.15 / 1.1304348, 1 / 1.1304348, 0.9 / 1.1304348, 1.15 / 1.1304348}},
	}
	ws := Worlds()
	if len(ws) != len(wants) {
		t.Fatalf("%d worlds", len(ws))
	}
	for _, w := range ws {
		wt, ok := wants[w.Name]
		if !ok {
			t.Fatalf("unexpected world %q", w.Name)
		}
		if w.TrueNeg != TrueNegDefault {
			t.Errorf("%s: θ_neg %v", w.Name, w.TrueNeg)
		}
		for p := range ged.NumParams {
			v, moved := wt.anchored[p]
			if !moved {
				v = th0[p]
			}
			if math.Abs(w.True[p]-v) > 1e-9 {
				t.Errorf("%s: %s = %v, want %v", w.Name, ged.ParamNames[p], w.True[p], v)
			}
		}
		norm, s := fit.Normalize(w.True[:], nil)
		if math.Abs(s-wt.sHat) > 1e-6 {
			t.Errorf("%s: ŝ_true %v, want %v", w.Name, s, wt.sHat)
		}
		for k, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, fit.NormEdgeExtra, ged.PTypeRAMA, ged.PDirection} {
			if math.Abs(norm[p]-wt.norm[k]) > 1e-6 {
				t.Errorf("%s: normalized %s = %.4f, want %.4f", w.Name, fit.NormName(p), norm[p], wt.norm[k])
			}
		}
		// band membership on the normalized scale, as documented in the report header
		th0n := fit.Theta0Extended(th0)
		var out []string
		for _, p := range fit.MarginJudged {
			if math.Abs(norm[p]-th0n[p]) > m+1e-9 {
				out = append(out, fit.NormName(p))
			}
		}
		wantOut := map[string]int{WorldNull: 0, WorldEdgeN: 0, WorldAlt: 1, WorldSingle: 0, WorldMild: 0}[w.Name]
		if len(out) != wantOut {
			t.Errorf("%s: outside the band on the normalized scale: %v", w.Name, out)
		}
		got, err := WorldByName(w.Name)
		if err != nil || got.True != w.True {
			t.Errorf("WorldByName(%q)", w.Name)
		}
	}
	if Default().True != withValues(th0, wants[WorldAlt].anchored) || Default().World != WorldAlt {
		t.Error("Default() is not alt′")
	}
	if _, err := WorldByName("nope"); err == nil {
		t.Error("unknown world accepted")
	}
}

func withValues(th ged.Theta, vals map[int]float64) ged.Theta {
	for p, v := range vals {
		th[p] = v
	}
	return th
}
