package stats

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat/distuv"
)

// Holm is the Holm step-down adjustment at level alpha. It returns the adjusted p-values (monotone, capped at 1)
// and the rejection indicator per hypothesis (Holm 1979).
func Holm(p []float64, alpha float64) (adjusted []float64, reject []bool) {
	m := len(p)
	adjusted = make([]float64, m)
	reject = make([]bool, m)
	if m == 0 {
		return adjusted, reject
	}
	order := make([]int, m)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return p[order[a]] < p[order[b]] })
	running := 0.0
	for rank, i := range order {
		v := math.Min(1, float64(m-rank)*p[i])
		running = math.Max(running, v)
		adjusted[i] = running
	}
	for i := range p {
		reject[i] = adjusted[i] <= alpha
	}
	return adjusted, reject
}

// ClopperPearsonUpper is the one-sided upper confidence bound at confidence conf (e.g. 0.90) for a binomial
// proportion with k successes in n trials; k may be non-integer (an effective count). k ≥ n gives 1.
func ClopperPearsonUpper(k float64, n int, conf float64) float64 {
	if n <= 0 {
		return math.NaN()
	}
	if k >= float64(n) {
		return 1
	}
	if k < 0 {
		k = 0
	}
	b := distuv.Beta{Alpha: k + 1, Beta: float64(n) - k}
	return b.Quantile(conf)
}

// ClopperPearsonLower is the one-sided lower bound at confidence conf; k ≤ 0 gives 0.
func ClopperPearsonLower(k float64, n int, conf float64) float64 {
	if n <= 0 {
		return math.NaN()
	}
	if k <= 0 {
		return 0
	}
	if k > float64(n) {
		k = float64(n)
	}
	b := distuv.Beta{Alpha: k, Beta: float64(n) - k + 1}
	return b.Quantile(1 - conf)
}

// ClopperPearson is the two-sided interval at confidence conf.
func ClopperPearson(k float64, n int, conf float64) Interval {
	a := (1 - conf) / 2
	return Interval{ClopperPearsonLower(k, n, 1-a), ClopperPearsonUpper(k, n, 1-a)}
}

// Side is the direction of a bootstrap contrast test.
type Side int

const (
	// Less: the alternative is contrast < null (supported when the one-sided upper 95% bound, q₀.₉₅, is below the null).
	Less Side = iota
	// Greater: the alternative is contrast > null (supported when q₀.₀₅ is above the null).
	Greater
	// TwoSided: supported when the 95% percentile interval excludes the null.
	TwoSided
)

// ContrastResult is a bootstrap contrast test with the null value stated (decisions §4).
type ContrastResult struct {
	Est  float64
	Null float64
	Side Side
	// P is the share of replicates on the wrong side of the null (doubled for two-sided tests, capped at 1).
	P float64
	// CI90 and CI95 are percentile intervals; Bound is the one-sided 95% bound used for Less/Greater.
	CI90, CI95 Interval
	Bound      float64
	Supported  bool
	Reps       int
}

// BootstrapContrast tests a contrast from its cluster-bootstrap replicates against a stated null value.
func BootstrapContrast(est float64, boot []float64, null float64, side Side) ContrastResult {
	col := cleanSorted(boot)
	r := ContrastResult{Est: est, Null: null, Side: side, Reps: len(col)}
	if len(col) == 0 {
		r.P = math.NaN()
		return r
	}
	ge, le := 0, 0
	for _, v := range col {
		if v >= null {
			ge++
		}
		if v <= null {
			le++
		}
	}
	n := float64(len(col))
	r.CI90 = Interval{Quantile(col, 0.05), Quantile(col, 0.95)}
	r.CI95 = Interval{Quantile(col, 0.025), Quantile(col, 0.975)}
	switch side {
	case Less:
		r.P = float64(ge) / n
		r.Bound = Quantile(col, 0.95)
		r.Supported = r.Bound < null
	case Greater:
		r.P = float64(le) / n
		r.Bound = Quantile(col, 0.05)
		r.Supported = r.Bound > null
	default:
		r.P = math.Min(1, 2*math.Min(float64(ge), float64(le))/n)
		r.Supported = r.CI95.Lo > null || r.CI95.Hi < null
	}
	return r
}

// McNemarExact is the exact (binomial) McNemar test on the discordant counts b (rule A right, rule B wrong) and
// c (A wrong, B right). oneSided tests "A better than B" (P[X ≥ b | n = b + c, ½]); otherwise the two-sided
// mid-less exact p (min(1, 2·min(P[X ≤ b], P[X ≥ b]))).
func McNemarExact(b, c int, oneSided bool) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	bin := distuv.Binomial{N: float64(n), P: 0.5}
	pGE := 1 - bin.CDF(float64(b-1))
	if b == 0 {
		pGE = 1
	}
	if oneSided {
		return pGE
	}
	pLE := bin.CDF(float64(b))
	return math.Min(1, 2*math.Min(pLE, pGE))
}

// ClusteredMcNemar is the cluster-bootstrap version of the C1/C4 comparison (decisions §4): the difference
// agree(A) − agree(B) over the eligible units, resampling clusters (stratified by stratum label) with
// replacement; one-sided (Greater) against null 0. rightA/rightB are per-unit correctness indicators;
// eligible marks units that enter; cluster and stratum give the resampling unit and its stratum per unit.
func ClusteredMcNemar(rightA, rightB, eligible []bool, cluster []int, stratum []string, reps int, seed uint64) ContrastResult {
	est, _ := agreeDiff(rightA, rightB, eligible, nil)
	byStratum := map[string][]int{}
	seen := map[int]bool{}
	for u, c := range cluster {
		if !eligible[u] || seen[c] {
			continue
		}
		seen[c] = true
		s := ""
		if stratum != nil {
			s = stratum[u]
		}
		byStratum[s] = append(byStratum[s], c)
	}
	names := make([]string, 0, len(byStratum))
	for s := range byStratum {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		slices.Sort(byStratum[s])
	}
	units := map[int][]int{}
	for u, c := range cluster {
		if eligible[u] {
			units[c] = append(units[c], u)
		}
	}
	boot := make([]float64, reps)
	for r := range reps {
		rng := rand.New(rand.NewPCG(seed, uint64(r)))
		var w []int // multiplicity per unit via drawn clusters
		w = make([]int, len(rightA))
		for _, s := range names {
			ids := byStratum[s]
			for range ids {
				c := ids[rng.IntN(len(ids))]
				for _, u := range units[c] {
					w[u]++
				}
			}
		}
		boot[r], _ = agreeDiff(rightA, rightB, eligible, w)
	}
	return BootstrapContrast(est, boot, 0, Greater)
}

// agreeDiff is 100·(mean rightA − mean rightB) over eligible units, with optional multiplicities.
func agreeDiff(rightA, rightB, eligible []bool, w []int) (float64, int) {
	a, b, n := 0.0, 0.0, 0
	for u := range rightA {
		if !eligible[u] {
			continue
		}
		m := 1
		if w != nil {
			m = w[u]
		}
		if m == 0 {
			continue
		}
		n += m
		if rightA[u] {
			a += float64(m)
		}
		if rightB[u] {
			b += float64(m)
		}
	}
	if n == 0 {
		return math.NaN(), 0
	}
	return 100 * (a - b) / float64(n), n
}

// DiscordantCounts returns b (A right, B wrong) and c (A wrong, B right) over the eligible units.
func DiscordantCounts(rightA, rightB, eligible []bool) (b, c int) {
	for u := range rightA {
		if !eligible[u] {
			continue
		}
		switch {
		case rightA[u] && !rightB[u]:
			b++
		case !rightA[u] && rightB[u]:
			c++
		}
	}
	return b, c
}

// KrippendorffAlpha is Krippendorff's α with the nominal metric. values[u][c] is coder c's category on unit u
// (−1 = missing). Units with fewer than two values are ignored (Krippendorff 2011).
func KrippendorffAlpha(values [][]int) float64 {
	cats := map[int]bool{}
	for _, row := range values {
		for _, v := range row {
			if v >= 0 {
				cats[v] = true
			}
		}
	}
	idx := map[int]int{}
	for c := range cats {
		idx[c] = len(idx)
	}
	k := len(idx)
	if k == 0 {
		return math.NaN()
	}
	o := make([][]float64, k)
	for i := range o {
		o[i] = make([]float64, k)
	}
	for _, row := range values {
		var vs []int
		for _, v := range row {
			if v >= 0 {
				vs = append(vs, idx[v])
			}
		}
		m := len(vs)
		if m < 2 {
			continue
		}
		for i := range vs {
			for j := range vs {
				if i != j {
					o[vs[i]][vs[j]] += 1 / float64(m-1)
				}
			}
		}
	}
	nc := make([]float64, k)
	n := 0.0
	for c := range k {
		for d := range k {
			nc[c] += o[c][d]
		}
		n += nc[c]
	}
	if n <= 1 {
		return math.NaN()
	}
	num, den := 0.0, 0.0
	for c := range k {
		for d := range k {
			if c != d {
				num += o[c][d]
				den += nc[c] * nc[d]
			}
		}
	}
	if den == 0 {
		return 1
	}
	return 1 - (n-1)*num/den
}

// WaldResult is a cluster-bootstrap Wald test of a contrast vector.
type WaldResult struct {
	W  float64
	DF int
	P  float64
}

// ClusterWald tests H₀: contrast = 0 with W = ĉᵀ Σ̂⁻¹ ĉ, where Σ̂ is the covariance of the bootstrap replicates
// boot[r][i] (decisions §9). A rank-deficient Σ̂ is inverted on its range (df = rank).
func ClusterWald(est []float64, boot [][]float64) WaldResult {
	d := len(est)
	rows := make([][]float64, 0, len(boot))
	for _, r := range boot {
		if len(r) == d {
			ok := true
			for _, v := range r {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					ok = false
				}
			}
			if ok {
				rows = append(rows, r)
			}
		}
	}
	B := len(rows)
	if d == 0 || B < d+2 {
		return WaldResult{W: math.NaN(), P: math.NaN()}
	}
	mu := make([]float64, d)
	for _, r := range rows {
		for i, v := range r {
			mu[i] += v / float64(B)
		}
	}
	S := mat.NewSymDense(d, nil)
	for _, r := range rows {
		for i := range d {
			for j := i; j < d; j++ {
				S.SetSym(i, j, S.At(i, j)+(r[i]-mu[i])*(r[j]-mu[j])/float64(B-1))
			}
		}
	}
	var es mat.EigenSym
	if !es.Factorize(S, true) {
		return WaldResult{W: math.NaN(), P: math.NaN()}
	}
	vals := es.Values(nil)
	var V mat.Dense
	es.VectorsTo(&V)
	maxV := 0.0
	for _, v := range vals {
		maxV = math.Max(maxV, v)
	}
	w, rank := 0.0, 0
	for k, v := range vals {
		if v <= 1e-10*maxV || v <= 0 {
			continue
		}
		rank++
		proj := 0.0
		for i := range d {
			proj += V.At(i, k) * est[i]
		}
		w += proj * proj / v
	}
	if rank == 0 {
		return WaldResult{W: 0, DF: 0, P: 1}
	}
	chi := distuv.ChiSquared{K: float64(rank)}
	return WaldResult{W: w, DF: rank, P: 1 - chi.CDF(w)}
}
