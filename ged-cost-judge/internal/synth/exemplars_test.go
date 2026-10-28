package synth

import (
	"context"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
)

const exemplarDir = "../../prompts/exemplars"

func loadBank(t *testing.T) []Exemplar {
	t.Helper()
	exs, err := LoadExemplars(exemplarDir)
	if err != nil {
		t.Fatal(err)
	}
	return exs
}

// All seven exemplars parse, their gold graphs match the front matter, and they share no 6-gram
// (the zero baseline of the copy rule, dataset-spec §1.5.2).
func TestLoadExemplars(t *testing.T) {
	exs := loadBank(t)
	if len(exs) != 7 {
		t.Fatalf("got %d exemplars, want 7", len(exs))
	}
	formats := map[string]int{}
	for _, ex := range exs {
		formats[ex.Format]++
		if strings.Contains(ex.Topic, "reserved") {
			t.Errorf("%s: topic keeps the reserved note: %q", ex.ID, ex.Topic)
		}
		if ex.CA < 1 || ex.Isolated < 1 {
			t.Errorf("%s: CA %d isolated %d", ex.ID, ex.CA, ex.Isolated)
		}
		r := ex.Render()
		if !strings.HasPrefix(r, "Format: "+ex.Format+"\nTopic: "+ex.Topic+"\nTargets: propositions ") || !strings.Contains(r, "```json\n{") {
			t.Errorf("%s: render:\n%s", ex.ID, r)
		}
	}
	for _, f := range Formats {
		if formats[f.Format] == 0 {
			t.Errorf("no exemplar for format %q", f.Format)
		}
	}
	if formats["TV debate with a moderator"] != 2 {
		t.Errorf("want two debate exemplars, got %d", formats["TV debate with a moderator"])
	}
	for i := range exs {
		for j := i + 1; j < len(exs); j++ {
			if s := SharedNgrams(exs[i].Text(), exs[j].Text(), 6); len(s) > 0 {
				t.Errorf("%s and %s share 6-grams %q", exs[i].ID, exs[j].ID, s)
			}
		}
	}
	ex1, _ := FindExemplar(exs, "ex01-debate-small")
	names := ex1.Names()
	for _, want := range []string{"Dev", "Marianne", "Tom", "Harlow Bay", "Ashcombe"} {
		if !contains(names, want) {
			t.Errorf("ex01 names %v lack %q", names, want)
		}
	}
	if contains(names, "Host") {
		t.Error("Host is a role, not a name")
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestPairID(t *testing.T) {
	exs := loadBank(t)
	ids := ExemplarIDs(exs)
	seen := map[int]bool{}
	for i := range ids {
		for j := range ids {
			if i == j {
				continue
			}
			p := PairID(ids, ids[i], ids[j])
			if p != PairID(ids, ids[j], ids[i]) || p < 1 || p > 21 {
				t.Fatalf("pair %s,%s → %d", ids[i], ids[j], p)
			}
			seen[p] = true
		}
	}
	if len(seen) != 21 {
		t.Fatalf("%d distinct pair ids", len(seen))
	}
}

// The v2 source prompt renders the two exemplars of the spec, in its order; the pair is therefore
// part of the request (and so of the cache key).
func TestSourcePromptCarriesExemplars(t *testing.T) {
	exs := loadBank(t)
	p, err := LoadPrompt("../../prompts", "synth_source_v2")
	if err != nil {
		t.Fatal(err)
	}
	var reqs []llm.Request
	c := &llm.FuncClient{Fn: func(_ context.Context, req llm.Request) (*llm.Response, error) {
		reqs = append(reqs, req)
		return &llm.Response{Text: exs[0].JSON, Model: req.Model}, nil
	}}
	g := &Generator{Client: c, Model: "gpt-5.6-luna", Version: V2, SourcePrompt: p, Exemplars: exs}
	ids := ExemplarIDs(exs)
	spec := Spec{Format: "meeting", Topic: "Should X?", Props: 11, RA: 5, CA: 1, MA: 2, Isolated: 2, Names: []string{"Abena", "Lars"},
		ExemplarPair: ExemplarPair{ID: PairID(ids, "ex03-meeting", "ex06-interview"), Order: []string{"ex06-interview", "ex03-meeting"}}}
	if _, _, err := g.GenerateSource(context.Background(), spec, "s0.", 0); err != nil {
		t.Fatal(err)
	}
	spec2 := spec
	spec2.ExemplarPair.Order = []string{"ex03-meeting", "ex07-opinion-essay"}
	if _, _, err := g.GenerateSource(context.Background(), spec2, "s0.", 0); err != nil {
		t.Fatal(err)
	}
	sys := reqs[0].Messages[0].Content
	i6, i3 := strings.Index(sys, "Topic: A free lighthouse museum"), strings.Index(sys, "Topic: Pay-what-you-can")
	if strings.Contains(sys, "{{exemplars}}") || i6 < 0 || i3 < 0 || i6 > i3 {
		t.Fatalf("exemplars not rendered in order:\n%s", sys)
	}
	if llm.CacheKey(reqs[0]) == llm.CacheKey(reqs[1]) {
		t.Fatal("different exemplar pairs gave the same cache key")
	}
	user := reqs[0].Messages[1].Content
	want := "Format: meeting\nTopic: Should X?\nSpeaker names: Abena, Lars\nTargets: propositions 11, RA 5, CA 1, MA 2, isolated 2, multi-premise no, chain no\n"
	if user != want {
		t.Fatalf("user message:\n%q\nwant\n%q", user, want)
	}
	noEx := RenderExemplarsInto(p.Body, nil)
	if strings.Contains(noEx, "example") || strings.Contains(noEx, "{{") {
		t.Fatalf("no-exemplar prompt still mentions examples:\n%s", noEx)
	}
}

func TestCopyCheck(t *testing.T) {
	exs := loadBank(t)
	ctx := context.Background()
	emb := &llm.HashEmbedder{}
	ex2, _ := FindExemplar(exs, "ex02-debate-large")

	// A verbatim copy (even with other speaker names) is caught by the n-gram, name and cos rules.
	cp := ex2.Discussion
	cp.Utterances = append([]Utterance(nil), cp.Utterances...)
	for i := range cp.Utterances {
		if cp.Utterances[i].Speaker != "Host" {
			cp.Utterances[i].Speaker = "Abena"
		}
	}
	res, err := CopyCheck(ctx, cp, exs, emb)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reject || len(res.Shared6) == 0 || !res.CosComputed || res.MaxCos < 0.99 || res.MaxCosWith != ex2.ID {
		t.Fatalf("verbatim copy not caught: %+v", res)
	}

	// A single borrowed place name is caught on its own.
	fresh := Discussion{Utterances: []Utterance{
		{Speaker: "Host", Text: "Should the county close its smallest rural bus routes? Priya, you sit on the transport board."},
		{Speaker: "Priya", Text: "Those routes carry almost nobody outside school hours, so the subsidy is hard to justify."},
		{Speaker: "Gareth", Text: "But for the people who do use them there is no other way to reach a doctor. In other words, the cost per rider is the wrong measure."},
		{Speaker: "Priya", Text: "A demand-responsive minibus could cover the same villages for less."},
	}}
	res, err = CopyCheck(ctx, fresh, exs, emb)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reject {
		t.Fatalf("fresh text rejected: %+v", res)
	}
	named := fresh
	named.Utterances = append([]Utterance(nil), fresh.Utterances...)
	named.Utterances[3].Text = "A demand-responsive minibus works well in Ashcombe."
	res, _ = CopyCheck(ctx, named, exs, nil)
	if !res.Reject || !contains(res.Names, "Ashcombe") || res.CosComputed {
		t.Fatalf("borrowed place name not caught: %+v", res)
	}
	// Signalling phrases are stoplisted: a shared "in other words" + 3 words does not count.
	if s := SharedNgrams("we said in other words the cost is high", "they said in other words the cost is high", 6); len(s) != 0 {
		t.Fatalf("stoplisted phrase counted: %q", s)
	}
}
