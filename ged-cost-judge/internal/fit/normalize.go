package fit

import (
	"math"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// Indices of the normalized vector: 0..10 are the costs, NormEdgeExtra is the (normalized) edge-extra cost,
// NormNeg is θ_neg (present when the input vector carries it).
const (
	NormEdgeExtra = ged.NumParams
	NormNeg       = ged.NumParams + 1
	NormLen       = ged.NumParams + 2
)

// DefaultU is the normalization set U of decisions §2: the six unit-type costs
// {I-missing, I-extra, edge-missing, edge-extra, direction, θ_type (RA↔MA)}.
var DefaultU = []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, NormEdgeExtra, ged.PDirection, ged.PTypeRAMA}

// MarginJudged lists the six margin-judged parameters of decisions §3 (indices into the normalized vector).
var MarginJudged = []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, NormEdgeExtra, ged.PTypeRAMA, ged.PDirection}

// NormName is the display name of a normalized-vector index.
func NormName(i int) string {
	switch {
	case i < ged.NumParams:
		return ged.ParamNames[i]
	case i == NormEdgeExtra:
		return "辺の余分"
	case i == NormNeg:
		return "θ_neg（極性反転）"
	}
	return "?"
}

// Theta0Extended returns θ₀ as a normalized-layout vector: the 11 costs, edge-extra = 1, θ_neg = 0.
func Theta0Extended(th0 ged.Theta) []float64 {
	out := make([]float64, NormLen)
	copy(out, th0[:])
	out[NormEdgeExtra] = ged.EdgeExtraCost
	return out
}

// Normalize maps an anchored θ vector (11 costs, optionally followed by θ_neg) to the sum-normalized scale of
// decisions §2 against the current table ged.Current(): ŝ = Σ_{p∈U} θ̂_p / Σ_{p∈U} θ₀,p with θ̂_edge-extra = 1,
// θ̃_p = θ̂_p / ŝ for every entry and θ̃_edge-extra = 1/ŝ. The output has one more entry than the input
// (the normalized edge-extra at index NormEdgeExtra; θ_neg, if present, moves to NormNeg). U nil means DefaultU.
func Normalize(theta []float64, U []int) (thetaTilde []float64, sHat float64) {
	return NormalizeTo(theta, U, ged.Current())
}

// NormalizeTo is Normalize against an explicit reference table.
func NormalizeTo(theta []float64, U []int, theta0 ged.Theta) ([]float64, float64) {
	if U == nil {
		U = DefaultU
	}
	n := len(theta)
	if n < ged.NumParams {
		return nil, math.NaN()
	}
	ext := make([]float64, n+1)
	copy(ext[:ged.NumParams], theta[:ged.NumParams])
	ext[NormEdgeExtra] = ged.EdgeExtraCost
	if n > ged.NumParams {
		copy(ext[NormNeg:], theta[ged.NumParams:])
	}
	ref := Theta0Extended(theta0)
	num, den := 0.0, 0.0
	for _, p := range U {
		if p < 0 || p > NormEdgeExtra {
			continue
		}
		num += ext[p]
		den += ref[p]
	}
	s := num / den
	out := make([]float64, len(ext))
	if !(s > 0) || math.IsInf(s, 0) {
		for i := range out {
			out[i] = math.NaN()
		}
		return out, math.NaN()
	}
	for i, v := range ext {
		out[i] = v / s
	}
	return out, s
}

// NormalizeRows applies Normalize to every replicate row; it returns the normalized rows and ŝ per row.
func NormalizeRows(rows [][]float64, U []int) ([][]float64, []float64) {
	out := make([][]float64, len(rows))
	s := make([]float64, len(rows))
	for i, r := range rows {
		if r == nil {
			s[i] = math.NaN()
			continue
		}
		out[i], s[i] = Normalize(r, U)
	}
	return out, s
}

// NormalizeParams normalizes the anchored estimate p (θ and θ_neg) and returns the 13-vector and ŝ.
func NormalizeParams(p Params, U []int) ([]float64, float64) {
	return Normalize(ThetaVec(p), U)
}
