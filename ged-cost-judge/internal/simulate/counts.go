package simulate

import (
	"math"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// CellSpec is a per-cell count with its design expectation (dataset-spec §2.3, 1–4 edits) and working minimum
// (decisions-stats §15.3, which governs), both at 3,000 triplets. Minimum 0 means no minimum.
type CellSpec struct {
	Name     string
	Expected float64
	Minimum  float64
	// Scope is "fit" (counted over the triplets that enter the fit), "all" (every triplet) or "draws" (edit draws).
	Scope string
	Note  string
}

// Cell names.
const (
	CellIMissing   = "I-missing"
	CellIExtra     = "I-extra"
	CellEMissing   = "edge-missing"
	CellEExtra     = "edge-extra"
	CellDirection  = "direction"
	CellTypeRAMA   = "type RA↔MA"
	CellRAtoMA     = "  RA→MA draws"
	CellMAtoRA     = "  MA→RA draws"
	CellNeg        = "θ_neg (NegDiff ≠ 0)"
	CellF09        = "f₀.₉ interval"
	CellF07        = "f₀.₇ interval"
	CellF05        = "f₀.₅ interval"
	CellF03        = "f₀.₃ interval"
	CellPDelOnly   = "P-del only (ΔI-missing ≠ 0, Δedge-missing = 0)"
	CellRDelOnly   = "R-del only (Δedge-missing ≠ 0, ΔI-missing = 0)"
	CellPDelIdent  = "P-del only or t3_ident (dataset-spec §2.3 counting)"
	CellRDelIdent  = "R-del only or t3_ident (dataset-spec §2.3 counting)"
	CellIdent      = "t3_ident triplets"
	CellDiagInf    = "diag CA informative (ΔRA↔CA or ΔCA↔MA ≠ 0)"
	CellDiagRACA   = "  diag RA↔CA pair draws"
	CellDiagCAMA   = "  diag CA↔MA pair draws"
	CellT1Low      = "T1 Δ(θ₀) ∈ [0.5, 1)"
	CellT1Mid      = "T1 Δ(θ₀) ∈ [1, 2)"
	CellT1High     = "T1 Δ(θ₀) ≥ 2 (gate set)"
	CellT1NonPos   = "T1 realized Δ(θ₀) ≤ 0 (excluded from gate)"
	CellCooccur    = "del_add_cooccur"
	CellExcluded   = "excluded from fit (x_spk or diag_ca_type)"
	feasibleAt50   = "draws; ≥ 50 feasible per pair wanted ⇒ ≈ 100 draws at 50% infeasible"
	ident210       = "10% of T3"
	pdelOnlyNote   = "§2.3's 340 counts t3_ident (210) as P-del-only, but t3_ident's edge side has an R-del, so Δedge-missing ≠ 0 there"
	strictFormNote = "strict Δφ definition (decisions-stats §15 item 4)"
)

// Cells are the cells reported by the P1 re-run, in order.
var Cells = []CellSpec{
	{CellIMissing, 800, 650, "fit", ""},
	{CellIExtra, 670, 650, "fit", "thin (§2.3 margin 3%)"},
	{CellEMissing, 1250, 650, "fit", ""},
	{CellEExtra, 720, 650, "fit", "thin (margin 11%)"},
	{CellDirection, 760, 650, "fit", ""},
	{CellTypeRAMA, 740, 450, "fit", "§15.3 governs (450; §2.3 says 400)"},
	{CellRAtoMA, 560, 0, "draws", ""},
	{CellMAtoRA, 560, 0, "draws", ""},
	{CellNeg, 305, 300, "fit", "thin (margin 2%)"},
	{CellF09, 1350, 600, "fit", ""},
	{CellF07, 1300, 600, "fit", ""},
	{CellF05, 700, 600, "fit", "thin (margin ≈ 15%)"},
	{CellF03, 800, 600, "fit", ""},
	{CellPDelOnly, 340, 300, "fit", strictFormNote + "; " + pdelOnlyNote},
	{CellRDelOnly, 660, 300, "fit", strictFormNote},
	{CellPDelIdent, 340, 300, "fit", "t3_ident counted for both cells, as §2.3 does"},
	{CellRDelIdent, 660, 300, "fit", ""},
	{CellIdent, 210, 0, "all", ident210},
	{CellDiagInf, 260, 0, "all", "sensitivity only"},
	{CellDiagRACA, 150, 100, "draws", feasibleAt50},
	{CellDiagCAMA, 150, 100, "draws", feasibleAt50},
	{CellT1Low, 95, 0, "all", "re-stratified by the simulated cos"},
	{CellT1Mid, 95, 0, "all", ""},
	{CellT1High, 110, 100, "all", "gate set ≥ 100 (§9.1)"},
	{CellT1NonPos, 0, 0, "all", ""},
	{CellCooccur, 0, 0, "all", "must be 0 (m7 ban)"},
	{CellExcluded, 0, 0, "all", ""},
}

// Counts are the realized per-cell counts of one simulated data set, plus edit-draw tallies.
type Counts struct {
	Triplets int
	FitSet   int
	// Cells maps a cell name to its count (in the cell's scope).
	Cells map[string]int
	// AllCells is Cells with the "fit"-scope cells counted over every triplet (diagnostic and X-spk triplets included).
	AllCells map[string]int
	// Infeasible counts the catalogue's infeasible-kind redraws per kind (all triplets, accepted plans only).
	Infeasible map[perturb.Kind]int
	// T3Kinds counts the edit draws per kind over T3 triplets.
	T3Kinds map[perturb.Kind]int
	// AddStrata counts R-add draws per distance stratum (≤1 / 2–4 / 5–8) over all triplets.
	AddStrata [3]int
	// TypeDirs counts R-type and R-type-CA draws per direction label over all triplets.
	TypeDirs map[string]int
}

func nz(x float64) bool { return math.Abs(x) > 1e-9 }

// Count computes the realized counts: a triplet is informative for p if Δφ_p ≠ 0 at θ₀ (decisions-stats §15.3).
func Count(trips []fit.Triplet, metas []Meta, th0 ged.Theta) (Counts, error) {
	c := Counts{Triplets: len(trips), Cells: map[string]int{}, AllCells: map[string]int{}, T3Kinds: map[perturb.Kind]int{},
		TypeDirs: map[string]int{}, Infeasible: map[perturb.Kind]int{}}
	for i, t := range trips {
		m := metas[i]
		fa, err := ged.Compute(t.Ref, t.A, t.SimA, th0)
		if err != nil {
			return c, err
		}
		fb, err := ged.Compute(t.Ref, t.B, t.SimB, th0)
		if err != nil {
			return c, err
		}
		var d [ged.NumParams]float64
		for p := range ged.NumParams {
			d[p] = fb.Phi[p] - fa.Phi[p]
		}
		dExtra := fb.EdgeExtra - fa.EdgeExtra

		for _, es := range [][]perturb.Edit{m.EditsA, m.EditsB} {
			for _, e := range es {
				if m.Type == perturb.T3Contrast {
					c.T3Kinds[e.Kind]++
				}
				switch e.Kind {
				case perturb.RAdd:
					if e.Distance != nil {
						if k := perturb.AddDistStratum(*e.Distance); k >= 0 {
							c.AddStrata[k]++
						}
					}
				case perturb.RType, perturb.RTypeCA:
					c.TypeDirs[e.Direction]++
				}
			}
		}
		for _, e := range m.Info.Infeasible {
			c.Infeasible[e.Kind]++
		}
		if m.Info.T3Ident {
			c.Cells[CellIdent]++
		}
		if m.Info.DelAddCooccur {
			c.Cells[CellCooccur]++
		}
		if m.Info.DiagCAType && (nz(d[ged.PTypeRACA]) || nz(d[ged.PTypeCAMA])) {
			c.Cells[CellDiagInf]++
		}
		if m.Type == perturb.T1Monotone {
			switch {
			case m.Delta0 <= 1e-9:
				c.Cells[CellT1NonPos]++
			case m.T1Stratum == perturb.T1Low:
				c.Cells[CellT1Low]++
			case m.T1Stratum == perturb.T1Mid:
				c.Cells[CellT1Mid]++
			case m.T1Stratum == perturb.T1High:
				c.Cells[CellT1High]++
			}
		}
		in := m.InFit()
		if in {
			c.FitSet++
		} else {
			c.Cells[CellExcluded]++
		}
		inc := func(name string, ok bool) {
			if ok {
				c.AllCells[name]++
				if in {
					c.Cells[name]++
				}
			}
		}
		inc(CellIMissing, nz(d[ged.PNodeMissing]))
		inc(CellIExtra, nz(d[ged.PNodeExtra]))
		inc(CellEMissing, nz(d[ged.PEdgeMissing]))
		inc(CellEExtra, nz(dExtra))
		inc(CellDirection, nz(d[ged.PDirection]))
		inc(CellTypeRAMA, nz(d[ged.PTypeRAMA]))
		inc(CellNeg, nz(t.NegDiff))
		inc(CellF09, nz(d[ged.PSub09]))
		inc(CellF07, nz(d[ged.PSub07]))
		inc(CellF05, nz(d[ged.PSub05]))
		inc(CellF03, nz(d[ged.PSub03]))
		inc(CellPDelOnly, nz(d[ged.PNodeMissing]) && !nz(d[ged.PEdgeMissing]))
		inc(CellRDelOnly, nz(d[ged.PEdgeMissing]) && !nz(d[ged.PNodeMissing]))
		inc(CellPDelIdent, m.Info.T3Ident || nz(d[ged.PNodeMissing]) && !nz(d[ged.PEdgeMissing]))
		inc(CellRDelIdent, m.Info.T3Ident || nz(d[ged.PEdgeMissing]) && !nz(d[ged.PNodeMissing]))
	}
	c.Cells[CellRAtoMA] = c.TypeDirs[perturb.DirectionLabel(ged.RA, ged.MA)]
	c.Cells[CellMAtoRA] = c.TypeDirs[perturb.DirectionLabel(ged.MA, ged.RA)]
	c.Cells[CellDiagRACA] = c.TypeDirs[perturb.DirectionLabel(ged.RA, ged.CA)] + c.TypeDirs[perturb.DirectionLabel(ged.CA, ged.RA)]
	c.Cells[CellDiagCAMA] = c.TypeDirs[perturb.DirectionLabel(ged.CA, ged.MA)] + c.TypeDirs[perturb.DirectionLabel(ged.MA, ged.CA)]
	return c, nil
}
