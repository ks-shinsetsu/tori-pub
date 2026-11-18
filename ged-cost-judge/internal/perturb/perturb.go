// Package perturb holds the edit catalog (plan §4.2, dataset-spec §2) and draws edit plans for triplets (§4.3, dataset-spec §3).
// The kinds and amounts of edits are decided here by random draws (fixed seed). The generating LLM does not know "which is closer".
//
// Every random choice uses only the *rand.Rand passed in, and candidate lists are built in graph order, so a plan is
// deterministic under a seed.
package perturb

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// Kind is a kind of edit.
type Kind string

const (
	PPara   Kind = "P-para"    // paraphrase a proposition (meaning preserved)
	PShift  Kind = "P-shift"   // change a proposition's content (role preserved)
	PNeg    Kind = "P-neg"     // flip polarity or quantification
	PDel    Kind = "P-del"     // missing isolated proposition
	PAdd    Kind = "P-add"     // extra isolated proposition
	PDelR   Kind = "P-del+R"   // proposition missing along with its relations
	RDel    Kind = "R-del"     // missing relation only
	RAdd    Kind = "R-add"     // extra relation only
	RType   Kind = "R-type"    // relabel the type, RA↔MA only (estimation cell)
	RTypeCA Kind = "R-type-CA" // relabel the type in a CA-involving direction (diagnostic cell, flagged diag_ca_type)
	RRev    Kind = "R-rev"     // reverse the direction (RA edges only)
	XSpk    Kind = "X-spk"     // swap speakers (proposition graph unchanged; T4 only)
	XSurf   Kind = "X-surf"    // change wording (proposition graph unchanged)
)

// Edit is a single edit.
type Edit struct {
	Kind Kind `json:"kind"`
	// Prop is the target proposition (P-para / P-shift / P-neg / P-del / P-del+R), or the new proposition's ID for P-add.
	Prop string `json:"prop,omitempty"`
	// Edge is the target edge (R-del / R-type / R-type-CA / R-rev), or the edge to add for R-add.
	Edge *ged.Edge `json:"edge,omitempty"`
	// NewKind is the new type for R-type / R-type-CA.
	NewKind string `json:"new_kind,omitempty"`
	// Strength is the strength for P-para (weak / medium / strong) and P-shift (small / medium / large).
	Strength string `json:"strength,omitempty"`
	// Direction is the relabel direction for R-type / R-type-CA ("RA→MA" etc.) and the reversed edge's kind for R-rev.
	Direction string `json:"direction,omitempty"`
	// Distance is the utterance distance between the edge's endpoints (relation edits; nil when not applicable or unknown).
	// Distance 0 means both propositions are stated in the same utterance.
	Distance *int `json:"distance,omitempty"`
	// Degree is the degree of the touched proposition (P-*) or of the edge's endpoints [from, to] (R-*), in the graph
	// the edit was applied to.
	Degree []int `json:"degree,omitempty"`
	// EdgesRemoved is the number of edges removed with the proposition (P-del+R).
	EdgesRemoved int `json:"edges_removed,omitempty"`
	// Utterance is the index of the utterance X-surf rewords (set by AssignSurface; nil: the realizer chooses).
	Utterance *int `json:"utterance,omitempty"`
	// Speakers are the two speakers X-spk swaps, as indices into the speakers in order of first appearance
	// (set by AssignSurface; empty: the first two).
	Speakers []int `json:"speakers,omitempty"`
	// Infeasible is the reason the edit could not be applied (catalogue redraws, or a realizer's "infeasible" answer).
	Infeasible string `json:"infeasible,omitempty"`
}

// Variant is a variant with edits applied.
type Variant struct {
	Graph ged.Graph `json:"graph"`
	Edits []Edit    `json:"edits"`
	// Origin maps variant propositions → source propositions (absent for added propositions).
	Origin map[string]string `json:"origin"`
	// Changed holds propositions whose content was changed, with their edits.
	Changed map[string]Edit `json:"changed"`
	// Neg is the number of polarity flips.
	Neg int `json:"neg"`
}

// BuildOptions carries the source-level information the catalogue needs. The zero value is usable
// (defaults of dataset-spec §2; utterance = index in src.Props; no cos filter for R-add).
type BuildOptions struct {
	// Utterance maps source proposition ID → utterance index (for utterance distances). nil: the proposition's index in src.Props.
	Utterance map[string]int
	// Cos is the cos between two source propositions (for the R-add filter). nil: no cos filter.
	Cos func(a, b string) float64
	// MinAddCos is the R-add cos floor (0 → 0.35). Applied only when Cos is set.
	MinAddCos float64
	// AddDistShares are the R-add utterance-distance strata shares over ≤1 / 2–4 / 5–8 (zero → 0.45 / 0.45 / 0.10).
	AddDistShares [3]float64
	// IdentShare is the share of T3 plans in the t3_ident sub-stratum (0 → 0.10; negative → none).
	IdentShare float64
	// DiagShare is the share of T3 plans in the t3_diag sub-stratum (0 → 0.10; negative → none).
	DiagShare float64
	// Sub, when set, fixes the T3 sub-stratum instead of drawing it with IdentShare / DiagShare.
	// A caller that redraws T3 plans (e.g. for Δ targeting) draws the sub-stratum once per slot with DrawSub and sets it here,
	// so that the redraw loop does not change the sub-strata's shares.
	Sub *T3Sub
	// T1MaxExtra caps the number of extra edits B \ A in T1 (0 → 3, dataset-spec §3.1 item 3).
	T1MaxExtra int
	// T1Shares are the T1 allocation shares over the Δ(θ₀) strata [0.5,1) / [1,2) / ≥2 (zero → 90 / 90 / 120 of 300).
	T1Shares [3]float64
	// Theta0 is the cost table for Δ(θ₀) (nil → ged.Current()).
	Theta0 *ged.Theta
	// CosPred is the strength-predicted cos of a changed proposition (nil → PredictedCos).
	CosPred func(Edit) float64
	// TypeDirections are the directions of the estimation relabel R-type (nil → both, RA→MA and MA→RA).
	TypeDirections [][2]string
	// AddKinds are the relation-type shares of R-add (nil → AddKindWeights); a type with share 0 is never added.
	AddKinds map[string]float64
}

// DrawSub draws the sub-stratum of a T3 slot: t3_ident with probability IdentShare (default 0.10), t3_diag with
// probability DiagShare (default 0.10), ordinary otherwise.
func (o BuildOptions) DrawSub(rng *rand.Rand) T3Sub {
	n := o.norm()
	switch u := rng.Float64(); {
	case u < n.IdentShare:
		return T3Ident
	case u < n.IdentShare+n.DiagShare:
		return T3Diag
	}
	return T3Plain
}

func (o BuildOptions) norm() BuildOptions {
	if o.MinAddCos == 0 {
		o.MinAddCos = 0.35
	}
	if o.AddDistShares == ([3]float64{}) {
		o.AddDistShares = [3]float64{0.45, 0.45, 0.10}
	}
	switch {
	case o.IdentShare == 0:
		o.IdentShare = 0.10
	case o.IdentShare < 0:
		o.IdentShare = 0
	}
	switch {
	case o.DiagShare == 0:
		o.DiagShare = 0.10
	case o.DiagShare < 0:
		o.DiagShare = 0
	}
	if o.T1MaxExtra <= 0 {
		o.T1MaxExtra = 3
	}
	if o.T1Shares == ([3]float64{}) {
		o.T1Shares = [3]float64{90, 90, 120}
	}
	if o.Theta0 == nil {
		t := ged.Current()
		o.Theta0 = &t
	}
	if o.CosPred == nil {
		o.CosPred = PredictedCos
	}
	if o.TypeDirections == nil {
		o.TypeDirections = TypeDirections
	}
	if o.AddKinds == nil {
		o.AddKinds = AddKindWeights
	}
	return o
}

// MaxAddDistance is the largest utterance distance R-add draws (dataset-spec §2.1: 9+ is not covered).
const MaxAddDistance = 8

// AddDistStratum maps an utterance distance to the R-add stratum (0: ≤1, 1: 2–4, 2: 5–8, -1: beyond).
func AddDistStratum(d int) int {
	switch {
	case d <= 1:
		return 0
	case d <= 4:
		return 1
	case d <= MaxAddDistance:
		return 2
	}
	return -1
}

type state struct {
	src      ged.Graph
	g        ged.Graph
	orig     map[string]int // source proposition → index in src.Props
	opts     *BuildOptions
	touchedP map[string]bool
	touchedE map[ged.Edge]bool
	added    int
}

func newState(src ged.Graph, opts *BuildOptions) *state {
	s := &state{src: src, g: src.Clone(), orig: map[string]int{}, opts: opts, touchedP: map[string]bool{}, touchedE: map[ged.Edge]bool{}}
	for i, p := range src.Props {
		s.orig[p.ID] = i
	}
	return s
}

func (s *state) clone() *state {
	c := *s
	c.g = s.g.Clone()
	c.touchedP = make(map[string]bool, len(s.touchedP))
	for k, v := range s.touchedP {
		c.touchedP[k] = v
	}
	c.touchedE = make(map[ged.Edge]bool, len(s.touchedE))
	for k, v := range s.touchedE {
		c.touchedE[k] = v
	}
	return &c
}

func (s *state) isOrig(id string) bool { _, ok := s.orig[id]; return ok }

func (s *state) utt(id string) (int, bool) {
	if s.opts.Utterance != nil {
		u, ok := s.opts.Utterance[id]
		return u, ok
	}
	i, ok := s.orig[id]
	return i, ok
}

// dist is the utterance distance between two propositions (nil if either is unknown).
func (s *state) dist(a, b string) *int {
	ua, ok1 := s.utt(a)
	ub, ok2 := s.utt(b)
	if !ok1 || !ok2 {
		return nil
	}
	d := ua - ub
	if d < 0 {
		d = -d
	}
	return &d
}

func (s *state) degree(id string) int {
	n := 0
	for _, e := range s.g.Edges {
		if e.From == id || e.To == id {
			n++
		}
	}
	return n
}

func (s *state) freeProps(ok func(id string) bool) []string {
	var out []string
	for _, p := range s.g.Props {
		if !s.touchedP[p.ID] && ok(p.ID) {
			out = append(out, p.ID)
		}
	}
	return out
}

func (s *state) freeEdges(ok func(e ged.Edge) bool) []ged.Edge {
	var out []ged.Edge
	for _, e := range s.g.Edges {
		if !s.touchedE[e] && ok(e) {
			out = append(out, e)
		}
	}
	return out
}

func pick[T any](rng *rand.Rand, xs []T) (T, bool) {
	var zero T
	if len(xs) == 0 {
		return zero, false
	}
	return xs[rng.IntN(len(xs))], true
}

// pickWeighted returns an index drawn with the given (unnormalized) weights; -1 if all are zero.
func pickWeighted(rng *rand.Rand, w []float64) int {
	tot := 0.0
	for _, x := range w {
		tot += x
	}
	if tot <= 0 {
		return -1
	}
	r := rng.Float64() * tot
	last := -1
	for i, x := range w {
		if x <= 0 {
			continue
		}
		last = i
		if r < x {
			return i
		}
		r -= x
	}
	return last
}

// Strength labels and draw weights (dataset-spec §2.1: P-para uniform, P-shift skewed to large).
var (
	ParaStrengths  = []string{"weak", "medium", "strong"}
	ShiftStrengths = []string{"small", "medium", "large"}
	ShiftWeights   = []float64{0.2, 0.3, 0.5}
)

// AddKindWeights are the R-add relation-type shares RA / MA / CA (dataset-spec §2.1).
var AddKindWeights = map[string]float64{ged.RA: 0.6, ged.MA: 0.3, ged.CA: 0.1}

// Relabel directions: R-type (estimation, RA↔MA) and R-type-CA (diagnostic, the four CA-involving directions).
var (
	TypeDirections   = [][2]string{{ged.RA, ged.MA}, {ged.MA, ged.RA}}
	TypeCADirections = [][2]string{{ged.RA, ged.CA}, {ged.CA, ged.RA}, {ged.CA, ged.MA}, {ged.MA, ged.CA}}
	// TypeCAPairs groups the diagnostic directions by parameter (RA↔CA, CA↔MA); a diagnostic relabel draws the pair
	// first (uniformly) and then a feasible direction within it, so that the two pairs get equal shares of the draws.
	TypeCAPairs = [][][2]string{TypeCADirections[:2], TypeCADirections[2:]}
)

// DirectionLabel is the label "RA→MA" of a relabel.
func DirectionLabel(from, to string) string { return from + "→" + to }

// want constrains a draw (same strength / direction / degree as another edit, targets to avoid).
type want struct {
	strength  string
	shiftW    []float64 // P-shift strength weights override
	direction string    // relabel direction label
	degree    int       // P-del+R: required degree (0 = 1 or 2)
	avoid     map[string]bool
}

func edgeKey(e ged.Edge) string { return e.From + ">" + e.To }

// targetKey identifies the target of an edit (for "same kind elsewhere").
func targetKey(e Edit) string {
	if e.Edge != nil {
		return edgeKey(*e.Edge)
	}
	return e.Prop
}

// draw picks and applies one edit of kind on the current graph. If none can be applied it returns false and the reason.
func (s *state) draw(rng *rand.Rand, kind Kind, w want) (Edit, bool, string) {
	switch kind {
	case PPara, PShift, PNeg:
		id, ok := pick(rng, s.freeProps(func(id string) bool { return s.isOrig(id) && !w.avoid[id] }))
		if !ok {
			return Edit{}, false, "no untouched source proposition"
		}
		e := Edit{Kind: kind, Prop: id, Degree: []int{s.degree(id)}}
		switch {
		case kind == PNeg:
		case w.strength != "":
			e.Strength = w.strength
		case kind == PPara:
			e.Strength = ParaStrengths[rng.IntN(len(ParaStrengths))]
		default:
			sw := ShiftWeights
			if w.shiftW != nil {
				sw = w.shiftW
			}
			e.Strength = ShiftStrengths[pickWeighted(rng, sw)]
		}
		s.touchedP[id] = true
		return e, true, ""
	case PDel, PDelR:
		want := func(id string) bool { return s.degree(id) == 0 }
		if kind == PDelR {
			want = func(id string) bool {
				d := s.degree(id)
				if w.degree > 0 {
					return d == w.degree
				}
				return d >= 1 && d <= 2
			}
		}
		id, ok := pick(rng, s.freeProps(func(id string) bool { return s.isOrig(id) && !w.avoid[id] && want(id) }))
		if !ok {
			if kind == PDel {
				return Edit{}, false, "no untouched isolated proposition"
			}
			return Edit{}, false, "no untouched proposition of degree 1–2"
		}
		d := s.degree(id)
		s.removeProp(id)
		e := Edit{Kind: kind, Prop: id, Degree: []int{d}}
		if kind == PDelR {
			e.EdgesRemoved = d
		}
		return e, true, ""
	case PAdd:
		var id string
		for {
			s.added++
			id = fmt.Sprintf("n%d", s.added)
			if !s.isOrig(id) {
				break
			}
		}
		s.g.Props = append(s.g.Props, ged.Prop{ID: id})
		s.touchedP[id] = true
		return Edit{Kind: kind, Prop: id, Degree: []int{0}}, true, ""
	case RDel:
		e, ok := pick(rng, s.freeEdges(func(e ged.Edge) bool { return !w.avoid[edgeKey(e)] }))
		if !ok {
			return Edit{}, false, "no untouched edge"
		}
		ed := s.edgeEdit(kind, e)
		s.g.Edges = slices.DeleteFunc(s.g.Edges, func(x ged.Edge) bool { return x == e })
		return ed, true, ""
	case RAdd:
		return s.drawAdd(rng, w)
	case RType, RTypeCA:
		groups := [][][2]string{s.opts.TypeDirections}
		if kind == RTypeCA {
			groups = slices.Clone(TypeCAPairs)
			if rng.IntN(2) == 1 {
				groups[0], groups[1] = groups[1], groups[0]
			}
		}
		for _, dirs := range groups {
			var feasible [][2]string
			var cands [][]ged.Edge
			for _, d := range dirs {
				if w.direction != "" && DirectionLabel(d[0], d[1]) != w.direction {
					continue
				}
				es := s.freeEdges(func(e ged.Edge) bool { return e.Kind == d[0] && !w.avoid[edgeKey(e)] })
				if len(es) > 0 {
					feasible = append(feasible, d)
					cands = append(cands, es)
				}
			}
			if len(feasible) == 0 {
				continue // the other pair, if any
			}
			i := rng.IntN(len(feasible))
			e, _ := pick(rng, cands[i])
			nk := feasible[i][1]
			ed := s.edgeEdit(kind, e)
			ed.NewKind, ed.Direction = nk, DirectionLabel(e.Kind, nk)
			s.replaceEdge(e, ged.Edge{From: e.From, To: e.To, Kind: nk})
			return ed, true, ""
		}
		return Edit{}, false, "no untouched edge of a relabelable type"
	case RRev:
		e, ok := pick(rng, s.freeEdges(func(e ged.Edge) bool { return e.Kind == ged.RA && !w.avoid[edgeKey(e)] }))
		if !ok {
			return Edit{}, false, "no untouched RA edge"
		}
		ed := s.edgeEdit(kind, e)
		ed.Direction = e.Kind
		s.replaceEdge(e, ged.Edge{From: e.To, To: e.From, Kind: e.Kind})
		return ed, true, ""
	case XSpk, XSurf:
		return Edit{Kind: kind}, true, ""
	}
	return Edit{}, false, "unknown kind"
}

func (s *state) edgeEdit(kind Kind, e ged.Edge) Edit {
	c := e
	return Edit{Kind: kind, Edge: &c, Distance: s.dist(e.From, e.To), Degree: []int{s.degree(e.From), s.degree(e.To)}}
}

// drawAdd draws an R-add: an utterance-distance stratum (0.45 / 0.45 / 0.10 over ≤1 / 2–4 / 5–8, renormalized over the
// strata that have candidates), then a pair of untouched source propositions in that stratum with cos ≥ MinAddCos and
// neither an edge nor a 2-step path between them, then the type (RA 0.6 / MA 0.3 / CA 0.1). CA and MA run from the
// later to the earlier utterance (the temporal convention); RA gets a random orientation.
func (s *state) drawAdd(rng *rand.Rand, w want) (Edit, bool, string) {
	ids := s.freeProps(func(id string) bool { return s.isOrig(id) && !w.avoid[id] })
	nb := map[string]map[string]bool{}
	link := func(a, b string) {
		if nb[a] == nil {
			nb[a] = map[string]bool{}
		}
		nb[a][b] = true
	}
	for _, e := range s.g.Edges {
		link(e.From, e.To)
		link(e.To, e.From)
	}
	var strata [3][][2]string
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if nb[a][b] || w.avoid[a+">"+b] || w.avoid[b+">"+a] {
				continue
			}
			twoStep := false
			for c := range nb[a] {
				if nb[b][c] {
					twoStep = true
					break
				}
			}
			if twoStep {
				continue
			}
			if s.opts.Cos != nil && s.opts.Cos(a, b) < s.opts.MinAddCos {
				continue
			}
			d := s.dist(a, b)
			if d == nil {
				continue
			}
			if k := AddDistStratum(*d); k >= 0 {
				strata[k] = append(strata[k], [2]string{a, b})
			}
		}
	}
	ws := make([]float64, 3)
	for k := range 3 {
		if len(strata[k]) > 0 {
			ws[k] = s.opts.AddDistShares[k]
		}
	}
	k := pickWeighted(rng, ws)
	if k < 0 {
		return Edit{}, false, "no pair without edge or 2-step path within distance 8 and cos floor"
	}
	ab, _ := pick(rng, strata[k])
	kinds := []string{ged.RA, ged.MA, ged.CA}
	kw := make([]float64, len(kinds))
	for i, x := range kinds {
		kw[i] = s.opts.AddKinds[x]
	}
	kind := kinds[pickWeighted(rng, kw)]
	from, to := ab[0], ab[1]
	ua, _ := s.utt(from)
	ub, _ := s.utt(to)
	switch {
	case kind != ged.RA:
		if ua < ub || (ua == ub && s.orig[from] < s.orig[to]) {
			from, to = to, from
		}
	case rng.IntN(2) == 1:
		from, to = to, from
	}
	e := ged.Edge{From: from, To: to, Kind: kind}
	ed := s.edgeEdit(RAdd, e)
	ed.Direction = kind
	s.g.Edges = append(s.g.Edges, e)
	s.touchedE[e] = true
	return ed, true, ""
}

func (s *state) removeProp(id string) {
	s.g.Props = slices.DeleteFunc(s.g.Props, func(p ged.Prop) bool { return p.ID == id })
	s.g.Edges = slices.DeleteFunc(s.g.Edges, func(e ged.Edge) bool { return e.From == id || e.To == id })
	s.touchedP[id] = true
}

func (s *state) replaceEdge(old, nw ged.Edge) {
	for i, e := range s.g.Edges {
		if e == old {
			s.g.Edges[i] = nw
			break
		}
	}
	s.touchedE[nw] = true
}

// Build applies kinds to S in order and returns the variant at each step (steps[i] has the first i+1 applied).
// Edits that cannot be applied are skipped. It is BuildWith with the zero BuildOptions (kept for cmd/gcj/pilot*.go).
func Build(rng *rand.Rand, src ged.Graph, kinds []Kind) []Variant {
	return BuildWith(rng, src, kinds, BuildOptions{})
}

// BuildWith is Build with source-level options (utterance map and source-proposition cos for R-add).
func BuildWith(rng *rand.Rand, src ged.Graph, kinds []Kind, opts BuildOptions) []Variant {
	o := opts.norm()
	s := newState(src, &o)
	var edits []Edit
	var steps []Variant
	for _, k := range kinds {
		e, ok, _ := s.draw(rng, k, want{})
		if !ok {
			continue
		}
		edits = append(edits, e)
		steps = append(steps, snapshot(src, s.g, edits))
	}
	if len(steps) == 0 {
		steps = append(steps, snapshot(src, s.g, nil))
	}
	return steps
}

func snapshot(src, g ged.Graph, edits []Edit) Variant {
	v := Variant{Graph: g.Clone(), Edits: slices.Clone(edits), Origin: map[string]string{}, Changed: map[string]Edit{}}
	orig := map[string]bool{}
	for _, p := range src.Props {
		orig[p.ID] = true
	}
	for _, p := range g.Props {
		if orig[p.ID] {
			v.Origin[p.ID] = p.ID
		}
	}
	for _, e := range edits {
		switch e.Kind {
		case PPara, PShift, PNeg:
			v.Changed[e.Prop] = e
			if e.Kind == PNeg {
				v.Neg++
			}
		}
	}
	return v
}

// UnrelatedCos is the predicted cos between two different propositions.
const UnrelatedCos = 0.3

// PredictedCos is the strength-predicted cos of a proposition changed by e (1 for edits that keep the content).
// The values are the strength bands' midpoints used in the pilots (cmd/gcj/pilot_verify.go predictedSim).
func PredictedCos(e Edit) float64 {
	switch e.Kind {
	case PPara:
		return map[string]float64{"weak": 0.935, "medium": 0.865, "strong": 0.79}[e.Strength]
	case PShift:
		return map[string]float64{"small": 0.675, "medium": 0.525, "large": 0.35}[e.Strength]
	case PNeg:
		return 0.9
	}
	return 1
}

// PredSim is the predicted similarity of v's propositions to the source's, from cosPred (nil → PredictedCos).
func PredSim(v Variant, cosPred func(Edit) float64) ged.Sim {
	if cosPred == nil {
		cosPred = PredictedCos
	}
	own := map[string]float64{}
	for id, e := range v.Changed {
		own[id] = cosPred(e)
	}
	return func(ref, vid string) float64 {
		o, ok := v.Origin[vid]
		if !ok || o != ref {
			return UnrelatedCos
		}
		if c, ok := own[vid]; ok {
			return c
		}
		return 1
	}
}

// Delta0 is GED(S, B) − GED(S, A) at theta0 on the frozen graphs with the predicted cos (dataset-spec §3.1 item 2).
func Delta0(src ged.Graph, a, b Variant, theta0 ged.Theta, cosPred func(Edit) float64) (float64, error) {
	fa, err := ged.Compute(src, a.Graph, PredSim(a, cosPred), theta0)
	if err != nil {
		return 0, err
	}
	fb, err := ged.Compute(src, b.Graph, PredSim(b, cosPred), theta0)
	if err != nil {
		return 0, err
	}
	return fb.Cost(theta0) - fa.Cost(theta0), nil
}
