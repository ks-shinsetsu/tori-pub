package fit

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
)

// TransferResult is the transfer slope κ of decisions §7 with its cluster-bootstrap replicates.
type TransferResult struct {
	Kappa float64
	Boot  []float64
	Beta  []float64
	Tau   []float64
	Pi    []float64
	// NegLogLik at the estimate.
	NegLogLik float64
	Converged bool
}

// transferObjective is the Rao–Kupper likelihood in z = β_j·(Δ0 + κ·(Δ2 − Δ0)) + π_j·o with κ free.
// Variable layout: κ, log β[J], log τ[J], π[J].
type transferObjective struct {
	d0, d2 []float64
	js     []Judgment
	j      int
	ridge  float64
}

func (o *transferObjective) eval(x, g []float64) float64 {
	if g != nil {
		clear(g)
	}
	J := o.j
	kappa := x[0]
	f := 0.0
	for _, k := range o.js {
		j := k.Judge
		beta, tau, pi := math.Exp(x[1+j]), math.Exp(x[1+J+j]), x[1+2*J+j]
		dd := o.d2[k.Triplet] - o.d0[k.Triplet]
		D := o.d0[k.Triplet] + kappa*dd
		z := beta*D + pi*k.Order
		ll, dz, dtau := answerLogLik(k.Answer, z, tau)
		f -= ll
		if g != nil {
			g[0] -= dz * beta * dd
			g[1+j] -= dz * D * beta
			g[1+J+j] -= dtau * tau
			g[1+2*J+j] -= dz * k.Order
		}
	}
	for i := 1; i < len(x); i++ {
		f += o.ridge * x[i] * x[i]
		if g != nil {
			g[i] += 2 * o.ridge * x[i]
		}
	}
	return f
}

func fitTransfer(d0, d2 []float64, js []Judgment, judges int, ridge float64) (kappa float64, x []float64, f float64, conv bool) {
	o := &transferObjective{d0: d0, d2: d2, js: js, j: judges, ridge: ridge}
	n := 1 + 3*judges
	x0 := make([]float64, n)
	lo, hi := make([]float64, n), make([]float64, n)
	lo[0], hi[0] = -5, 5
	for j := range judges {
		x0[1+judges+j] = math.Log(0.5)
		lo[1+j], hi[1+j] = logBetaMin, logBetaMax
		lo[1+judges+j], hi[1+judges+j] = logTauMin, logTauMax
		lo[1+2*judges+j], hi[1+2*judges+j] = -piBound, piBound
	}
	r := projLBFGS(o.eval, x0, lo, hi, boxOptions{})
	return r.X[0], r.X, r.F, r.Converged
}

// TransferSlope fits κ in z = β_j·(Δ_R0 + κ·(Δ_R2 − Δ_R0)) + π_j·o − τ_j on transfer judgments (QT30), with a
// cluster bootstrap over cluster[t] (the nodeset of triplet t). κ = 1: the synthetic calibration transfers unchanged;
// κ = 0: R0 is as good on real transcripts (decisions §7, descriptive). ridge ≤ 0 means 1e-4 on the auxiliary parameters.
func TransferSlope(d0, d2 []float64, js []Judgment, judges int, cluster []int, reps int, seed uint64, ridge float64) (TransferResult, error) {
	if len(d0) != len(d2) || len(cluster) != len(d0) {
		return TransferResult{}, errors.New("transfer: d0, d2 and cluster must have the same length")
	}
	if judges <= 0 || len(js) == 0 {
		return TransferResult{}, errors.New("transfer: need judges and judgments")
	}
	if ridge <= 0 {
		ridge = 1e-4
	}
	kappa, x, f, conv := fitTransfer(d0, d2, js, judges, ridge)
	res := TransferResult{Kappa: kappa, NegLogLik: f, Converged: conv,
		Beta: make([]float64, judges), Tau: make([]float64, judges), Pi: make([]float64, judges)}
	for j := range judges {
		res.Beta[j] = math.Exp(x[1+j])
		res.Tau[j] = math.Exp(x[1+judges+j])
		res.Pi[j] = x[1+2*judges+j]
	}
	if reps <= 0 {
		return res, nil
	}
	ids := map[int]bool{}
	for _, c := range cluster {
		ids[c] = true
	}
	cl := make([]int, 0, len(ids))
	for c := range ids {
		cl = append(cl, c)
	}
	slices.Sort(cl)
	byCluster := map[int][]int{}
	for t, c := range cluster {
		byCluster[c] = append(byCluster[c], t)
	}
	byTrip := make([][]Judgment, len(d0))
	for _, k := range js {
		byTrip[k.Triplet] = append(byTrip[k.Triplet], k)
	}
	res.Boot = make([]float64, reps)
	for r := range reps {
		rng := rand.New(rand.NewPCG(seed, uint64(r)))
		var bd0, bd2 []float64
		var bjs []Judgment
		for range cl {
			c := cl[rng.IntN(len(cl))]
			for _, t := range byCluster[c] {
				idx := len(bd0)
				bd0 = append(bd0, d0[t])
				bd2 = append(bd2, d2[t])
				for _, k := range byTrip[t] {
					k.Triplet = idx
					bjs = append(bjs, k)
				}
			}
		}
		k, _, _, _ := fitTransfer(bd0, bd2, bjs, judges, ridge)
		res.Boot[r] = k
	}
	return res, nil
}
