package synth

import (
	"context"
	"encoding/json"
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
