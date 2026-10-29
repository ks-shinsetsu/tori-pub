package stats

import "math"

// Answer codes used by the judge statistics (same values as fit.Answer).
const (
	AnsA   = 0
	AnsB   = 1
	AnsTie = 2
)

// NoiseCeiling is the leave-one-judge-out ceiling of decisions §7: answers[j][t] lists judge j's answers on
// triplet t (both orders; nil when absent). Judge j's label on t is the majority of its A/B answers (skipped on a
// tie or none); the panel label is the A/B majority of the other judges' answers (skipped on a tie). The ceiling is
// the mean over judges of P(label_j = panel label); perJudge gives each judge's rate and n the triplets counted.
func NoiseCeiling(answers [][][]int) (ceiling float64, perJudge []float64, n []int) {
	J := len(answers)
	perJudge = make([]float64, J)
	n = make([]int, J)
	if J < 2 {
		return math.NaN(), perJudge, n
	}
	T := 0
	for _, a := range answers {
		T = max(T, len(a))
	}
	label := func(as []int) int {
		a, b := 0, 0
		for _, v := range as {
			switch v {
			case AnsA:
				a++
			case AnsB:
				b++
			}
		}
		switch {
		case a > b:
			return AnsA
		case b > a:
			return AnsB
		}
		return -1
	}
	sum := 0.0
	cnt := 0
	for j := range J {
		hit := 0
		for t := range T {
			if t >= len(answers[j]) {
				continue
			}
			mine := label(answers[j][t])
			if mine < 0 {
				continue
			}
			var others []int
			for k := range J {
				if k != j && t < len(answers[k]) {
					others = append(others, answers[k][t]...)
				}
			}
			panel := label(others)
			if panel < 0 {
				continue
			}
			n[j]++
			if mine == panel {
				hit++
			}
		}
		if n[j] > 0 {
			perJudge[j] = float64(hit) / float64(n[j])
			sum += perJudge[j]
			cnt++
		} else {
			perJudge[j] = math.NaN()
		}
	}
	if cnt == 0 {
		return math.NaN(), perJudge, n
	}
	return sum / float64(cnt), perJudge, n
}

// CeilingRatios returns r0/ceiling and (r0 − 0.5)/(ceiling − 0.5) (decisions §7, descriptive).
func CeilingRatios(r0, ceiling float64) (ratio, excess float64) {
	return r0 / ceiling, (r0 - 0.5) / (ceiling - 0.5)
}

// GateResult is the T1 reversal gate of decisions §9.1 for one judge.
type GateResult struct {
	// R is the reversal rate: judgments that called the superset B closer, over all gate judgments (ties in the denominator only).
	R float64
	// KEff = R·n_T and NEff = n_T (the number of gate triplets) form the binomial reference (design effect 2).
	KEff  float64
	NEff  int
	Upper float64 // one-sided upper Clopper–Pearson bound at the gate confidence
	RMax  float64
	Fail  bool
	// Judgments is the number of gate judgments; Ties the number answered "same".
	Judgments, Ties, Reversals int
}

// Gate defaults of decisions §9.1.
const (
	GateRMax = 0.20
	GateConf = 0.90
)

// ReversalGate evaluates the T1 gate: reversals and ties counted over the judgments on the gate triplets (nT triplets);
// the judge fails iff the upper one-sided bound at confidence conf of r = reversals/judgments, computed with
// k_eff = r·nT and n_eff = nT, exceeds rMax (rMax ≤ 0 → 0.20, conf ≤ 0 → 0.90).
func ReversalGate(reversals, ties, judgments, nT int, rMax, conf float64) GateResult {
	if rMax <= 0 {
		rMax = GateRMax
	}
	if conf <= 0 {
		conf = GateConf
	}
	g := GateResult{RMax: rMax, Judgments: judgments, Ties: ties, Reversals: reversals, NEff: nT}
	if judgments == 0 || nT == 0 {
		g.R, g.Upper = math.NaN(), math.NaN()
		return g
	}
	g.R = float64(reversals) / float64(judgments)
	g.KEff = g.R * float64(nT)
	g.Upper = ClopperPearsonUpper(g.KEff, nT, conf)
	g.Fail = g.Upper > rMax
	return g
}

// GatePassProbability is the exact binomial probability that a judge with true reversal rate r passes the gate
// with nT gate triplets (used for the P2 check of §9.1).
func GatePassProbability(r float64, nT int, rMax, conf float64) float64 {
	if rMax <= 0 {
		rMax = GateRMax
	}
	if conf <= 0 {
		conf = GateConf
	}
	p := 0.0
	for k := 0; k <= nT; k++ {
		if ClopperPearsonUpper(float64(k), nT, conf) > rMax {
			break
		}
		p += binomPMF(k, nT, r)
	}
	return p
}

func binomPMF(k, n int, p float64) float64 {
	switch {
	case p <= 0:
		if k == 0 {
			return 1
		}
		return 0
	case p >= 1:
		if k == n {
			return 1
		}
		return 0
	}
	lc := lgamma(float64(n+1)) - lgamma(float64(k+1)) - lgamma(float64(n-k+1))
	return math.Exp(lc + float64(k)*math.Log(p) + float64(n-k)*math.Log(1-p))
}

func lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// OrderConsistency is the share of triplets (with two orders) on which a judge gave the same answer in both orders.
func OrderConsistency(pairs [][2]int) (rate float64, n int) {
	same := 0
	for _, p := range pairs {
		n++
		if p[0] == p[1] {
			same++
		}
	}
	if n == 0 {
		return math.NaN(), 0
	}
	return float64(same) / float64(n), n
}
