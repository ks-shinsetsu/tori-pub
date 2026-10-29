package main

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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
// v2 (dataset-spec §2–§4): plans come from the catalogue (perturb.DrawTriplet with the source's prop→utterance
// map, the embedding cos between source propositions, θ₀ and the strength-predicted cos); the writer realizes
// with realize_v3 (touched-proposition texts, infeasible flag), the other-family verifier re-annotates the touched
// elements blind (verify_v2), an infeasible return or a failed plan is redrawn (≤ 3 plans per slot) and kept as a
// discarded plan; a t3_diag relabel returned infeasible is redrawn inside the slot (≤ 3, then an ordinary T3 slot);
// T1's B is realized incrementally from A's realized text and must reach realized Δ(θ₀) ≥ 0.5.

const nearThreshold = 0.3

// ---------------------------------------------------------------------------------------------
// Records (dataset-spec §6). The plan metadata is perturb.PlanInfo verbatim (pilotTriplet.Plan); the
// fields here are the pilot-only ones.

// tripletFlags are the pilot-only flags of dataset-spec §6. The plan flags (x_spk, diag_ca_type, t3_ident,
// t3_ident_cell, t3_diag, del_add_cooccur) are in the triplet's "plan" (perturb.PlanInfo).
type tripletFlags struct {
	QT30    bool   `json:"qt30,omitempty"`
	Episode string `json:"episode,omitempty"`
	Pilot   bool   `json:"pilot,omitempty"`
	PNegA   int    `json:"p_neg_a,omitempty"`
	PNegB   int    `json:"p_neg_b,omitempty"`
	// Fit is the flag list handed to fit.Triplet.Flags (tripletFitFlags; decisions-stats §16 #16).
	Fit []string `json:"fit,omitempty"`
}

// tripletFitFlags is the fit.Triplet flag list (decisions-stats §16 #16). The first six follow
// simulate.Meta.Flags exactly (TestFitFlagsMatchSimulator): x_spk, diag_ca_type, del_add_cooccur, t3_ident and
// t3_diag from the plan, and t1_gate for a T1 triplet whose realized Δ(θ₀) is > 0 and in the ≥ 2 stratum (§9.1);
// then the pilot-only qt30 and pilot.
func tripletFitFlags(typ perturb.TripletType, info perturb.PlanInfo, delta0 float64, t1Stratum string, qt30, pilot bool) []string {
	var f []string
	add := func(name string, ok bool) {
		if ok {
			f = append(f, name)
		}
	}
	add("x_spk", info.XSpk)
	add("diag_ca_type", info.DiagCAType)
	add("del_add_cooccur", info.DelAddCooccur)
	add("t3_ident", info.T3Ident)
	add("t3_diag", info.T3Diag)
	add("t1_gate", typ == perturb.T1Monotone && delta0 > 1e-9 && t1Stratum == perturb.T1High)
	add("qt30", qt30)
	add("pilot", pilot)
	return f
}

// editRecord is one planned edit as stored (dataset-spec §6): the catalogue's perturb.Edit (kind, targets,
// direction, strength, new kind, utterance distance, degree, edges removed, infeasible reason) plus the
// realized cos of a substitution edit.
type editRecord struct {
	perturb.Edit
	// RealizedCos is the embedding cos between the source and the realized proposition (P-para / P-shift / P-neg).
	RealizedCos *float64 `json:"realized_cos,omitempty"`
}

// editRecords wraps the planned edits for the record.
func editRecords(edits []perturb.Edit) []editRecord {
	out := make([]editRecord, len(edits))
	for i, e := range edits {
		out[i] = editRecord{Edit: e}
	}
	return out
}

// planRecord is the plan metadata as stored: perturb.PlanInfo verbatim, except that a Δ(θ₀) that could not be
// computed (NaN, only when ged.Compute fails) is stored as 0, since encoding/json cannot encode NaN.
func planRecord(tp perturb.TripletPlan) *perturb.PlanInfo {
	p := tp.PlanInfo
	p.Infeasible = slices.Clone(p.Infeasible)
	if math.IsNaN(p.PredDelta0) || math.IsInf(p.PredDelta0, 0) {
		p.PredDelta0 = 0
	}
	if math.IsNaN(p.T1Delta0) || math.IsInf(p.T1Delta0, 0) {
		p.T1Delta0 = 0
	}
	return &p
}

// attemptRecord is one realize (+ verify) attempt.
type attemptRecord struct {
	Attempt    int    `json:"attempt"`
	Error      string `json:"error,omitempty"`
	Infeasible bool   `json:"infeasible,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// InfeasibleEdit is the index (into the edits this variant realized) of the edit the realizer returned
	// infeasible (review-synth-impl M3).
	InfeasibleEdit *int `json:"infeasible_edit,omitempty"`
	// RealizeKey / VerifyKey are the cache keys of the attempt's requests; RealizeRaw / VerifyRaw the raw
	// responses (dataset-spec §6, review M6).
	RealizeKey   string               `json:"realize_key,omitempty"`
	VerifyKey    string               `json:"verify_key,omitempty"`
	RealizeRaw   string               `json:"realize_raw,omitempty"`
	VerifyRaw    string               `json:"verify_raw,omitempty"`
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
	Plan int `json:"plan"`
	// Info is the plan's metadata (perturb.PlanInfo verbatim, as in pilotTriplet.Plan).
	Info *perturb.PlanInfo `json:"plan_info,omitempty"`
	// EditsA / EditsB are the planned edits; an edit the realizer returned infeasible carries the reason (Edit.Infeasible).
	EditsA []editRecord `json:"edits_a"`
	EditsB []editRecord `json:"edits_b"`
	// Reason is "infeasible", "verification", "t1_delta" or "error".
	Reason  string   `json:"reason"`
	Details []string `json:"details,omitempty"`
	// DiagRedraw marks a t3_diag relabel returned infeasible: redrawn inside the slot, not counted against the
	// plan cap (dataset-spec §2.2).
	DiagRedraw bool           `json:"diag_redraw,omitempty"`
	A          *variantRecord `json:"a,omitempty"`
	B          *variantRecord `json:"b,omitempty"`
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
	// Refusal / Truncated mark an answer the judge refused or cut off (never a tie; review M7).
	Refusal   bool `json:"refusal,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
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
	// sub is the T3 sub-stratum of a v2 slot, drawn once per slot (dataset-spec §2.2; the T1 Δ(θ₀) stratum is
	// drawn by perturb.DrawTriplet).
	sub perturb.T3Sub
	// replaces is the id of the discarded slot this one replaces (v2, review-synth-impl M2).
	replaces string
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

const (
	// maxPlans is the number of plans a v2 slot may use (dataset-spec §4.2); in-slot t3_diag redraws do not count.
	maxPlans = 3
	// maxDiagInfeasible is the number of infeasible relabel returns on a source after which a t3_diag slot falls
	// back to an ordinary T3 slot (dataset-spec §2.2).
	maxDiagInfeasible = 3
	// diagCountsOnly is the per-direction infeasibility rate above which a CA pair is reported as counts only
	// (decisions-stats §12b).
	diagCountsOnly = 0.6
)

// catalogueOptions are the catalogue options of a v2 run: the dataset-spec §2–§3 defaults with θ₀ = ged.Current()
// and the strength-predicted cos for the Δ(θ₀) targeting; diagShare overrides the t3_diag share when non-zero.
func catalogueOptions(diagShare float64) perturb.BuildOptions {
	th0 := ged.Current()
	return perturb.BuildOptions{Theta0: &th0, CosPred: perturb.PredictedCos, DiagShare: diagShare}
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
		case perturb.RType, perturb.RTypeCA:
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

// cellKind is the kind × direction part of an edit's statKeys cell ("R-type RA→MA", "R-add d5-8", "P-del").
func cellKind(r editRecord) string {
	k := statKeys([]editRecord{r}, false)
	if len(k) == 0 {
		return string(r.Kind)
	}
	return strings.TrimSuffix(k[0], " ×1") + " ×"
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
	// t3_diag (dataset-spec §2.2): slots planned in the sub-stratum, accepted with the relabel, fallen back to an
	// ordinary T3 slot; diagDir counts relabel realizations and infeasible returns per direction.
	diagSlots, diagAccepted, diagFallback int
	// replaced counts replacement slots (review M2).
	replaced int
	diagDir  map[string]*[2]int
	// discards counts plans and discarded plans per edit kind × generator (decisions-stats §15 item 8).
	discards map[[2]string]*[2]int
}

func newV2Stats() *v2Stats {
	return &v2Stats{cells: map[string]*[5]int{}, reasons: map[string]int{}, confusions: map[string]int{},
		byPair: map[string]*[2]int{}, infeasible: map[string]int{}, diagDir: map[string]*[2]int{}, discards: map[[2]string]*[2]int{}}
}

// countPlan adds one realized plan (its edit kinds, once each) to the discard table of generator gen.
func (s *v2Stats) countPlan(gen string, edits []editRecord, discarded bool) {
	seen := map[perturb.Kind]bool{}
	for _, e := range edits {
		if seen[e.Kind] {
			continue
		}
		seen[e.Kind] = true
		k := [2]string{string(e.Kind), gen}
		c := s.discards[k]
		if c == nil {
			c = &[2]int{}
			s.discards[k] = c
		}
		c[0]++
		if discarded {
			c[1]++
		}
	}
}

// discardRows is the discard-rate table (edit kind × generator) for the P2 report: plans containing the kind,
// discarded plans and the rate.
func (s *v2Stats) discardRows() [][]string {
	if len(s.discards) == 0 {
		return nil
	}
	keys := make([][2]string, 0, len(s.discards))
	for k := range s.discards {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	rows := [][]string{{"編集", "生成モデル", "計画", "破棄した計画", "破棄率"}}
	for _, k := range keys {
		c := s.discards[k]
		rows = append(rows, []string{k[0], k[1], fmt.Sprint(c[0]), fmt.Sprint(c[1]), fmt.Sprintf("%.0f%%", pct(c[1], c[0]))})
	}
	return rows
}

// record counts one realized variant (recs: its edits). inf is the index of the edit the realizer returned
// infeasible (-1 otherwise): an infeasible return is attributed to that edit's cell only (review M3).
func (s *v2Stats) record(recs []editRecord, rec *variantRecord, inf int, qt30 bool, pair string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qt30 {
		pair = "QT30 " + pair
	}
	infeasible := inf >= 0
	keys := statKeys(recs, qt30)
	if infeasible {
		keys = nil
		if inf < len(recs) {
			for _, k := range statKeys(recs, qt30) {
				if strings.HasPrefix(strings.TrimPrefix(k, "QT30 "), cellKind(recs[inf])) {
					keys = append(keys, k)
				}
			}
		}
	}
	for _, k := range keys {
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
	for i, r := range recs {
		if r.Kind != perturb.RTypeCA || qt30 {
			continue
		}
		c := s.diagDir[r.Direction]
		if c == nil {
			c = &[2]int{}
			s.diagDir[r.Direction] = c
		}
		c[0]++
		if i == inf {
			c[1]++
		}
	}
	if infeasible && inf < len(recs) {
		s.infeasible[string(recs[inf].Kind)+" "+recs[inf].Direction]++
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

// diagInfeasibleRate is the infeasibility rate of the relabels in direction dir (NaN if none was realized).
func (s *v2Stats) diagInfeasibleRate(dir string) float64 {
	c := s.diagDir[dir]
	if c == nil || c[0] == 0 {
		return math.NaN()
	}
	return float64(c[1]) / float64(c[0])
}

type pipelineV2 struct {
	ctx     context.Context
	gens    map[string]*synth.Generator // by generator model id
	mt      *meter
	emb     llm.Embedder
	sources []*sourceRecord
	seed    uint64
	stats   *v2Stats
	// catalogue are the run's catalogue options; srcOpts adds each source's utterance map and proposition cos.
	catalogue perturb.BuildOptions
	srcOpts   []perturb.BuildOptions
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
		key := g.RealizeKey(in)
		res, resp, err := g.Realize(pp.ctx, in)
		pp.mt.add("realize", resp)
		att := attemptRecord{Attempt: r, Stats: res.Stats, Changed: res.ChangedUtterances, RealizeKey: key}
		if resp != nil {
			att.Model, att.RealizeRaw = resp.Model, resp.Text
			att.Usage = map[string]llm.Usage{"realize": resp.Usage}
		}
		if err != nil {
			att.Error = err.Error()
			rec.Attempts = append(rec.Attempts, att)
			in.Feedback = "the output could not be used: " + err.Error()
			continue
		}
		if res.Infeasible {
			idx := res.InfeasibleEdit
			att.Infeasible, att.Reason, att.InfeasibleEdit = true, res.Reason, &idx
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
		att.VerifyKey = g.VerifyKey(res.Discussion, q)
		ver, meta, resp, err := g.Verify(pp.ctx, res.Discussion, q)
		pp.mt.add("verify", resp)
		att.Verifier = &meta
		if resp != nil {
			att.VerifyRaw = resp.Text
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

// sourceOptions fills the per-source catalogue options: the prop→utterance map (R-add distance strata and the
// recorded utterance distances) and the embedding cos between source propositions (the R-add cos floor).
func (pp *pipelineV2) sourceOptions() error {
	var texts []string
	seen := map[string]bool{}
	for _, sr := range pp.sources {
		for _, p := range sr.Discussion.Graph.Props {
			if p.Text != "" && !seen[p.Text] {
				seen[p.Text] = true
				texts = append(texts, p.Text)
			}
		}
	}
	vec := map[string][]float32{}
	if len(texts) > 0 {
		vs, err := pp.emb.Embed(pp.ctx, texts)
		if err != nil {
			return fmt.Errorf("pilot: embed source propositions: %w", err)
		}
		for i, s := range texts {
			vec[s] = vs[i]
		}
	}
	pp.srcOpts = make([]perturb.BuildOptions, len(pp.sources))
	for i, sr := range pp.sources {
		o := pp.catalogue
		if len(sr.Discussion.PropUtterance) > 0 {
			o.Utterance = sr.Discussion.PropUtterance
		}
		o.Cos = propCos(sr.Discussion.Graph, vec)
		pp.srcOpts[i] = o
	}
	return nil
}

// propCos is the embedding cos between two propositions of g by id (1 for identical text). The table is filled up
// front, so the slots, which run in parallel, only read it.
func propCos(g ged.Graph, vec map[string][]float32) func(a, b string) float64 {
	table := map[[2]string]float64{}
	for i, p := range g.Props {
		for _, q := range g.Props[i+1:] {
			c := 1.0
			if p.Text != q.Text {
				c = synth.Cosine(vec[p.Text], vec[q.Text])
			}
			table[[2]string{p.ID, q.ID}], table[[2]string{q.ID, p.ID}] = c, c
		}
	}
	return func(a, b string) float64 { return table[[2]string{a, b}] }
}

// drawPlan draws the plan of slot s from the catalogue (perturb.DrawTriplet). A T3 slot keeps the sub-stratum
// sub (drawn once per slot); an ordinary T3 slot is redrawn until the predicted Δ(θ₀) hits the slot's near /
// clear target (≤ 50 redraws, plan §4.3), as the simulator does with its simulated cos.
func (pp *pipelineV2) drawPlan(rng *rand.Rand, s slotSpec, sub perturb.T3Sub) perturb.TripletPlan {
	o := pp.srcOpts[s.source]
	src := pp.sources[s.source].Discussion.Graph
	if s.typ != perturb.T3Contrast {
		return perturb.DrawTriplet(rng, src, s.typ, o)
	}
	o.Sub = &sub
	near := s.k%2 == 0
	for attempt := 0; ; attempt++ {
		tp := perturb.DrawTriplet(rng, src, s.typ, o)
		if tp.T3Ident || tp.T3Diag || (math.Abs(tp.PredDelta0) < nearThreshold) == near || attempt >= 50 {
			return tp
		}
	}
}

// setPlan makes tp the plan being realized.
func (t *pilotTriplet) setPlan(tp perturb.TripletPlan, k int) {
	t.A, t.B, t.EditsA, t.EditsB = tp.A, tp.B, tp.A.Edits, tp.B.Edits
	t.Plan = planRecord(tp)
	t.PlanA, t.PlanB = editRecords(tp.A.Edits), editRecords(tp.B.Edits)
	t.NearTarget = nil
	if tp.Type == perturb.T3Contrast && !tp.T3Ident && !tp.T3Diag {
		v := k%2 == 0
		t.NearTarget = &v
	}
}

// infeasibleEdit is the index of the edit the realizer returned infeasible in rec (-1 if none).
func infeasibleEdit(rec *variantRecord) int {
	for _, a := range rec.Attempts {
		if a.Infeasible && a.InfeasibleEdit != nil {
			return *a.InfeasibleEdit
		}
	}
	return -1
}

// failPlan keeps dp as a discarded plan after outcome o of the variant with edits side (recs: their records in
// dp). An infeasible return is attributed to the edit the realizer named, which gets the reason (Edit.Infeasible).
// It reports whether that edit is a t3_diag relabel, which is redrawn inside the slot (dataset-spec §2.2).
func (t *pilotTriplet) failPlan(dp discardedPlan, o realizeOutcome, rec *variantRecord, side []perturb.Edit, recs []editRecord) bool {
	dp.Reason, dp.Details = outcomeReason(o), lastProblems(rec)
	if o == outcomeInfeasible {
		reason := infeasibleReason(rec)
		t.Infeasible = append(t.Infeasible, reason)
		if i := infeasibleEdit(rec); i >= 0 && i < len(recs) && i < len(side) {
			recs[i].Infeasible = reason
			dp.DiagRedraw = dp.Info != nil && dp.Info.T3Diag && side[i].Kind == perturb.RTypeCA
		}
	}
	t.Discarded = append(t.Discarded, dp)
	return dp.DiagRedraw
}

// relabelKey identifies a relabel (edge and direction) refused on a source.
func relabelKey(e ged.Edge, dir string) string { return e.From + ">" + e.To + " " + dir }

// relabelVariant is the one-edit variant of src that relabels edge e to newKind (a t3_diag side), with the edit's
// record attributes as perturb computes them (utterance distance, endpoint degrees in src).
func relabelVariant(src ged.Graph, e ged.Edge, newKind string, utt map[string]int) perturb.Variant {
	g := src.Clone()
	for i, x := range g.Edges {
		if x == e {
			g.Edges[i].Kind = newKind
			break
		}
	}
	deg := func(id string) int {
		n := 0
		for _, x := range src.Edges {
			if x.From == id || x.To == id {
				n++
			}
		}
		return n
	}
	pos := func(id string) (int, bool) {
		if utt != nil {
			u, ok := utt[id]
			return u, ok
		}
		i := slices.IndexFunc(src.Props, func(p ged.Prop) bool { return p.ID == id })
		return i, i >= 0
	}
	edge := e
	ed := perturb.Edit{Kind: perturb.RTypeCA, Edge: &edge, NewKind: newKind, Direction: perturb.DirectionLabel(e.Kind, newKind),
		Degree: []int{deg(e.From), deg(e.To)}}
	if ua, ok := pos(e.From); ok {
		if ub, ok := pos(e.To); ok {
			d := ua - ub
			if d < 0 {
				d = -d
			}
			ed.Distance = &d
		}
	}
	v := perturb.Variant{Graph: g, Edits: []perturb.Edit{ed}, Origin: map[string]string{}, Changed: map[string]perturb.Edit{}}
	for _, p := range g.Props {
		v.Origin[p.ID] = p.ID
	}
	return v
}

// redrawRelabel replaces the relabel side of t3_diag plan tp (A when onA) by another CA-involving relabel on the
// same source, keeping the other side (dataset-spec §2.2: "another edge or direction on the same source"). As in
// the catalogue, the parameter pair (RA↔CA, CA↔MA) is drawn first and then a direction with an edge left; edge ×
// direction combinations already refused are skipped. ok is false when none is left.
func (pp *pipelineV2) redrawRelabel(rng *rand.Rand, source int, tp perturb.TripletPlan, onA bool, refused map[string]bool) (perturb.TripletPlan, bool) {
	src := pp.sources[source].Discussion.Graph
	o := pp.srcOpts[source]
	pairs := slices.Clone(perturb.TypeCAPairs)
	if rng.IntN(2) == 1 {
		pairs[0], pairs[1] = pairs[1], pairs[0]
	}
	for _, dirs := range pairs {
		var feasible [][2]string
		var cands [][]ged.Edge
		for _, d := range dirs {
			var es []ged.Edge
			for _, e := range src.Edges {
				if e.Kind == d[0] && !refused[relabelKey(e, perturb.DirectionLabel(d[0], d[1]))] {
					es = append(es, e)
				}
			}
			if len(es) > 0 {
				feasible, cands = append(feasible, d), append(cands, es)
			}
		}
		if len(feasible) == 0 {
			continue
		}
		i := rng.IntN(len(feasible))
		e := cands[i][rng.IntN(len(cands[i]))]
		v := relabelVariant(src, e, feasible[i][1], o.Utterance)
		np := tp
		np.Infeasible = slices.Clone(tp.Infeasible)
		if onA {
			np.A = v
		} else {
			np.B = v
		}
		np.PredDelta0 = math.NaN()
		if d, err := perturb.Delta0(src, np.A, np.B, *o.Theta0, o.CosPred); err == nil {
			np.PredDelta0 = d
		}
		return np, true
	}
	return tp, false
}

// realizedSide is a variant realized and verified in an earlier plan of the slot, reused when only the other
// side's relabel is redrawn.
type realizedSide struct {
	d   synth.Discussion
	rec *variantRecord
}

// slot builds one v2 triplet: ≤ 3 plans, plus ≤ 3 in-slot redraws of a t3_diag relabel returned infeasible
// (only the relabel is redrawn; a side already realized is kept).
func (pp *pipelineV2) slot(s slotSpec) *pilotTriplet {
	sr := pp.sources[s.source]
	rng := rand.New(rand.NewPCG(pp.seed^0xc0ffee^sourceSeed(sr.SourceID), uint64(s.k)))
	src := sr.Discussion
	g := pp.gens[sr.Generator]
	t := newTripletRecord(sr, s)
	prefix := sr.prefix()
	pair := g.Writer().Family + "→" + g.Writer().Family
	if g.Verifier != nil {
		pair = g.Writer().Family + "→" + g.Verifier.Family
	}
	sub := s.sub
	var lastReason string
	plans := 0
	var next *perturb.TripletPlan // a t3_diag plan with its relabel redrawn
	var keptA *realizedSide       // A's realization, kept while only B's relabel is redrawn
	refused := map[string]bool{}  // relabels returned infeasible on this source
	for attempt := 0; plans < maxPlans; attempt++ {
		if err := pp.mt.check(); err != nil {
			t.Err = err.Error()
			return t
		}
		t.Replans = attempt
		var tp perturb.TripletPlan
		if next != nil {
			tp, next = *next, nil
		} else {
			tp, keptA = pp.drawPlan(rng, s, sub), nil
		}
		t.setPlan(tp, s.k)
		dp := discardedPlan{Plan: attempt, Info: t.Plan, EditsA: slices.Clone(t.PlanA), EditsB: slices.Clone(t.PlanB)}
		// diagRedraw handles a relabel returned infeasible (the edit e on side A when onA): the relabel alone is
		// redrawn; after maxDiagInfeasible returns on the source, or with no relabel left, the slot is ordinary T3.
		diagRedraw := func(e perturb.Edit, onA bool) {
			refused[relabelKey(*e.Edge, e.Direction)] = true
			t.DiagInfeasible++
			if t.DiagInfeasible < maxDiagInfeasible {
				if np, ok := pp.redrawRelabel(rng, s.source, tp, onA, refused); ok {
					next = &np
					return
				}
			}
			sub, t.DiagFallback = perturb.T3Plain, true
		}

		var da synth.Discussion
		var recA *variantRecord
		oa := outcomeOK
		if keptA != nil {
			da, recA = keptA.d, keptA.rec
		} else {
			da, recA, oa = pp.realizeVerifiedV2(g, synth.RealizeInput{Base: src, Target: tp.A, Edits: tp.A.Edits, Prefix: prefix}, rng)
			pp.stats.record(t.PlanA, recA, outcomeInfeasibleEdit(oa, recA), sr.QT30, pair)
		}
		dp.A = recA
		if oa == outcomeBudget {
			t.Err = errBudget.Error()
			return t
		}
		if oa != outcomeOK {
			lastReason = outcomeReason(oa)
			if t.failPlan(dp, oa, recA, tp.A.Edits, dp.EditsA) {
				diagRedraw(tp.A.Edits[infeasibleEdit(recA)], true)
				continue
			}
			plans++
			continue
		}
		inB := synth.RealizeInput{Base: src, Target: tp.B, Edits: tp.B.Edits, Prefix: prefix}
		sideB, extraRecs, dpRecsB := tp.B.Edits, t.PlanB, dp.EditsB
		if s.typ == perturb.T1Monotone {
			// B = A plus the extra edits, realized from A's realized text (dataset-spec §3.1 item 1).
			n := len(tp.A.Edits)
			inB = synth.RealizeInput{Base: da, Target: tp.B, Edits: tp.B.Edits[n:], Incremental: true, Prefix: prefix}
			sideB, extraRecs, dpRecsB = tp.B.Edits[n:], t.PlanB[n:], dp.EditsB[n:]
		}
		db, recB, ob := pp.realizeVerifiedV2(g, inB, rng)
		pp.stats.record(extraRecs, recB, outcomeInfeasibleEdit(ob, recB), sr.QT30, pair)
		dp.B = recB
		if ob == outcomeBudget {
			t.Err = errBudget.Error()
			return t
		}
		if ob != outcomeOK {
			lastReason = outcomeReason(ob)
			if t.failPlan(dp, ob, recB, sideB, dpRecsB) {
				keptA = &realizedSide{da, recA}
				diagRedraw(sideB[infeasibleEdit(recB)], false)
				if next == nil {
					keptA = nil
				}
				continue
			}
			plans++
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
				plans++
				continue
			}
			// Realized re-stratification (dataset-spec §3.1 item 3): the stratum of the realized Δ(θ₀).
			t.T1Delta0 = t.Delta0
			t.T1Stratum = perturb.T1StratumOf(*t.Delta0)
		}
		t.Err = ""
		t.Flags.PNegA, t.Flags.PNegB = tp.A.Neg, tp.B.Neg
		t.Flags.Fit = t.fitFlagList()
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

// outcomeInfeasibleEdit is the infeasible edit's index for the stats (-1 unless o is an infeasible return).
func outcomeInfeasibleEdit(o realizeOutcome, rec *variantRecord) int {
	if o != outcomeInfeasible {
		return -1
	}
	return max(infeasibleEdit(rec), 0)
}

// sourceSeed is the slot RNG stream of a source, from its stable id (not from the accepted index, so that
// acceptance and dedup do not change other sources' plans; review m1).
func sourceSeed(id string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	return h.Sum64()
}

// fitFlagList is the fit.Triplet flag list of the triplet (tripletFitFlags on its plan and realized Δ(θ₀)).
func (t *pilotTriplet) fitFlagList() []string {
	var info perturb.PlanInfo
	if t.Plan != nil {
		info = *t.Plan
	}
	d0 := math.NaN()
	if t.Delta0 != nil {
		d0 = *t.Delta0
	}
	return tripletFitFlags(t.Type, info, d0, t.T1Stratum, t.Flags.QT30, t.Flags.Pilot)
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

// measure embeds the propositions of S, A and B and fills cos, realized substitution cos, lengths and the
// realized Δ under θ₀ (Delta0). The variant graphs are the frozen plan graphs (Realize copies Target.Graph), so
// Delta0 is Δ(θ₀) on the frozen graphs with the realized cos (dataset-spec §3.1, decisions-stats §9.1): the
// value fit.Delta gives at θ₀, and what the simulator computes with its simulated cos.
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

// buildAll runs every slot of the accepted sources. The T3 sub-stratum is drawn once per slot here
// (BuildOptions.DrawSub), so plan redraws stay inside it (dataset-spec §2.2).
func (pp *pipelineV2) buildAll(rng *rand.Rand) ([]*pilotTriplet, error) {
	if err := pp.sourceOptions(); err != nil {
		return nil, err
	}
	var slots []slotSpec
	for i := range pp.sources {
		for k := range 4 {
			s := slotSpec{source: i, k: k, typ: perturb.DrawType(rng)}
			if s.typ == perturb.T3Contrast {
				s.sub = pp.catalogue.DrawSub(rng)
			}
			slots = append(slots, s)
		}
	}
	trips := make([]*pilotTriplet, len(slots))
	parallel(len(slots), 8, func(i int) error {
		trips[i] = pp.slot(slots[i])
		return nil
	})
	// Replenish discarded slots (review-synth-impl M2, dataset-spec §2.3: the triplet counts are accepted ones): a
	// replacement is the next slot of the same source with the same type and sub-stratum; ≤ maxReplaceRounds rounds,
	// none once the budget is spent. Slots lost to the budget or to an embedding error are not replaced.
	nextK := map[int]int{}
	var pending []int
	for i, t := range trips {
		nextK[slots[i].source] = max(nextK[slots[i].source], slots[i].k+1)
		if strings.HasPrefix(t.Err, "discarded") {
			pending = append(pending, i)
		}
	}
	for round := 0; round < maxReplaceRounds && len(pending) > 0 && pp.mt.check() == nil; round++ {
		rs := make([]slotSpec, len(pending))
		for j, i := range pending {
			r := slots[i]
			r.k, r.replaces = nextK[r.source], trips[i].ID
			nextK[r.source]++
			rs[j] = r
		}
		rt := make([]*pilotTriplet, len(rs))
		parallel(len(rs), 8, func(j int) error {
			rt[j] = pp.slot(rs[j])
			return nil
		})
		pending = nil
		for j, t := range rt {
			if strings.HasPrefix(t.Err, "discarded") {
				pending = append(pending, len(trips))
			}
			trips, slots = append(trips, t), append(slots, rs[j])
			pp.stats.replaced++
		}
	}
	pp.stats.slots = len(slots)
	for i, t := range trips {
		gen := pp.sources[slots[i].source].Generator
		for _, dp := range t.Discarded {
			pp.stats.countPlan(gen, append(slices.Clone(dp.EditsA), dp.EditsB...), true)
		}
		if t.Err == "" {
			pp.stats.countPlan(gen, append(slices.Clone(t.PlanA), t.PlanB...), false)
		}
		pp.stats.replans += t.Replans
		if slots[i].sub == perturb.T3Diag {
			pp.stats.diagSlots++
		}
		if t.DiagFallback {
			pp.stats.diagFallback++
		}
		if t.Err == "" {
			pp.stats.accepted++
			pp.stats.fluency[t.FluA]++
			pp.stats.fluency[t.FluB]++
			pp.stats.coherence[t.CohA]++
			pp.stats.coherence[t.CohB]++
			if t.Plan != nil && t.Plan.T3Diag {
				pp.stats.diagAccepted++
			}
		} else {
			pp.stats.discarded++
		}
	}
	return trips, nil
}

// writeVerifyReportV2 writes the G3/G4 summary (dataset-spec §7 first-pass gate per kind × direction × edit count).
func writeVerifyReportV2(b *bytes.Buffer, s *v2Stats, kept []*pilotTriplet, d0 []float64) {
	fmt.Fprintln(b, "\n## 書き下し（G3）と盲検の再注釈による検証（G4）")
	fmt.Fprintf(b, "\n- 三つ組の枠 %d（うち破棄した枠の補充 %d、最大 %d 巡）、採用 %d、破棄 %d（%.1f%%）。編集計画の引き直し 計 %d 回（infeasible を含む）。T1 の実現後 Δ(θ₀) < 0.5 による引き直し %d 回\n",
		s.slots, s.replaced, maxReplaceRounds, s.accepted, s.discarded, pct(s.discarded, s.slots), s.replans, s.t1Dropped)
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
	writeDiagStratum(b, s)
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

// writeDiagStratum reports the t3_diag sub-stratum (dataset-spec §2.2): slots, accepted relabels, fallbacks and
// the infeasibility rate per direction from the realization attempts (no θ; decisions-stats §12b).
func writeDiagStratum(b *bytes.Buffer, s *v2Stats) {
	fmt.Fprintln(b, "\n## 診断層 t3_diag（CA を含む種別の付け替え、dataset-spec §2.2）")
	fmt.Fprintf(b, "\n- 計画した枠 %d、付け替えを含んで採用 %d、通常の T3 への切り替え %d（同じ元の議論で infeasible が %d 回）。当てはめからは既定で除く（diag_ca_type）\n",
		s.diagSlots, s.diagAccepted, s.diagFallback, maxDiagInfeasible)
	if len(s.diagDir) == 0 {
		return
	}
	fmt.Fprintln(b, "\n| 向き | 書き下し | infeasible | infeasible の率 |")
	fmt.Fprintln(b, "| --- | --: | --: | --: |")
	for _, k := range sortedKeys(s.diagDir) {
		c := s.diagDir[k]
		flag := ""
		if r := s.diagInfeasibleRate(k); r > diagCountsOnly {
			flag = "（> 60%: この組は件数のみ、§12b）"
		}
		fmt.Fprintf(b, "| %s | %d | %d | %.0f%%%s |\n", k, c[0], c[1], pct(c[1], c[0]), flag)
	}
}
