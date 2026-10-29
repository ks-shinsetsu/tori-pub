package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/synth"
)

// Runs realization (G3) and verification (G4) per triplet slot.
//
// v1 (pilot 2, -prompt-version v1 -verify):
//   - After realizing a variant, have luna verify it in a separate request (the same model)
//   - If it fails, regenerate once with the reasons attached; if it still fails, redraw the edit plan (up to 3 times per slot)
//   - "Edits that change the graph but text unchanged from the source" are dropped mechanically before verification
//   - For T3, half the pairs have a difference near 0 under current prices and half a clear difference (plan §4.3).
//     cos is unknown before realization, so selection predicts the difference from a cos estimated from edit strength
//
// v2 (dataset-spec §3.1, §4.2–§4.3): the writer realizes with realize_v3 (touched-proposition texts,
// infeasible flag), the other-family verifier re-annotates the touched elements blind (verify_v2),
// an infeasible return or a failed plan is redrawn (≤ 3 plans per slot) and kept as a discarded plan,
// T1's B is realized incrementally from A's realized text and must reach realized Δ(θ₀) ≥ 0.5.

const nearThreshold = 0.3

// ---------------------------------------------------------------------------------------------
// Records (dataset-spec §6). Every value known here is stored so that the integration with the
// revised perturb.Edit / fit.Triplet is a copy.

// tripletFlags are the per-triplet flags of dataset-spec §6.
type tripletFlags struct {
	QT30          bool   `json:"qt30,omitempty"`
	Episode       string `json:"episode,omitempty"`
	Pilot         bool   `json:"pilot,omitempty"`
	XSpk          bool   `json:"x_spk,omitempty"`
	DiagCAType    bool   `json:"diag_ca_type,omitempty"`
	T3Ident       bool   `json:"t3_ident,omitempty"`
	DelAddCooccur bool   `json:"del_add_cooccur,omitempty"`
	PNegA         int    `json:"p_neg_a,omitempty"`
	PNegB         int    `json:"p_neg_b,omitempty"`
}

// editRecord is one edit with the attributes dataset-spec §6 asks for.
// TODO(integration): perturb.Edit gains Direction, Distance, Degree, EdgesRemoved and Infeasible; then
// take them from the edit instead of computing them here (editRecords) and drop the duplicates.
type editRecord struct {
	perturb.Edit
	// Direction is "RA→MA" etc. for R-type, the edge kind for the other relation edits.
	Direction string `json:"direction,omitempty"`
	// Distance is the utterance distance of the edge in S (relation edits, P-del+R: max over removed edges).
	Distance *int `json:"distance,omitempty"`
	// Degree is the degree in S of each touched source proposition.
	Degree []int `json:"degree,omitempty"`
	// EdgesRemoved is the number of edges removed with the proposition (P-del+R).
	EdgesRemoved int `json:"edges_removed,omitempty"`
	// RealizedCos is the embedding cos between the source and the realized proposition (P-para / P-shift / P-neg).
	RealizedCos *float64 `json:"realized_cos,omitempty"`
}

// attemptRecord is one realize (+ verify) attempt.
type attemptRecord struct {
	Attempt      int                  `json:"attempt"`
	Error        string               `json:"error,omitempty"`
	Infeasible   bool                 `json:"infeasible,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Model        string               `json:"model,omitempty"`
	Stats        synth.RealizeStats   `json:"stats"`
	Changed      []int                `json:"changed_utterances,omitempty"`
	Query        *synth.VerifyQuery   `json:"query,omitempty"`
	Verification *synth.Verification  `json:"verification,omitempty"`
	Verifier     *synth.VerifierMeta  `json:"verifier,omitempty"`
	Problems     []string             `json:"problems,omitempty"`
	Confusions   []string             `json:"confusions,omitempty"`
	Usage        map[string]llm.Usage `json:"usage,omitempty"`
}

// variantRecord is the realization history of one variant.
type variantRecord struct {
	// Base is "S" or, for T1's B, "A" (incremental realization).
	Base      string          `json:"base"`
	Attempts  []attemptRecord `json:"attempts"`
	Passed    bool            `json:"passed"`
	FirstPass bool            `json:"first_pass"`
	Fluency   int             `json:"fluency,omitempty"`
	Coherence int             `json:"coherence,omitempty"`
	// Cos is the embedding cos of every variant proposition to its source proposition (1 for identical text).
	Cos            map[string]float64 `json:"cos,omitempty"`
	EmbeddingModel string             `json:"embedding_model,omitempty"`
	Chars          int                `json:"chars,omitempty"`
	Words          int                `json:"words,omitempty"`
	// Verifier is the verifier of the accepted attempt.
	Verifier *synth.VerifierMeta `json:"verifier,omitempty"`
}

// discardedPlan is a plan that was redrawn, with its reasons (dataset-spec §4.2, decisions-stats §15 item 7).
type discardedPlan struct {
	Plan   int          `json:"plan"`
	EditsA []editRecord `json:"edits_a"`
	EditsB []editRecord `json:"edits_b"`
	// Reason is "infeasible", "verification", "t1_delta" or "error".
	Reason  string         `json:"reason"`
	Details []string       `json:"details,omitempty"`
	A       *variantRecord `json:"a,omitempty"`
	B       *variantRecord `json:"b,omitempty"`
}

// judgmentRecord is one judgment (judge × presentation order).
type judgmentRecord struct {
	Judge   string `json:"judge"` // configured model id
	Family  string `json:"family"`
	Model   string `json:"model,omitempty"` // as returned
	Setting string `json:"reasoning_setting"`
	// Order is +1 when A was shown first (Version 1), −1 otherwise.
	Order      int       `json:"order"`
	Raw        string    `json:"raw,omitempty"`
	Answer     string    `json:"answer,omitempty"` // A / B / same (relative to the triplet's A and B)
	Usage      llm.Usage `json:"usage"`
	CacheKey   string    `json:"cache_key"`
	PromptHash string    `json:"prompt_hash"`
	Time       string    `json:"time"`
	Cached     bool      `json:"cached,omitempty"`
	Batch      bool      `json:"batch,omitempty"`
	Error      string    `json:"error,omitempty"`
	// VerifierFamilyEqJudgeFamily marks judgments whose judge family equals the triplet's verifier family (§12c).
	VerifierFamilyEqJudgeFamily bool `json:"verifier_family_eq_judge_family"`
}

type variantCheck struct {
	Tries        int                   `json:"tries"`
	Passed       bool                  `json:"passed"`
	Naturalness  int                   `json:"naturalness,omitempty"`
	Problems     [][]string            `json:"problems,omitempty"`
	Verification *synth.VerificationV1 `json:"verification,omitempty"`
}

// ---------------------------------------------------------------------------------------------
// v1 (pilot 2) slot pipeline, unchanged.

type verifyStats struct {
	mu        sync.Mutex
	byKind    map[perturb.Kind]*[4]int // variants tried, passed first time, passed after regeneration, failed
	reasons   map[string]int
	natural   [6]int
	slots     int
	accepted  int
	discarded int
	replans   int
}

func (s *verifyStats) record(v perturb.Variant, passedAt int, problems [][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[perturb.Kind]bool{}
	for _, e := range v.Edits {
		if e.Kind == perturb.XSpk || seen[e.Kind] {
			continue
		}
		seen[e.Kind] = true
		c := s.byKind[e.Kind]
		if c == nil {
			c = &[4]int{}
			s.byKind[e.Kind] = c
		}
		c[0]++
		switch passedAt {
		case 0:
			c[1]++
		case 1:
			c[2]++
		default:
			c[3]++
		}
	}
	for _, ps := range problems {
		for _, p := range ps {
			s.reasons[reasonOf(p)]++
		}
	}
}

func reasonOf(p string) string {
	switch {
	case strings.Contains(p, "not clearly visible"), strings.Contains(p, "not checked"):
		return "編集が文章に出ていない"
	case strings.Contains(p, "left unchanged"):
		return "文章が元のまま（機械検査）"
	case strings.Contains(p, "no edit asked for"):
		return "指示していない変更がある"
	case strings.Contains(p, "unnaturally"):
		return "不自然（自然さ 2 以下）"
	case strings.Contains(p, "reversed polarity"):
		return "指示していない極性の反転"
	case strings.Contains(p, "still stated"):
		return "削除した命題が残っている"
	case strings.Contains(p, "relative to its intended text"):
		return "命題の内容が意図と違う"
	case strings.Contains(p, "untouched pair"):
		return "触れていない関係が変わった"
	case strings.Contains(p, "pair "):
		return "関係が意図と違う"
	case strings.Contains(p, "fluency"):
		return "流暢さ 2 以下"
	}
	return "出力を使えない（形式の誤りなど）"
}

type slotSpec struct {
	source, k int
	typ       perturb.TripletType
	// v2: T3 identifiability sub-stratum and the planned T1 Δ stratum.
	ident     bool
	t1Stratum string
}

type verifyPipeline struct {
	ctx     context.Context
	g       *synth.Generator
	mt      *meter
	sources []synth.Discussion
	seed    uint64
	stats   *verifyStats
}

// buildVerified builds the triplets for all slots (v1). Failed slots are returned with Err set.
func buildVerified(ctx context.Context, g *synth.Generator, mt *meter, sources []synth.Discussion, srcErr []error, rng *rand.Rand, seed uint64) ([]*pilotTriplet, *verifyStats) {
	pp := &verifyPipeline{ctx: ctx, g: g, mt: mt, sources: sources, seed: seed,
		stats: &verifyStats{byKind: map[perturb.Kind]*[4]int{}, reasons: map[string]int{}}}
	var slots []slotSpec
	for i := range sources {
		if srcErr[i] != nil {
			continue
		}
		for k := range 4 {
			slots = append(slots, slotSpec{source: i, k: k, typ: perturb.DrawType(rng)})
		}
	}
	trips := make([]*pilotTriplet, len(slots))
	parallel(len(slots), 8, func(i int) error {
		trips[i] = pp.slot(slots[i])
		return nil
	})
	pp.stats.slots = len(slots)
	for _, t := range trips {
		pp.stats.replans += t.Replans
		if t.Err == "" {
			pp.stats.accepted++
			pp.stats.natural[t.CheckA.Naturalness]++
			pp.stats.natural[t.CheckB.Naturalness]++
		} else {
			pp.stats.discarded++
		}
	}
	return trips, pp.stats
}

func (pp *verifyPipeline) slot(s slotSpec) *pilotTriplet {
	rng := rand.New(rand.NewPCG(pp.seed^0xc0ffee, uint64(s.source*4+s.k)))
	src := pp.sources[s.source]
	prefix := fmt.Sprintf("s%d.", s.source)
	t := &pilotTriplet{Source: s.source, Type: s.typ}
	var near *bool
	if s.typ == perturb.T3Contrast {
		v := s.k%2 == 0
		near = &v
		t.NearTarget = near
	}
	for plan := range 3 {
		if err := pp.mt.check(); err != nil {
			t.Err = err.Error()
			return t
		}
		t.Replans = plan
		a, b, pred := drawPair(rng, src.Graph, s.typ, near)
		t.A, t.B, t.EditsA, t.EditsB, t.PredDelta = a, b, a.Edits, b.Edits, finite(pred)
		da, sa, ca, ok := pp.realizeVerified(src, a, prefix)
		t.CheckA = ca
		if !ok {
			continue
		}
		db, sb, cb, ok := pp.realizeVerified(src, b, prefix)
		t.CheckB = cb
		if !ok {
			continue
		}
		t.TextA, t.TextB, t.StatA, t.StatB, t.Err = da, db, sa, sb, ""
		return t
	}
	t.Err = "discarded: 3 回の編集計画とも検証に通らなかった"
	return t
}

// drawPair draws an edit plan. If near is non-nil, it redraws until the predicted difference matches the target (near 0 or clear).
func drawPair(rng *rand.Rand, src ged.Graph, typ perturb.TripletType, near *bool) (a, b perturb.Variant, pred float64) {
	for range 40 {
		ka, kb := perturb.Plan(rng, typ)
		if typ == perturb.T1Monotone {
			steps := perturb.Build(rng, src, kb)
			a, b = steps[min(len(ka), len(steps))-1], steps[len(steps)-1]
		} else {
			a, b = lastOf(perturb.Build(rng, src, ka)), lastOf(perturb.Build(rng, src, kb))
		}
		if near == nil {
			return a, b, math.NaN()
		}
		pred = predictedDelta(src, a, b)
		if (math.Abs(pred) < nearThreshold) == *near {
			return a, b, pred
		}
	}
	return a, b, pred
}

// finite returns nil for NaN, which encoding/json cannot encode.
func finite(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

// predictedDelta predicts the difference Δ under current prices, using a cos estimated from edit strength.
func predictedDelta(src ged.Graph, a, b perturb.Variant) float64 {
	th := ged.Current()
	fa, errA := ged.Compute(src, a.Graph, predictedSim(a), th)
	fb, errB := ged.Compute(src, b.Graph, predictedSim(b), th)
	if errA != nil || errB != nil {
		return math.NaN()
	}
	return fb.Cost(th) - fa.Cost(th)
}

func predictedSim(v perturb.Variant) ged.Sim {
	para := map[string]float64{"weak": 0.935, "medium": 0.865, "strong": 0.79}
	shift := map[string]float64{"small": 0.675, "medium": 0.525, "large": 0.35}
	own := map[string]float64{}
	for id, e := range v.Changed {
		switch e.Kind {
		case perturb.PPara:
			own[id] = para[e.Strength]
		case perturb.PShift:
			own[id] = shift[e.Strength]
		case perturb.PNeg:
			own[id] = 0.9
		}
	}
	return func(ref, vid string) float64 {
		o, ok := v.Origin[vid]
		switch {
		case !ok || o != ref:
			return 0.3 // expected cos between different propositions
		case own[vid] > 0:
			return own[vid]
		}
		return 1
	}
}

// realizeVerified realizes and verifies, regenerating once with the reasons attached if it fails (v1).
func (pp *verifyPipeline) realizeVerified(src synth.Discussion, v perturb.Variant, prefix string) (synth.Discussion, synth.RealizeStats, variantCheck, bool) {
	var rec variantCheck
	feedback := ""
	n := len(synth.Instructions(v))
	for r := range 2 {
		if pp.mt.check() != nil {
			break
		}
		rec.Tries++
		d, st, resp, err := pp.g.RealizeV1(pp.ctx, src, v, prefix, r, feedback)
		pp.mt.add("realize", resp)
		if err != nil {
			rec.Problems = append(rec.Problems, []string{"error: " + err.Error()})
			feedback = "the output could not be used"
			continue
		}
		if n == 0 {
			rec.Passed = true
			pp.stats.record(v, r, nil)
			return d, st, rec, true
		}
		if changesGraph(v.Edits) && slices.Equal(d.Utterances, src.Utterances) {
			rec.Problems = append(rec.Problems, []string{"the text was left unchanged"})
			feedback = "the text was left unchanged; every edit must be visible in the text"
			continue
		}
		ver, resp, err := pp.g.VerifyV1(pp.ctx, src, d, v)
		pp.mt.add("verify", resp)
		if err != nil {
			rec.Problems = append(rec.Problems, []string{"error: " + err.Error()})
			continue
		}
		probs := ver.Problems(n)
		if len(probs) == 0 {
			rec.Passed, rec.Naturalness, rec.Verification = true, ver.Natural(), &ver
			pp.stats.record(v, r, rec.Problems)
			return d, st, rec, true
		}
		rec.Problems = append(rec.Problems, probs)
		feedback = strings.Join(probs, "; ")
	}
	pp.stats.record(v, -1, rec.Problems)
	return synth.Discussion{}, synth.RealizeStats{}, rec, false
}

// writeVerifyReport writes the verification and allocation summary (v1).
func writeVerifyReport(b *bytes.Buffer, s *verifyStats, kept []*pilotTriplet, d0 []float64) {
	fmt.Fprintln(b, "\n## 書き下しの検証（G4 の代わり。luna が別のリクエストで検証）")
	fmt.Fprintf(b, "\n- 三つ組の枠 %d、採用 %d、破棄 %d（3 回の編集計画とも不合格）。編集計画の引き直し 計 %d 回\n", s.slots, s.accepted, s.discarded, s.replans)
	fmt.Fprintln(b, "- 合格の基準: 指示した編集がすべて文章に出ている・指示外の変更が無い・自然さ 3 以上。不合格は理由を添えて 1 回だけ作り直す")
	fmt.Fprintln(b, "\n| 編集 | 書き下した変種 | 1 回目で合格 | 作り直しで合格 | 不合格 |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: |")
	for _, k := range sortedKeys(s.byKind) {
		c := s.byKind[k]
		fmt.Fprintf(b, "| %s | %d | %d（%.0f%%） | %d | %d（%.0f%%） |\n", k, c[0], c[1], pct(c[1], c[0]), c[2], c[3], pct(c[3], c[0]))
	}
	writeReasons(b, s.reasons)
	fmt.Fprintf(b, "\n採用した変種の自然さ（検証者の評価）: 5 = %d、4 = %d、3 = %d（話者の入れ替えだけの変種は評価なし: %d）\n",
		s.natural[5], s.natural[4], s.natural[3], s.natural[0])
	writeNearClear(b, kept, d0)
}

func writeReasons(b *bytes.Buffer, reasons map[string]int) {
	fmt.Fprintln(b, "\n不合格の理由（試行ごと、重複あり）:")
	fmt.Fprintln(b, "\n| 理由 | 回数 |")
	fmt.Fprintln(b, "| --- | --: |")
	for _, r := range sortedKeys(reasons) {
		fmt.Fprintf(b, "| %s | %d |\n", r, reasons[r])
	}
}

// writeNearClear reports how the T3 allocation turned out by actual difference (predicted uses estimated cos; actual uses embedding cos).
func writeNearClear(b *bytes.Buffer, kept []*pilotTriplet, d0 []float64) {
	var nearN, nearHit, clearN, clearHit int
	for i, t := range kept {
		if t.NearTarget == nil {
			continue
		}
		isNear := math.Abs(d0[i]) < nearThreshold
		if *t.NearTarget {
			nearN++
			if isNear {
				nearHit++
			}
		} else {
			clearN++
			if !isNear {
				clearHit++
			}
		}
	}
	fmt.Fprintln(b, "\n## T3 の割り付け（現行の値段で差が 0 付近 / 明確）")
	fmt.Fprintf(b, "\n- 「0 付近（|Δ| < %.1f）」を狙った組: %d、実際の埋め込みで 0 付近だったもの %s\n", nearThreshold, nearN, ratio(nearHit, nearN))
	fmt.Fprintf(b, "- 「明確」を狙った組: %d、実際に明確だったもの %s\n", clearN, ratio(clearHit, clearN))
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	ks := make([]K, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// ---------------------------------------------------------------------------------------------
// v2 slot pipeline.

// t1Kinds are the kinds allowed as T1's extra edit (dataset-spec §3.1 item 2).
func t1Allowed(e perturb.Edit) bool {
	switch e.Kind {
	case perturb.RDel, perturb.RAdd, perturb.RRev, perturb.PDel, perturb.PAdd, perturb.PDelR:
		return true
	case perturb.RType:
		return !involvesCA(e)
	case perturb.PShift:
		return e.Strength == "medium" || e.Strength == "large"
	}
	return false
}

func involvesCA(e perturb.Edit) bool {
	return e.Kind == perturb.RType && e.Edge != nil && (e.Edge.Kind == ged.CA || e.NewKind == ged.CA)
}

// T1 Δ(θ₀) strata (dataset-spec §3.1 item 3; the gate set is "≥2").
const (
	t1Low  = "[0.5,1)"
	t1Mid  = "[1,2)"
	t1Gate = ">=2"
)

func t1StratumOf(d float64) string {
	switch {
	case d >= 2:
		return t1Gate
	case d >= 1:
		return t1Mid
	case d >= 0.5:
		return t1Low
	case d > 0:
		return "<0.5"
	}
	return "<=0"
}

// drawT1Stratum draws the planned stratum: ≈ 110 of 300 T1 slots go to the gate stratum (§3.1 item 3).
func drawT1Stratum(rng *rand.Rand) string {
	switch r := rng.Float64(); {
	case r < 0.32:
		return t1Low
	case r < 0.64:
		return t1Mid
	}
	return t1Gate
}

// delAddCooccur reports the m7 co-occurrence (P-add with P-del or P-del+R in one variant).
func delAddCooccur(es []perturb.Edit) bool {
	var add, del bool
	for _, e := range es {
		add = add || e.Kind == perturb.PAdd
		del = del || e.Kind == perturb.PDel || e.Kind == perturb.PDelR
	}
	return add && del
}

// planT3Ident builds the node-only vs edge-only contrast of the t3_ident sub-stratum (dataset-spec §2.2):
// side 1 from {P-del, P-para, X-surf} containing a P-del (P-add is excluded by the m7 ban), side 2 from
// {R-del, R-add, R-rev, R-type} containing an R-del, 1–4 edits per side, sides randomized.
// TODO(integration): replace with perturb.Plan's t3_ident once the revised perturb lands.
func planT3Ident(rng *rand.Rand) (a, b []perturb.Kind) {
	node := []perturb.Kind{perturb.PDel}
	for range rng.IntN(4) {
		node = append(node, []perturb.Kind{perturb.PDel, perturb.PPara, perturb.XSurf}[rng.IntN(3)])
	}
	edge := []perturb.Kind{perturb.RDel}
	for range rng.IntN(4) {
		edge = append(edge, []perturb.Kind{perturb.RDel, perturb.RAdd, perturb.RRev, perturb.RType}[rng.IntN(4)])
	}
	if rng.IntN(2) == 0 {
		return node, edge
	}
	return edge, node
}

// drawPairV2 draws a plan for a v2 slot. T1: A's edits are a prefix of B's, the extra edits are of the
// §3.1 kinds, and the predicted Δ(θ₀) falls into the planned stratum (best effort over 60 draws; at
// least ≥ 0.5). T3: the m7 ban, the t3_ident sub-stratum, and near/clear targeting for the rest.
func drawPairV2(rng *rand.Rand, src ged.Graph, s slotSpec) (a, b perturb.Variant, pred float64) {
	near := s.k%2 == 0
	var fallback *[2]perturb.Variant
	fallbackPred := math.NaN()
	for range 60 {
		var ka, kb []perturb.Kind
		if s.ident {
			ka, kb = planT3Ident(rng)
		} else {
			ka, kb = perturb.Plan(rng, s.typ)
		}
		// TODO(integration): perturb.Build takes options (source-proposition Sim and the prop→utterance
		// map, for the R-add distance strata and the cos ≥ 0.35 filter); pass the source's here (drawPairV2
		// then needs the synth.Discussion and the embeddings of the source propositions).
		if s.typ == perturb.T1Monotone {
			steps := perturb.Build(rng, src, kb)
			a, b = steps[min(len(ka), len(steps))-1], steps[len(steps)-1]
			if len(b.Edits) <= len(a.Edits) || len(a.Edits) == 0 {
				continue
			}
			ok := !delAddCooccur(a.Edits) && !delAddCooccur(b.Edits)
			for _, e := range b.Edits[len(a.Edits):] {
				ok = ok && t1Allowed(e)
			}
			if !ok {
				continue
			}
			// TODO(integration): use perturb's Delta0 helper and its T1 strata once available.
			pred = predictedDelta(src, a, b)
			if pred >= 0.5 && fallback == nil {
				fallback, fallbackPred = &[2]perturb.Variant{a, b}, pred
			}
			if t1StratumOf(pred) == s.t1Stratum {
				return a, b, pred
			}
			continue
		}
		a, b = lastOf(perturb.Build(rng, src, ka)), lastOf(perturb.Build(rng, src, kb))
		if delAddCooccur(a.Edits) || delAddCooccur(b.Edits) {
			continue
		}
		if s.typ != perturb.T3Contrast || s.ident {
			return a, b, math.NaN()
		}
		pred = predictedDelta(src, a, b)
		if fallback == nil {
			fallback, fallbackPred = &[2]perturb.Variant{a, b}, pred
		}
		if (math.Abs(pred) < nearThreshold) == near {
			return a, b, pred
		}
	}
	if fallback != nil {
		return fallback[0], fallback[1], fallbackPred
	}
	return a, b, pred
}

// editRecords annotates edits with the §6 attributes, measured on the source.
func editRecords(src synth.Discussion, edits []perturb.Edit) []editRecord {
	deg := map[string]int{}
	for _, e := range src.Graph.Edges {
		deg[e.From]++
		deg[e.To]++
	}
	dist := func(a, b string) *int {
		ua, ok1 := src.PropUtterance[a]
		ub, ok2 := src.PropUtterance[b]
		if !ok1 || !ok2 {
			return nil
		}
		d := ua - ub
		if d < 0 {
			d = -d
		}
		return &d
	}
	out := make([]editRecord, len(edits))
	for i, e := range edits {
		r := editRecord{Edit: e}
		switch e.Kind {
		case perturb.RType:
			r.Direction = e.Edge.Kind + "→" + e.NewKind
		case perturb.RRev, perturb.RAdd, perturb.RDel:
			r.Direction = e.Edge.Kind
		}
		if e.Edge != nil {
			r.Distance = dist(e.Edge.From, e.Edge.To)
			for _, id := range []string{e.Edge.From, e.Edge.To} {
				r.Degree = append(r.Degree, deg[id])
			}
		}
		if e.Prop != "" {
			if _, inSrc := src.PropUtterance[e.Prop]; inSrc {
				r.Degree = append(r.Degree, deg[e.Prop])
			}
		}
		if e.Kind == perturb.PDelR {
			r.EdgesRemoved = deg[e.Prop]
			for _, x := range src.Graph.Edges {
				if x.From == e.Prop || x.To == e.Prop {
					if d := dist(x.From, x.To); d != nil && (r.Distance == nil || *d > *r.Distance) {
						r.Distance = d
					}
				}
			}
		}
		out[i] = r
	}
	return out
}

// statKeys are the kind × direction × edit-count cells of the first-pass gate (dataset-spec §7).
// QT30-anchored variants are counted in separate cells (prefix "QT30"; §1.4: not counted against the gates).
func statKeys(recs []editRecord, qt30 bool) []string {
	n := len(recs)
	var out []string
	seen := map[string]bool{}
	for _, r := range recs {
		if r.Kind == perturb.XSpk {
			continue
		}
		k := string(r.Kind)
		switch r.Kind {
		case perturb.RType:
			k += " " + r.Direction
		case perturb.RAdd:
			if r.Distance != nil && *r.Distance >= 5 {
				k += " d5-8"
			}
		}
		k = fmt.Sprintf("%s ×%d", k, n)
		if qt30 {
			k = "QT30 " + k
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// v2Stats aggregates realization outcomes.
type v2Stats struct {
	mu sync.Mutex
	// cells: kind × direction × edit count → tried, first-pass, after retry, failed, infeasible
	cells      map[string]*[5]int
	reasons    map[string]int
	confusions map[string]int
	fluency    [6]int
	coherence  [6]int
	// byPair counts passed / tried variants per generator → verifier family.
	byPair    map[string]*[2]int
	slots     int
	accepted  int
	discarded int
	replans   int
	// infeasible per kind × direction (redraws).
	infeasible map[string]int
	t1Dropped  int
}

func newV2Stats() *v2Stats {
	return &v2Stats{cells: map[string]*[5]int{}, reasons: map[string]int{}, confusions: map[string]int{},
		byPair: map[string]*[2]int{}, infeasible: map[string]int{}}
}

func (s *v2Stats) record(recs []editRecord, rec *variantRecord, infeasible, qt30 bool, pair string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qt30 {
		pair = "QT30 " + pair
	}
	for _, k := range statKeys(recs, qt30) {
		c := s.cells[k]
		if c == nil {
			c = &[5]int{}
			s.cells[k] = c
		}
		c[0]++
		switch {
		case infeasible:
			c[4]++
		case rec.Passed && rec.FirstPass:
			c[1]++
		case rec.Passed:
			c[2]++
		default:
			c[3]++
		}
	}
	if infeasible {
		for _, r := range recs {
			if slices.Contains(synth.InfeasibleKinds, r.Kind) {
				s.infeasible[string(r.Kind)+" "+r.Direction]++
			}
		}
	}
	for _, a := range rec.Attempts {
		for _, p := range a.Problems {
			s.reasons[reasonOf(p)]++
		}
		if a.Error != "" {
			s.reasons[reasonOf(a.Error)]++
		}
		for _, c := range a.Confusions {
			s.confusions[c]++
		}
	}
	if !infeasible && pair != "" {
		c := s.byPair[pair]
		if c == nil {
			c = &[2]int{}
			s.byPair[pair] = c
		}
		c[1]++
		if rec.Passed {
			c[0]++
		}
	}
}

type pipelineV2 struct {
	ctx     context.Context
	gens    map[string]*synth.Generator // by generator model id
	mt      *meter
	emb     llm.Embedder
	sources []*sourceRecord
	seed    uint64
	stats   *v2Stats
}

// realizeOutcome is the result of realizing one variant.
type realizeOutcome int

const (
	outcomeOK realizeOutcome = iota
	outcomeInfeasible
	outcomeFailed
	outcomeBudget
)

// realizeVerifiedV2 realizes edits on base and verifies blind, retrying once with the verifier's reasons.
func (pp *pipelineV2) realizeVerifiedV2(g *synth.Generator, in synth.RealizeInput, rng *rand.Rand) (synth.Discussion, *variantRecord, realizeOutcome) {
	rec := &variantRecord{Base: "S"}
	if in.Incremental {
		rec.Base = "A"
	}
	needLLM := false
	for _, e := range in.Edits {
		needLLM = needLLM || e.Kind != perturb.XSpk
	}
	for r := range 2 {
		if pp.mt.check() != nil {
			return synth.Discussion{}, rec, outcomeBudget
		}
		in.Attempt = r
		res, resp, err := g.Realize(pp.ctx, in)
		pp.mt.add("realize", resp)
		att := attemptRecord{Attempt: r, Stats: res.Stats, Changed: res.ChangedUtterances}
		if resp != nil {
			att.Model = resp.Model
			att.Usage = map[string]llm.Usage{"realize": resp.Usage}
		}
		if err != nil {
			att.Error = err.Error()
			rec.Attempts = append(rec.Attempts, att)
			in.Feedback = "the output could not be used: " + err.Error()
			continue
		}
		if res.Infeasible {
			att.Infeasible, att.Reason = true, res.Reason
			rec.Attempts = append(rec.Attempts, att)
			return synth.Discussion{}, rec, outcomeInfeasible
		}
		if !needLLM {
			rec.Attempts = append(rec.Attempts, att)
			rec.Passed, rec.FirstPass = true, r == 0
			return res.Discussion, rec, outcomeOK
		}
		if changesGraph(in.Edits) && slices.Equal(res.Discussion.Utterances, in.Base.Utterances) {
			att.Problems = []string{"the text was left unchanged"}
			rec.Attempts = append(rec.Attempts, att)
			in.Feedback = "the text was left unchanged; every edit must be visible in the text"
			continue
		}
		q := synth.BuildVerifyQuery(in.Base, res.Discussion, in.Target, in.Edits, rng, 2)
		att.Query = &q
		ver, meta, resp, err := g.Verify(pp.ctx, res.Discussion, q)
		pp.mt.add("verify", resp)
		att.Verifier = &meta
		if resp != nil {
			if att.Usage == nil {
				att.Usage = map[string]llm.Usage{}
			}
			att.Usage["verify"] = resp.Usage
		}
		if err != nil {
			att.Error = "verify: " + err.Error()
			rec.Attempts = append(rec.Attempts, att)
			continue
		}
		att.Verification = &ver
		att.Problems = ver.Problems(q)
		att.Confusions = ver.Confusions(q)
		rec.Attempts = append(rec.Attempts, att)
		if len(att.Problems) == 0 {
			rec.Passed, rec.FirstPass = true, r == 0
			rec.Fluency, rec.Coherence, rec.Verifier = ver.Fluent(), ver.Coherent(), &meta
			return res.Discussion, rec, outcomeOK
		}
		in.Feedback = strings.Join(att.Problems, "; ")
	}
	return synth.Discussion{}, rec, outcomeFailed
}

// slot builds one v2 triplet (≤ 3 plans).
func (pp *pipelineV2) slot(s slotSpec) *pilotTriplet {
	rng := rand.New(rand.NewPCG(pp.seed^0xc0ffee, uint64(s.source*4+s.k)))
	sr := pp.sources[s.source]
	src := sr.Discussion
	g := pp.gens[sr.Generator]
	t := newTripletRecord(sr, s)
	if s.typ == perturb.T3Contrast && !s.ident {
		v := s.k%2 == 0
		t.NearTarget = &v
	}
	prefix := sr.prefix()
	pair := g.Writer().Family + "→" + g.Writer().Family
	if g.Verifier != nil {
		pair = g.Writer().Family + "→" + g.Verifier.Family
	}
	var lastReason string
	for plan := range 3 {
		if err := pp.mt.check(); err != nil {
			t.Err = err.Error()
			return t
		}
		t.Replans = plan
		a, b, pred := drawPairV2(rng, src.Graph, s)
		t.A, t.B, t.EditsA, t.EditsB, t.PredDelta = a, b, a.Edits, b.Edits, finite(pred)
		t.PlanA, t.PlanB = editRecords(src, a.Edits), editRecords(src, b.Edits)
		dp := discardedPlan{Plan: plan, EditsA: t.PlanA, EditsB: t.PlanB}

		da, recA, oa := pp.realizeVerifiedV2(g, synth.RealizeInput{Base: src, Target: a, Edits: a.Edits, Prefix: prefix}, rng)
		pp.stats.record(t.PlanA, recA, oa == outcomeInfeasible, sr.QT30, pair)
		dp.A = recA
		if oa == outcomeBudget {
			t.Err = errBudget.Error()
			return t
		}
		if oa != outcomeOK {
			dp.Reason, dp.Details = outcomeReason(oa), lastProblems(recA)
			t.Discarded = append(t.Discarded, dp)
			lastReason = dp.Reason
			if oa == outcomeInfeasible {
				t.Infeasible = append(t.Infeasible, infeasibleReason(recA))
			}
			continue
		}
		inB := synth.RealizeInput{Base: src, Target: b, Edits: b.Edits, Prefix: prefix}
		var extraRecs []editRecord
		if s.typ == perturb.T1Monotone {
			inB = synth.RealizeInput{Base: da, Target: b, Edits: b.Edits[len(a.Edits):], Incremental: true, Prefix: prefix}
			extraRecs = t.PlanB[len(t.PlanA):]
		} else {
			extraRecs = t.PlanB
		}
		db, recB, ob := pp.realizeVerifiedV2(g, inB, rng)
		pp.stats.record(extraRecs, recB, ob == outcomeInfeasible, sr.QT30, pair)
		dp.B = recB
		if ob == outcomeBudget {
			t.Err = errBudget.Error()
			return t
		}
		if ob != outcomeOK {
			dp.Reason, dp.Details = outcomeReason(ob), lastProblems(recB)
			t.Discarded = append(t.Discarded, dp)
			lastReason = dp.Reason
			if ob == outcomeInfeasible {
				t.Infeasible = append(t.Infeasible, infeasibleReason(recB))
			}
			continue
		}
		t.TextA, t.TextB, t.RealizeA, t.RealizeB = da, db, recA, recB
		t.StatA, t.StatB = lastStats(recA), lastStats(recB)
		if err := pp.measure(t, src); err != nil {
			t.Err = "embed: " + err.Error()
			return t
		}
		if s.typ == perturb.T1Monotone {
			if t.Delta0 == nil || *t.Delta0 < 0.5 {
				d := math.NaN()
				if t.Delta0 != nil {
					d = *t.Delta0
				}
				dp.Reason, dp.Details = "t1_delta", []string{fmt.Sprintf("realized Δ(θ₀) = %.2f < 0.5", d)}
				t.Discarded = append(t.Discarded, dp)
				lastReason = dp.Reason
				pp.stats.mu.Lock()
				pp.stats.t1Dropped++
				pp.stats.mu.Unlock()
				t.TextA, t.TextB, t.RealizeA, t.RealizeB, t.Delta0 = synth.Discussion{}, synth.Discussion{}, nil, nil, nil
				continue
			}
			t.T1Delta0 = t.Delta0
			t.T1Stratum = t1StratumOf(*t.Delta0)
		}
		t.Err = ""
		t.Flags.XSpk = hasKind(a.Edits, perturb.XSpk) || hasKind(b.Edits, perturb.XSpk)
		t.Flags.DiagCAType = slices.ContainsFunc(append(slices.Clone(a.Edits), b.Edits...), involvesCA)
		t.Flags.DelAddCooccur = delAddCooccur(a.Edits) || delAddCooccur(b.Edits)
		t.Flags.PNegA, t.Flags.PNegB = a.Neg, b.Neg
		if recB.Verifier != nil {
			t.VerifierFamily, t.VerifierModel = recB.Verifier.Family, recB.Verifier.Model
		} else if recA.Verifier != nil {
			t.VerifierFamily, t.VerifierModel = recA.Verifier.Family, recA.Verifier.Model
		} else if g.Verifier != nil {
			t.VerifierFamily, t.VerifierModel = g.Verifier.Family, g.Verifier.Model
		}
		t.CohA, t.CohB, t.FluA, t.FluB = recA.Coherence, recB.Coherence, recA.Fluency, recB.Fluency
		return t
	}
	t.Err = "discarded: 3 回の編集計画とも使えなかった（最後の理由: " + lastReason + "）"
	return t
}

func outcomeReason(o realizeOutcome) string {
	switch o {
	case outcomeInfeasible:
		return "infeasible"
	case outcomeFailed:
		return "verification"
	}
	return "error"
}

func lastProblems(r *variantRecord) []string {
	if len(r.Attempts) == 0 {
		return nil
	}
	a := r.Attempts[len(r.Attempts)-1]
	out := slices.Clone(a.Problems)
	if a.Error != "" {
		out = append(out, a.Error)
	}
	if a.Infeasible {
		out = append(out, "infeasible: "+a.Reason)
	}
	return out
}

func infeasibleReason(r *variantRecord) string {
	for _, a := range r.Attempts {
		if a.Infeasible {
			return a.Reason
		}
	}
	return ""
}

func lastStats(r *variantRecord) synth.RealizeStats {
	if len(r.Attempts) == 0 {
		return synth.RealizeStats{}
	}
	return r.Attempts[len(r.Attempts)-1].Stats
}

func hasKind(es []perturb.Edit, k perturb.Kind) bool {
	return slices.ContainsFunc(es, func(e perturb.Edit) bool { return e.Kind == k })
}

// measure embeds the propositions of S, A and B and fills cos, realized substitution cos, lengths and
// the realized Δ under θ₀ (Delta0).
func (pp *pipelineV2) measure(t *pilotTriplet, src synth.Discussion) error {
	var texts []string
	for _, d := range []synth.Discussion{src, t.TextA, t.TextB} {
		for _, p := range d.Graph.Props {
			if p.Text != "" && !slices.Contains(texts, p.Text) {
				texts = append(texts, p.Text)
			}
		}
	}
	vecs, err := pp.emb.Embed(pp.ctx, texts)
	if err != nil {
		return err
	}
	vec := map[string][]float32{}
	for i, s := range texts {
		vec[s] = vecs[i]
	}
	simA, simB := simFromVec(src.Graph, t.TextA.Graph, vec), simFromVec(src.Graph, t.TextB.Graph, vec)
	for _, side := range []struct {
		d    synth.Discussion
		rec  *variantRecord
		plan []editRecord
		sim  ged.Sim
	}{{t.TextA, t.RealizeA, t.PlanA, simA}, {t.TextB, t.RealizeB, t.PlanB, simB}} {
		side.rec.Cos = map[string]float64{}
		side.rec.EmbeddingModel = pp.emb.Model()
		for _, p := range side.d.Graph.Props {
			if src.PropText(p.ID) != "" {
				side.rec.Cos[p.ID] = side.sim(p.ID, p.ID)
			}
		}
		text := side.d.Render()
		side.rec.Chars, side.rec.Words = len(text), len(strings.Fields(text))
		for i, r := range side.plan {
			switch r.Kind {
			case perturb.PPara, perturb.PShift, perturb.PNeg:
				if c, ok := side.rec.Cos[r.Prop]; ok {
					side.plan[i].RealizedCos = &c
				}
			}
		}
	}
	th := ged.Current()
	fa, errA := ged.Compute(src.Graph, t.TextA.Graph, simA, th)
	fb, errB := ged.Compute(src.Graph, t.TextB.Graph, simB, th)
	if errA != nil || errB != nil {
		return fmt.Errorf("ged: %v %v", errA, errB)
	}
	// TODO(integration): perturb.Delta0 on the frozen graphs with the realized cos.
	t.Delta0 = finite(fb.Cost(th) - fa.Cost(th))
	t.PhiA, t.PhiB = &fa, &fb
	t.simA, t.simB = simA, simB
	return nil
}

// simFromVec builds the cos table between source and variant propositions (1 for identical text).
func simFromVec(src, v ged.Graph, vec map[string][]float32) ged.Sim {
	table := map[[2]string]float64{}
	for _, r := range src.Props {
		for _, p := range v.Props {
			c := 1.0
			if r.Text != p.Text {
				c = synth.Cosine(vec[r.Text], vec[p.Text])
			}
			table[[2]string{r.ID, p.ID}] = c
		}
	}
	return func(a, b string) float64 { return table[[2]string{a, b}] }
}

// buildVerifiedV2 runs every slot of the accepted sources.
func (pp *pipelineV2) buildAll(rng *rand.Rand) []*pilotTriplet {
	var slots []slotSpec
	for i := range pp.sources {
		for k := range 4 {
			s := slotSpec{source: i, k: k, typ: perturb.DrawType(rng)}
			switch s.typ {
			case perturb.T3Contrast:
				s.ident = rng.Float64() < 0.1
			case perturb.T1Monotone:
				s.t1Stratum = drawT1Stratum(rng)
			}
			slots = append(slots, s)
		}
	}
	trips := make([]*pilotTriplet, len(slots))
	parallel(len(slots), 8, func(i int) error {
		trips[i] = pp.slot(slots[i])
		return nil
	})
	pp.stats.slots = len(slots)
	for _, t := range trips {
		pp.stats.replans += t.Replans
		if t.Err == "" {
			pp.stats.accepted++
			pp.stats.fluency[t.FluA]++
			pp.stats.fluency[t.FluB]++
			pp.stats.coherence[t.CohA]++
			pp.stats.coherence[t.CohB]++
		} else {
			pp.stats.discarded++
		}
	}
	return trips
}

// writeVerifyReportV2 writes the G3/G4 summary (dataset-spec §7 first-pass gate per kind × direction × edit count).
func writeVerifyReportV2(b *bytes.Buffer, s *v2Stats, kept []*pilotTriplet, d0 []float64) {
	fmt.Fprintln(b, "\n## 書き下し（G3）と盲検の再注釈による検証（G4）")
	fmt.Fprintf(b, "\n- 三つ組の枠 %d、採用 %d、破棄 %d（%.1f%%）。編集計画の引き直し 計 %d 回（infeasible を含む）。T1 の実現後 Δ(θ₀) < 0.5 による引き直し %d 回\n",
		s.slots, s.accepted, s.discarded, pct(s.discarded, s.slots), s.replans, s.t1Dropped)
	fmt.Fprintln(b, "- 合格の基準（dataset-spec §4.3）: 触れた命題がすべて same / paraphrase（削除したものは absent）、触れた組と抜き取りの 2 組の関係が意図どおり、流暢さ 3 以上。整合性は記録のみ")
	fmt.Fprintln(b, "- 1 回目の合格率は infeasible を分母から外して数える（§7）。R-add の距離 5–8 は別の行（d5-8）")
	fmt.Fprintln(b, "\n| 編集 × 方向 × 編集数 | 書き下し | 1 回目で合格 | 作り直しで合格 | 不合格 | infeasible |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: | --: |")
	for _, k := range sortedKeys(s.cells) {
		c := s.cells[k]
		fmt.Fprintf(b, "| %s | %d | %d（%.0f%%） | %d | %d | %d |\n", k, c[0], c[1], pct(c[1], c[0]-c[4]), c[2], c[3], c[4])
	}
	if len(s.infeasible) > 0 {
		fmt.Fprintln(b, "\ninfeasible（引き直し）の回数（種類 × 方向）:")
		fmt.Fprintln(b, "\n| 種類 × 方向 | 回数 |")
		fmt.Fprintln(b, "| --- | --: |")
		for _, k := range sortedKeys(s.infeasible) {
			fmt.Fprintf(b, "| %s | %d |\n", k, s.infeasible[k])
		}
	}
	writeReasons(b, s.reasons)
	if len(s.confusions) > 0 {
		fmt.Fprintln(b, "\n検証者の RA↔MA の読み違い（意図→読み）:")
		for _, k := range sortedKeys(s.confusions) {
			fmt.Fprintf(b, "- %s: %d\n", k, s.confusions[k])
		}
	}
	fmt.Fprintln(b, "\n生成者 → 検証者の系列ごとの合格率（変種単位、infeasible を除く）:")
	fmt.Fprintln(b, "\n| 生成 → 検証 | 合格 / 書き下し |")
	fmt.Fprintln(b, "| --- | --: |")
	for _, k := range sortedKeys(s.byPair) {
		c := s.byPair[k]
		fmt.Fprintf(b, "| %s | %s |\n", k, ratio(c[0], c[1]))
	}
	fmt.Fprintf(b, "\n採用した変種の流暢さ: 5 = %d、4 = %d、3 = %d。整合性: 5 = %d、4 = %d、3 = %d、2 = %d、1 = %d（LLM を使わない変種は 0: %d）\n",
		s.fluency[5], s.fluency[4], s.fluency[3], s.coherence[5], s.coherence[4], s.coherence[3], s.coherence[2], s.coherence[1], s.fluency[0])
	writeNearClear(b, kept, d0)
}
