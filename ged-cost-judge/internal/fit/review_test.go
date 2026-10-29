package fit

import (
	"math"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestDirectionHandCase pins the model's direction independently of the generator: Δ > 0 (A closer) must give
// P(A) > P(B), with the documented values σ(z − τ).
func TestDirectionHandCase(t *testing.T) {
	pa, pb, pt := Probs(1.0, 0.5)
	if !(pa > pb) || math.Abs(pa-sigmoid(0.5)) > 1e-12 || math.Abs(pb-sigmoid(-1.5)) > 1e-12 || math.Abs(pt-(1-pa-pb)) > 1e-12 {
		t.Fatalf("P = %v %v %v", pa, pb, pt)
	}
	ll, _, _ := answerLogLik(AnswerA, 1.0, 0.5)
	if math.Abs(ll-math.Log(sigmoid(0.5))) > 1e-12 {
		t.Fatalf("ll = %v", ll)
	}
	// A toy triplet: A = ref, B = ref minus one edge → Δ = +1 at θ₀ → P(A) > P(B) for a judge with β = 2.
	ref := ged.Graph{Props: []ged.Prop{{ID: "c"}, {ID: "p"}}, Edges: []ged.Edge{{From: "p", To: "c", Kind: ged.RA}}}
	b := ged.Graph{Props: ref.Props}
	id := func(x, y string) float64 {
		if x == y {
			return 1
		}
		return 0
	}
	d, err := Delta([]Triplet{{Ref: ref, A: ref, B: b, SimA: id, SimB: id}}, ged.Current())
	if err != nil || math.Abs(d[0]-1) > 1e-12 {
		t.Fatalf("Δ = %v (%v)", d, err)
	}
	// Simulated answers from that triplet favour A.
	rng := rand.New(rand.NewPCG(1, 1))
	p := Params{Theta: ged.Current(), Beta: []float64{2}, Tau: []float64{0.5}, Pi: []float64{0}}
	design := make([]Judgment, 400)
	for i := range design {
		design[i] = Judgment{Triplet: 0, Judge: 0, Order: float64(1 - 2*(i%2))}
	}
	sim, err := SimulateJudgments([]Triplet{{Ref: ref, A: ref, B: b, SimA: id, SimB: id}}, design, p, rng)
	if err != nil {
		t.Fatal(err)
	}
	a, bb := 0, 0
	for _, k := range sim {
		switch k.Answer {
		case AnswerA:
			a++
		case AnswerB:
			bb++
		}
	}
	if a <= 2*bb {
		t.Fatalf("A %d, B %d", a, bb)
	}
}

// TestProjLBFGSCorrelated: a correlated quadratic with two active bounds, started on a bound with an inward gradient.
func TestProjLBFGSCorrelated(t *testing.T) {
	// f = ½ xᵀ Q x − bᵀ x with Q = [[4,2,1],[2,3,1],[1,1,2]], b = (−3, 6, 1); box [0, 1]³.
	Q := [3][3]float64{{4, 2, 1}, {2, 3, 1}, {1, 1, 2}}
	bv := [3]float64{-3, 6, 1}
	f := func(x, g []float64) float64 {
		v := 0.0
		for i := range 3 {
			gi := -bv[i]
			for j := range 3 {
				v += 0.5 * x[i] * Q[i][j] * x[j]
				gi += Q[i][j] * x[j]
			}
			v -= bv[i] * x[i]
			if g != nil {
				g[i] = gi
			}
		}
		return v
	}
	r := projLBFGS(f, []float64{0, 0, 1}, []float64{0, 0, 0}, []float64{1, 1, 1}, boxOptions{})
	// Brute-force reference on a fine grid.
	best, bx := math.Inf(1), [3]float64{}
	for i := 0; i <= 200; i++ {
		for j := 0; j <= 200; j++ {
			for k := 0; k <= 200; k++ {
				x := []float64{float64(i) / 200, float64(j) / 200, float64(k) / 200}
				if v := f(x, nil); v < best {
					best, bx = v, [3]float64{x[0], x[1], x[2]}
				}
			}
		}
	}
	if r.F > best+1e-9 || math.Abs(r.X[0]-bx[0]) > 0.01 || math.Abs(r.X[1]-bx[1]) > 0.01 || math.Abs(r.X[2]-bx[2]) > 0.01 {
		t.Fatalf("x = %v f = %v; grid x = %v f = %v", r.X, r.F, bx, best)
	}
	// x0 is at the lower bound with an inward gradient (−b₀ > 0 → gradient 3 > 0 → stays at 0); x1 leaves the bound.
	if r.X[0] > 1e-9 || r.X[1] < 0.5 {
		t.Fatalf("active set wrong: %v", r.X)
	}
}

// TestAdaptiveQuadrature checks the per-triplet marginal log-likelihood (lapse mixture on) against a dense reference
// at σ = 1, 3 and 5 (review-fit-impl M3, N12): the adaptive trapezoid rule must be accurate where the plain 20-node
// Gauss–Hermite rule was off by 0.2–1.7 and a mode-centred Gauss–Hermite rule by up to 0.9 on contradictory triplets.
func TestAdaptiveQuadrature(t *testing.T) {
	for _, c := range []struct {
		sigma float64
		nodes int
		tol   float64
	}{{1, 20, 1e-5}, {3, 20, 1e-5}, {3, 40, 1e-6}, {5, 20, 1e-5}, {5, 40, 1e-6}} {
		sigma := c.sigma
		w := newWorld(41, altTheta(), sigma)
		trips, js := w.generate(25, false)
		p := Init(baseOpts(3))
		p.Theta = altTheta()
		p.Sigma = sigma
		for j := range 3 {
			p.Beta[j], p.Tau[j], p.Pi[j] = w.judges[j].beta, w.judges[j].tau, w.judges[j].pi
		}
		p.Lapse = []float64{0.05, 0.1, 0.02}
		ds, err := features(trips, p.Theta)
		if err != nil {
			t.Fatal(err)
		}
		opts := baseOpts(3)
		opts.Quadrature = c.nodes
		l := layout{j: 3}
		o := newObjective(l, ds, trips, js, opts, 0, true, p)
		// Per-triplet quadrature value vs. a 20000-point trapezoid over ±12σ.
		maxErr := 0.0
		for tt, ks := range o.byTrip {
			if len(ks) == 0 {
				continue
			}
			D := o.delta(tt, p)
			ref := math.Inf(-1)
			const n = 20000
			h := 24 * sigma / n
			vals := make([]float64, n+1)
			for i := 0; i <= n; i++ {
				u := -12*sigma + float64(i)*h
				s := -u*u/(2*sigma*sigma) - math.Log(sigma) - 0.5*math.Log(2*math.Pi)
				for _, ki := range ks {
					k := js[ki]
					ll, _, _, _ := mixLogLik(k.Answer, p.Beta[k.Judge]*D+p.Pi[k.Judge]*k.Order+u, p.Tau[k.Judge], p.Lapse[k.Judge])
					s += ll
				}
				vals[i] = s
			}
			ref = logSumExp(vals) + math.Log(h)
			// Quadrature value of this triplet alone.
			one := &objective{l: l, ds: ds, fold: o.fold, trips: trips, js: js, opts: opts, mixed: true, ghX: o.ghX, nodes: o.nodes, logc: o.logc}
			one.byTrip = make([][]int, len(trips))
			one.byTrip[tt] = ks
			got := -one.eval(l.pack(p, true), nil)
			maxErr = math.Max(maxErr, math.Abs(got-ref))
		}
		t.Logf("σ = %.0f, %d nodes: max per-triplet |error| = %.2e (plain 20-node rule: 0.2 at σ = 3, 1.5 at σ = 5)", sigma, c.nodes, maxErr)
		if maxErr > c.tol {
			t.Errorf("σ = %.0f, %d nodes: adaptive quadrature error %.2e > %.0e", sigma, c.nodes, maxErr, c.tol)
		}
	}
}

// TestFoldsKeepCopiesTogether (review-fit-impl M2): inside a bootstrap replicate, copies of one original source never
// straddle CV folds.
func TestFoldsKeepCopiesTogether(t *testing.T) {
	w := newWorld(42, altTheta(), 0)
	trips, js := w.generate(60, false)
	rng := rand.New(rand.NewPCG(3, 4))
	draw := drawSources(sourceStrata(trips), rng)
	bt, _, orig := subset(trips, js, draw)
	fold := assignFolds(bt, 5, rand.New(rand.NewPCG(5, 6)))
	foldOfOrig := map[int]int{}
	copies := map[int]map[int]bool{} // original source → distinct bootstrap cluster ids
	for i, tr := range bt {
		o := trips[orig[i]].Source
		if f, ok := foldOfOrig[o]; ok && f != fold[tr.Source] {
			t.Fatalf("original source %d in folds %d and %d", o, f, fold[tr.Source])
		}
		foldOfOrig[o] = fold[tr.Source]
		if copies[o] == nil {
			copies[o] = map[int]bool{}
		}
		copies[o][tr.Source] = true
	}
	dup := 0
	for _, c := range copies {
		if len(c) >= 2 {
			dup++
		}
	}
	if dup == 0 {
		t.Fatalf("no source drawn more than once; test is vacuous")
	}
	// Folds remain balanced across generators.
	cnt := map[[2]any]int{}
	for _, tr := range bt {
		cnt[[2]any{tr.Generator, fold[tr.Source]}]++
	}
	for _, g := range []string{"g1", "g2"} {
		for f := range 5 {
			if cnt[[2]any{g, f}] == 0 {
				t.Fatalf("generator %s absent from fold %d", g, f)
			}
		}
	}
}

// TestColdStartsAreCold (review-fit-impl M1): a cold replicate never lists the warm start.
func TestColdStartsAreCold(t *testing.T) {
	w := newWorld(43, altTheta(), 0)
	trips, js := w.generate(30, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	opts.Tie = TypeTie()
	res, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	br, err := BootstrapOpts(trips, js, opts, res.Params, BootOptions{Reps: 20, Seed: 9, ColdShare: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	sh := Summarise(br.Meta)
	if sh.Cold == 0 || sh.Cold == 20 {
		t.Fatalf("cold = %d", sh.Cold)
	}
	warm, cold := br.WarmCold(br.Theta, br.Meta, ged.PNodeMissing)
	if math.IsNaN(warm[0]) || math.IsNaN(cold[0]) {
		t.Fatalf("warm %v cold %v", warm, cold)
	}
}

// TestZeroColumnNotEstimable (review item 9): with every edge RA the type Δφ columns are identically zero; the untied
// confirmatory run labels them "not estimable" and, as members of U, the scale is not identified.
func TestZeroColumnNotEstimable(t *testing.T) {
	w := newWorld(44, altTheta(), 0)
	trips, js := w.generate(30, false)
	for i := range trips {
		for _, g := range []*ged.Graph{&trips[i].A, &trips[i].B, &trips[i].Ref} {
			for j := range g.Edges {
				g.Edges[j].Kind = ged.RA
			}
		}
	}
	n, _ := Informative(trips, ged.Current(), nil)
	if n[ged.PTypeRACA]+n[ged.PTypeRAMA]+n[ged.PTypeCAMA] != 0 {
		t.Fatalf("type columns not zero: %v", n)
	}
	opts := baseOpts(3)
	res, err := Confirmatory(trips, js, opts, ConfirmOptions{B: 12, Seed: 1, MinInformative: map[int]int{}, SkipD: true, SkipLOJO: true, SkipMixed: true, Untied: true, RandomStarts: -1})
	if err != nil {
		t.Fatal(err)
	}
	if res.ScaleIdentified {
		t.Fatal("scale identified with a frozen member of U")
	}
	for _, p := range res.Params {
		if p.Combined != stats.NotEstimable {
			t.Errorf("%s: %v", p.Name, p.Combined)
		}
	}
}

// TestRecoverySlow (review item 9, N12): σ = 1 with 10% lapses over several seeds; per-parameter mean standardized
// error of the margin-judged parameters below 0.5 and 90% coverage of the mixed (lapse-mixture) fit against the
// conditional truth. GCJ_SLOW=1; ≈ 5 min at GOMAXPROCS = 3.
func TestRecoverySlow(t *testing.T) {
	if os.Getenv("GCJ_SLOW") == "" {
		t.Skip("set GCJ_SLOW=1 to run (≈ 20 seeds × 2 fits × bootstrap)")
	}
	truth := altTheta()
	U := DefaultU
	truthN, _ := Normalize(truth[:], U)
	const seeds = 8
	zs := map[int][]float64{}
	covered, total := 0, 0
	for s := range seeds {
		w := newWorld(uint64(100+s), truth, 1)
		w.lapse = 0.10
		trips, js := w.generate(300, false)
		opts := baseOpts(3)
		opts.FitNeg = false
		opts.Tie = TypeTie()
		mix, err := FitMixedMulti(trips, js, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		br, err := BootstrapOpts(trips, js, opts, mix.Params, BootOptions{Reps: 60, Seed: uint64(s), Mixed: true, MixedInit: &mix.Params})
		if err != nil {
			t.Fatal(err)
		}
		estN, _ := NormalizeParams(mix.Params, U)
		rows, _ := NormalizeRows(br.MixedTheta, U)
		for _, p := range MarginJudged {
			col := Column(rows, p)
			sd := (Quantile(col, 0.8413) - Quantile(col, 0.1587)) / 2
			zs[p] = append(zs[p], (estN[p]-truthN[p])/sd)
			total++
			if truthN[p] >= Quantile(col, 0.05) && truthN[p] <= Quantile(col, 0.95) {
				covered++
			}
		}
	}
	cov := float64(covered) / float64(total)
	t.Logf("90%% coverage %.3f (n = %d)", cov, total)
	for _, p := range MarginJudged {
		mz := Mean(zs[p])
		t.Logf("%s: mean standardized error %+.3f", NormName(p), mz)
		if math.Abs(mz) > 0.5 {
			t.Errorf("%s: mean standardized error %.3f", NormName(p), mz)
		}
	}
	if cov < 0.80 {
		t.Errorf("coverage %.3f", cov)
	}
}
