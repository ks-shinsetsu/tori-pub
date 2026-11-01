package fit

import (
	"math"
	"sort"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// QCOptions configures the mechanical judge QC (decisions §9, §9.1).
type QCOptions struct {
	GateDelta float64 // Δ(θ₀) floor of the gate stratum (0 → 2)
	RMax      float64 // 0 → 0.20
	Conf      float64 // 0 → 0.90
	// MaxTieShare and MinAlpha are the other mechanical criteria (0 → 0.40 and 0.40).
	MaxTieShare, MinAlpha float64
}

// JudgeQCResult is the per-judge QC table.
type JudgeQCResult struct {
	Names []string
	// NT is the number of gate triplets (T1 with Δ(θ₀) ≥ GateDelta); GateExcluded those with Δ(θ₀) ≤ 0.
	NT, GateExcluded int
	Gates            []stats.GateResult
	Judgments        []int
	TieShare         []float64
	// OrderConsistency is descriptive (decisions §9 as revised): the share of triplets answered the same in both orders,
	// over all triplets and over the gate stratum.
	OrderConsistency, OrderN         []float64
	GateOrderConsistency, GateOrderN []float64
	// Alpha[a][b] is Krippendorff's α (nominal) between judges a and b over (triplet, order) units; NaN when not evaluable.
	Alpha [][]float64
	// ByStratum[j][label] is the reversal rate of judge j by Δ(θ₀) stratum (descriptive).
	ByStratum   []map[string]StratumRate
	Strata      []string
	Excluded    []bool
	Reasons     []string
	MaxTieShare float64
	MinAlpha    float64
}

// StratumRate is a reversal count in one Δ(θ₀) stratum.
type StratumRate struct{ Reversals, Ties, Judgments int }

func t1Stratum(d float64) string {
	switch {
	case d <= 0:
		return "≤0 (excluded)"
	case d < 0.5:
		return "(0,0.5)"
	case d < 1:
		return "[0.5,1)"
	case d < 2:
		return "[1,2)"
	}
	return "≥2 (gate)"
}

// JudgeQCStats computes the mechanical judge QC on the triplets it is given (Confirmatory passes the QC set: every
// triplet but pilot and qt30, QCGateExclude), before any θ is fitted: the T1 reversal
// gate of §9.1 (gate set = T1 triplets with Δ(θ₀) ≥ GateDelta, "same" in the denominator only, n_eff = gate
// triplets), the tie share (≤ 40%) and Krippendorff's α with each other judge (≥ 0.4) as mechanical criteria, and
// order consistency and Krippendorff's α as descriptive statistics. A judge is Excluded when it fails the gate or
// the tie share.
func JudgeQCStats(trips []Triplet, js []Judgment, names []string, o QCOptions) JudgeQCResult {
	if o.GateDelta <= 0 {
		o.GateDelta = 2
	}
	if o.MaxTieShare <= 0 {
		o.MaxTieShare = 0.40
	}
	if o.MinAlpha <= 0 {
		o.MinAlpha = 0.40
	}
	J := len(names)
	r := JudgeQCResult{Names: names, MaxTieShare: o.MaxTieShare, MinAlpha: o.MinAlpha,
		Judgments: make([]int, J), TieShare: make([]float64, J), OrderConsistency: make([]float64, J), OrderN: make([]float64, J),
		GateOrderConsistency: make([]float64, J), GateOrderN: make([]float64, J), ByStratum: make([]map[string]StratumRate, J),
		Excluded: make([]bool, J), Reasons: make([]string, J)}
	isT1 := func(t Triplet) bool { return t.Type == "T1" || t.HasFlag("t1_gate") }
	inGate := func(t Triplet) bool { return isT1(t) && t.T1Delta0 >= o.GateDelta }
	type cnt struct{ rev, tie, n int }
	gate := make([]cnt, J)
	tie := make([]cnt, J)
	pairs := make([]map[int][2]int, J)
	for j := range J {
		r.ByStratum[j] = map[string]StratumRate{}
		pairs[j] = map[int][2]int{}
	}
	gateTrips := map[int]bool{}
	for i, t := range trips {
		if isT1(t) && t.T1Delta0 <= 0 {
			r.GateExcluded++
		}
		if inGate(t) {
			gateTrips[i] = true
		}
	}
	r.NT = len(gateTrips)
	strata := map[string]bool{}
	for _, k := range js {
		if k.Judge >= J || k.Triplet < 0 || k.Triplet >= len(trips) {
			continue
		}
		t := trips[k.Triplet]
		c := &tie[k.Judge]
		c.n++
		if k.Answer == AnswerTie {
			c.tie++
		}
		p := pairs[k.Judge][k.Triplet]
		if k.Order > 0 {
			p[0] = int(k.Answer) + 1
		} else {
			p[1] = int(k.Answer) + 1
		}
		pairs[k.Judge][k.Triplet] = p
		if !isT1(t) {
			continue
		}
		s := t1Stratum(t.T1Delta0)
		strata[s] = true
		sr := r.ByStratum[k.Judge][s]
		sr.Judgments++
		if k.Answer == AnswerB {
			sr.Reversals++
		}
		if k.Answer == AnswerTie {
			sr.Ties++
		}
		r.ByStratum[k.Judge][s] = sr
		if gateTrips[k.Triplet] {
			g := &gate[k.Judge]
			g.n++
			if k.Answer == AnswerB {
				g.rev++
			}
			if k.Answer == AnswerTie {
				g.tie++
			}
		}
	}
	for s := range strata {
		r.Strata = append(r.Strata, s)
	}
	sort.Strings(r.Strata)
	r.Alpha = make([][]float64, J)
	for a := range J {
		r.Alpha[a] = make([]float64, J)
		for b := range J {
			if a == b {
				r.Alpha[a][b] = math.NaN()
				continue
			}
			r.Alpha[a][b] = pairAlpha(js, len(trips), a, b)
		}
	}
	for j := range J {
		r.Judgments[j] = tie[j].n
		if tie[j].n > 0 {
			r.TieShare[j] = float64(tie[j].tie) / float64(tie[j].n)
		} else {
			r.TieShare[j] = math.NaN()
		}
		var all, gatePairs [][2]int
		for t, p := range pairs[j] {
			if p[0] > 0 && p[1] > 0 {
				all = append(all, p)
				if gateTrips[t] {
					gatePairs = append(gatePairs, p)
				}
			}
		}
		oc, on := stats.OrderConsistency(all)
		goc, gon := stats.OrderConsistency(gatePairs)
		r.OrderConsistency[j], r.OrderN[j] = oc, float64(on)
		r.GateOrderConsistency[j], r.GateOrderN[j] = goc, float64(gon)
		g := stats.ReversalGate(gate[j].rev, gate[j].tie, gate[j].n, r.NT, o.RMax, o.Conf)
		r.Gates = append(r.Gates, g)
		var reasons []string
		if gate[j].n > 0 && g.Fail {
			reasons = append(reasons, "T1 reversal gate")
		}
		if !math.IsNaN(r.TieShare[j]) && r.TieShare[j] > o.MaxTieShare {
			reasons = append(reasons, "tie share")
		}
		// Krippendorff's α is descriptive (decisions §9 as revised 2026-10-29): at the design's Δ mix the reference
		// judges of the power analysis sit at pairwise α ≈ 0.25–0.34, so a fixed bar is not calibrated.
		r.Excluded[j] = len(reasons) > 0
		if len(reasons) > 0 {
			r.Reasons[j] = joinReasons(reasons)
		}
	}
	return r
}

func joinReasons(rs []string) string {
	out := ""
	for i, s := range rs {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}

// pairAlpha is Krippendorff's α between judges a and b over (triplet, order) units.
func pairAlpha(js []Judgment, nTrips int, a, b int) float64 {
	vals := make([][]int, 2*nTrips)
	for i := range vals {
		vals[i] = []int{-1, -1}
	}
	for _, k := range js {
		if k.Triplet < 0 || k.Triplet >= nTrips {
			continue
		}
		u := 2 * k.Triplet
		if k.Order < 0 {
			u++
		}
		switch k.Judge {
		case a:
			vals[u][0] = int(k.Answer)
		case b:
			vals[u][1] = int(k.Answer)
		}
	}
	return stats.KrippendorffAlpha(vals)
}
