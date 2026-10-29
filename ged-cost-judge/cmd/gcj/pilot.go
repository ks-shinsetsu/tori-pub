package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"gonum.org/v1/gonum/stat/distuv"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/synth"
)

type pilotOptions struct {
	sources, boot int
	seed          uint64
	lambda, m     float64
	out           string
	// budget is the API spending cap (USD); < 0 means defaultBudget(sources).
	budget float64
	// verify uses the v1 procedure with verification of the realizations (realize_v2 + verify_v1, T3 allocation).
	verify bool

	// promptVersion is synth.V1 (pilot-2 replay: luna only) or synth.V2 (the revised pipeline).
	promptVersion string
	promptDir     string
	generators    []string
	judges        []string
	genEffort     map[string]string
	genThinking   map[string]string
	judgeEffort   map[string]string
	judgeThinking map[string]string
	// topicBank is the stage-0 bank (default <out>/topics.json).
	topicBank string
	// p2 marks the run as the P2 pilot (records pilot=true) and writes the report without θ.
	p2 bool
	// withQT30 adds <out>/qt30_sources.json (from -import-qt30) as transfer-set sources.
	withQT30 bool
	aifdb    string
	// diagShare overrides the share of T3 slots in the t3_diag sub-stratum (0 → the dataset-spec §2.2 default
	// 0.10); not a flag, set by the offline tests so that the sub-stratum is exercised.
	diagShare float64
	// dReps overrides the nested-D replicates of the confirmatory analysis (0 → fit's pre-registered 200); not a
	// flag, set by the offline tests.
	dReps int
	// keepJudges keeps judges that fail the mechanical QC in the confirmatory fit (fit.ConfirmOptions.SkipExclusions);
	// not a flag: the offline tests have too few gate triplets for any judge to pass §9.1.
	keepJudges bool
}

// Defaults of the revised pipeline (dataset-spec §4.1, plan §5.1).
var (
	defaultGenerators = []string{"gpt-5.6-luna", "claude-sonnet-5-5"}
	defaultJudges     = []string{"gpt-5.6-luna", "claude-sonnet-5-5", "claude-haiku-5-5"}
)

// listBudget is the §4.5 list-price total for 750 sources; -budget defaults to 1.5× that, scaled by -sources.
const (
	listBudget750    = 172.7
	defaultBudget750 = 260.0
)

func defaultBudget(sources int) float64 {
	return math.Max(1, defaultBudget750*float64(sources)/750)
}

var (
	formats = func() []string {
		var out []string
		for _, f := range synth.Formats {
			out = append(out, f.Format)
		}
		return out
	}()
	domains    = synth.Domains
	structures = []string{
		"chain (each claim supports the next)",
		"convergent (several premises support one conclusion)",
		"divergent (one claim supports several conclusions)",
		"conflict-heavy (at least 3 attacks)",
		"restatement-heavy (at least 3 restatements)",
	}
)

// v1Formats is the pilot-2 format order (part of the v1 allocation, kept for replay).
var v1Formats = []string{
	"TV debate with a moderator", "meeting", "one-on-one argument",
	"online forum thread", "opinion essay by a single author", "interview",
}

// meter counts API spending and stops further calls once the cap is exceeded.
type meter struct {
	mu     sync.Mutex
	limit  float64
	byTag  map[string]llm.Usage
	calls  map[string]int
	cached map[string]int
	// byModel[tag][returned model] and modelCalls[tag][model] are the per-model totals (cost projection).
	byModel    map[string]map[string]llm.Usage
	modelCalls map[string]map[string]int
	// reserved is the projected cost of submitted batches not yet metered; check and remaining count it as spent,
	// so concurrent request-by-request calls cannot spend it (review-synth-impl M5).
	reserved float64
}

func newMeter(limit float64) *meter {
	return &meter{limit: limit, byTag: map[string]llm.Usage{}, calls: map[string]int{}, cached: map[string]int{},
		byModel: map[string]map[string]llm.Usage{}, modelCalls: map[string]map[string]int{}}
}

var errBudget = errors.New("pilot: API の費用が上限に達した")

func (m *meter) add(tag string, r *llm.Response) {
	if r == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byTag[tag] = m.byTag[tag].Add(r.Usage)
	if r.Cached {
		m.cached[tag]++
	} else {
		m.calls[tag]++
	}
	if m.byModel == nil {
		m.byModel, m.modelCalls = map[string]map[string]llm.Usage{}, map[string]map[string]int{}
	}
	if m.byModel[tag] == nil {
		m.byModel[tag], m.modelCalls[tag] = map[string]llm.Usage{}, map[string]int{}
	}
	// token counts of cached responses are kept (they measure the prompt), cost is 0
	m.byModel[tag][r.Model] = m.byModel[tag][r.Model].Add(r.Usage)
	m.modelCalls[tag][r.Model]++
}

func (m *meter) total() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := 0.0
	for _, u := range m.byTag {
		t += u.CostUSD
	}
	return t
}

func (m *meter) remaining() float64 { return m.limit - m.total() - m.pending() }

func (m *meter) pending() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserved
}

// reserve holds x (a batch projection) against the budget until release.
func (m *meter) reserve(x float64) {
	m.mu.Lock()
	m.reserved += x
	m.mu.Unlock()
}

// release drops a reservation once the batch's responses are metered.
func (m *meter) release(x float64) {
	m.mu.Lock()
	m.reserved = math.Max(0, m.reserved-x)
	m.mu.Unlock()
}

// check fails once the metered cost plus the reserved batch projections reach the limit (-budget 0: no calls).
func (m *meter) check() error {
	if m.total()+m.pending() >= m.limit {
		return errBudget
	}
	return nil
}

// perCall returns the measured mean input/output tokens per call for tag and the configured model
// (returned model ids are matched by prefix, so dated ids count). ok is false without measurements.
func (m *meter) perCall(tag, model string) (in, out float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var u llm.Usage
	n := 0
	for ret, x := range m.byModel[tag] {
		if strings.HasPrefix(ret, model) {
			u = u.Add(x)
			n += m.modelCalls[tag][ret]
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return float64(u.InputTokens) / float64(n), float64(u.OutputTokens) / float64(n), true
}

func parallel(n, workers int, f func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			errs[i] = f(i)
		}(i)
	}
	wg.Wait()
	return errs
}

// pilotTriplet is the per-triplet record (dataset-spec §6). The first block is the pilot-2 (v1) record;
// the v2 fields are omitted when empty, so a v1 record is unchanged.
type pilotTriplet struct {
	Source int                 `json:"source"`
	Type   perturb.TripletType `json:"type"`
	A, B   perturb.Variant     `json:"-"`
	TextA  synth.Discussion    `json:"a"`
	TextB  synth.Discussion    `json:"b"`
	EditsA []perturb.Edit      `json:"edits_a"`
	EditsB []perturb.Edit      `json:"edits_b"`
	StatA  synth.RealizeStats  `json:"realize_a"`
	StatB  synth.RealizeStats  `json:"realize_b"`
	Raw    []string            `json:"judge_raw"` // raw answers for presentation order +1, −1 (v1: one judge)
	// Unrealized means a variant is included whose text equals the source argument despite edits that change the proposition graph.
	Unrealized bool   `json:"unrealized"`
	Err        string `json:"error,omitempty"`

	// The following are filled only by the procedure with verification (-verify, and always in v2).
	Replans    int          `json:"replans,omitempty"`
	NearTarget *bool        `json:"near_target,omitempty"`
	PredDelta  *float64     `json:"pred_delta,omitempty"` // T3 (v2: and T1) only
	CheckA     variantCheck `json:"check_a,omitzero"`
	CheckB     variantCheck `json:"check_b,omitzero"`

	// v2 (dataset-spec §6). fitTriplets copies the contract fields into fit.Triplet.
	ID              string       `json:"id,omitempty"`
	SourceID        string       `json:"source_id,omitempty"`
	Seed            uint64       `json:"seed,omitempty"`
	Generator       string       `json:"generator,omitempty"`
	GeneratorFamily string       `json:"generator_family,omitempty"`
	VerifierFamily  string       `json:"verifier_family,omitempty"`
	VerifierModel   string       `json:"verifier_model,omitempty"`
	ExemplarPair    int          `json:"exemplar_pair,omitempty"`
	Exemplars       []string     `json:"exemplars,omitempty"`
	Domain          string       `json:"domain,omitempty"`
	Format          string       `json:"format,omitempty"`
	TopicID         string       `json:"topic_id,omitempty"`
	Flags           tripletFlags `json:"flags,omitzero"`
	// Plan is the catalogue's plan metadata verbatim (perturb.PlanInfo: sub-strata, flags, T1 target and
	// predicted Δ(θ₀), per-side counts, swap, catalogue redraws and infeasible kinds); PlanA / PlanB its edits.
	Plan  *perturb.PlanInfo `json:"plan,omitempty"`
	PlanA []editRecord      `json:"plan_a,omitempty"`
	PlanB []editRecord      `json:"plan_b,omitempty"`
	// SubPlanned is the T3 sub-stratum drawn for the slot; DiagInfeasible counts the t3_diag relabels returned
	// infeasible and redrawn inside the slot; DiagFallback marks a t3_diag slot that became ordinary T3 after
	// maxDiagInfeasible of them (dataset-spec §2.2).
	SubPlanned     perturb.T3Sub `json:"t3_sub_planned,omitempty"`
	ReplacementOf  string        `json:"replacement_of,omitempty"` // the discarded slot this one replaces
	DiagInfeasible int           `json:"diag_infeasible,omitempty"`
	DiagFallback   bool          `json:"diag_fallback,omitempty"`
	// T1Delta0 / T1Stratum are the realized Δ(θ₀) and its stratum (Plan.T1Target / T1Delta0 are the planned ones).
	T1Delta0   *float64         `json:"t1_delta0,omitempty"`
	T1Stratum  string           `json:"t1_stratum,omitempty"`
	Delta0     *float64         `json:"delta0,omitempty"` // realized Δ under θ₀ (all types)
	PhiA       *ged.Features    `json:"phi_a,omitempty"`
	PhiB       *ged.Features    `json:"phi_b,omitempty"`
	RealizeA   *variantRecord   `json:"realization_a,omitempty"`
	RealizeB   *variantRecord   `json:"realization_b,omitempty"`
	Discarded  []discardedPlan  `json:"discarded_plans,omitempty"`
	Infeasible []string         `json:"infeasible_reasons,omitempty"`
	CohA       int              `json:"coh_a,omitempty"`
	CohB       int              `json:"coh_b,omitempty"`
	FluA       int              `json:"flu_a,omitempty"`
	FluB       int              `json:"flu_b,omitempty"`
	Judgments  []judgmentRecord `json:"judgments,omitempty"`
	// Weight is the §0.1 reweighting weight (computed by the statistics side; nil here).
	Weight *float64 `json:"weight,omitempty"`

	// simA / simB are the embedding cos tables of S × A and S × B (set by measure; not serialized).
	simA, simB ged.Sim
}

// sourceRecord is the per-source record (dataset-spec §6).
type sourceRecord struct {
	SourceID string     `json:"source_id"`
	Index    int        `json:"index"`
	Spec     synth.Spec `json:"spec"`
	// Generator is the configured model id; GeneratorModel the id returned by the API.
	Generator        string            `json:"generator"`
	GeneratorFamily  string            `json:"generator_family"`
	GeneratorModel   string            `json:"generator_model,omitempty"`
	GeneratorSetting string            `json:"generator_setting,omitempty"`
	PromptHashes     map[string]string `json:"prompt_hashes,omitempty"`
	ExemplarPair     int               `json:"exemplar_pair,omitempty"`
	Exemplars        []string          `json:"exemplars,omitempty"`
	Discussion       synth.Discussion  `json:"discussion"`
	Stats            synth.GraphStats  `json:"stats"`
	Attempts         []sourceAttempt   `json:"attempts,omitempty"`
	Accepted         bool              `json:"accepted"`
	Reason           string            `json:"reason,omitempty"`
	DuplicateOf      string            `json:"duplicate_of,omitempty"`
	EmbeddingModel   string            `json:"embedding_model,omitempty"`
	// G1 re-annotation of the accepted attempt.
	Agreement *synth.GraphAgreement `json:"g1_agreement,omitempty"`
	Verifier  *synth.VerifierMeta   `json:"g1_verifier,omitempty"`
	QT30      bool                  `json:"qt30,omitempty"`
	Episode   string                `json:"episode,omitempty"`
	NodesetID string                `json:"nodeset_id,omitempty"`
	Pilot     bool                  `json:"pilot,omitempty"`
	// ReplacementOf / ReplacedBy link a rejected or duplicate source and its replacement (review M2).
	ReplacementOf string `json:"replacement_of,omitempty"`
	ReplacedBy    string `json:"replaced_by,omitempty"`
}

func (s *sourceRecord) prefix() string {
	if s.QT30 {
		return "q" + s.NodesetID + "."
	}
	return fmt.Sprintf("s%d.", s.Index)
}

// sourceAttempt is one G1 generation attempt with its checks.
type sourceAttempt struct {
	Attempt    int                     `json:"attempt"`
	Model      string                  `json:"model,omitempty"`
	Usage      llm.Usage               `json:"usage"`
	Error      string                  `json:"error,omitempty"`
	Targets    []string                `json:"target_problems,omitempty"`
	Copy       *synth.CopyResult       `json:"copy_check,omitempty"`
	Annotation *synth.SourceAnnotation `json:"g1_annotation,omitempty"`
	Agreement  *synth.GraphAgreement   `json:"g1_agreement,omitempty"`
	Verifier   *synth.VerifierMeta     `json:"g1_verifier,omitempty"`
	Discussion *synth.Discussion       `json:"discussion,omitempty"`
}

func newTripletRecord(sr *sourceRecord, s slotSpec) *pilotTriplet {
	return &pilotTriplet{
		Source: s.source, Type: s.typ, ID: fmt.Sprintf("%s-t%d", sr.SourceID, s.k), SourceID: sr.SourceID,
		Seed: sr.Spec.Seed, Generator: sr.Generator, GeneratorFamily: sr.GeneratorFamily,
		ExemplarPair: sr.ExemplarPair, Exemplars: sr.Exemplars, Domain: sr.Spec.Domain, Format: sr.Spec.Format,
		TopicID: sr.Spec.TopicID, SubPlanned: s.sub, ReplacementOf: s.replaces,
		Flags: tripletFlags{QT30: sr.QT30, Episode: sr.Episode, Pilot: sr.Pilot},
	}
}

func runPilot(o pilotOptions) error {
	if o.budget < 0 {
		o.budget = defaultBudget(o.sources)
	}
	if o.promptVersion == synth.V1 {
		return runPilotV1(o)
	}
	env, err := newRealEnv(o)
	if err != nil {
		return err
	}
	return runPilotV2(context.Background(), o, env)
}

// ---------------------------------------------------------------------------------------------
// v1: the pilot-2 pipeline (luna only), kept for cache replay.

func runPilotV1(o pilotOptions) error {
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../aif-with-llm/.env") // reuse the previous experiment's .env (OpenAI key)
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return errors.New("pilot: OPENAI_API_KEY が無い")
	}
	model := cmp(os.Getenv("GCJ_MODEL"), "gpt-5.6-luna")
	ctx := context.Background()

	base, err := llm.NewOpenAI(llm.OpenAIConfig{APIKey: key, MaxRetries: 2, Timeout: 5 * time.Minute})
	if err != nil {
		return err
	}
	client := llm.NewCachingClient(llm.NewThrottledClient(base, 4, 4, llm.DefaultRetryConfig()), llm.NewFileCache(".cache/llm"))
	embBase, err := llm.NewOpenAIEmbedder(llm.OpenAIEmbedderConfig{
		OpenAIConfig: llm.OpenAIConfig{APIKey: key, MaxRetries: 2, Timeout: 2 * time.Minute},
		Model:        "text-embedding-3-small",
	})
	if err != nil {
		return err
	}
	emb := llm.NewCachingEmbedder(embBase, ".cache/embed")

	g := &synth.Generator{Client: client, Model: model, Version: synth.V1}
	for _, p := range []struct {
		dst  *synth.Prompt
		name string
	}{{&g.SourcePrompt, "synth_source_v1"}, {&g.RealizePrompt, map[bool]string{false: "realize_v1", true: "realize_v2"}[o.verify]},
		{&g.JudgePrompt, "judge_v1"}, {&g.VerifyPrompt, "verify_v1"}} {
		if *p.dst, err = synth.LoadPrompt("prompts", p.name); err != nil {
			return err
		}
	}
	mt := newMeter(o.budget)
	rng := rand.New(rand.NewPCG(o.seed, 0xb11d))

	// ---- G1 source arguments ----
	specs := make([]synth.Spec, o.sources)
	for i := range specs {
		f := v1Formats[rng.IntN(len(v1Formats))]
		sp := 2 + rng.IntN(3)
		if f == v1Formats[4] {
			sp = 1
		}
		props := 6 + rng.IntN(5)
		if rng.IntN(2) == 1 {
			props = 11 + rng.IntN(8)
		}
		specs[i] = synth.Spec{Format: f, Domain: domains[rng.IntN(len(domains))], Speakers: sp, Props: props,
			Structure: structures[rng.IntN(len(structures))]}
	}
	sources := make([]synth.Discussion, o.sources)
	srcErr := parallel(o.sources, 8, func(i int) error {
		var last error
		for attempt := range 3 {
			if err := mt.check(); err != nil {
				return err
			}
			d, resp, err := g.GenerateSource(ctx, specs[i], fmt.Sprintf("s%d.", i), attempt)
			mt.add("synth_source", resp)
			if err == nil {
				sources[i] = d
				return nil
			}
			last = err
		}
		return last
	})
	okSrc := 0
	for _, e := range srcErr {
		if e == nil {
			okSrc++
		}
	}
	fmt.Fprintf(os.Stderr, "sources: %d/%d ok, cost $%.3f\n", okSrc, o.sources, mt.total())

	var trips []*pilotTriplet
	var vstats *verifyStats
	if o.verify {
		// ---- G2–G4 edit plan, realization, verification (per slot) ----
		trips, vstats = buildVerified(ctx, g, mt, sources, srcErr, rng, o.seed)
	} else {
		// ---- G2 edit plan ----
		for i, src := range sources {
			if srcErr[i] != nil {
				continue
			}
			for range 4 {
				typ := perturb.DrawType(rng)
				ka, kb := perturb.Plan(rng, typ)
				t := &pilotTriplet{Source: i, Type: typ}
				if typ == perturb.T1Monotone {
					steps := perturb.Build(rng, src.Graph, kb)
					t.A, t.B = steps[min(len(ka), len(steps))-1], steps[len(steps)-1]
				} else {
					t.A = lastOf(perturb.Build(rng, src.Graph, ka))
					t.B = lastOf(perturb.Build(rng, src.Graph, kb))
				}
				t.EditsA, t.EditsB = t.A.Edits, t.B.Edits
				trips = append(trips, t)
			}
		}

		// ---- G3 realization ----
		parallel(len(trips), 8, func(i int) error {
			t := trips[i]
			src := sources[t.Source]
			prefix := fmt.Sprintf("s%d.", t.Source)
			for _, side := range []struct {
				v    perturb.Variant
				dst  *synth.Discussion
				stat *synth.RealizeStats
			}{{t.A, &t.TextA, &t.StatA}, {t.B, &t.TextB, &t.StatB}} {
				if err := mt.check(); err != nil {
					t.Err = err.Error()
					return err
				}
				d, st, resp, err := g.RealizeV1(ctx, src, side.v, prefix, 0, "")
				mt.add("realize", resp)
				if err != nil {
					t.Err = "realize: " + err.Error()
					return err
				}
				*side.dst, *side.stat = d, st
			}
			return nil
		})
	}
	fmt.Fprintf(os.Stderr, "realized, cost $%.3f\n", mt.total())

	// ---- Embeddings (proposition cos) ----
	vec, err := embedAll(ctx, emb, sources, trips)
	if err != nil {
		return err
	}

	// ---- J judgments (2 presentation orders) ----
	parallel(len(trips), 8, func(i int) error {
		t := trips[i]
		if t.Err != "" {
			return nil
		}
		s := sources[t.Source]
		t.Raw = make([]string, 2)
		for k, pair := range [][2]synth.Discussion{{t.TextA, t.TextB}, {t.TextB, t.TextA}} {
			if err := mt.check(); err != nil {
				t.Err = err.Error()
				return err
			}
			ans, resp, err := g.JudgeAnswer(ctx, s, pair[0], pair[1])
			mt.add("judge", resp)
			if err != nil {
				t.Err = "judge: " + err.Error()
				return err
			}
			t.Raw[k] = ans
		}
		return nil
	})
	fmt.Fprintf(os.Stderr, "judged, cost $%.3f\n", mt.total())

	for _, t := range trips {
		if t.Err != "" {
			continue
		}
		src := sources[t.Source]
		for _, side := range []struct {
			v perturb.Variant
			d synth.Discussion
		}{{t.A, t.TextA}, {t.B, t.TextB}} {
			if changesGraph(side.v.Edits) && slices.Equal(side.d.Utterances, src.Utterances) {
				t.Unrealized = true
			}
		}
	}

	// ---- Estimation ----
	var ft []fit.Triplet
	var js []fit.Judgment
	var kept []*pilotTriplet
	for _, t := range trips {
		if t.Err != "" {
			continue
		}
		s := sources[t.Source]
		idx := len(ft)
		ft = append(ft, fit.Triplet{
			Source: t.Source, Ref: s.Graph, A: t.TextA.Graph, B: t.TextB.Graph,
			SimA: simFromVec(s.Graph, t.TextA.Graph, vec), SimB: simFromVec(s.Graph, t.TextB.Graph, vec),
			NegDiff: float64(t.B.Neg - t.A.Neg),
		})
		kept = append(kept, t)
		for k, ord := range []float64{1, -1} {
			js = append(js, fit.Judgment{Triplet: idx, Judge: 0, Order: ord, Answer: mapAnswer(t.Raw[k], ord)})
		}
	}
	if len(ft) < 10 {
		return fmt.Errorf("pilot: 使える三つ組が %d しかない", len(ft))
	}
	opts := fit.Options{Judges: 1, Theta0: ged.Current(), Lambda: o.lambda, FitNeg: true, MaxOuter: 20, AuxRidge: 0.5}
	res, err := fit.Fit(ft, js, opts, nil)
	if err != nil {
		return err
	}
	boot, err := fit.Bootstrap(ft, js, opts, res.Params, o.boot, o.seed+100)
	if err != nil {
		return err
	}
	cv, err := fit.CrossValidate(ft, js, opts, 5, o.seed)
	if err != nil {
		return err
	}
	d0, _ := fit.Delta(ft, ged.Current())
	dEst, _ := fit.Delta(ft, res.Params.Theta)
	naive := naiveTheta()
	dNaive, _ := fit.Delta(ft, naive)

	types, sign, neg := tripletMasks(kept, ft, d0)

	if err := writeProfiles(filepath.Join(o.out, "profile.csv"), ft, js, opts, res.Params); err != nil {
		return err
	}

	// Excluding triplets whose realization failed a mechanical check (a simple check in place of plan G4).
	var keepIdx []int
	for i, t := range kept {
		if !t.Unrealized {
			keepIdx = append(keepIdx, i)
		}
	}
	ft2, js2 := pick(ft, js, keepIdx)
	res2, err := fit.Fit(ft2, js2, opts, nil)
	if err != nil {
		return err
	}
	boot2, err := fit.Bootstrap(ft2, js2, opts, res2.Params, o.boot, o.seed+200)
	if err != nil {
		return err
	}
	cv2, err := fit.CrossValidate(ft2, js2, opts, 5, o.seed)
	if err != nil {
		return err
	}
	d02, _ := fit.Delta(ft2, ged.Current())
	dNaive2, _ := fit.Delta(ft2, naive)
	neg2 := make([]bool, len(ft2))
	for i := range ft2 {
		neg2[i] = ft2[i].NegDiff != 0
	}

	// ---- Report ----
	var b bytes.Buffer
	fmt.Fprintf(&b, "# PoC: %s で通しの実行（元の議論 %d 件）\n", model, o.sources)
	fmt.Fprintf(&b, "\n- 元の議論: %d/%d 件を生成（構造の検査に通ったもの）\n", okSrc, o.sources)
	nErr := len(trips) - len(kept)
	fmt.Fprintf(&b, "- 三つ組: %d 組を計画し、%d 組を書き下し・判定まで通した（失敗 %d）\n", len(trips), len(kept), nErr)
	fmt.Fprintf(&b, "- 判定: %d 回（判定者 1 × 提示順 2）\n", len(js))
	fmt.Fprintf(&b, "- プロンプト: %s=%s, %s=%s, %s=%s", g.SourcePrompt.Name, g.SourcePrompt.Hash, g.RealizePrompt.Name, g.RealizePrompt.Hash, g.JudgePrompt.Name, g.JudgePrompt.Hash)
	if o.verify {
		fmt.Fprintf(&b, ", %s=%s", g.VerifyPrompt.Name, g.VerifyPrompt.Hash)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 正則化 λ = %.2f（θ₀ を中心）、補助パラメータ（log β・log τ・π）に係数 %.2f の L2、交互最適化 %d 回（収束: %v、循環: %v）\n", o.lambda, opts.AuxRidge, res.Outer, res.Converged, res.Cycled)

	writeCostTable(&b, mt, []string{"synth_source", "realize", "verify", "judge"})
	writeCostProjection(&b, mt, projectionFor(o, nil))
	if vstats != nil {
		writeVerifyReport(&b, vstats, kept, d0)
	}

	var connective, missing, changed, realized int
	for _, t := range kept {
		for _, st := range []synth.RealizeStats{t.StatA, t.StatB} {
			if st.Changed > 0 {
				realized++
			}
			changed += st.Changed
			connective += st.Connective
			missing += st.MissingText
		}
	}
	fmt.Fprintln(&b, "\n## 書き下しの機械検査")
	fmt.Fprintf(&b, "\n- LLM で書き下した変種 %d 個、変更した発話 %d（うち接続の手直し %d）\n", realized, changed, connective)
	fmt.Fprintf(&b, "- 命題の新しい本文が返らなかったもの: %d（元の本文で代用した）\n", missing)
	unrealized := map[perturb.Kind][2]int{}
	for _, t := range kept {
		src := sources[t.Source]
		for _, side := range []struct {
			v perturb.Variant
			d synth.Discussion
		}{{t.A, t.TextA}, {t.B, t.TextB}} {
			same := slices.Equal(side.d.Utterances, src.Utterances)
			seen := map[perturb.Kind]bool{}
			for _, e := range side.v.Edits {
				if seen[e.Kind] || e.Kind == perturb.XSpk {
					continue
				}
				seen[e.Kind] = true
				c := unrealized[e.Kind]
				c[0]++
				if same {
					c[1]++
				}
				unrealized[e.Kind] = c
			}
		}
	}
	fmt.Fprintln(&b, "- 計画にある別系列のモデルによる検証と自然さの評価（G4）は、この PoC では行っていない。代わりに「命題グラフを変える編集があるのに、テキストが元の議論と 1 文字も変わっていない変種」を数えた（書き下しの失敗の下限）")
	fmt.Fprintln(&b, "\n| 編集 | その編集を含む変種 | テキストが元のまま |")
	fmt.Fprintln(&b, "| --- | --: | --: |")
	for _, k := range slices.Sorted(maps.Keys(unrealized)) {
		c := unrealized[k]
		fmt.Fprintf(&b, "| %s | %d | %d（%.0f%%） |\n", k, c[0], c[1], pct(c[1], c[0]))
	}
	fmt.Fprintf(&b, "\nこの失敗を含む三つ組は %d / %d 組。\n", len(kept)-len(keepIdx), len(kept))

	judgeDiagnostics(&b, []string{model}, types, sign, js)
	agreementTable(&b, []agreeRow{
		{"R0 現行のコスト表", d0},
		{"R1 素朴な一様コスト（種別違い・向き違い = 2.0）", dNaive},
		{"R2 学習したコスト（学習に使ったデータ）", dEst},
		{"R2 学習したコスト（5 分割交差検証）", cv},
	}, js, neg)
	paramTable(&b, ged.Current(), nil, nil, res.Params, boot, o.m)
	auxTable(&b, []string{model}, res.Params, nil)
	writeTypeBreakdown(&b, kept, js, d0)

	fmt.Fprintf(&b, "\n# 書き下しの失敗を除いた場合（%d 組）\n", len(ft2))
	fmt.Fprintf(&b, "\n交互最適化 %d 回（収束: %v、循環: %v）\n", res2.Outer, res2.Converged, res2.Cycled)
	agreementTable(&b, []agreeRow{
		{"R0 現行のコスト表", d02},
		{"R1 素朴な一様コスト（種別違い・向き違い = 2.0）", dNaive2},
		{"R2 学習したコスト（5 分割交差検証）", cv2},
	}, js2, neg2)
	paramTable(&b, ged.Current(), nil, nil, res2.Params, boot2, o.m)

	if err := os.WriteFile(filepath.Join(o.out, "pilot.md"), b.Bytes(), 0o644); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		v    any
	}{{"pilot_sources.json", sources}, {"pilot_triplets.json", trips}} {
		if err := writeJSON(filepath.Join(o.out, f.name), f.v); err != nil {
			return err
		}
	}
	fmt.Print(b.String())
	if fails := errors.Join(srcErr...); fails != nil && errors.Is(fails, errBudget) {
		return errBudget
	}
	return nil
}

func naiveTheta() ged.Theta {
	naive := ged.Current()
	naive[ged.PTypeRACA], naive[ged.PTypeRAMA], naive[ged.PTypeCAMA], naive[ged.PDirection] = 2, 2, 2, 2
	return naive
}

func tripletMasks(kept []*pilotTriplet, ft []fit.Triplet, d0 []float64) ([]perturb.TripletType, []int, []bool) {
	types := make([]perturb.TripletType, len(kept))
	sign := make([]int, len(kept))
	neg := make([]bool, len(kept))
	for i, t := range kept {
		types[i] = t.Type
		if t.Type == perturb.T1Monotone && math.Abs(d0[i]) > 1e-9 {
			sign[i] = 1 // A's edits ⊂ B's edits, so A being closer is correct
		}
		neg[i] = ft[i].NegDiff != 0
	}
	return types, sign, neg
}

// embedAll embeds every proposition text of the sources and the realized variants.
func embedAll(ctx context.Context, emb llm.Embedder, sources []synth.Discussion, trips []*pilotTriplet) (map[string][]float32, error) {
	textSet := map[string]bool{}
	for _, s := range sources {
		for _, p := range s.Graph.Props {
			textSet[p.Text] = true
		}
	}
	for _, t := range trips {
		if t.Err != "" {
			continue
		}
		for _, p := range append(slices.Clone(t.TextA.Graph.Props), t.TextB.Graph.Props...) {
			textSet[p.Text] = true
		}
	}
	texts := make([]string, 0, len(textSet))
	for s := range textSet {
		if s != "" {
			texts = append(texts, s)
		}
	}
	sort.Strings(texts)
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("pilot: embed: %w", err)
	}
	vec := make(map[string][]float32, len(texts))
	for i, s := range texts {
		vec[s] = vecs[i]
	}
	return vec, nil
}

// ---------------------------------------------------------------------------------------------
// v2 environment: one client per model (cache → throttle → provider) and the embedder.

type pilotEnv struct {
	clients map[string]llm.Client
	// cached reports whether req is already in the response cache (nil: unknown → treated as a miss).
	cached  func(c llm.Client, req llm.Request) bool
	emb     llm.Embedder
	pricing *llm.PriceTable
}

func (o pilotOptions) allModels() []string {
	var ms []string
	for _, m := range append(slices.Clone(o.generators), o.judges...) {
		if !slices.Contains(ms, m) {
			ms = append(ms, m)
		}
	}
	return ms
}

func newRealEnv(o pilotOptions) (*pilotEnv, error) {
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../aif-with-llm/.env")
	cache := llm.NewFileCache(".cache/llm")
	env := &pilotEnv{clients: map[string]llm.Client{}, pricing: llm.DefaultPricing()}
	for _, m := range o.allModels() {
		base, err := llm.NewClientForModel(m, llm.ClientConfig{MaxRetries: 2, Timeout: 5 * time.Minute, BatchStateDir: ".cache/llm-batches"})
		if err != nil {
			return nil, fmt.Errorf("pilot: %s: %w", m, err)
		}
		env.clients[m] = llm.NewCachingClient(llm.NewThrottledClient(base, 4, 4, llm.DefaultRetryConfig()), cache)
	}
	env.cached = func(c llm.Client, req llm.Request) bool {
		if d, ok := c.(llm.RequestDefaulter); ok {
			req = d.ApplyDefaults(req)
		}
		_, err := cache.Get(req.Tag, llm.CacheKey(req))
		return err == nil
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, errors.New("pilot: OPENAI_API_KEY が無い（埋め込みに使う）")
	}
	embBase, err := llm.NewOpenAIEmbedder(llm.OpenAIEmbedderConfig{
		OpenAIConfig: llm.OpenAIConfig{APIKey: key, MaxRetries: 2, Timeout: 2 * time.Minute},
		Model:        "text-embedding-3-small",
	})
	if err != nil {
		return nil, err
	}
	env.emb = llm.NewCachingEmbedder(embBase, ".cache/embed")
	return env, nil
}

// v2run holds the configured models and prompts of a v2 run.
type v2run struct {
	o         pilotOptions
	env       *pilotEnv
	mt        *meter
	gens      map[string]*synth.Generator
	judges    []synth.Judge
	exemplars []synth.Exemplar
	prompts   map[string]synth.Prompt
}

func newV2Run(o pilotOptions, env *pilotEnv) (*v2run, error) {
	r := &v2run{o: o, env: env, mt: newMeter(o.budget), gens: map[string]*synth.Generator{}, prompts: map[string]synth.Prompt{}}
	dir := cmp(o.promptDir, "prompts")
	for _, name := range []string{"synth_source_v2", "realize_v3", "verify_v2", "verify_source_v1", "judge_v1", "synth_topics_v1"} {
		p, err := synth.LoadPrompt(dir, name)
		if err != nil {
			return nil, err
		}
		r.prompts[name] = p
	}
	exs, err := synth.LoadExemplars(filepath.Join(dir, "exemplars"))
	if err != nil {
		return nil, err
	}
	r.exemplars = exs
	if len(o.generators) == 0 {
		return nil, errors.New("pilot: no generator")
	}
	for i, m := range o.generators {
		c, ok := env.clients[m]
		if !ok {
			return nil, fmt.Errorf("pilot: no client for %s", m)
		}
		g := &synth.Generator{Client: c, Model: m, Effort: o.genEffort[m], Thinking: o.genThinking[m], Version: synth.V2,
			SourcePrompt: r.prompts["synth_source_v2"], RealizePrompt: r.prompts["realize_v3"], VerifyPrompt: r.prompts["verify_v2"],
			VerifySourcePrompt: r.prompts["verify_source_v1"], JudgePrompt: r.prompts["judge_v1"], TopicPrompt: r.prompts["synth_topics_v1"],
			Exemplars: exs}
		// The verifier is the other family (dataset-spec §4.1): the next generator of a different family.
		for k := 1; k < len(o.generators); k++ {
			v := o.generators[(i+k)%len(o.generators)]
			if synth.FamilyOf(v) != synth.FamilyOf(m) {
				a := synth.NewAgent(env.clients[v], v, o.genEffort[v], o.genThinking[v])
				g.Verifier = &a
				break
			}
		}
		if g.Verifier == nil {
			return nil, fmt.Errorf("pilot: no generator of another family than %s to verify it (dataset-spec §4.1: the verifier is never the realizer's family); add one to -generators", m)
		}
		r.gens[m] = g
	}
	for _, m := range o.judges {
		c, ok := env.clients[m]
		if !ok {
			return nil, fmt.Errorf("pilot: no client for judge %s", m)
		}
		r.judges = append(r.judges, synth.Judge{Agent: synth.NewAgent(c, m, o.judgeEffort[m], o.judgeThinking[m]), Prompt: r.prompts["judge_v1"]})
	}
	return r, nil
}

func (r *v2run) promptHashes() map[string]string {
	out := map[string]string{}
	for n, p := range r.prompts {
		out[n] = p.Hash
	}
	return out
}

func runPilotV2(ctx context.Context, o pilotOptions, env *pilotEnv) error {
	r, err := newV2Run(o, env)
	if err != nil {
		return err
	}
	mt := r.mt

	// ---- allocation (dataset-spec §1.2) ----
	var exNames []string
	for _, ex := range r.exemplars {
		exNames = append(exNames, ex.Names()...)
	}
	names := synth.NewNameAllocator(synth.NamePool, exNames, rand.New(rand.NewPCG(o.seed, 0x4a4e)))
	specs, err := synth.Allocate(synth.AllocOptions{N: o.sources, Seed: o.seed, Generators: o.generators, Exemplars: r.exemplars, Names: names})
	if err != nil {
		return err
	}
	var topics *synth.TopicQueue
	if len(specs) > 0 {
		bank, err := readTopics(cmp(o.topicBank, filepath.Join(o.out, "topics.json")))
		if err != nil {
			return fmt.Errorf("pilot: 話題バンクが読めない（先に -topics を実行する）: %w", err)
		}
		topics = synth.NewTopicQueue(bank)
		if err := synth.AssignTopics(specs, topics); err != nil {
			return err
		}
	}

	// ---- G1 sources: generate, targets, copy check, cross-family re-annotation; dedup; replenish ----
	jobs := make([]sourceJob, len(specs))
	for i, sp := range specs {
		jobs[i] = sourceJob{spec: sp, index: i}
	}
	all := r.generateSources(ctx, jobs)
	if err := r.dedup(ctx, all); err != nil {
		return err
	}
	if all, err = r.replenishSources(ctx, all, topics); err != nil {
		return err
	}
	var accepted []*sourceRecord
	for _, s := range all {
		if s.Accepted {
			accepted = append(accepted, s)
		}
	}
	fmt.Fprintf(os.Stderr, "sources: %d/%d accepted, cost $%.3f\n", len(accepted), len(all), mt.total())
	if o.withQT30 {
		qs, err := r.qt30Sources(len(all))
		if err != nil {
			return err
		}
		all = append(all, qs...)
		accepted = append(accepted, qs...)
	}
	for i, s := range accepted {
		s.Index = i // slot index (the fit's source cluster)
	}

	// ---- G2–G4 plans, realization, verification ----
	pp := &pipelineV2{ctx: ctx, gens: r.gens, mt: mt, emb: env.emb, sources: accepted, seed: o.seed, stats: newV2Stats(),
		catalogue: catalogueOptions(o.diagShare)}
	trips, err := pp.buildAll(rand.New(rand.NewPCG(o.seed, 0xb11d)))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "realized, cost $%.3f\n", mt.total())
	var kept []*pilotTriplet
	for _, t := range trips {
		if t.Err == "" {
			kept = append(kept, t)
		}
	}

	// ---- J judgments: every judge × both orders; Anthropic judges in Message Batches ----
	js, judgeErr := r.judgeAll(ctx, kept, accepted)
	fmt.Fprintf(os.Stderr, "judged, cost $%.3f\n", mt.total())

	// ---- records ----
	if err := writeJSONL(filepath.Join(o.out, "pilot_sources.jsonl"), all); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "pilot_triplets.jsonl"), trips); err != nil {
		return err
	}

	// ---- report ----
	var b bytes.Buffer
	title := "本番"
	if o.p2 {
		title = "P2 パイロット（θ を出さない報告）"
	}
	fmt.Fprintf(&b, "# %s: 改訂版の生成パイプライン（元の議論 %d 件を割り付け）\n", title, o.sources)
	r.writeProvenance(&b)
	writeCostTable(&b, mt, []string{"synth_topics", "synth_source", "verify_source", "realize", "verify", "judge"})
	writeCostProjection(&b, mt, projectionFor(o, env.pricing))
	writeSourceReport(&b, all)
	ft, d0 := fitTriplets(kept, accepted)
	writeVerifyReportV2(&b, pp.stats, kept, d0)
	writeT1Report(&b, kept, js, o.judges)
	writeJudgeFailures(&b, kept, o.judges)
	if judgeErr != nil {
		fmt.Fprintf(&b, "\n**判定の一部が失敗した**: %v\n", judgeErr)
	}
	if err := r.writeFitReport(&b, kept, ft, js, d0, pp.stats); err != nil {
		fmt.Fprintf(&b, "\n（当てはめを行わなかった: %v）\n", err)
	}
	if err := os.WriteFile(filepath.Join(o.out, "pilot.md"), b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	if errors.Is(judgeErr, errBudget) || mt.check() != nil {
		return errBudget
	}
	return nil
}

// sourceJob is one G1 generation: a spec of the allocation (index = its position, source id s%04d) or the
// replacement of a rejected / duplicate source (index after the allocation, attempts continued so that the
// requests differ from the rejected ones).
type sourceJob struct {
	spec          synth.Spec
	index         int
	firstAttempt  int
	replacementOf string
}

// generateSources runs G1 for every job: up to 3 attempts, each checked against the numeric targets,
// the exemplar copy rules (§1.5.2) and the other family's blind re-annotation (§4.3). A retry keeps
// the spec and its topic (the rejected topic goes first to the replacement, §1.3).
func (r *v2run) generateSources(ctx context.Context, jobs []sourceJob) []*sourceRecord {
	out := make([]*sourceRecord, len(jobs))
	parallel(len(jobs), 8, func(j int) error {
		sp, i := jobs[j].spec, jobs[j].index
		g := r.gens[sp.Generator]
		rec := &sourceRecord{SourceID: fmt.Sprintf("s%04d", i), Index: i, Spec: sp, Generator: sp.Generator,
			GeneratorFamily: synth.FamilyOf(sp.Generator), GeneratorSetting: g.Writer().Setting(), PromptHashes: r.promptHashes(),
			ExemplarPair: sp.ExemplarPair.ID, Exemplars: sp.ExemplarPair.Order, EmbeddingModel: r.env.emb.Model(), Pilot: r.o.p2,
			ReplacementOf: jobs[j].replacementOf}
		out[j] = rec
		prefix := fmt.Sprintf("s%d.", i)
		for attempt := jobs[j].firstAttempt; attempt < jobs[j].firstAttempt+3; attempt++ {
			if err := r.mt.check(); err != nil {
				rec.Reason = err.Error()
				return err
			}
			att := sourceAttempt{Attempt: attempt}
			d, resp, err := g.GenerateSource(ctx, sp, prefix, attempt)
			r.mt.add("synth_source", resp)
			if resp != nil {
				att.Model, att.Usage = resp.Model, resp.Usage
			}
			if err != nil {
				att.Error = err.Error()
				rec.Attempts = append(rec.Attempts, att)
				rec.Reason = "generation error"
				continue
			}
			att.Discussion = &d
			att.Targets = synth.TargetProblems(d, sp)
			if len(att.Targets) > 0 {
				rec.Attempts = append(rec.Attempts, att)
				rec.Reason = "numeric targets"
				continue
			}
			cp, err := synth.CopyCheck(ctx, d, r.exemplars, r.env.emb)
			att.Copy = &cp
			if err != nil || cp.Reject {
				if err != nil {
					att.Error = err.Error()
				}
				rec.Attempts = append(rec.Attempts, att)
				rec.Reason = "exemplar copy"
				continue
			}
			ann, agr, meta, vresp, err := g.VerifySource(ctx, d)
			r.mt.add("verify_source", vresp)
			att.Annotation, att.Agreement, att.Verifier = &ann, &agr, &meta
			if err != nil {
				att.Error = "verify_source: " + err.Error()
				rec.Attempts = append(rec.Attempts, att)
				rec.Reason = "G1 verification error"
				continue
			}
			rec.Attempts = append(rec.Attempts, att)
			if !agr.Pass {
				rec.Reason = "G1 re-annotation"
				continue
			}
			rec.Discussion, rec.Stats, rec.Accepted, rec.Reason = d, synth.Stats(d), true, ""
			rec.GeneratorModel, rec.Agreement, rec.Verifier = att.Model, &agr, &meta
			att.Discussion = nil // the accepted text is rec.Discussion
			rec.Attempts[len(rec.Attempts)-1] = att
			return nil
		}
		return nil
	})
	return out
}

// Source dedup thresholds (dataset-spec §1.3): rendered-text cos > 0.9 with an accepted source, or
// ≥ 50% of the propositions matched at cos > 0.9 (checked only for pairs with text cos > 0.6).
const (
	dedupTextCos = 0.9
	dedupPropCos = 0.9
	dedupGate    = 0.6
)

// dedup marks accepted sources that duplicate an earlier accepted one.
func (r *v2run) dedup(ctx context.Context, all []*sourceRecord) error {
	var acc []*sourceRecord
	for _, s := range all {
		if s.Accepted {
			acc = append(acc, s)
		}
	}
	if len(acc) < 2 {
		return nil
	}
	texts := make([]string, len(acc))
	var props []string
	for i, s := range acc {
		texts[i] = s.Discussion.Render()
		for _, p := range s.Discussion.Graph.Props {
			props = append(props, p.Text)
		}
	}
	tv, err := r.env.emb.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("pilot: dedup embed: %w", err)
	}
	pv, err := r.env.emb.Embed(ctx, props)
	if err != nil {
		return fmt.Errorf("pilot: dedup embed: %w", err)
	}
	pvec := map[string][]float32{}
	for i, p := range props {
		pvec[p] = pv[i]
	}
	overlap := func(a, b *sourceRecord) float64 {
		n := 0
		for _, p := range a.Discussion.Graph.Props {
			for _, q := range b.Discussion.Graph.Props {
				if synth.Cosine(pvec[p.Text], pvec[q.Text]) > dedupPropCos {
					n++
					break
				}
			}
		}
		return float64(n) / float64(max(1, len(a.Discussion.Graph.Props)))
	}
	var kept []int
	for i, s := range acc {
		for _, j := range kept {
			c := synth.Cosine(tv[i], tv[j])
			if c > dedupTextCos || (c > dedupGate && overlap(s, acc[j]) >= 0.5) {
				s.Accepted, s.Reason, s.DuplicateOf = false, fmt.Sprintf("duplicate (text cos %.2f)", c), acc[j].SourceID
				break
			}
		}
		if s.Accepted {
			kept = append(kept, i)
		}
	}
	return nil
}

// maxReplaceRounds caps the replacement rounds of rejected sources and of discarded triplet slots.
const maxReplaceRounds = 3

// replenishSources replaces rejected and duplicate sources until -sources are accepted, the budget stops, or
// maxReplaceRounds rounds have run (review-synth-impl M2, dataset-spec §1.3 / §4.5). A replacement keeps the
// rejected spec's stratum (format, targets, generator, exemplar pair, names) and continues its attempt numbers;
// a rejected source passes its topic on (§1.3: a topic is consumed only by an accepted source), a duplicate takes
// the next topic of its domain. Each round is de-duplicated against every accepted source.
func (r *v2run) replenishSources(ctx context.Context, all []*sourceRecord, topics *synth.TopicQueue) ([]*sourceRecord, error) {
	for round := 1; round <= maxReplaceRounds; round++ {
		acc := 0
		for _, s := range all {
			if s.Accepted {
				acc++
			}
		}
		deficit := r.o.sources - acc
		if deficit <= 0 || r.mt.check() != nil {
			break
		}
		var jobs []sourceJob
		for _, s := range all {
			if len(jobs) == deficit {
				break
			}
			if s.Accepted || s.ReplacedBy != "" {
				continue
			}
			sp := s.Spec
			if s.DuplicateOf != "" && topics != nil {
				if t, ok := topics.Next(sp.Domain); ok {
					sp.TopicID, sp.Topic = t.ID, t.Question
				}
			}
			first := 0
			if n := len(s.Attempts); n > 0 {
				first = s.Attempts[n-1].Attempt + 1
			}
			j := sourceJob{spec: sp, index: len(all) + len(jobs), firstAttempt: first, replacementOf: s.SourceID}
			s.ReplacedBy = fmt.Sprintf("s%04d", j.index)
			jobs = append(jobs, j)
		}
		if len(jobs) == 0 {
			break
		}
		all = append(all, r.generateSources(ctx, jobs)...)
		if err := r.dedup(ctx, all); err != nil {
			return all, err
		}
		fmt.Fprintf(os.Stderr, "replacement round %d: %d sources\n", round, len(jobs))
	}
	return all, nil
}

// qt30Sources loads the transfer set written by -import-qt30; generators alternate over nodesets.
func (r *v2run) qt30Sources(offset int) ([]*sourceRecord, error) {
	var qs []synth.QT30Source
	b, err := os.ReadFile(filepath.Join(r.o.out, "qt30_sources.json"))
	if err != nil {
		return nil, fmt.Errorf("pilot: QT30 の元の議論が無い（先に -import-qt30 を実行する）: %w", err)
	}
	if err := json.Unmarshal(b, &qs); err != nil {
		return nil, err
	}
	var out []*sourceRecord
	for i, q := range qs {
		gen := r.o.generators[i%len(r.o.generators)]
		out = append(out, &sourceRecord{SourceID: "q" + q.NodesetID, Index: offset + i, Generator: gen,
			GeneratorFamily: synth.FamilyOf(gen), GeneratorSetting: r.gens[gen].Writer().Setting(), PromptHashes: r.promptHashes(),
			Spec:       synth.Spec{Format: "TV debate with a moderator", Domain: "qt30", Generator: gen},
			Discussion: q.Discussion, Stats: synth.Stats(q.Discussion), Accepted: true, QT30: true, Episode: q.Episode,
			NodesetID: q.NodesetID, EmbeddingModel: r.env.emb.Model(), Pilot: r.o.p2})
	}
	return out, nil
}

// judgeAll asks every judge for both presentation orders of every kept triplet. Anthropic judges go
// through CompleteBatch (one batch per judge per run, all judges concurrently; cached requests are
// served by the CachingClient). Before anything is submitted, the projected cost of the uncached batch
// requests is checked against the remaining budget; a batch that does not fit is not submitted.
// Other judges run request by request with the meter checked before each call.
func (r *v2run) judgeAll(ctx context.Context, kept []*pilotTriplet, sources []*sourceRecord) ([]fit.Judgment, error) {
	type item struct {
		t     int
		order int
	}
	type judgeRun struct {
		reqs     []llm.Request
		items    []item
		results  []llm.BatchResult
		useBatch bool
		reserved float64 // batch projection held in the meter until the results are metered
		skip     error
		err      error
	}
	runs := make([]*judgeRun, len(r.judges))
	for j, judge := range r.judges {
		jr := &judgeRun{}
		runs[j] = jr
		for ti, t := range kept {
			s := sources[t.Source].Discussion
			for _, order := range []int{1, -1} {
				first, second := t.TextA, t.TextB
				if order < 0 {
					first, second = second, first
				}
				req, err := judge.Request(s, first, second)
				if err != nil {
					return nil, err
				}
				jr.reqs = append(jr.reqs, req)
				jr.items = append(jr.items, item{ti, order})
			}
		}
		jr.results = make([]llm.BatchResult, len(jr.reqs))
		_, isBatch := judge.Client.(llm.BatchClient)
		jr.useBatch = isBatch && judge.Family == "anthropic"
		if jr.useBatch && len(jr.reqs) > 0 {
			proj := r.projectBatch(judge, jr.reqs)
			if rem := r.mt.remaining(); proj > rem {
				jr.skip = fmt.Errorf("%w: %s の判定 %d 件（未キャッシュ分の見込み $%.2f）が残りの予算 $%.2f を超えるので投げなかった",
					errBudget, judge.Model, len(jr.reqs), proj, rem)
				continue
			}
			r.mt.reserve(proj)
			jr.reserved = proj
		}
	}
	var wg sync.WaitGroup
	for j, judge := range r.judges {
		jr := runs[j]
		if jr.skip != nil || len(jr.reqs) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if jr.useBatch {
				// On a whole-call error the slice still holds every response obtained before the failure (llm
				// contract): they are paid for, so they are metered and recorded like the others (review M1).
				res, err := judge.Client.(llm.BatchClient).CompleteBatch(ctx, jr.reqs)
				for i, x := range res {
					r.mt.add("judge", x.Response)
					if i < len(jr.results) {
						jr.results[i] = x
					}
				}
				r.mt.release(jr.reserved)
				if err != nil {
					jr.err = fmt.Errorf("judge %s: batch: %w", judge.Model, err)
				}
				return
			}
			parallel(len(jr.reqs), 8, func(i int) error {
				if err := r.mt.check(); err != nil {
					jr.results[i].Err = err
					return err
				}
				resp, err := judge.Client.Complete(ctx, jr.reqs[i])
				r.mt.add("judge", resp)
				jr.results[i] = llm.BatchResult{Response: resp, Err: err}
				return nil
			})
		}()
	}
	wg.Wait()

	var js []fit.Judgment
	var errs []error
	for j, judge := range r.judges {
		jr := runs[j]
		if jr.skip != nil {
			errs = append(errs, jr.skip)
			continue
		}
		if jr.err != nil {
			errs = append(errs, jr.err)
		}
		failed := 0
		for i, res := range jr.results {
			it := jr.items[i]
			t := kept[it.t]
			rec := judgmentRecord{Judge: judge.Model, Family: judge.Family, Setting: judge.Setting(), Order: it.order,
				CacheKey: llm.CacheKey(applyDefaults(judge.Client, jr.reqs[i])), PromptHash: judge.Prompt.Hash,
				Time: time.Now().UTC().Format(time.RFC3339), Batch: jr.useBatch,
				VerifierFamilyEqJudgeFamily: t.VerifierFamily != "" && t.VerifierFamily == judge.Family}
			if res.Response != nil {
				rec.Model, rec.Usage, rec.Cached = res.Response.Model, res.Response.Usage, res.Response.Cached
			}
			var ans string
			err := res.Err
			if err == nil && res.Response == nil {
				err = errors.New("no result (the batch call failed before this item)")
			}
			if err == nil {
				ans, err = synth.ParseJudgeAnswer(res.Response)
			}
			if err != nil {
				rec.Error = err.Error()
				// A refusal or truncation is recorded as such and never becomes an answer (review M7).
				rec.Refusal, rec.Truncated = errors.Is(err, llm.ErrRefusal), errors.Is(err, llm.ErrTruncated)
				failed++
				t.Judgments = append(t.Judgments, rec)
				continue
			}
			a := mapAnswer(ans, float64(it.order))
			rec.Raw, rec.Answer = ans, a.String()
			if a == fit.AnswerTie {
				rec.Answer = "same"
			}
			t.Judgments = append(t.Judgments, rec)
			js = append(js, fit.Judgment{Triplet: it.t, Judge: j, Order: float64(it.order), Answer: a, Family: judge.Family})
		}
		if failed > 0 {
			errs = append(errs, fmt.Errorf("judge %s: %d/%d judgments failed", judge.Model, failed, len(jr.results)))
		}
	}
	return js, errors.Join(errs...)
}

func applyDefaults(c llm.Client, req llm.Request) llm.Request {
	if d, ok := c.(llm.RequestDefaulter); ok {
		return d.ApplyDefaults(req)
	}
	return req
}

// projectBatch estimates the batch cost of the requests that are not cached: input ≈ chars / 4 × 1.2
// (Claude tokenizer), output = the measured mean for this judge or 300 (dataset-spec §4.5 assumption).
func (r *v2run) projectBatch(judge synth.Judge, reqs []llm.Request) float64 {
	_, outTok, ok := r.mt.perCall("judge", judge.Model)
	if !ok {
		outTok = 300
	}
	total := 0.0
	for _, req := range reqs {
		if r.env.cached != nil && r.env.cached(judge.Client, req) {
			continue
		}
		chars := 0
		for _, m := range req.Messages {
			chars += len(m.Content)
		}
		u := llm.Usage{InputTokens: int64(float64(chars) / 4 * 1.2), OutputTokens: int64(outTok)}
		total += r.env.pricing.ChatCostBatch(judge.Model, u)
	}
	return total
}

// fitTriplets builds the fit input of the kept triplets (sources are the accepted list): every fit.Triplet
// contract field is copied from the record (decisions-stats §16 #16–17), so a re-fit from the records needs no
// other state than the cos tables.
func fitTriplets(kept []*pilotTriplet, sources []*sourceRecord) ([]fit.Triplet, []float64) {
	ft := make([]fit.Triplet, len(kept))
	d0 := make([]float64, len(kept))
	for i, t := range kept {
		s := sources[t.Source].Discussion
		ft[i] = fit.Triplet{Source: t.Source, Ref: s.Graph, A: t.TextA.Graph, B: t.TextB.Graph,
			SimA: t.simA, SimB: t.simB,
			NegDiff: float64(t.Flags.PNegB - t.Flags.PNegA),
			Type:    string(t.Type), Flags: t.fitFlagList(), Generator: t.Generator, VerifierFamily: t.VerifierFamily,
			Domain: t.Domain, CohA: float64(t.CohA), CohB: float64(t.CohB)}
		if t.ExemplarPair != 0 {
			ft[i].ExemplarPair = strconv.Itoa(t.ExemplarPair)
		}
		if t.Weight != nil {
			ft[i].Weight = *t.Weight
		}
		if t.T1Delta0 != nil {
			ft[i].T1Delta0 = *t.T1Delta0
		}
		if t.Delta0 != nil {
			d0[i] = *t.Delta0
		}
	}
	return ft, d0
}

// pilotExclude is fit.DefaultExclude without "pilot": a pilot run fits its own (pilot-flagged) triplets, which the
// main analysis drops by default; every other default exclusion (x_spk, diag_ca_type, qt30, del_add_cooccur) stays.
func pilotExclude() []string {
	return slices.DeleteFunc(slices.Clone(fit.DefaultExclude), func(f string) bool { return f == "pilot" })
}

// pilotFitOptions are the pre-registered fit settings of a run: the type tie (decisions-stats §12b), the default
// exclusions (pilotExclude) and the three starts θ₀ / R1 / all-0.5 (§11.3). The P2 fit drops the starts (θ fixed).
func (r *v2run) pilotFitOptions() fit.Options {
	th0 := ged.Current()
	starts, names := fit.DefaultStarts(th0)
	return fit.Options{Judges: len(r.judges), Theta0: th0, Lambda: r.o.lambda, FitNeg: true, MaxOuter: 20,
		Tie: fit.TypeTie(), Exclude: pilotExclude(), Starts: starts, StartNames: names}
}

// writeFitSet reports which triplets enter the fit.
func writeFitSet(b *bytes.Buffer, ft []fit.Triplet, js []fit.Judgment, opts fit.Options) {
	fs, fj, _ := fit.Filter(ft, js, opts)
	var counts []string
	for _, kv := range sortedKV(fit.ExclusionCounts(ft, opts)) {
		counts = append(counts, kv[0]+" "+kv[1])
	}
	fmt.Fprintf(b, "\n- 当てはめに使う三つ組 %d / %d、判定 %d / %d（除いた三つ組: %s）。種別のタイ（RA↔CA、CA↔MA を RA↔MA に、決定文書 §12b）\n",
		len(fs), len(ft), len(fj), len(js), strings.Join(counts, "、"))
}

// writeFitReport fits the pairwise model. In P2 mode θ is fixed at θ₀ and the P2 report of decisions-stats
// §16 #12 is written (no θ); otherwise the confirmatory analysis (writeConfirmatory) and the untied diagnostic
// fit of §12b run.
func (r *v2run) writeFitReport(b *bytes.Buffer, kept []*pilotTriplet, ft []fit.Triplet, js []fit.Judgment, d0 []float64, st *v2Stats) error {
	if len(ft) < 10 || len(js) == 0 {
		return fmt.Errorf("使える三つ組が %d、判定が %d しかない", len(ft), len(js))
	}
	o := r.o
	types, sign, neg := tripletMasks(kept, ft, d0)
	judgeDiagnostics(b, o.judges, types, sign, js)
	dNaive, _ := fit.Delta(ft, naiveTheta())
	opts := r.pilotFitOptions()
	writeICC(b, kept, js, d0)
	if o.p2 {
		// θ fixed at θ₀; the weak aux ridge of the pilots keeps β finite on ≈ 100 triplets.
		opts.FixTheta, opts.AuxRidge, opts.Starts, opts.StartNames = true, 0.5, nil, nil
		aux, err := fit.Fit(ft, js, opts, nil)
		if err != nil {
			return err
		}
		in := p2Input{Names: o.judges, Aux: aux.Params, Tie: opts.Tie, MinInf: fit.DefaultMinInformative(), Discards: st.discardRows()}
		in.Trips, in.Js, _ = fit.Filter(ft, js, opts)
		if mix, err := fit.FitMixed(ft, js, opts, nil); err == nil {
			in.Mixed = &mix.Params
		} else {
			fmt.Fprintf(b, "\n（ランダム切片の当てはめに失敗した: %v）\n", err)
		}
		fmt.Fprintln(b, "\n## 補助パラメータ（θ = θ₀ に固定。P2 は θ を出さない）")
		writeFitSet(b, ft, js, opts)
		if err := writeP2Report(b, in); err != nil {
			return err
		}
		agreementTable(b, []agreeRow{{"R0 現行のコスト表", d0}, {"R1 素朴な一様コスト（種別違い・向き違い = 2.0）", dNaive}}, js, neg)
		writeTypeBreakdown(b, kept, js, d0)
		return nil
	}
	if err := r.writeConfirmatory(b, ft, js, opts); err != nil {
		return err
	}
	if err := r.writeDiagnosticFit(b, kept, ft, js, opts, st); err != nil {
		fmt.Fprintf(b, "\n（診断用の当てはめに失敗した: %v）\n", err)
	}
	agreementTable(b, []agreeRow{{"R0 現行のコスト表", d0}, {"R1 素朴な一様コスト（種別違い・向き違い = 2.0）", dNaive}}, js, neg)
	writeTypeBreakdown(b, kept, js, d0)
	return nil
}

// writeConfirmatory is the θ analysis of a non-P2 run: fit.Confirmatory (judge QC before θ, the default filters
// without "pilot", co-primary multi-start fits, paired bootstrap, §3 rule, §4 contrasts, D, verdict, LOJO) and its
// only formatter WriteConfirmatory, then the profile likelihoods of the independence fit.
func (r *v2run) writeConfirmatory(b *bytes.Buffer, ft []fit.Triplet, js []fit.Judgment, opts fit.Options) error {
	o := r.o
	co := fit.ConfirmOptions{Theta0: opts.Theta0, M: o.m, B: o.boot, DReps: o.dReps, Seed: o.seed, JudgeNames: o.judges, SkipExclusions: o.keepJudges}
	res, err := fit.Confirmatory(ft, js, opts, co)
	if err != nil {
		if res.QC.Names != nil { // the QC ran before θ (e.g. every judge failed it): show why
			writeJudgeQC(b, res.QC, res.Options)
		}
		return err
	}
	models := map[string]string{}
	for _, m := range o.generators {
		models["generator "+m] = m + "（" + r.gens[m].Writer().Setting() + "）"
		if v := r.gens[m].Verifier; v != nil {
			models["verifier of "+m] = v.Model + "（" + v.Setting() + "）"
		}
	}
	for _, j := range r.judges {
		models["judge "+j.Model] = j.Model + "（" + j.Setting() + "）"
	}
	WriteConfirmatory(b, res, provenance{Seeds: map[string]uint64{"pilot": o.seed}, PromptHashes: r.promptHashes(), ModelIDs: models,
		M: res.Options.M, B: res.Options.B, U: res.Options.U, Extra: map[string]string{"sources": strconv.Itoa(o.sources), "qt30": strconv.FormatBool(o.withQT30)}})
	single := opts
	single.Starts, single.StartNames = nil, nil
	return writeProfiles(filepath.Join(o.out, "profile.csv"), ft, js, single, res.Indep.Params)
}

// caPairOf maps a diagnostic relabel direction ("RA→CA" etc.) to its cost (RA↔CA or CA↔MA).
func caPairOf(dir string) int {
	if strings.Contains(dir, ged.RA) {
		return ged.PTypeRACA
	}
	return ged.PTypeCAMA
}

// diagCounts are the accepted diagnostic relabels per CA pair among the kept triplets, and whether a pair is
// reported as counts only (a direction's infeasibility rate > 60% in the realization attempts, decisions-stats §12b).
func diagCounts(kept []*pilotTriplet, st *v2Stats) (feasible map[int]int, countsOnly map[int]bool) {
	feasible, countsOnly = map[int]int{}, map[int]bool{}
	for _, t := range kept {
		if t.Plan == nil || !t.Plan.DiagCAType || t.Flags.QT30 {
			continue
		}
		for _, e := range append(slices.Clone(t.EditsA), t.EditsB...) {
			if e.Kind == perturb.RTypeCA {
				feasible[caPairOf(e.Direction)]++
			}
		}
	}
	if st != nil {
		for dir := range st.diagDir {
			if st.diagInfeasibleRate(dir) > diagCountsOnly {
				countsOnly[caPairOf(dir)] = true
			}
		}
	}
	return feasible, countsOnly
}

// writeDiagnosticFit is the pre-registered untied diagnostic fit (decisions-stats §12b; descriptive, no §3 label is
// printed): fit.Confirmatory with Untied, the diag_ca_type triplets included (the other exclusions kept), the same
// judge QC, θ₀ / R1 / all-0.5 starts and stratified bootstrap as the primary fit (no random-intercept fit, no D, no
// LOJO). It reports the CA pairs' normalized estimates with 90% intervals and their contrasts with the fit's own
// θ̃_RA↔MA (the two directions of a pair pooled; a pair with a direction > 60% infeasible is counts only).
func (r *v2run) writeDiagnosticFit(b *bytes.Buffer, kept []*pilotTriplet, ft []fit.Triplet, js []fit.Judgment, base fit.Options, st *v2Stats) error {
	od := base
	od.Tie = nil
	od.Exclude = slices.DeleteFunc(slices.Clone(base.Exclude), func(f string) bool { return f == "diag_ca_type" })
	feas, countsOnly := diagCounts(kept, st)
	fmt.Fprintln(b, "\n## 種別のタイを外した診断用の当てはめ（決定文書 §12b、記述のみ）")
	if feas[ged.PTypeRACA]+feas[ged.PTypeCAMA] == 0 {
		fmt.Fprintln(b, "\n採用した t3_diag の付け替えが無いので当てはめない。")
		return nil
	}
	o := r.o
	res, err := fit.Confirmatory(ft, js, od, fit.ConfirmOptions{Theta0: od.Theta0, M: o.m, B: o.boot, Seed: o.seed, JudgeNames: o.judges,
		Untied: true, SkipMixed: true, SkipD: true, SkipLOJO: true, SkipExclusions: o.keepJudges, MinInformative: map[int]int{}})
	if err != nil {
		return err
	}
	param := map[int]fit.ParamResult{}
	for _, p := range res.Params {
		param[p.Index] = p
	}
	boot, _ := fit.NormalizeRows(res.Boot.Theta, res.Options.U)
	ownType := param[ged.PTypeRAMA].Indep.Est
	fmt.Fprintf(b, "\n`fit.Confirmatory`（Untied）: タイなし、diag_ca_type の三つ組を含める（ほかの除外は同じ）。三つ組 %d、判定 %d、除外した判定者 %v。自身の θ̃_RA↔MA = %.3f。区間はクラスタブートストラップ B = %d のパーセンタイル。\n",
		res.Triplets, res.Judgments, res.ExcludedJudges, ownType, res.Options.B)
	fmt.Fprintln(b, "\n| 種別の組 | θ₀ | 推定（固定尺度） | 推定（正規化） | 90% 区間 | θ̃_RA↔MA との差 | 95% 区間 | 採用した付け替え |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --- | --: | --- | --: |")
	for _, p := range []int{ged.PTypeRACA, ged.PTypeCAMA} {
		pr := param[p]
		var cb []float64
		for _, row := range boot {
			if row != nil {
				cb = append(cb, row[p]-row[ged.PTypeRAMA])
			}
		}
		con := stats.BootstrapContrast(pr.Indep.Est-ownType, cb, 0, stats.TwoSided)
		cell := fmt.Sprintf("%.3f | %.3f | %s | %+.3f | %s", pr.Anchored, pr.Indep.Est, intervalCell(pr.Indep), con.Est, ci(con.CI95))
		if countsOnly[p] {
			cell = "— | — | 件数のみ（infeasible の率 > 60%） | — | —"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d |\n", ged.ParamNames[p], num(pr.Theta0), cell, feas[p])
	}
	fmt.Fprintln(b, "\n診断セルは実現可能な（弱い関係の）付け替えだけなので、一般の符号反転の費用ではない（review m14）。m の帯による判断はしない。")
	return nil
}

// writeJudgeFailures counts, per judge, the judgments that failed, separating refusals and truncations (which are
// neither answers nor ties; review-synth-impl M7).
func writeJudgeFailures(b *bytes.Buffer, kept []*pilotTriplet, judges []string) {
	type cnt struct{ n, failed, refused, truncated int }
	by := map[string]*cnt{}
	for _, t := range kept {
		for _, j := range t.Judgments {
			c := by[j.Judge]
			if c == nil {
				c = &cnt{}
				by[j.Judge] = c
			}
			c.n++
			if j.Error != "" {
				c.failed++
			}
			if j.Refusal {
				c.refused++
			}
			if j.Truncated {
				c.truncated++
			}
		}
	}
	fmt.Fprintln(b, "\n## 判定の失敗（拒否・打ち切りは回答にも「同程度」にも数えない）")
	fmt.Fprintln(b, "\n| 判定者 | 判定 | 失敗 | うち拒否 | うち打ち切り |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: |")
	for _, name := range judges {
		c := by[name]
		if c == nil {
			c = &cnt{}
		}
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d |\n", name, c.n, c.failed, c.refused, c.truncated)
	}
}

// writeProvenance is the provenance block of every v2 output (decisions-stats §16 #13).
func (r *v2run) writeProvenance(b *bytes.Buffer) {
	o := r.o
	fmt.Fprintln(b, "\n## 来歴")
	fmt.Fprintf(b, "\n- git: %s（tag: %s）\n", gitOut("rev-parse", "--short", "HEAD"), gitOut("describe", "--tags", "--always", "--dirty"))
	fmt.Fprintf(b, "- seed: %d、元の議論 %d 件、P2: %v、QT30 を含む: %v、予算 $%.2f\n", o.seed, o.sources, o.p2, o.withQT30, o.budget)
	fmt.Fprintf(b, "- λ = %.2f、m = %.2f、B = %d（正規化の集合 U は統計側の設定）\n", o.lambda, o.m, o.boot)
	var ps []string
	for _, n := range sortedKeys(r.prompts) {
		ps = append(ps, n+"="+r.prompts[n].Hash)
	}
	fmt.Fprintf(b, "- プロンプト: %s\n", strings.Join(ps, ", "))
	for _, m := range o.generators {
		g := r.gens[m]
		v := "（なし: 同じモデル）"
		if g.Verifier != nil {
			v = g.Verifier.Model + "（" + g.Verifier.Setting() + "）"
		}
		fmt.Fprintf(b, "- 生成: %s（%s）→ 検証: %s\n", m, g.Writer().Setting(), v)
	}
	for _, j := range r.judges {
		fmt.Fprintf(b, "- 判定: %s（%s）\n", j.Model, j.Setting())
	}
	fmt.Fprintf(b, "- 埋め込み: %s\n", r.env.emb.Model())
}

func gitOut(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "—"
	}
	return strings.TrimSpace(string(out))
}

// QT30 reference values for the distribution report (dataset-spec §0).
const qt30Note = "QT30（274 件）: 命題の中央値 14（IQR 10–17）、命題あたりの辺 0.62、孤立した命題 16%、RA 54% / CA 7% / MA 39%、発話距離 1: 68% / 2–4: 21% / 5+: 11%"

// writeSourceReport is the G1 summary: acceptance and rejection reasons per generator, and the graph
// distribution per generator next to QT30 (dataset-spec §1.3, §7).
func writeSourceReport(b *bytes.Buffer, all []*sourceRecord) {
	fmt.Fprintln(b, "\n## 元の議論（G1）")
	type agg struct {
		n, acc, calls                      int
		props, edges, ra, ca, ma, iso      int
		d1, d24, d5                        int
		reasons                            map[string]int
		propsList                          []int
		copyReject, targetReject, g1Reject int
		g1Recall                           []float64
	}
	by := map[string]*agg{}
	names := map[string]int{}
	topics := map[string]int{}
	nGen := 0
	for _, s := range all {
		if s.QT30 {
			continue
		}
		nGen++
		a := by[s.Generator]
		if a == nil {
			a = &agg{reasons: map[string]int{}}
			by[s.Generator] = a
		}
		a.n++
		a.calls += len(s.Attempts)
		for _, at := range s.Attempts {
			switch {
			case len(at.Targets) > 0:
				a.targetReject++
			case at.Copy != nil && at.Copy.Reject:
				a.copyReject++
			case at.Agreement != nil:
				a.g1Recall = append(a.g1Recall, at.Agreement.Recall)
				if !at.Agreement.Pass {
					a.g1Reject++
				}
			}
		}
		if !s.Accepted {
			a.reasons[cmp(s.Reason, "unknown")]++
			continue
		}
		a.acc++
		st := s.Stats
		a.props += st.Props
		a.edges += st.Edges
		a.ra, a.ca, a.ma, a.iso = a.ra+st.RA, a.ca+st.CA, a.ma+st.MA, a.iso+st.Isolated
		a.propsList = append(a.propsList, st.Props)
		for d, c := range st.Distances {
			switch {
			case d <= 1:
				a.d1 += c
			case d <= 4:
				a.d24 += c
			default:
				a.d5 += c
			}
		}
		for _, n := range s.Spec.Names {
			names[n]++
		}
		topics[s.Spec.TopicID]++
	}
	fmt.Fprintln(b, "\n| 生成モデル | 割り付け | 受理 | 生成の呼び出し | 数値目標で棄却 | 写しで棄却 | 再注釈で棄却 | 再注釈の recall（中央値） |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: | --: | --: | --: |")
	for _, g := range sortedKeys(by) {
		a := by[g]
		fmt.Fprintf(b, "| %s | %d | %s | %d | %d | %d | %d | %s |\n", g, a.n, ratio(a.acc, a.n), a.calls, a.targetReject, a.copyReject, a.g1Reject, num(median(a.g1Recall)))
	}
	fmt.Fprintln(b, "\n受理されなかった理由（最後の試行）:")
	for _, g := range sortedKeys(by) {
		for _, r := range sortedKeys(by[g].reasons) {
			fmt.Fprintf(b, "- %s: %s %d\n", g, r, by[g].reasons[r])
		}
	}
	fmt.Fprintln(b, "\n### グラフの分布（受理した元の議論）")
	fmt.Fprintln(b, "\n| 生成モデル | 命題の中央値 | 命題あたりの辺 | 孤立 | RA / CA / MA | 距離 1 / 2–4 / 5+ |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --- | --- |")
	for _, g := range sortedKeys(by) {
		a := by[g]
		if a.acc == 0 {
			continue
		}
		e := float64(max(1, a.edges))
		dt := float64(max(1, a.d1+a.d24+a.d5))
		fmt.Fprintf(b, "| %s | %.0f | %.2f | %.0f%% | %.0f%% / %.0f%% / %.0f%% | %.0f%% / %.0f%% / %.0f%% |\n", g,
			median(intsToFloats(a.propsList)), float64(a.edges)/float64(max(1, a.props)), 100*float64(a.iso)/float64(max(1, a.props)),
			100*float64(a.ra)/e, 100*float64(a.ca)/e, 100*float64(a.ma)/e, 100*float64(a.d1)/dt, 100*float64(a.d24)/dt, 100*float64(a.d5)/dt)
	}
	fmt.Fprintln(b, "\n"+qt30Note)
	maxName, maxN := "", 0
	for n, c := range names {
		if c > maxN {
			maxName, maxN = n, c
		}
	}
	reuse := 0
	for _, c := range topics {
		if c > 1 {
			reuse += c - 1
		}
	}
	fmt.Fprintf(b, "\n- 話題の再利用（受理した元の議論の間）: %d。最も多く使われた名前: %s（%d 件、%.1f%%。基準 ≤ 3%%）\n", reuse, maxName, maxN, pct(maxN, max(1, nGen)))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func intsToFloats(xs []int) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = float64(x)
	}
	return out
}

// writeT1Report reports the T1 construction (dataset-spec §3.1, §7) and, per judge, the descriptive
// reversal rate on the gate stratum with the one-sided 90% Clopper–Pearson bound on n_T triplets
// (decisions-stats §9.1; the gate itself is computed by the statistics side).
func writeT1Report(b *bytes.Buffer, kept []*pilotTriplet, js []fit.Judgment, judges []string) {
	fmt.Fprintln(b, "\n## T1（増分の書き下しと Δ(θ₀) の層）")
	strata := map[string]int{}
	planned := map[string][2]int{}
	n := 0
	for _, t := range kept {
		if t.Type != perturb.T1Monotone {
			continue
		}
		n++
		strata[t.T1Stratum]++
		target := ""
		if t.Plan != nil {
			target = t.Plan.T1Target
		}
		p := planned[target]
		p[0]++
		if target == t.T1Stratum {
			p[1]++
		}
		planned[target] = p
	}
	fmt.Fprintf(b, "\n- T1 の三つ組 %d。実現後の層: [0.5,1) %d、[1,2) %d、≥2（関門集合）%d\n", n, strata[perturb.T1Low], strata[perturb.T1Mid], strata[perturb.T1High])
	for _, k := range sortedKeys(planned) {
		fmt.Fprintf(b, "- 計画の層 %s: %d 件、うち実現後も同じ層 %d\n", k, planned[k][0], planned[k][1])
	}
	gate := map[int]bool{}
	for i, t := range kept {
		if t.Type == perturb.T1Monotone && t.T1Stratum == perturb.T1High {
			gate[i] = true
		}
	}
	if len(gate) == 0 {
		return
	}
	fmt.Fprintln(b, "\n| 判定者 | 関門の三つ組 n_T | 判定 | 逆転（B が近い） | 同程度 | r_j | 片側 90% 上限（n_eff = n_T） |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: | --: | --: |")
	for j, name := range judges {
		var nj, rev, tie int
		for _, k := range js {
			if k.Judge != j || !gate[k.Triplet] {
				continue
			}
			nj++
			switch k.Answer {
			case fit.AnswerB:
				rev++
			case fit.AnswerTie:
				tie++
			}
		}
		if nj == 0 {
			continue
		}
		rj := float64(rev) / float64(nj)
		nT := len(gate)
		kEff := int(math.Round(rj * float64(nT)))
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %.3f | %.3f |\n", name, nT, nj, rev, tie, rj, cpUpper(kEff, nT, 0.9))
	}
}

// cpUpper is the one-sided Clopper–Pearson upper bound at level conf.
func cpUpper(k, n int, conf float64) float64 {
	if k >= n {
		return 1
	}
	return distuv.Beta{Alpha: float64(k + 1), Beta: float64(n - k)}.Quantile(conf)
}

// writeICC reports the intraclass correlation of the per-triplet agreement with θ₀ by exemplar pair
// and by domain (one-way ANOVA ICC(1); decisions-stats §16 #12, dataset-spec §1.5.2). The per-triplet
// score is the mean over its judgments of +1 (the answer agrees with the sign of Δ(θ₀)), −1 (opposite)
// and 0 (same); triplets with Δ(θ₀) = 0 and QT30 triplets are left out.
func writeICC(b *bytes.Buffer, kept []*pilotTriplet, js []fit.Judgment, d0 []float64) {
	score := map[int][]float64{}
	for _, k := range js {
		d := d0[k.Triplet]
		if math.Abs(d) < 1e-9 || kept[k.Triplet].Flags.QT30 {
			continue
		}
		s := 0.0
		switch k.Answer {
		case fit.AnswerA:
			s = 1
		case fit.AnswerB:
			s = -1
		}
		if d < 0 {
			s = -s
		}
		score[k.Triplet] = append(score[k.Triplet], s)
	}
	by := func(key func(t *pilotTriplet) string) map[string][]float64 {
		g := map[string][]float64{}
		for i, ss := range score {
			m := 0.0
			for _, s := range ss {
				m += s
			}
			g[key(kept[i])] = append(g[key(kept[i])], m/float64(len(ss)))
		}
		return g
	}
	fmt.Fprintln(b, "\n## 級内相関（例示の組・領域。判定と θ₀ の一致度の三つ組平均、一元配置 ICC(1)）")
	fmt.Fprintln(b, "\n| 群 | 群の数 | 三つ組 | ICC(1) |")
	fmt.Fprintln(b, "| --- | --: | --: | --: |")
	for _, row := range []struct {
		name string
		key  func(t *pilotTriplet) string
	}{
		{"例示の組", func(t *pilotTriplet) string { return fmt.Sprint(t.ExemplarPair) }},
		{"領域", func(t *pilotTriplet) string { return t.Domain }},
		{"生成モデル", func(t *pilotTriplet) string { return t.Generator }},
	} {
		icc, k, n := iccOneWay(by(row.key))
		fmt.Fprintf(b, "| %s | %d | %d | %s |\n", row.name, k, n, num(icc))
	}
}

// iccOneWay is the one-way random-effects ICC(1) = (MSB − MSW) / (MSB + (n₀ − 1)·MSW) with
// n₀ = (N − Σ nᵢ² / N) / (k − 1) for unbalanced groups. NaN if it is not estimable.
func iccOneWay(groups map[string][]float64) (icc float64, k, n int) {
	var all []float64
	for _, g := range groups {
		if len(g) > 0 {
			k++
			all = append(all, g...)
		}
	}
	n = len(all)
	if k < 2 || n <= k {
		return math.NaN(), k, n
	}
	grand := 0.0
	for _, x := range all {
		grand += x
	}
	grand /= float64(n)
	var ssb, ssw, sumSq float64
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		m := 0.0
		for _, x := range g {
			m += x
		}
		m /= float64(len(g))
		ssb += float64(len(g)) * (m - grand) * (m - grand)
		for _, x := range g {
			ssw += (x - m) * (x - m)
		}
		sumSq += float64(len(g) * len(g))
	}
	msb := ssb / float64(k-1)
	msw := ssw / float64(n-k)
	n0 := (float64(n) - sumSq/float64(n)) / float64(k-1)
	den := msb + (n0-1)*msw
	if den == 0 {
		return math.NaN(), k, n
	}
	return (msb - msw) / den, k, n
}

// ---------------------------------------------------------------------------------------------
// Cost: table of the run and the projection to the single-stage sample (dataset-spec §4.5).

func writeCostTable(b *bytes.Buffer, mt *meter, tags []string) {
	fmt.Fprintln(b, "\n## 費用")
	fmt.Fprintln(b, "\n| 用途 | API 呼び出し | キャッシュ | 入力トークン | 出力トークン | 費用 |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: | --: |")
	var tot llm.Usage
	for _, tg := range tags {
		u := mt.byTag[tg]
		tot = tot.Add(u)
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d | $%.4f |\n", tg, mt.calls[tg], mt.cached[tg], u.InputTokens, u.OutputTokens, u.CostUSD)
	}
	fmt.Fprintf(b, "| 計 | | | %d | %d | $%.4f |\n", tot.InputTokens, tot.OutputTokens, tot.CostUSD)
	fmt.Fprintln(b, "\n埋め込み（text-embedding-3-small）は数セント未満なので表に入れていない。キャッシュから返した呼び出しは $0 として数える。")
}

// projection holds what writeCostProjection needs.
type projection struct {
	generators, judges []string
	pricing            *llm.PriceTable
	budget             float64
}

func projectionFor(o pilotOptions, pricing *llm.PriceTable) projection {
	gens, judges := o.generators, o.judges
	if len(gens) == 0 {
		gens = defaultGenerators
	}
	if len(judges) == 0 {
		judges = defaultJudges
	}
	if pricing == nil {
		pricing = llm.DefaultPricing()
	}
	return projection{generators: gens, judges: judges, pricing: pricing, budget: defaultBudget(750)}
}

// projPhase is one row of the §4.5 table: call count and the default tokens per call (pilot-2 luna
// measurements plus the §4.5 assumptions), used when the run has no measurement for that tag × model.
type projPhase struct {
	name, tag     string
	calls         float64
	judge         bool
	defIn, defOut float64
}

var projPhases = []projPhase{
	{"G0 話題バンク", "synth_topics", 24, false, 400, 2500},
	{"G1 生成", "synth_source", 940, false, 590 + 1900, 1870},
	{"G1 検証（別系列）", "verify_source", 940, false, 1500, 900},
	{"G3 書き下し", "realize", 9200, false, 1407 + 300, 859 + 200},
	{"G4 検証（別系列）", "verify", 9200, false, 1252 + 300, 434 + 200},
	{"監査", "qc", 150, false, 1500, 900},
	{"J 判定", "judge", 19800, true, 833, 95},
}

// writeCostProjection projects the single-stage cost with the §4.5 call counts (3,300 triplets,
// G1 verification, QC audits, three judges): generation / verification calls split evenly over the
// generator families, judge calls evenly over the judges. Tokens per call are the run's measurements
// per tag × model when available, otherwise the §4.5 values (Claude judges: 1.2× input, 300 output
// including thinking). Prices: list (ChatCost) and Message Batches (ChatCostBatch) for Anthropic.
func writeCostProjection(b *bytes.Buffer, mt *meter, p projection) {
	fmt.Fprintln(b, "\n### 単段（750 件 = 3,300 三つ組）の費用の見積もり（dataset-spec §4.5 の呼び出し数）")
	fmt.Fprintln(b, "\n1 回あたりのトークンは、この実行の実測（用途 × モデル）があればそれを、無ければ §4.5 の値を使う（* は §4.5 の値）。")
	fmt.Fprintln(b, "\n| 段階 | モデル | 呼び出し | 入力 / 出力（1 回） | 定価 | バッチ（Anthropic 半額） |")
	fmt.Fprintln(b, "| --- | --- | --: | --- | --: | --: |")
	var list, batchAll, asRun float64
	byModel := map[string]float64{}
	for _, ph := range projPhases {
		models := p.generators
		if ph.judge {
			models = p.judges
		}
		for _, m := range models {
			n := ph.calls / float64(len(models))
			in, out, ok := mt.perCall(ph.tag, m)
			mark := ""
			if !ok {
				in, out, mark = ph.defIn, ph.defOut, "*"
				if ph.judge && synth.FamilyOf(m) == "anthropic" {
					in, out = ph.defIn*1.2, 300
				}
			}
			u := llm.Usage{InputTokens: int64(in * n), OutputTokens: int64(out * n)}
			c := p.pricing.ChatCost(m, u)
			cb := c
			if synth.FamilyOf(m) == "anthropic" {
				cb = p.pricing.ChatCostBatch(m, u)
			}
			list += c
			batchAll += cb
			if ph.judge {
				asRun += cb // the pipeline batches the Anthropic judges only
			} else {
				asRun += c
			}
			byModel[m] += c
			fmt.Fprintf(b, "| %s | %s | %.0f | %.0f / %.0f%s | $%.1f | $%.1f |\n", ph.name, m, n, in, out, mark, c, cb)
		}
	}
	fmt.Fprintf(b, "| 計 | | | | **$%.1f** | **$%.1f** |\n", list, batchAll)
	var parts []string
	for _, m := range sortedKeys(byModel) {
		parts = append(parts, fmt.Sprintf("%s $%.1f", m, byModel[m]))
	}
	fmt.Fprintf(b, "\n- 定価の内訳: %s（埋め込み ≈ $0.5 は別）。dataset-spec §4.5 の定価の合計は $%.1f\n", strings.Join(parts, "、"), listBudget750)
	fmt.Fprintf(b, "- このパイプラインの実際の経路（Anthropic の判定だけバッチ、書き下しと検証は逐次）: $%.1f\n", asRun)
	fmt.Fprintf(b, "- -budget の既定値は 750 件で $%.0f（定価の 1.5 倍）。件数に比例して縮める\n", p.budget)
}

func writeTypeBreakdown(b *bytes.Buffer, kept []*pilotTriplet, js []fit.Judgment, d0 []float64) {
	fmt.Fprintln(b, "\n## 三つ組の種類ごとの回答の分布")
	fmt.Fprintln(b, "\n| 種類 | 三つ組 | A | B | 同程度 | R0 の一致率 |")
	fmt.Fprintln(b, "| --- | --: | --: | --: | --: | --- |")
	for _, typ := range []perturb.TripletType{perturb.T1Monotone, perturb.T2SameKind, perturb.T3Contrast, perturb.T4Invariant} {
		mask := make([]bool, len(kept))
		n := 0
		var cnt [3]int
		for i, t := range kept {
			if t.Type == typ {
				mask[i] = true
				n++
			}
		}
		for _, k := range js {
			if mask[k.Triplet] {
				cnt[k.Answer]++
			}
		}
		a, na := fit.Agreement(d0, js, mask)
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %s |\n", typ, n, cnt[0], cnt[1], cnt[2], agr(a, na))
	}
}

// runTopics builds the topic bank (dataset-spec §1.3): two calls per domain (one per generator
// family, the second told the first's questions), then embedding dedup (cos < 0.85) within the bank
// and against the exemplar topics. Writes <out>/topics.json and topics.md.
func runTopics(o pilotOptions) error {
	if o.budget < 0 {
		o.budget = 2
	}
	env, err := newRealEnv(o)
	if err != nil {
		return err
	}
	return runTopicsEnv(context.Background(), o, env)
}

func runTopicsEnv(ctx context.Context, o pilotOptions, env *pilotEnv) error {
	r, err := newV2Run(o, env)
	if err != nil {
		return err
	}
	perDomain := make([][]synth.Topic, len(synth.Domains))
	errs := parallel(len(synth.Domains), 4, func(i int) error {
		var avoid []string
		for batch := range 2 {
			if err := r.mt.check(); err != nil {
				return err
			}
			m := o.generators[batch%len(o.generators)]
			ts, resp, err := synth.GenerateTopics(ctx, r.gens[m].Writer(), r.prompts["synth_topics_v1"], synth.Domains[i], synth.TopicsPerCall, batch, avoid)
			r.mt.add("synth_topics", resp)
			if err != nil {
				return fmt.Errorf("%s batch %d: %w", synth.Domains[i], batch, err)
			}
			perDomain[i] = append(perDomain[i], ts...)
			for _, t := range ts {
				avoid = append(avoid, t.Question)
			}
		}
		return nil
	})
	if err := errors.Join(errs...); err != nil {
		return err
	}
	var all []synth.Topic
	for _, ts := range perDomain {
		all = append(all, ts...)
	}
	var reserved []string
	for _, ex := range r.exemplars {
		reserved = append(reserved, ex.Topic)
	}
	kept, dropped, err := synth.DedupTopics(ctx, env.emb, all, reserved)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(o.out, "topics.json"), kept); err != nil {
		return err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# 話題バンク（G0）\n\n- 候補 %d、重複（cos ≥ %.2f、例示の話題を含む）で除いたもの %d、残り %d（必要: 940 回の生成に対して ≥ 900）\n",
		len(all), synth.TopicDedupCos, dropped, len(kept))
	fmt.Fprintf(&b, "- プロンプト: synth_topics_v1=%s。費用 $%.3f\n\n| 領域 | 話題 |\n| --- | --: |\n", r.prompts["synth_topics_v1"].Hash, r.mt.total())
	count := map[string]int{}
	for _, t := range kept {
		count[t.Domain]++
	}
	for _, d := range synth.Domains {
		fmt.Fprintf(&b, "| %s | %d |\n", d, count[d])
	}
	if err := os.WriteFile(filepath.Join(o.out, "topics.md"), b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

func readTopics(path string) ([]synth.Topic, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ts []synth.Topic
	return ts, json.Unmarshal(b, &ts)
}

// runImportQT30 writes the QT30 transfer set (dataset-spec §1.4) to <out>/qt30_sources.json. No API.
func runImportQT30(o pilotOptions) error {
	srcs, f, err := synth.ImportQT30(cmp(o.aifdb, "../aif-with-llm/data/aifdb"))
	if err != nil {
		return err
	}
	if f.Files == 0 {
		return fmt.Errorf("pilot: %s に nodeset*.json が無い", cmp(o.aifdb, "../aif-with-llm/data/aifdb"))
	}
	if err := writeJSON(filepath.Join(o.out, "qt30_sources.json"), srcs); err != nil {
		return err
	}
	byEp := map[string]int{}
	for _, s := range srcs {
		byEp[s.Episode]++
	}
	fmt.Printf("QT30 の転移セット: %d 件中 %d 件が条件を満たした（テスト以外のエピソード %d、メタモデル違反 %d、I-node が 8–20 の外 %d、未定位 %d、折り畳んだ辺 < 4: %d）\n",
		f.Files, f.Eligible, f.NotTest, f.Metamodel, f.Size, f.NotLocated, f.FewEdges)
	for _, ep := range sortedKeys(byEp) {
		fmt.Printf("- %s: %d\n", ep, byEp[ep])
	}
	fmt.Println("（QT30 の本文を含むので out/ 以下に置き、公開しない）")
	return nil
}

// runAudit (-audit) runs the P2 data audits on the records of a previous run (dataset-spec §7, §1.5.2):
// (a) label-correctness audit: 100 accepted variants stratified by edit kind × direction, fully
// re-annotated blind by the triplet's verifier family; (b) homogeneity monitor: 40 accepted sources
// (20 per generator) against 40 generated without exemplars; (c) the G1 verifier on the 7 exemplars
// (both families). Writes <out>/audit.md.
func runAudit(o pilotOptions) error {
	if o.budget < 0 {
		o.budget = 5
	}
	env, err := newRealEnv(o)
	if err != nil {
		return err
	}
	return runAuditEnv(context.Background(), o, env)
}

func runAuditEnv(ctx context.Context, o pilotOptions, env *pilotEnv) error {
	r, err := newV2Run(o, env)
	if err != nil {
		return err
	}
	srcs, err := readJSONL[sourceRecord](filepath.Join(o.out, "pilot_sources.jsonl"))
	if err != nil {
		return err
	}
	trips, err := readJSONL[pilotTriplet](filepath.Join(o.out, "pilot_triplets.jsonl"))
	if err != nil {
		return err
	}
	var b bytes.Buffer
	fmt.Fprintln(&b, "# P2 のデータの監査（-audit）")
	r.qcExemplars(ctx, &b)
	r.qcLabels(ctx, &b, trips)
	r.qcHomogeneity(ctx, &b, srcs)
	writeCostTable(&b, r.mt, []string{"qc"})
	if err := os.WriteFile(filepath.Join(o.out, "audit.md"), b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

// qcExemplars: the G1 verifier must recover ≥ 90% of every exemplar's relations and read no extra one.
func (r *v2run) qcExemplars(ctx context.Context, b *bytes.Buffer) {
	fmt.Fprintln(b, "\n## 例示の再注釈（G1 の検証者の固定の検査）")
	fmt.Fprintln(b, "\n| 例示 | 検証者 | 一致 / 金 | 種別違い | 向き違い | 余分 | 合格 |")
	fmt.Fprintln(b, "| --- | --- | --: | --: | --: | --: | --- |")
	for _, m := range r.o.generators {
		g := &synth.Generator{Client: r.gens[m].Client, Model: m, Effort: r.gens[m].Effort, Thinking: r.gens[m].Thinking,
			VerifySourcePrompt: r.prompts["verify_source_v1"]}
		for _, ex := range r.exemplars {
			if r.mt.check() != nil {
				return
			}
			_, a, meta, resp, err := g.VerifySource(ctx, ex.Discussion)
			r.mt.add("qc", resp)
			if err != nil {
				fmt.Fprintf(b, "| %s | %s | エラー: %v | | | | |\n", ex.ID, m, err)
				continue
			}
			fmt.Fprintf(b, "| %s | %s | %d / %d | %d | %d | %d | %v |\n", ex.ID, meta.Model, a.Matched, a.Gold, a.TypeWrong, a.DirWrong, a.Extra, a.Pass)
		}
	}
}

// qcLabels is the label-correctness audit: 100 variants, ≥ 5 per kind × direction where available.
func (r *v2run) qcLabels(ctx context.Context, b *bytes.Buffer, trips []pilotTriplet) {
	type cand struct {
		t    *pilotTriplet
		side int // 0: A, 1: B
		key  string
	}
	byKey := map[string][]cand{}
	for i := range trips {
		t := &trips[i]
		if t.Err != "" || t.Flags.QT30 {
			continue
		}
		for side, plan := range [][]editRecord{t.PlanA, t.PlanB} {
			if len(plan) == 0 {
				continue
			}
			k := strings.TrimSpace(string(plan[0].Kind) + " " + plan[0].Direction)
			byKey[k] = append(byKey[k], cand{t, side, k})
		}
	}
	rng := rand.New(rand.NewPCG(r.o.seed, 0x9c))
	var sample []cand
	keys := sortedKeys(byKey)
	for _, k := range keys {
		rng.Shuffle(len(byKey[k]), func(i, j int) { byKey[k][i], byKey[k][j] = byKey[k][j], byKey[k][i] })
	}
	for round := 0; len(sample) < 100; round++ {
		added := false
		for _, k := range keys {
			if round < len(byKey[k]) && len(sample) < 100 {
				sample = append(sample, byKey[k][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	var edits, recovered, flips int
	perKey := map[string][2]int{}
	for _, c := range sample {
		if r.mt.check() != nil {
			break
		}
		d, plan := c.t.TextA, c.t.PlanA
		if c.side == 1 {
			d, plan = c.t.TextB, c.t.PlanB
		}
		// the audit uses the triplet's verifier family
		vm := c.t.VerifierModel
		var ag synth.Agent
		for _, m := range r.o.generators {
			if strings.HasPrefix(vm, m) || (vm == "" && synth.FamilyOf(m) == c.t.VerifierFamily) {
				ag = synth.NewAgent(r.gens[m].Client, m, r.gens[m].Effort, r.gens[m].Thinking)
			}
		}
		if ag.Client == nil {
			continue
		}
		g := &synth.Generator{Client: ag.Client, Model: ag.Model, Effort: ag.Effort, Thinking: ag.Thinking, VerifySourcePrompt: r.prompts["verify_source_v1"]}
		ann, _, _, resp, err := g.VerifySource(ctx, d)
		r.mt.add("qc", resp)
		if err != nil {
			continue
		}
		read := ann.Relabel(d.Graph)
		toLabel := map[string]string{}
		for i, p := range d.Graph.Props {
			toLabel[p.ID] = fmt.Sprintf("P%d", i+1)
		}
		content := map[string]string{}
		for _, p := range ann.Propositions {
			content[p.Label] = p.Content
			if p.Content == synth.ContentNegated {
				flips++ // reference = intended text, so any negation is unrequested
			}
		}
		for _, e := range plan {
			ok, checkable := auditEdit(e, d.Graph, read, content, toLabel)
			if !checkable {
				continue
			}
			edits++
			pk := perKey[c.key]
			pk[1]++
			if ok {
				recovered++
				pk[0]++
			}
			perKey[c.key] = pk
		}
	}
	fmt.Fprintln(b, "\n## ラベルの正しさの監査（採用した変種の盲検の全再注釈）")
	fmt.Fprintf(b, "\n- 変種 %d、確かめた編集 %d、意図どおりに読めた編集 %s（基準 ≥ 90%%）、指示していない極性の反転 %d（基準 0）\n",
		len(sample), edits, ratio(recovered, edits), flips)
	fmt.Fprintln(b, "\n| 先頭の編集 × 方向 | 読めた / 確かめた |")
	fmt.Fprintln(b, "| --- | --: |")
	for _, k := range sortedKeys(perKey) {
		fmt.Fprintf(b, "| %s | %s |\n", k, ratio(perKey[k][0], perKey[k][1]))
	}
}

// auditEdit checks one edit against a full re-annotation of the variant text.
func auditEdit(e editRecord, g ged.Graph, read []ged.Edge, content map[string]string, label map[string]string) (ok, checkable bool) {
	has := func(from, to, kind string) bool {
		return slices.ContainsFunc(read, func(x ged.Edge) bool { return x.From == from && x.To == to && (kind == "" || x.Kind == kind) })
	}
	related := func(a, b string) bool { return has(a, b, "") || has(b, a, "") }
	switch e.Kind {
	case perturb.RDel:
		return !related(e.Edge.From, e.Edge.To), true
	case perturb.RAdd:
		return has(e.Edge.From, e.Edge.To, e.Edge.Kind), true
	case perturb.RType, perturb.RTypeCA:
		return has(e.Edge.From, e.Edge.To, e.NewKind), true
	case perturb.RRev:
		return has(e.Edge.To, e.Edge.From, e.Edge.Kind), true
	case perturb.PPara, perturb.PShift, perturb.PNeg, perturb.PAdd:
		l, inV := label[e.Prop]
		if !inV {
			return false, false
		}
		c := content[l]
		return c == synth.ContentSame || c == synth.ContentParaphrase, true
	}
	return false, false
}

// qcHomogeneity is the homogeneity monitor (dataset-spec §1.5.2 (4)): mean pairwise cos of 40 accepted
// sources (20 per generator) against 40 generated for this check without exemplars (same specs), and
// the largest share of one signalling phrase per relation kind in each arm.
func (r *v2run) qcHomogeneity(ctx context.Context, b *bytes.Buffer, srcs []sourceRecord) {
	const perGen = 20
	var with []*sourceRecord
	count := map[string]int{}
	for i := range srcs {
		s := &srcs[i]
		if s.Accepted && !s.QT30 && count[s.Generator] < perGen {
			with = append(with, s)
			count[s.Generator]++
		}
	}
	var without []synth.Discussion
	for i, s := range with {
		if r.mt.check() != nil {
			break
		}
		g := r.gens[s.Generator]
		if g == nil {
			continue
		}
		d, resp, err := g.GenerateSourceNoExemplars(ctx, s.Spec, fmt.Sprintf("h%d.", i))
		r.mt.add("qc", resp)
		if err == nil {
			without = append(without, d)
		}
	}
	var withD []synth.Discussion
	for _, s := range with {
		withD = append(withD, s.Discussion)
	}
	mw, errW := r.meanPairwiseCos(ctx, withD)
	mo, errO := r.meanPairwiseCos(ctx, without)
	fmt.Fprintln(b, "\n## 均質さの監視（例示あり 40 件 / 例示なし 40 件）")
	if errW != nil || errO != nil {
		fmt.Fprintf(b, "\n埋め込みに失敗: %v %v\n", errW, errO)
		return
	}
	flag := ""
	if mw-mo > 0.03 {
		flag = "（**基準 0.03 を超える**）"
	}
	fmt.Fprintf(b, "\n- 平均の対ごとの cos: 例示あり %.3f（%d 件）、例示なし %.3f（%d 件）、差 %+.3f%s\n", mw, len(withD), mo, len(without), mw-mo, flag)
	for _, arm := range []struct {
		name string
		ds   []synth.Discussion
	}{{"例示あり", withD}, {"例示なし", without}} {
		for _, k := range ged.Kinds {
			ph, share := topSignal(arm.ds, k)
			mark := ""
			if share > 0.5 {
				mark = "（**50% を超える**）"
			}
			fmt.Fprintf(b, "- %s・%s: 最も多い合図「%s」%.0f%%%s\n", arm.name, k, ph, 100*share, mark)
		}
	}
}

func (r *v2run) meanPairwiseCos(ctx context.Context, ds []synth.Discussion) (float64, error) {
	if len(ds) < 2 {
		return math.NaN(), nil
	}
	texts := make([]string, len(ds))
	for i, d := range ds {
		texts[i] = d.Render()
	}
	vs, err := r.env.emb.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	s, n := 0.0, 0
	for i := range vs {
		for j := i + 1; j < len(vs); j++ {
			s += synth.Cosine(vs[i], vs[j])
			n++
		}
	}
	return s / float64(n), nil
}

// signalPhrases are the signalling phrases counted by the homogeneity monitor.
var signalPhrases = []string{
	"because", "so", "that is why", "which is another reason", "therefore", "since", "which means",
	"but", "except that", "though", "that ignores", "however", "on the contrary", "yet",
	"in other words", "that is to say", "so you are saying", "put another way", "to put it plainly", "exactly",
}

// topSignal returns the most frequent signalling phrase among the relations of kind (searched in the
// utterance of the later proposition of each relation) and its share.
func topSignal(ds []synth.Discussion, kind string) (string, float64) {
	cnt := map[string]int{}
	n := 0
	for _, d := range ds {
		for _, e := range d.Graph.Edges {
			if e.Kind != kind {
				continue
			}
			u := max(d.PropUtterance[e.From], d.PropUtterance[e.To])
			if u < 0 || u >= len(d.Utterances) {
				continue
			}
			text := " " + strings.ToLower(d.Utterances[u].Text) + " "
			for _, ph := range signalPhrases {
				if strings.Contains(text, " "+ph+" ") || strings.Contains(text, " "+ph+",") {
					cnt[ph]++
					n++
					break
				}
			}
		}
	}
	best, bn := "—", 0
	for _, ph := range sortedKeys(cnt) {
		if cnt[ph] > bn {
			best, bn = ph, cnt[ph]
		}
	}
	if n == 0 {
		return best, 0
	}
	return best, float64(bn) / float64(n)
}

// ---------------------------------------------------------------------------------------------
// helpers

func writeJSON(path string, v any) error {
	js, err := synth.MarshalIndent(v)
	if err != nil {
		return fmt.Errorf("pilot: %s: %w", filepath.Base(path), err)
	}
	return os.WriteFile(path, js, 0o644)
}

func writeJSONL[T any](path string, xs []T) error {
	var b bytes.Buffer
	for _, x := range xs {
		js, err := json.Marshal(x)
		if err != nil {
			return fmt.Errorf("pilot: %s: %w", filepath.Base(path), err)
		}
		b.Write(js)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var x T
		if err := json.Unmarshal(sc.Bytes(), &x); err != nil {
			return nil, fmt.Errorf("pilot: %s: %w", filepath.Base(path), err)
		}
		out = append(out, x)
	}
	return out, sc.Err()
}

// parseModelMap parses "model=value,model=value".
func parseModelMap(s string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func parseList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func changesGraph(es []perturb.Edit) bool {
	for _, e := range es {
		if e.Kind != perturb.XSpk && e.Kind != perturb.XSurf {
			return true
		}
	}
	return false
}

// pick keeps only the triplets in idx and renumbers the judgment indices.
func pick(ft []fit.Triplet, js []fit.Judgment, idx []int) ([]fit.Triplet, []fit.Judgment) {
	pos := map[int]int{}
	var out []fit.Triplet
	for _, i := range idx {
		pos[i] = len(out)
		out = append(out, ft[i])
	}
	var oj []fit.Judgment
	for _, k := range js {
		if p, ok := pos[k.Triplet]; ok {
			k.Triplet = p
			oj = append(oj, k)
		}
	}
	return out, oj
}

func mapAnswer(raw string, order float64) fit.Answer {
	switch {
	case raw == "same":
		return fit.AnswerTie
	case (raw == "1") == (order > 0):
		return fit.AnswerA
	default:
		return fit.AnswerB
	}
}

func lastOf(vs []perturb.Variant) perturb.Variant { return vs[len(vs)-1] }

func cmp(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
