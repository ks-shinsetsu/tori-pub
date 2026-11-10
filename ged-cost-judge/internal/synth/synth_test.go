package synth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// baseDiscussion has p1..p4 in utterances 0, 1, 3, 5 with p2 supports p1 and p4 attacks p3.
func baseDiscussion() Discussion {
	return Discussion{
		Utterances: []Utterance{
			{"Host", "Should the town pedestrianise the high street?"},
			{"Priya", "Shops lose passing trade without parking, so pedestrianising would hurt them."},
			{"Host", "Gareth?"},
			{"Gareth", "Footfall rises when streets are closed to cars."},
			{"Host", "Let us hear from the floor."},
			{"Noor", "But the studies he means were done in large cities."},
		},
		Graph: ged.Graph{
			Props: []ged.Prop{
				{ID: "s0.p1", Text: "Pedestrianising the high street would hurt the shops."},
				{ID: "s0.p2", Text: "Shops lose passing trade without parking."},
				{ID: "s0.p3", Text: "Footfall rises when streets are closed to cars."},
				{ID: "s0.p4", Text: "The studies showing higher footfall were done in large cities."},
			},
			Edges: []ged.Edge{{From: "s0.p2", To: "s0.p1", Kind: ged.RA}, {From: "s0.p4", To: "s0.p3", Kind: ged.CA}},
		},
		PropUtterance: map[string]int{"s0.p1": 1, "s0.p2": 1, "s0.p3": 3, "s0.p4": 5},
	}
}

func rdelVariant(base Discussion) (perturb.Variant, []perturb.Edit) {
	e := base.Graph.Edges[0]
	g := base.Graph.Clone()
	g.Edges = g.Edges[1:]
	edits := []perturb.Edit{{Kind: perturb.RDel, Edge: &e}}
	v := perturb.Variant{Graph: g, Edits: edits, Origin: map[string]string{}, Changed: map[string]perturb.Edit{}}
	for _, p := range g.Props {
		v.Origin[p.ID] = p.ID
	}
	return v, edits
}

func realizeClient(out any) *llm.FuncClient {
	b, _ := json.Marshal(out)
	return &llm.FuncClient{Fn: func(_ context.Context, req llm.Request) (*llm.Response, error) {
		return &llm.Response{Text: string(b), Model: req.Model}, nil
	}}
}

type change struct {
	Utterance int    `json:"utterance"`
	Op        string `json:"op"`
	Speaker   string `json:"speaker"`
	Text      string `json:"text"`
	Role      string `json:"role"`
}
type propRet struct {
	ID        string `json:"id"`
	Unchanged bool   `json:"unchanged"`
	Text      string `json:"text"`
}

func TestRealizeIncremental(t *testing.T) {
	base := baseDiscussion()
	v, edits := rdelVariant(base)
	ctx := context.Background()
	sep := change{Utterance: 1, Op: "replace", Speaker: "Priya", Text: "Pedestrianising would hurt the shops. Separately, shops lose passing trade without parking.", Role: "edit"}
	props := []propRet{{ID: "p1", Unchanged: true}, {ID: "p2", Unchanged: true}}

	// OK: only the edited utterance changes; every other utterance stays byte-equal.
	g := &Generator{Client: realizeClient(map[string]any{"infeasible": false, "reason": "", "changes": []change{sep}, "propositions": props}), Model: "m", Version: V2}
	res, _, err := g.Realize(ctx, RealizeInput{Base: base, Target: v, Edits: edits, Incremental: true, Prefix: "s0."})
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range base.Utterances {
		if i != 1 && res.Discussion.Utterances[i] != u {
			t.Fatalf("utterance %d changed: %+v", i, res.Discussion.Utterances[i])
		}
	}
	if res.Discussion.PropText("s0.p1") != base.PropText("s0.p1") || res.Discussion.PropUtterance["s0.p4"] != 5 {
		t.Fatalf("proposition texts/locations: %+v", res.Discussion)
	}
	if !slices.Equal(res.Touched, []string{"s0.p2", "s0.p1"}) {
		t.Fatalf("touched %v", res.Touched)
	}

	// Rejected: a "connective" change to utterance 5, which is neither edited nor a neighbour.
	far := change{Utterance: 5, Op: "replace", Speaker: "Noor", Text: "Yet the studies he means were done in large cities.", Role: "connective"}
	g.Client = realizeClient(map[string]any{"changes": []change{sep, far}, "propositions": props})
	if _, _, err := g.Realize(ctx, RealizeInput{Base: base, Target: v, Edits: edits, Incremental: true, Prefix: "s0."}); err == nil || !strings.Contains(err.Error(), "untouched utterance 5") {
		t.Fatalf("changed untouched utterance not rejected: %v", err)
	}
	// The same output in a non-incremental realization is only counted.
	res, _, err = g.Realize(ctx, RealizeInput{Base: base, Target: v, Edits: edits, Prefix: "s0."})
	if err != nil || res.Stats.Collateral != 1 {
		t.Fatalf("non-incremental: err %v collateral %d", err, res.Stats.Collateral)
	}

	// Hard error: a touched proposition without returned text.
	g.Client = realizeClient(map[string]any{"changes": []change{sep}, "propositions": props[:1]})
	if _, _, err := g.Realize(ctx, RealizeInput{Base: base, Target: v, Edits: edits, Prefix: "s0."}); err == nil || !strings.Contains(err.Error(), "p2 has no returned text") {
		t.Fatalf("missing touched text not rejected: %v", err)
	}

	// Infeasible is allowed for R-type but not for R-del.
	g.Client = realizeClient(map[string]any{"infeasible": true, "reason": "cannot"})
	if _, _, err := g.Realize(ctx, RealizeInput{Base: base, Target: v, Edits: edits, Prefix: "s0."}); err == nil {
		t.Fatal("infeasible accepted for R-del")
	}
	e := base.Graph.Edges[1]
	tv := perturb.Variant{Graph: base.Graph.Clone(), Edits: []perturb.Edit{{Kind: perturb.RType, Edge: &e, NewKind: ged.RA}}}
	res, _, err = g.Realize(ctx, RealizeInput{Base: base, Target: tv, Edits: tv.Edits, Prefix: "s0."})
	if err != nil || !res.Infeasible || res.Reason != "cannot" {
		t.Fatalf("infeasible R-type: %+v %v", res, err)
	}
}

// The diagnostic CA relabel (dataset-spec §2.2): the v3 instruction names the direction in words and the signalling
// of the new relation, says that infeasible is an expected answer, and the realizer may return it infeasible.
func TestRealizeTypeCA(t *testing.T) {
	for _, d := range perturb.TypeCADirections {
		e := perturb.Edit{Kind: perturb.RTypeCA, Edge: &ged.Edge{From: "s0.p4", To: "s0.p3", Kind: d[0]}, NewKind: d[1], Direction: perturb.DirectionLabel(d[0], d[1])}
		got := describeV3(e)
		gloss := relationNoun[d[0]] + " becomes " + relationNoun[d[1]]
		for _, want := range []string{`Change the relation "p4 ` + verbs[d[0]] + ` p3"`, "p4 now " + verbs[d[1]] + " p3", gloss, "expected answer for this edit"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q lacks %q", e.Direction, got, want)
			}
		}
		if hint := caDirectionHint(e); !strings.Contains(got, hint) {
			t.Errorf("%s: no signalling hint in %q", e.Direction, got)
		}
		if describe(e) == "" {
			t.Errorf("%s: no v1 wording", e.Direction)
		}
	}
	// Edit.Direction is what the realizer is told, even if the edge were recorded after the relabel.
	e := perturb.Edit{Kind: perturb.RTypeCA, Edge: &ged.Edge{From: "s0.p2", To: "s0.p1", Kind: ged.CA}, NewKind: ged.CA, Direction: "RA→CA"}
	if g := caDirectionGloss(e); g != "a support becomes an attack" {
		t.Errorf("gloss %q", g)
	}
	// The R-type (estimation) wording is unchanged by the new kind.
	rt := perturb.Edit{Kind: perturb.RType, Edge: &ged.Edge{From: "s0.p2", To: "s0.p1", Kind: ged.RA}, NewKind: ged.MA}
	if strings.Contains(describeV3(rt), "expected answer") || strings.Contains(describe(rt), "wording of both") {
		t.Errorf("R-type wording changed: %q / %q", describeV3(rt), describe(rt))
	}

	base := baseDiscussion()
	edge := base.Graph.Edges[1] // p4 attacks p3
	g := base.Graph.Clone()
	g.Edges[1].Kind = ged.RA
	tv := perturb.Variant{Graph: g, Edits: []perturb.Edit{{Kind: perturb.RTypeCA, Edge: &edge, NewKind: ged.RA, Direction: "CA→RA"}}}
	var user string
	gen := &Generator{Model: "m", Version: V2, Client: &llm.FuncClient{Fn: func(_ context.Context, req llm.Request) (*llm.Response, error) {
		user = req.Messages[len(req.Messages)-1].Content
		return &llm.Response{Text: `{"infeasible": true, "reason": "p4 would have to be negated", "changes": [], "propositions": []}`, Model: req.Model}, nil
	}}}
	res, _, err := gen.Realize(context.Background(), RealizeInput{Base: base, Target: tv, Edits: tv.Edits, Prefix: "s0."})
	if err != nil || !res.Infeasible || res.Reason != "p4 would have to be negated" {
		t.Fatalf("infeasible R-type-CA: %+v %v", res, err)
	}
	if !strings.Contains(user, "an attack becomes a support") || !slices.Contains(InfeasibleKinds, perturb.RTypeCA) {
		t.Fatalf("realize request lacks the CA instruction:\n%s", user)
	}
}

func TestCheckUntouched(t *testing.T) {
	base := baseDiscussion().Utterances
	out := slices.Clone(base)
	newIdx := []int{0, 1, 2, 3, 4, 5}
	if err := CheckUntouched(base, out, newIdx, nil); err != nil {
		t.Fatal(err)
	}
	out[4].Text += " "
	if err := CheckUntouched(base, out, newIdx, nil); err == nil {
		t.Fatal("a changed untouched utterance passed")
	}
	if err := CheckUntouched(base, out, newIdx, []int{4}); err != nil {
		t.Fatal(err)
	}
}

func verification(t *testing.T, s string) Verification {
	t.Helper()
	var v Verification
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerificationProblems(t *testing.T) {
	base := baseDiscussion()
	e := base.Graph.Edges[0] // p2 RA p1
	target := perturb.Variant{Graph: base.Graph.Clone()}
	target.Graph.Edges[0].Kind = ged.MA
	edits := []perturb.Edit{{Kind: perturb.RType, Edge: &e, NewKind: ged.MA}}
	q := BuildVerifyQuery(base, base, target, edits, rand.New(rand.NewPCG(1, 1)), 2)
	if len(q.Pairs) != 2 || q.Pairs[0].Kind != ged.MA || q.Pairs[0].From != "s0.p2" || !q.Pairs[1].Sampled {
		t.Fatalf("query pairs %+v", q.Pairs)
	}
	msg := q.userMessage(base)
	if strings.Contains(msg, "supports") || strings.Contains(msg, "restates") || !strings.Contains(msg, "1. C2 and C1") {
		t.Fatalf("verifier input leaks the plan or misses the pair:\n%s", msg)
	}
	// intended: p2 (C1? labels follow TouchedProps order: p2 → C1, p1 → C2) restates p1 → pair (p1, p2), from second
	ok := verification(t, `{"propositions":[{"label":"C1","content":"same"},{"label":"C2","content":"paraphrase"},{"label":"C3","content":"same"},{"label":"C4","content":"same"}],
		"pairs":[{"index":1,"relation":"MA","direction":"second_to_first"},{"index":2,"relation":"CA","direction":"second_to_first"}],"fluency":"4","coherence":"2"}`)
	if p := ok.Problems(q); len(p) != 0 {
		t.Fatalf("correct verification has problems %v", p)
	}
	// wrong relation type (RA read where MA was intended) and an unrequested negation
	bad := verification(t, `{"propositions":[{"label":"C1","content":"negated"},{"label":"C2","content":"same"},{"label":"C3","content":"same"},{"label":"C4","content":"same"}],
		"pairs":[{"index":1,"relation":"RA","direction":"second_to_first"},{"index":2,"relation":"CA","direction":"second_to_first"}],"fluency":"4","coherence":"4"}`)
	p := bad.Problems(q)
	if len(p) != 2 || !strings.Contains(p[0], "p2 is stated with reversed polarity") || !strings.Contains(p[1], "p2 supports p1, intended p2 restates p1") {
		t.Fatalf("problems %q", p)
	}
	if c := bad.Confusions(q); !slices.Equal(c, []string{"MA→RA"}) {
		t.Fatalf("confusions %v", c)
	}
	// collateral damage on a sampled edge, and fluency 2
	coll := verification(t, `{"propositions":[{"label":"C1","content":"same"},{"label":"C2","content":"same"},{"label":"C3","content":"same"},{"label":"C4","content":"same"}],
		"pairs":[{"index":1,"relation":"MA","direction":"second_to_first"},{"index":2,"relation":"none","direction":"none"}],"fluency":"2","coherence":"4"}`)
	if p := coll.Problems(q); len(p) != 2 || !strings.Contains(p[0], "untouched pair") || !strings.Contains(p[1], "fluency 2") {
		t.Fatalf("problems %q", p)
	}
}

// B1 (review-synth-impl): a relation-edit endpoint is referenced by its base text, so a realizer that negates the
// attacker to make CA → RA fit, and reports the negated text, is read as "negated" and rejected; a content edit
// (P-para) keeps the realizer's text as reference.
func TestVerifyReferenceIsBaseForRelationEndpoints(t *testing.T) {
	base := baseDiscussion()
	e := base.Graph.Edges[1] // p4 attacks p3
	target := perturb.Variant{Graph: base.Graph.Clone()}
	target.Graph.Edges[1].Kind = ged.RA
	edits := []perturb.Edit{{Kind: perturb.RTypeCA, Edge: &e, NewKind: ged.RA, Direction: "CA→RA"}, {Kind: perturb.PPara, Prop: "s0.p2", Strength: "weak"}}
	out := base
	out.Graph = base.Graph.Clone()
	negated := "The studies showing higher footfall were not done in large cities."
	para := "Without parking, shops lose passing trade."
	for i, p := range out.Graph.Props {
		switch p.ID {
		case "s0.p4":
			out.Graph.Props[i].Text = negated // the realizer's honest report of a negated endpoint
		case "s0.p2":
			out.Graph.Props[i].Text = para
		}
	}
	q := BuildVerifyQuery(base, out, target, edits, nil, 0)
	ref := map[string]PropExpect{}
	for _, pe := range q.Props {
		ref[pe.ID] = pe
	}
	if ref["s0.p4"].Ref != base.PropText("s0.p4") || ref["s0.p3"].Ref != base.PropText("s0.p3") || ref["s0.p2"].Ref != para {
		t.Fatalf("references %+v", q.Props)
	}
	// The verifier reads the negated endpoint against the base text.
	var props []string
	for _, pe := range q.Props {
		c := "same"
		if pe.ID == "s0.p4" {
			c = "negated"
		}
		props = append(props, fmt.Sprintf(`{"label":%q,"content":%q}`, pe.Label, c))
	}
	v := verification(t, `{"propositions":[`+strings.Join(props, ",")+`],"pairs":[{"index":1,"relation":"RA","direction":"second_to_first"}],"fluency":"4","coherence":"4"}`)
	if q.Pairs[0].From != "s0.p4" {
		t.Fatalf("pair %+v", q.Pairs[0])
	}
	p := v.Problems(q)
	if len(p) != 1 || !strings.Contains(p[0], "p4 is stated with reversed polarity") {
		t.Fatalf("polarity leak not caught: %q", p)
	}
}

// M3 (review-synth-impl): an infeasible return names the edit; without a valid number it falls back to the side's
// only refusable edit, and is an error when several could be meant.
func TestRealizeInfeasibleEdit(t *testing.T) {
	base := baseDiscussion()
	ra, ca := base.Graph.Edges[0], base.Graph.Edges[1]
	edits := []perturb.Edit{{Kind: perturb.XSpk}, {Kind: perturb.PPara, Prop: "s0.p1", Strength: "weak"},
		{Kind: perturb.RRev, Edge: &ra}, {Kind: perturb.RTypeCA, Edge: &ca, NewKind: ged.MA, Direction: "CA→MA"}}
	tv := perturb.Variant{Graph: base.Graph.Clone(), Edits: edits}
	g := &Generator{Model: "m", Version: V2}
	for _, c := range []struct {
		n    int
		want int
		err  bool
	}{{3, 3, false}, {2, 2, false}, {1, 0, true}, {0, 0, true}, {9, 0, true}} {
		g.Client = realizeClient(map[string]any{"infeasible": true, "reason": "x", "infeasible_edit": c.n})
		res, _, err := g.Realize(context.Background(), RealizeInput{Base: base, Target: tv, Edits: edits, Prefix: "s0."})
		if c.err != (err != nil) || (!c.err && (!res.Infeasible || res.InfeasibleEdit != c.want)) {
			t.Errorf("infeasible_edit %d: %+v %v", c.n, res, err)
		}
	}
	// one refusable edit: a missing number is attributed to it
	one := []perturb.Edit{{Kind: perturb.PPara, Prop: "s0.p1", Strength: "weak"}, {Kind: perturb.RTypeCA, Edge: &ca, NewKind: ged.MA, Direction: "CA→MA"}}
	g.Client = realizeClient(map[string]any{"infeasible": true, "reason": "x"})
	if res, _, err := g.Realize(context.Background(), RealizeInput{Base: base, Target: tv, Edits: one, Prefix: "s0."}); err != nil || res.InfeasibleEdit != 1 {
		t.Fatalf("single refusable edit: %+v %v", res, err)
	}
}

// M6: RealizeKey / VerifyKey are the cache keys of exactly the requests Realize / Verify send.
func TestRequestKeys(t *testing.T) {
	base := baseDiscussion()
	v, edits := rdelVariant(base)
	var sent []llm.Request
	c := &llm.FuncClient{Fn: func(_ context.Context, req llm.Request) (*llm.Response, error) {
		sent = append(sent, req)
		return &llm.Response{Text: `{"infeasible": false}`, Model: req.Model}, nil
	}}
	g := &Generator{Client: c, Model: "m", Version: V2, Verifier: &Agent{Client: c, Model: "v", Family: "openai"}}
	in := RealizeInput{Base: base, Target: v, Edits: edits, Prefix: "s0."}
	_, _, _ = g.Realize(context.Background(), in)
	q := BuildVerifyQuery(base, base, v, edits, nil, 1)
	_, _, _, _ = g.Verify(context.Background(), base, q)
	if len(sent) != 2 || g.RealizeKey(in) != llm.CacheKey(sent[0]) || g.VerifyKey(base, q) != llm.CacheKey(sent[1]) || sent[0].Schema == nil {
		t.Fatalf("keys do not match the sent requests (%d sent)", len(sent))
	}
}

// M7: a refused judgment is an error wrapping llm.ErrRefusal, never an answer.
func TestParseJudgeAnswerRefusal(t *testing.T) {
	if a, err := ParseJudgeAnswer(&llm.Response{Text: `{"answer":"same"}`}); err != nil || a != "same" {
		t.Fatalf("%q %v", a, err)
	}
	if _, err := ParseJudgeAnswer(&llm.Response{FinishReason: llm.FinishRefusal, Model: "m"}); !errors.Is(err, llm.ErrRefusal) {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := ParseJudgeAnswer(&llm.Response{FinishReason: "max_tokens", Text: `{"answ`}); !errors.Is(err, llm.ErrTruncated) {
		t.Fatalf("truncation: %v", err)
	}
}

func TestAgreement(t *testing.T) {
	gold := baseDiscussion().Graph
	ann := SourceAnnotation{}
	if err := json.Unmarshal([]byte(`{"propositions":[],"relations":[{"kind":"RA","from":"P2","to":"P1"},{"kind":"CA","from":"P4","to":"P3"}]}`), &ann); err != nil {
		t.Fatal(err)
	}
	if a := Agreement(gold, ann); !a.Pass || a.Matched != 2 || a.Extra != 0 {
		t.Fatalf("%+v", a)
	}
	_ = json.Unmarshal([]byte(`{"propositions":[{"label":"P1","content":"negated"}],"relations":[{"kind":"RA","from":"P2","to":"P1"},{"kind":"CA","from":"P3","to":"P4"},{"kind":"RA","from":"P1","to":"P3"}]}`), &ann)
	if a := Agreement(gold, ann); a.Pass || a.DirWrong != 1 || a.Extra != 1 || a.Negated != 1 || a.Recall != 0.5 {
		t.Fatalf("%+v", a)
	}
}

// Allocation for 750 sources: generator × exemplar pair balanced within ±1, generators balanced
// within every format, the same-format exemplar always in the pair, no two consecutive sources on
// the same pair, and no name in more than 3% of sources.
func TestAllocateBalanced(t *testing.T) {
	exs := loadBank(t)
	gens := []string{"gpt-5.6-luna", "claude-sonnet-5-5"}
	names := NewNameAllocator(NamePool, nil, rand.New(rand.NewPCG(3, 3)))
	specs, err := Allocate(AllocOptions{N: 750, Seed: 7, Generators: gens, Exemplars: exs, Names: names})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 750 {
		t.Fatalf("%d specs", len(specs))
	}
	pairGen := map[int]map[string]int{}
	fmtGen := map[string]map[string]int{}
	fmtN := map[string]int{}
	for i, s := range specs {
		if s.ExemplarPair.ID < 1 || s.ExemplarPair.ID > 21 || len(s.ExemplarPair.Order) != 2 {
			t.Fatalf("spec %d pair %+v", i, s.ExemplarPair)
		}
		hasSame := false
		for _, id := range s.ExemplarPair.Order {
			ex, _ := FindExemplar(exs, id)
			hasSame = hasSame || ex.Format == s.Format
		}
		if !hasSame {
			t.Fatalf("spec %d (%s) lacks a same-format exemplar: %v", i, s.Format, s.ExemplarPair.Order)
		}
		if i > 0 && specs[i-1].ExemplarPair.ID == s.ExemplarPair.ID {
			t.Errorf("specs %d and %d share pair %d", i-1, i, s.ExemplarPair.ID)
		}
		if pairGen[s.ExemplarPair.ID] == nil {
			pairGen[s.ExemplarPair.ID] = map[string]int{}
		}
		pairGen[s.ExemplarPair.ID][s.Generator]++
		if fmtGen[s.Format] == nil {
			fmtGen[s.Format] = map[string]int{}
		}
		fmtGen[s.Format][s.Generator]++
		fmtN[s.Format]++
		if s.CA < 1 || s.Isolated < 2 || s.Props < 8 || s.Props > 20 || s.RA < 1 || s.Props-s.Isolated > 2*s.Edges() {
			t.Fatalf("spec %d targets %+v", i, s)
		}
	}
	for pid, c := range pairGen {
		if d := c[gens[0]] - c[gens[1]]; d < -1 || d > 1 {
			t.Errorf("pair %d: %v", pid, c)
		}
	}
	for f, c := range fmtGen {
		if d := c[gens[0]] - c[gens[1]]; d < -1 || d > 1 {
			t.Errorf("format %s: %v", f, c)
		}
	}
	if fmtN["TV debate with a moderator"] != 375 || fmtN["interview"] != 75 {
		t.Errorf("format counts %v", fmtN)
	}
	for n, u := range names.Uses() {
		if float64(u) > 0.03*750 {
			t.Errorf("name %s used in %d sources", n, u)
		}
	}
	if len(NamePool) < 120 {
		t.Errorf("name pool has %d names", len(NamePool))
	}
	for _, ex := range exs {
		for _, n := range ex.Names() {
			if slices.Contains(NamePool, n) {
				t.Errorf("exemplar name %q in the pool", n)
			}
		}
	}
}

func TestImportQT30(t *testing.T) {
	srcs, f, err := ImportQT30("testdata/qt30")
	if err != nil {
		t.Fatal(err)
	}
	want := QT30Filter{Files: 6, NotTest: 1, Metamodel: 1, Size: 1, NotLocated: 1, FewEdges: 1, Eligible: 1}
	if f != want {
		t.Fatalf("filter %+v, want %+v", f, want)
	}
	s := srcs[0]
	d := s.Discussion
	if s.NodesetID != "90001" || s.Episode != "cutietestrun28May2020" || len(d.Graph.Props) != 9 || len(d.Graph.Edges) != 6 || len(d.Utterances) != 9 {
		t.Fatalf("source %+v", s)
	}
	if d.Utterances[0].Speaker != "Fiona Bruce" || d.Utterances[0].Text != "claim number 1 is stated here" {
		t.Fatalf("utterance %+v", d.Utterances[0])
	}
	if d.PropUtterance["q90001.p5"] != 4 || s.Unanchored != 0 {
		t.Fatalf("prop→utterance %v", d.PropUtterance)
	}
	kinds := map[string]int{}
	for _, e := range d.Graph.Edges {
		kinds[e.Kind]++
	}
	if kinds[ged.RA] != 4 || kinds[ged.CA] != 1 || kinds[ged.MA] != 1 {
		t.Fatalf("edge kinds %v", kinds)
	}
	if !slices.ContainsFunc(d.Graph.Edges, func(e ged.Edge) bool { return e.From == "q90001.p2" && e.To == "q90001.p1" && e.Kind == ged.RA }) {
		t.Fatalf("multi-premise RA not folded: %v", d.Graph.Edges)
	}
}

// TestSurfaceTargets: the two sides of a graph-invariant triplet are realized differently. A targeted X-surf
// names its utterance (so the two requests, and their cache keys, differ) and may change only that
// neighbourhood; an X-spk swaps the pair of speakers its edit names.
func TestSurfaceTargets(t *testing.T) {
	base := baseDiscussion()
	u1, u3 := 1, 3
	v := perturb.Variant{Graph: base.Graph.Clone()}
	c := realizeClient(map[string]any{"changes": []any{map[string]any{"utterance": 3, "op": "replace", "speaker": "Gareth", "text": "When streets are closed to cars, footfall goes up.", "role": "edit"}},
		"propositions": []any{map[string]any{"id": "p3", "unchanged": true}}})
	g := &Generator{Client: c, Model: "m", Version: V2}
	inA := RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSurf, Utterance: &u1}}, Prefix: "s0."}
	inB := RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSurf, Utterance: &u3}}, Prefix: "s0."}
	if g.RealizeKey(inA) == g.RealizeKey(inB) {
		t.Error("X-surf on different utterances has the same realization request")
	}
	// The realizer rewrites utterance 3: on target for inB, collateral (counted, as for any other edit) for a target of 0.
	if res, _, err := g.Realize(context.Background(), inB); err != nil || res.Stats.Collateral != 0 {
		t.Errorf("reword of the target utterance: collateral %d, err %v", res.Stats.Collateral, err)
	}
	inFar := RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSurf, Utterance: new(int)}}, Prefix: "s0."}
	if res, _, err := g.Realize(context.Background(), inFar); err != nil || res.Stats.Collateral != 1 {
		t.Errorf("reword of utterance 3 for an X-surf that targets utterance 0: collateral %d, err %v", res.Stats.Collateral, err)
	}

	names := func(us []Utterance) (out []string) {
		for _, u := range us {
			out = append(out, u.Speaker)
		}
		return out
	}
	// Speakers in order of first appearance: Host, Priya, Gareth, Noor.
	a, _, err := g.Realize(context.Background(), RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSpk, Speakers: []int{1, 2}}}, Prefix: "s0."})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := g.Realize(context.Background(), RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSpk, Speakers: []int{0, 3}}}, Prefix: "s0."})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(a.Discussion.Utterances), []string{"Host", "Gareth", "Host", "Priya", "Host", "Noor"}; !slices.Equal(got, want) {
		t.Errorf("swap of speakers 1 and 2: %v, want %v", got, want)
	}
	if got, want := names(b.Discussion.Utterances), []string{"Noor", "Priya", "Noor", "Gareth", "Noor", "Host"}; !slices.Equal(got, want) {
		t.Errorf("swap of speakers 0 and 3: %v, want %v", got, want)
	}
	// An X-spk without a pair swaps the first two speakers, as before.
	old, _, _ := g.Realize(context.Background(), RealizeInput{Base: base, Target: v, Edits: []perturb.Edit{{Kind: perturb.XSpk}}, Prefix: "s0."})
	if got, want := names(old.Discussion.Utterances), []string{"Priya", "Host", "Priya", "Gareth", "Priya", "Noor"}; !slices.Equal(got, want) {
		t.Errorf("default swap: %v, want %v", got, want)
	}
}
