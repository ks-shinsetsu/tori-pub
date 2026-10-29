package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/synth"
)

// Non-T3 triplets have no predicted difference; writing them must not fail (pilot_triplets.json used to come out empty).
func TestPilotTripletJSON(t *testing.T) {
	src := ged.Graph{Props: []ged.Prop{{ID: "p1", Text: "a"}, {ID: "p2", Text: "b"}, {ID: "p3", Text: "c"}},
		Edges: []ged.Edge{{From: "p1", To: "p2", Kind: ged.RA}, {From: "p3", To: "p2", Kind: ged.CA}}}
	near := true
	rng := rand.New(rand.NewPCG(1, 2))
	for _, c := range []struct {
		typ  perturb.TripletType
		near *bool
	}{{perturb.T1Monotone, nil}, {perturb.T2SameKind, nil}, {perturb.T4Invariant, nil}, {perturb.T3Contrast, &near}} {
		a, b, pred := drawPair(rng, src, c.typ, c.near)
		tr := &pilotTriplet{Type: c.typ, NearTarget: c.near, EditsA: a.Edits, EditsB: b.Edits, PredDelta: finite(pred)}
		js, err := synth.MarshalIndent([]*pilotTriplet{tr})
		if err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		var back []map[string]any
		if err := json.Unmarshal(js, &back); err != nil || len(back) != 1 {
			t.Fatalf("%s: round trip: %v (%s)", c.typ, err, js)
		}
		if _, ok := back[0]["pred_delta"]; ok != (c.near != nil) {
			t.Errorf("%s: pred_delta present = %v", c.typ, ok)
		}
		for _, k := range []string{"flags", "plan", "plan_a", "judgments", "t1_delta0"} {
			if _, ok := back[0][k]; ok {
				t.Errorf("%s: v1 record carries v2 field %q", c.typ, k)
			}
		}
	}
}

// The v2 record (§6) round-trips through JSON with every field the statistics side reads: a T1 triplet and a
// t3_diag triplet whose first relabel was returned infeasible and redrawn inside the slot.
func TestPilotTripletV2RoundTrip(t *testing.T) {
	d0, five, one := 2.25, 5, 1
	cos := 0.71
	t1 := pilotTriplet{
		Type: perturb.T1Monotone, ID: "s0003-t1", SourceID: "s0003", Generator: "claude-sonnet-5-5", GeneratorFamily: "anthropic",
		VerifierFamily: "openai", VerifierModel: "gpt-5.6-luna-2026-07-01", ExemplarPair: 7, Exemplars: []string{"ex03-meeting", "ex01-debate-small"},
		Domain: "economy", Format: "meeting", TopicID: "d02-t004",
		Flags: tripletFlags{Pilot: true, PNegB: 1, Fit: []string{"t1_gate", "pilot"}},
		Plan: &perturb.PlanInfo{Type: perturb.T1Monotone, T1Target: perturb.T1High, T1Stratum: perturb.T1High, T1Delta0: 2.1, T1Extra: 2,
			PredDelta0: 2.1, EditsA: 1, EditsB: 3, Redraws: 4, Infeasible: []perturb.Edit{{Kind: perturb.PDel, Infeasible: "no untouched isolated proposition"}}},
		PlanA: []editRecord{{Edit: perturb.Edit{Kind: perturb.RAdd, Edge: &ged.Edge{From: "s3.p1", To: "s3.p4", Kind: ged.RA}, Direction: "RA", Distance: &five, Degree: []int{1, 2}}}},
		PlanB: []editRecord{{Edit: perturb.Edit{Kind: perturb.PShift, Prop: "s3.p2", Strength: "large", Degree: []int{1}}, RealizedCos: &cos},
			{Edit: perturb.Edit{Kind: perturb.PDelR, Prop: "s3.p5", Degree: []int{2}, EdgesRemoved: 2}}},
		T1Delta0: &d0, T1Stratum: perturb.T1High, CohA: 4, CohB: 2, FluA: 5, FluB: 4,
		Discarded:  []discardedPlan{{Plan: 0, Reason: "infeasible", Details: []string{"infeasible: needs a negation"}}},
		Infeasible: []string{"needs a negation"},
		Judgments: []judgmentRecord{{Judge: "claude-haiku-5-5", Family: "anthropic", Setting: "effort=low,thinking=adaptive", Order: -1,
			Raw: "2", Answer: "A", CacheKey: "k", PromptHash: "h", Batch: true}},
	}
	diagInfo := &perturb.PlanInfo{Type: perturb.T3Contrast, T3Diag: true, DiagCAType: true, Swapped: true, PredDelta0: -0.4, EditsA: 1, EditsB: 1}
	relabel := func(dir, reason string) editRecord {
		from, to, _ := strings.Cut(dir, "→")
		return editRecord{Edit: perturb.Edit{Kind: perturb.RTypeCA, Edge: &ged.Edge{From: "s3.p3", To: "s3.p1", Kind: from}, NewKind: to,
			Direction: dir, Distance: &one, Degree: []int{1, 3}, Infeasible: reason}}
	}
	diag := pilotTriplet{
		Type: perturb.T3Contrast, ID: "s0003-t2", SourceID: "s0003", Generator: "gpt-5.6-luna", VerifierFamily: "anthropic",
		Flags: tripletFlags{Pilot: true, Fit: []string{"diag_ca_type", "t3_diag", "pilot"}},
		Plan:  diagInfo, SubPlanned: perturb.T3Diag, DiagInfeasible: 1, Replans: 1,
		PlanA: []editRecord{{Edit: perturb.Edit{Kind: perturb.RDel, Edge: &ged.Edge{From: "s3.p2", To: "s3.p1", Kind: ged.RA}, Distance: &one, Degree: []int{1, 3}}}},
		PlanB: []editRecord{relabel("RA→CA", "")},
		Discarded: []discardedPlan{{Plan: 0, Info: diagInfo, Reason: "infeasible", DiagRedraw: true, Details: []string{"infeasible: would negate p3"},
			EditsA: []editRecord{{Edit: perturb.Edit{Kind: perturb.PAdd, Prop: "n1", Degree: []int{0}}}}, EditsB: []editRecord{relabel("CA→RA", "would negate p3")}}},
		Infeasible: []string{"would negate p3"},
	}
	for _, tr := range []pilotTriplet{t1, diag} {
		b, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		var back pilotTriplet
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		b2, _ := json.Marshal(back)
		if string(b) != string(b2) {
			t.Fatalf("round trip differs:\n%s\n%s", b, b2)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for _, k := range []string{"plan", "plan_a", "plan_b", "verifier_family", "flags", "discarded_plans", "infeasible_reasons"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: record lacks %q", tr.ID, k)
			}
		}
		if f := m["flags"].(map[string]any); f["pilot"] != true || f["fit"] == nil {
			t.Errorf("%s: flags %v", tr.ID, f)
		}
	}
	b1, _ := json.Marshal(t1)
	for _, want := range []string{`"t1_delta0":2.25`, `"t1_stratum":"\u003e=2"`, `"t1_target":"\u003e=2"`, `"exemplar_pair":7`, `"coh_a":4`,
		`"distance":5`, `"degree":[1,2]`, `"edges_removed":2`, `"realized_cos":0.71`, `"infeasible":[{"kind":"P-del","infeasible":"no untouched isolated proposition"}]`,
		`"pred_delta0":2.1`, `"n_edits_b":3`, `"judgments"`} {
		if !strings.Contains(string(b1), want) {
			t.Errorf("T1 record lacks %s: %s", want, b1)
		}
	}
	b2, _ := json.Marshal(diag)
	for _, want := range []string{`"t3_diag":true`, `"diag_ca_type":true`, `"swapped":true`, `"t3_sub_planned":"diag"`, `"diag_infeasible":1`,
		`"diag_redraw":true`, `"plan_info":{`, `"direction":"CA→RA","distance":1,"degree":[1,3],"infeasible":"would negate p3"`, `"new_kind":"CA","direction":"RA→CA"`} {
		if !strings.Contains(string(b2), want) {
			t.Errorf("t3_diag record lacks %s: %s", want, b2)
		}
	}
}

func TestICC(t *testing.T) {
	// identical groups → ICC ≤ 0; separated groups → ICC near 1
	same := map[string][]float64{"a": {0, 1, 0, 1}, "b": {0, 1, 0, 1}}
	if icc, _, _ := iccOneWay(same); icc > 0 {
		t.Fatalf("icc %v", icc)
	}
	sep := map[string][]float64{"a": {0, 0.1, 0}, "b": {1, 0.9, 1}, "c": {2, 2.1, 2}}
	if icc, k, n := iccOneWay(sep); icc < 0.95 || k != 3 || n != 9 {
		t.Fatalf("icc %v k %d n %d", icc, k, n)
	}
}

// ---------------------------------------------------------------------------------------------
// An offline end-to-end run of the v2 pipeline with a fake LLM that behaves like a careful model:
// sources meet their targets, the realizer applies the edits and returns every touched proposition,
// the verifier reads the intended structure, the judges answer pseudo-randomly.

type fakeVariant struct {
	ids     map[string]string // proposition text → short id
	deleted map[string]bool
	rels    map[[2]string]string // (from, to) short ids → kind
	retried bool
}

type fakeLLM struct {
	mu       sync.Mutex
	nonce    int
	sources  map[string][][3]string // first proposition text → relations (from, kind, to) as short ids
	variants map[string]*fakeVariant
	calls    map[string]int
	batches  int
	// caSeen marks the sources (by first utterance) whose first CA relabel was declared infeasible.
	caSeen       map[string]bool
	caInfeasible int
}

func newFakeLLM() *fakeLLM {
	return &fakeLLM{sources: map[string][][3]string{}, variants: map[string]*fakeVariant{}, calls: map[string]int{}, caSeen: map[string]bool{}}
}

var (
	reTarget = regexp.MustCompile(`propositions (\d+), RA (\d+), CA (\d+), MA (\d+), isolated (\d+)`)
	reUtt    = regexp.MustCompile(`^\[(\d+)\] ([^:]+): (.*)$`)
	reProp   = regexp.MustCompile(`^(\S+) \(utterance (-?\d+)\): (.*)$`)
	reRel    = regexp.MustCompile(`^(\S+) (supports|attacks|restates) (\S+)$`)
	reLabel  = regexp.MustCompile(`^([CP]\d+): (.*)$`)
	rePair   = regexp.MustCompile(`^(\d+)\. (C\d+) and (C\d+)$`)
	kindOf   = map[string]string{"supports": ged.RA, "attacks": ged.CA, "restates": ged.MA}
	verbOf   = map[string]string{ged.RA: "supports", ged.CA: "attacks", ged.MA: "restates"}
)

func hashWords(seed string, n int) string {
	words := []string{"harbour", "levy", "ledger", "orchard", "ferry", "quota", "tariff", "mural", "canal", "pantry", "kiln", "depot",
		"glacier", "ballot", "meadow", "lantern", "satchel", "pylon", "tundra", "cobble", "furnace", "ripple", "saddle", "trellis"}
	h := sha256.Sum256([]byte(seed))
	var out []string
	for i := range n {
		out = append(out, words[int(h[i%32]+byte(i*7))%len(words)])
	}
	return strings.Join(out, " ")
}

func (f *fakeLLM) next() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce++
	return f.nonce
}

func (f *fakeLLM) complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.mu.Lock()
	f.calls[req.Tag]++
	f.mu.Unlock()
	user := req.Messages[len(req.Messages)-1].Content
	var out any
	switch req.Tag {
	case "synth_source":
		out = f.source(user)
	case "verify_source":
		out = f.verifySource(user)
	case "realize":
		out = f.realize(user)
	case "verify":
		out = f.verify(user)
	case "synth_topics":
		var qs []string
		for i := range synth.TopicsPerCall {
			qs = append(qs, fmt.Sprintf("Should %s be decided by %d?", hashWords(user+strconv.Itoa(i), 4), i))
		}
		out = map[string]any{"questions": qs}
	case "judge":
		out = map[string]string{"answer": fakeJudge(user)}
	default:
		return nil, fmt.Errorf("fake: unexpected tag %q", req.Tag)
	}
	b, _ := json.Marshal(out)
	return &llm.Response{Text: string(b), Model: req.Model + "-test", Usage: llm.Usage{InputTokens: int64(len(user) / 4), OutputTokens: int64(len(b) / 4)}}, nil
}

// fakeJudge prefers the version with fewer lines changed from S, so the T1 superset is mostly judged farther
// (the judge QC of decisions-stats §9.1 passes); ties and one answer in eight are pseudo-random.
func fakeJudge(user string) string {
	h := sha256.Sum256([]byte(user))
	rest, v2, _ := strings.Cut(user, "\nVersion 2:\n")
	s, v1, _ := strings.Cut(rest, "\nVersion 1:\n")
	orig := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		orig[l] = true
	}
	changed := func(v string) int {
		n := 0
		for _, l := range strings.Split(v, "\n") {
			if l != "" && !orig[l] {
				n++
			}
		}
		return n
	}
	c1, c2 := changed(v1), changed(v2)
	switch {
	case h[1]%8 == 0 || c1 == c2:
		return []string{"1", "2", "same", "1"}[h[0]%4]
	case c1 < c2:
		return "1"
	}
	return "2"
}

func (f *fakeLLM) source(user string) any {
	m := reTarget.FindStringSubmatch(user)
	at := func(i int) int { n, _ := strconv.Atoi(m[i]); return n }
	n, ra, ca, ma, iso := at(1), at(2), at(3), at(4), at(5)
	tag := fmt.Sprintf("src%d", f.next())
	type prop struct {
		ID        string `json:"id"`
		Utterance int    `json:"utterance"`
		Text      string `json:"text"`
	}
	type rel struct {
		Kind string `json:"kind"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	var utts []synth.Utterance
	var props []prop
	for i := range n {
		text := fmt.Sprintf("The %s claim %d says %s.", tag, i+1, hashWords(tag+strconv.Itoa(i), 5))
		utts = append(utts, synth.Utterance{Speaker: []string{"Host", "Abena", "Lars"}[i%3], Text: text})
		props = append(props, prop{ID: fmt.Sprintf("p%d", i+1), Utterance: i, Text: text})
	}
	conn := n - iso
	var pairs [][2]int
	for i := 2; i <= conn; i++ {
		pairs = append(pairs, [2]int{i, 1})
	}
	for j := 2; j <= conn; j++ {
		for i := j + 1; i <= conn; i++ {
			pairs = append(pairs, [2]int{i, j})
		}
	}
	var rels []rel
	var gold [][3]string
	kinds := append(append(slicesRepeat(ged.RA, ra), slicesRepeat(ged.CA, ca)...), slicesRepeat(ged.MA, ma)...)
	for k, kind := range kinds {
		if k >= len(pairs) {
			break
		}
		from, to := fmt.Sprintf("p%d", pairs[k][0]), fmt.Sprintf("p%d", pairs[k][1])
		rels = append(rels, rel{kind, from, to})
		gold = append(gold, [3]string{from, kind, to})
	}
	f.mu.Lock()
	f.sources[props[0].Text] = gold
	f.mu.Unlock()
	return map[string]any{"utterances": utts, "propositions": props, "relations": rels}
}

func slicesRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func (f *fakeLLM) verifySource(user string) any {
	var first string
	for _, line := range strings.Split(user, "\n") {
		if m := reLabel.FindStringSubmatch(line); m != nil && m[1] == "P1" {
			first = m[2]
		}
	}
	f.mu.Lock()
	gold := f.sources[first]
	f.mu.Unlock()
	var rels []map[string]string
	for _, g := range gold {
		rels = append(rels, map[string]string{"kind": g[1], "from": "P" + g[0][1:], "to": "P" + g[2][1:]})
	}
	return map[string]any{"propositions": []any{}, "relations": rels}
}

func (f *fakeLLM) realize(user string) any {
	var utts []synth.Utterance
	propUtt := map[string]int{}
	propText := map[string]string{}
	var propOrder []string
	rels := map[[2]string]string{}
	var mustReturn []string
	var edits []string
	section := ""
	for _, line := range strings.Split(user, "\n") {
		switch {
		case line == "Utterances:" || line == "Propositions:" || line == "Relations:" || line == "Edits:":
			section = line
			continue
		case strings.HasPrefix(line, "Propositions you must return"):
			_, list, _ := strings.Cut(line, ": ")
			mustReturn = parseList(list)
			continue
		}
		switch section {
		case "Utterances:":
			if m := reUtt.FindStringSubmatch(line); m != nil {
				utts = append(utts, synth.Utterance{Speaker: m[2], Text: m[3]})
			}
		case "Propositions:":
			if m := reProp.FindStringSubmatch(line); m != nil {
				u, _ := strconv.Atoi(m[2])
				propUtt[m[1]], propText[m[1]] = u, m[3]
				propOrder = append(propOrder, m[1])
			}
		case "Relations:":
			if m := reRel.FindStringSubmatch(line); m != nil {
				rels[[2]string{m[1], m[3]}] = kindOf[m[2]]
			}
		case "Edits:":
			if strings.TrimSpace(line) != "" {
				edits = append(edits, line)
			}
		}
	}
	v := &fakeVariant{ids: map[string]string{}, deleted: map[string]bool{}, rels: rels}
	reEdge := regexp.MustCompile(`"(\S+) (supports|attacks|restates) (\S+)"`)
	for _, e := range edits {
		m := reEdge.FindStringSubmatch(e)
		switch {
		case strings.Contains(e, "Remove the relation"):
			delete(v.rels, [2]string{m[1], m[3]})
		case strings.Contains(e, "Add the relation"):
			v.rels[[2]string{m[1], m[3]}] = kindOf[m[2]]
		case strings.Contains(e, "Change the relation"):
			nk := regexp.MustCompile(`now (supports|attacks|restates) `).FindStringSubmatch(e)
			v.rels[[2]string{m[1], m[3]}] = kindOf[nk[1]]
		case strings.Contains(e, "Reverse the relation"):
			delete(v.rels, [2]string{m[1], m[3]})
			v.rels[[2]string{m[3], m[1]}] = kindOf[m[2]]
		case strings.Contains(e, "Remove proposition "):
			id := strings.Fields(strings.SplitN(e, "Remove proposition ", 2)[1])[0]
			v.deleted[id] = true
			for k := range v.rels {
				if k[0] == id || k[1] == id {
					delete(v.rels, k)
				}
			}
		}
	}
	nonce := f.next()
	// The first diagnostic CA relabel on each source is declared infeasible, later ones are feasible (exercises the
	// in-slot t3_diag redraw); every third other realization with a relabel declares it infeasible (the plan redraw).
	if strings.Contains(user, "expected answer for this edit") && len(utts) > 0 {
		f.mu.Lock()
		first := !f.caSeen[utts[0].Text]
		f.caSeen[utts[0].Text] = true
		if first {
			f.caInfeasible++
		}
		f.mu.Unlock()
		if first {
			return map[string]any{"infeasible": true, "reason": "the CA relabel would negate a proposition", "changes": []any{}, "propositions": []any{}}
		}
	} else if nonce%3 == 0 && strings.Contains(user, "Change the relation") && !strings.Contains(user, "was rejected") {
		return map[string]any{"infeasible": true, "reason": "the relabel needs a negation", "changes": []any{}, "propositions": []any{}}
	}
	newText := map[string]string{}
	var props []map[string]any
	for _, id := range mustReturn {
		t := fmt.Sprintf("%s (rev %d)", propText[id], nonce)
		if _, ok := propText[id]; !ok {
			t = fmt.Sprintf("A fresh aside %d about %s.", nonce, hashWords(strconv.Itoa(nonce), 3))
		}
		newText[id] = t
		props = append(props, map[string]any{"id": id, "unchanged": false, "text": t})
	}
	// rewrite the utterances of the returned propositions; new ones go into a new utterance at the end
	byUtt := map[int][]string{}
	var fresh []string
	for _, id := range mustReturn {
		if u, ok := propUtt[id]; ok {
			byUtt[u] = append(byUtt[u], id)
		} else {
			fresh = append(fresh, id)
		}
	}
	var changes []map[string]any
	for u, ids := range byUtt {
		var parts []string
		for _, id := range propOrder {
			if propUtt[id] != u || v.deleted[id] {
				continue
			}
			if t, ok := newText[id]; ok {
				parts = append(parts, t)
			} else {
				parts = append(parts, propText[id])
			}
		}
		_ = ids
		changes = append(changes, map[string]any{"utterance": u, "op": "replace", "speaker": utts[u].Speaker, "text": strings.Join(parts, " ") + fmt.Sprintf(" #n%d", nonce), "role": "edit"})
	}
	for _, id := range fresh {
		changes = append(changes, map[string]any{"utterance": len(utts) - 1, "op": "insert_after", "speaker": "Lars", "text": newText[id] + fmt.Sprintf(" #n%d", nonce), "role": "edit"})
	}
	if len(changes) == 0 && len(utts) > 0 { // X-surf
		changes = append(changes, map[string]any{"utterance": 0, "op": "replace", "speaker": utts[0].Speaker, "text": utts[0].Text + fmt.Sprintf(" Indeed. #n%d", nonce), "role": "edit"})
	}
	for id, t := range propText {
		v.ids[t] = id
	}
	f.mu.Lock()
	for id, t := range newText {
		v.ids[t] = id
	}
	f.variants[fmt.Sprintf("#n%d", nonce)] = v
	f.mu.Unlock()
	return map[string]any{"infeasible": false, "reason": "", "changes": changes, "propositions": props}
}

func (f *fakeLLM) verify(user string) any {
	labels := map[string]string{}
	var v *fakeVariant
	// the variant is identified by the newest realization marker in the discussion
	best := 0
	for _, m := range regexp.MustCompile(`#n(\d+)`).FindAllStringSubmatch(user, -1) {
		if n, _ := strconv.Atoi(m[1]); n > best {
			best = n
		}
	}
	f.mu.Lock()
	v = f.variants[fmt.Sprintf("#n%d", best)]
	f.mu.Unlock()
	var order []string
	for _, line := range strings.Split(user, "\n") {
		if m := reLabel.FindStringSubmatch(line); m != nil {
			labels[m[1]] = m[2]
			order = append(order, m[1])
		}
	}
	var props []map[string]string
	for _, l := range order {
		c := "same"
		if v != nil && v.deleted[v.ids[labels[l]]] {
			c = "absent"
		}
		props = append(props, map[string]string{"label": l, "content": c})
	}
	var pairs []map[string]any
	for _, line := range strings.Split(user, "\n") {
		m := rePair.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		rel, dir := "none", "none"
		if v != nil {
			a, b := v.ids[labels[m[2]]], v.ids[labels[m[3]]]
			if k, ok := v.rels[[2]string{a, b}]; ok {
				rel, dir = k, "first_to_second"
			} else if k, ok := v.rels[[2]string{b, a}]; ok {
				rel, dir = k, "second_to_first"
			}
		}
		pairs = append(pairs, map[string]any{"index": idx, "relation": rel, "direction": dir})
	}
	// every fifth first attempt reads too badly (exercises the retry with feedback)
	if v != nil && best%5 == 0 && !v.retried {
		v.retried = true
		return map[string]any{"propositions": props, "pairs": pairs, "fluency": "2", "coherence": "3", "note": ""}
	}
	return map[string]any{"propositions": props, "pairs": pairs, "fluency": "4", "coherence": "3", "note": ""}
}

// fakeBatchClient is a BatchClient over the fake (records the number of batches).
type fakeBatchClient struct {
	f *fakeLLM
}

func (c *fakeBatchClient) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return c.f.complete(ctx, req)
}

func (c *fakeBatchClient) CompleteBatch(ctx context.Context, reqs []llm.Request) ([]llm.BatchResult, error) {
	c.f.mu.Lock()
	c.f.batches++
	c.f.mu.Unlock()
	out := make([]llm.BatchResult, len(reqs))
	for i, r := range reqs {
		resp, err := c.f.complete(ctx, r)
		out[i] = llm.BatchResult{Response: resp, Err: err}
	}
	return out, nil
}

func testEnv(f *fakeLLM, models []string) *pilotEnv {
	env := &pilotEnv{clients: map[string]llm.Client{}, emb: &llm.HashEmbedder{D: 256}, pricing: llm.DefaultPricing()}
	for _, m := range models {
		if synth.FamilyOf(m) == "anthropic" {
			env.clients[m] = &fakeBatchClient{f}
		} else {
			env.clients[m] = &llm.FuncClient{Fn: f.complete}
		}
	}
	return env
}

func testOptions(t *testing.T) pilotOptions {
	dir := t.TempDir()
	if keep := os.Getenv("GCJ_TEST_OUT"); keep != "" { // inspect the offline run's outputs
		dir = keep
	}
	var bank []synth.Topic
	for _, d := range synth.Domains {
		for k := range 4 {
			bank = append(bank, synth.Topic{ID: fmt.Sprintf("%s-%d", d, k), Domain: d, Question: fmt.Sprintf("Should %s question %d be settled?", d, k)})
		}
	}
	if err := writeJSON(filepath.Join(dir, "topics.json"), bank); err != nil {
		t.Fatal(err)
	}
	return pilotOptions{sources: 12, boot: 2, seed: 3, lambda: 0.5, m: 0.3, out: dir, budget: 100, promptVersion: synth.V2,
		promptDir: "../../prompts", generators: defaultGenerators, judges: defaultJudges,
		genEffort: parseModelMap(defGenEffort), judgeEffort: parseModelMap(defJudgeEffort), judgeThinking: parseModelMap(defJudgeThinking), p2: true}
}

func TestPilotV2Offline(t *testing.T) {
	f := newFakeLLM()
	o := testOptions(t)
	o.diagShare = 0.5 // enough t3_diag slots to see the in-slot redraw
	env := testEnv(f, o.allModels())
	null, _ := os.Open(os.DevNull)
	stdout := os.Stdout
	os.Stdout = null
	err := runPilotV2(context.Background(), o, env)
	os.Stdout = stdout
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := readJSONL[sourceRecord](filepath.Join(o.out, "pilot_sources.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	trips, err := readJSONL[pilotTriplet](filepath.Join(o.out, "pilot_triplets.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	acc := 0
	for _, s := range srcs {
		if s.Accepted {
			acc++
			if s.Verifier == nil || s.Verifier.Family == s.GeneratorFamily || s.ExemplarPair == 0 || !s.Pilot {
				t.Errorf("source %s: verifier %+v generator %s pair %d", s.SourceID, s.Verifier, s.GeneratorFamily, s.ExemplarPair)
			}
		}
	}
	if acc < 10 {
		t.Fatalf("only %d of %d sources accepted: %+v", acc, len(srcs), srcs[0].Attempts)
	}
	if len(trips) != 4*acc {
		t.Fatalf("%d triplet records for %d sources", len(trips), acc)
	}
	ok, t1, judged := 0, 0, 0
	for _, tr := range trips {
		if tr.Err != "" {
			continue
		}
		ok++
		if tr.VerifierFamily == "" || tr.VerifierFamily == tr.GeneratorFamily || tr.RealizeA == nil || tr.Delta0 == nil || !tr.Flags.Pilot || tr.Plan == nil {
			t.Errorf("%s: %+v", tr.ID, tr)
			continue
		}
		if want := tripletFitFlags(tr.Type, *tr.Plan, *tr.Delta0, tr.T1Stratum, tr.Flags.QT30, tr.Flags.Pilot); !slices.Equal(tr.Flags.Fit, want) {
			t.Errorf("%s: fit flags %v, want %v", tr.ID, tr.Flags.Fit, want)
		}
		if tr.Plan.Type != tr.Type || tr.Plan.EditsA != len(tr.PlanA) || tr.Plan.EditsB != len(tr.PlanB) || (tr.SubPlanned != "" && tr.Type != perturb.T3Contrast) {
			t.Errorf("%s: plan %+v vs %d/%d edits, sub %q", tr.ID, tr.Plan, len(tr.PlanA), len(tr.PlanB), tr.SubPlanned)
		}
		if len(tr.Judgments) == 6 {
			judged++
		}
		for _, j := range tr.Judgments {
			if j.VerifierFamilyEqJudgeFamily != (j.Family == tr.VerifierFamily) || j.CacheKey == "" || (j.Family == "anthropic") != j.Batch {
				t.Errorf("%s: judgment %+v", tr.ID, j)
			}
		}
		if tr.Type == perturb.T1Monotone {
			t1++
			if tr.T1Delta0 == nil || *tr.T1Delta0 < 0.5 || tr.T1Stratum != perturb.T1StratumOf(*tr.T1Delta0) || tr.Plan.T1Target == "" || tr.RealizeB.Base != "A" {
				t.Errorf("%s: T1 record %+v", tr.ID, tr)
			}
			// B's untouched utterances equal A's (B is A plus the extra edit, dataset-spec §3.1)
			last := tr.RealizeB.Attempts[len(tr.RealizeB.Attempts)-1]
			for i, u := range tr.TextA.Utterances {
				if !slices.Contains(last.Changed, i) && !slices.Contains(tr.TextB.Utterances, u) {
					t.Errorf("%s: A's untouched utterance %d is not in B", tr.ID, i)
				}
			}
		}
	}
	if ok < len(trips)/2 {
		t.Fatalf("only %d of %d slots realized", ok, len(trips))
	}
	// t3_diag (dataset-spec §2.2): a relabel returned infeasible is redrawn inside the slot, and the redraw succeeds.
	diagOK := 0
	for _, tr := range trips {
		if tr.Err != "" || tr.Plan == nil || !tr.Plan.T3Diag || tr.DiagInfeasible == 0 {
			continue
		}
		var redraw *discardedPlan
		for i := range tr.Discarded {
			if tr.Discarded[i].DiagRedraw {
				redraw = &tr.Discarded[i]
			}
		}
		if redraw == nil || redraw.Reason != "infeasible" || redraw.Info == nil || !redraw.Info.T3Diag || tr.DiagFallback || tr.SubPlanned != perturb.T3Diag {
			t.Errorf("%s: t3_diag redraw %+v (fallback %v, sub %q)", tr.ID, redraw, tr.DiagFallback, tr.SubPlanned)
			continue
		}
		refused := false
		for _, e := range append(slices.Clone(redraw.EditsA), redraw.EditsB...) {
			refused = refused || (e.Kind == perturb.RTypeCA && e.Infeasible != "" && e.Direction != "" && e.Distance != nil)
		}
		if !refused || !slices.Contains(tr.Flags.Fit, "diag_ca_type") || !slices.Contains(tr.Flags.Fit, "t3_diag") || !hasKind(append(slices.Clone(tr.EditsA), tr.EditsB...), perturb.RTypeCA) {
			t.Errorf("%s: refused relabel not recorded or flags %v", tr.ID, tr.Flags.Fit)
			continue
		}
		diagOK++
	}
	if diagOK == 0 {
		t.Fatalf("no t3_diag slot with an infeasible relabel return followed by a successful redraw (CA infeasible returns %d)", f.caInfeasible)
	}
	if judged != ok {
		t.Errorf("%d of %d triplets have 6 judgments", judged, ok)
	}
	if f.batches != 2 {
		t.Errorf("%d judge batches, want 2 (one per Anthropic judge)", f.batches)
	}
	md, _ := os.ReadFile(filepath.Join(o.out, "pilot.md"))
	for _, want := range []string{"## 来歴", "## 元の議論（G1）", "級内相関", "補助パラメータ", "単段（750 件 = 3,300 三つ組）",
		"## 診断層 t3_diag", "P2 報告", "φ の情報量", "合格確率", "当てはめに使う三つ組", "破棄率"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(string(md), "## パラメータ") {
		t.Error("P2 report prints θ")
	}
	if strings.Contains(string(md), "種別のタイを外した診断用の当てはめ") {
		t.Error("P2 report prints the untied diagnostic fit (θ)")
	}
	t.Logf("sources %d/%d, triplets %d/%d (T1 %d, t3_diag redrawn %d), calls %v", acc, len(srcs), ok, len(trips), t1, diagOK, f.calls)
}

// The non-P2 run reports the confirmatory analysis (fit.Confirmatory + WriteConfirmatory) and the untied
// diagnostic fit of the t3_diag triplets; the run's triplets are not pilot-flagged.
func TestPilotV2OfflineConfirmatory(t *testing.T) {
	f := newFakeLLM()
	o := testOptions(t)
	// keepJudges: a single gate triplet would make every judge fail §9.1 (Clopper–Pearson bound at n_T = 1).
	o.p2, o.boot, o.dReps, o.diagShare, o.keepJudges = false, 2, 2, 0.5, true
	null, _ := os.Open(os.DevNull)
	stdout := os.Stdout
	os.Stdout = null
	defer func() { os.Stdout = stdout }()
	if err := runPilotV2(context.Background(), o, testEnv(f, o.allModels())); err != nil {
		t.Fatal(err)
	}
	trips, err := readJSONL[pilotTriplet](filepath.Join(o.out, "pilot_triplets.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range trips {
		if tr.Flags.Pilot || slices.Contains(tr.Flags.Fit, "pilot") {
			t.Fatalf("%s: non-P2 triplet flagged pilot", tr.ID)
		}
	}
	md, _ := os.ReadFile(filepath.Join(o.out, "pilot.md"))
	for _, want := range []string{"確認的分析", "パラメータの判断", "外挿、較正なし", "種別のタイを外した診断用の当てはめ", "RA↔CA", "CA↔MA"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(o.out, "profile.csv")); err != nil {
		t.Error(err)
	}
}

// parityGraph is a small source with every relation kind and three isolated propositions.
func parityGraph() ged.Graph {
	var g ged.Graph
	for i := range 14 {
		g.Props = append(g.Props, ged.Prop{ID: fmt.Sprintf("p%d", i)})
	}
	for i, k := range []string{ged.RA, ged.RA, ged.CA, ged.MA, ged.RA, ged.MA, ged.CA, ged.RA, ged.RA, ged.MA} {
		g.Edges = append(g.Edges, ged.Edge{From: fmt.Sprintf("p%d", i+1), To: fmt.Sprintf("p%d", i/2), Kind: k})
	}
	return g
}

// The pilot's fit flags equal the simulator's on the same plan and realized Δ(θ₀) (decisions-stats §16 #16), with
// the pilot-only qt30 and pilot appended; every flag is exercised.
func TestFitFlagsMatchSimulator(t *testing.T) {
	src := parityGraph()
	rng := rand.New(rand.NewPCG(7, 8))
	opts := catalogueOptions(0)
	seen := map[string]bool{}
	deltas := []float64{math.NaN(), -0.5, 0, 1e-12, 0.7, 1.5, 2, 3.4}
	for i := range 3000 {
		typ := perturb.DrawType(rng)
		o := opts
		if typ == perturb.T3Contrast {
			sub := []perturb.T3Sub{perturb.T3Plain, perturb.T3Ident, perturb.T3Diag}[i%3]
			o.Sub = &sub
		}
		info := perturb.DrawTriplet(rng, src, typ, o).PlanInfo
		if i%7 == 0 {
			info.DelAddCooccur = true // never drawn by the catalogue; set by hand
		}
		d0 := deltas[i%len(deltas)]
		stratum := ""
		switch {
		case typ == perturb.T1Monotone && i%5 == 0:
			stratum = info.T1Stratum // a stale (planned) stratum: t1_gate must still need Δ > 0
		case typ == perturb.T1Monotone:
			stratum = perturb.T1StratumOf(d0)
		}
		want := simulate.Meta{Type: typ, Info: info, Delta0: d0, T1Stratum: stratum}.Flags()
		if got := tripletFitFlags(typ, info, d0, stratum, false, false); !slices.Equal(got, want) {
			t.Fatalf("plan %d (%s, Δ %v, %q): pilot %v, simulator %v (%+v)", i, typ, d0, stratum, got, want, info)
		}
		if got := tripletFitFlags(typ, info, d0, stratum, true, true); !slices.Equal(got, append(slices.Clone(want), "qt30", "pilot")) {
			t.Fatalf("plan %d: with qt30/pilot %v", i, got)
		}
		for _, f := range want {
			seen[f] = true
		}
	}
	for _, f := range []string{"x_spk", "diag_ca_type", "del_add_cooccur", "t3_ident", "t3_diag", "t1_gate"} {
		if !seen[f] {
			t.Errorf("flag %s never exercised", f)
		}
	}
}

// The pilot fit drops what the main analysis drops except its own pilot flag.
func TestPilotExclude(t *testing.T) {
	ex := pilotExclude()
	if slices.Contains(ex, "pilot") || len(ex) != len(fit.DefaultExclude)-1 {
		t.Fatalf("pilotExclude %v (default %v)", ex, fit.DefaultExclude)
	}
	trips := []fit.Triplet{{Flags: []string{"pilot"}}, {Flags: []string{"pilot", "diag_ca_type"}}, {Flags: []string{"pilot", "x_spk"}}, {}}
	if kept, _, _ := fit.Filter(trips, nil, fit.Options{Exclude: ex}); len(kept) != 2 {
		t.Errorf("pilot exclusion keeps %d, want 2", len(kept))
	}
	if kept, _, _ := fit.Filter(trips, nil, fit.Options{}); len(kept) != 1 {
		t.Errorf("default exclusion keeps %d, want 1", len(kept))
	}
}

// fitTriplets copies every fit.Triplet contract field from the record.
func TestFitTripletsCopiesContract(t *testing.T) {
	w, d0, t1 := 0.8, 2.4, 2.4
	src := &sourceRecord{Discussion: synth.Discussion{Graph: ged.Graph{Props: []ged.Prop{{ID: "s0.p1", Text: "a"}}}}}
	tr := &pilotTriplet{Source: 0, Type: perturb.T1Monotone, Generator: "claude-sonnet-5-5", VerifierFamily: "openai", ExemplarPair: 7,
		Domain: "economy", CohA: 4, CohB: 2, Weight: &w, Delta0: &d0, T1Delta0: &t1, T1Stratum: perturb.T1High,
		Plan: &perturb.PlanInfo{Type: perturb.T1Monotone}, Flags: tripletFlags{Pilot: true, PNegB: 1}}
	ft, dd := fitTriplets([]*pilotTriplet{tr}, []*sourceRecord{src})
	got := ft[0]
	if got.Type != "T1" || !slices.Equal(got.Flags, []string{"t1_gate", "pilot"}) || got.Generator != tr.Generator || got.VerifierFamily != "openai" ||
		got.ExemplarPair != "7" || got.Domain != "economy" || got.Weight != 0.8 || got.CohA != 4 || got.CohB != 2 || got.T1Delta0 != 2.4 ||
		got.NegDiff != 1 || dd[0] != 2.4 || len(got.Ref.Props) != 1 {
		t.Fatalf("fit.Triplet %+v", got)
	}
}

// Stage 0 and the QC audits run offline on the fake (the QC reads the records of an offline pilot).
func TestTopicsAndQCOffline(t *testing.T) {
	f := newFakeLLM()
	o := testOptions(t)
	o.sources = 6
	env := testEnv(f, o.allModels())
	null, _ := os.Open(os.DevNull)
	stdout := os.Stdout
	os.Stdout = null
	defer func() { os.Stdout = stdout }()
	if err := runTopicsEnv(context.Background(), o, env); err != nil {
		t.Fatal(err)
	}
	bank, err := readTopics(filepath.Join(o.out, "topics.json"))
	if err != nil || len(bank) < 12*synth.TopicsPerCall {
		t.Fatalf("bank %d topics, err %v (calls %v)", len(bank), err, f.calls)
	}
	if f.calls["synth_topics"] != 24 {
		t.Fatalf("%d topic calls, want 24", f.calls["synth_topics"])
	}
	if err := runPilotV2(context.Background(), o, env); err != nil {
		t.Fatal(err)
	}
	if err := runQCEnv(context.Background(), o, env); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(o.out, "qc.md"))
	for _, want := range []string{"例示の再注釈", "ラベルの正しさの監査", "均質さの監視"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("qc report lacks %q:\n%s", want, md)
		}
	}
}

// QT30 transfer sources (from -import-qt30) join the run with the qt30 flag and their episode.
func TestPilotV2WithQT30(t *testing.T) {
	f := newFakeLLM()
	o := testOptions(t)
	o.sources, o.withQT30, o.aifdb = 2, true, "../../internal/synth/testdata/qt30"
	null, _ := os.Open(os.DevNull)
	stdout := os.Stdout
	os.Stdout = null
	defer func() { os.Stdout = stdout }()
	if err := runImportQT30(o); err != nil {
		t.Fatal(err)
	}
	if err := runPilotV2(context.Background(), o, testEnv(f, o.allModels())); err != nil && !strings.Contains(err.Error(), "使える三つ組") {
		t.Fatal(err)
	}
	trips, err := readJSONL[pilotTriplet](filepath.Join(o.out, "pilot_triplets.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	q := 0
	for _, tr := range trips {
		if tr.Flags.QT30 {
			q++
			if tr.Flags.Episode != "cutietestrun28May2020" || tr.SourceID != "q90001" {
				t.Errorf("qt30 triplet %+v", tr.Flags)
			}
		}
	}
	if q != 4 {
		t.Fatalf("%d QT30 triplets, want 4", q)
	}
}

// A judge batch whose projected cost exceeds the remaining budget is not submitted.
func TestJudgeBatchBudgetGuard(t *testing.T) {
	f := newFakeLLM()
	o := testOptions(t)
	o.budget = 1e-6
	o.judges = []string{"claude-sonnet-5-5"}
	env := testEnv(f, o.allModels())
	r, err := newV2Run(o, env)
	if err != nil {
		t.Fatal(err)
	}
	src := &sourceRecord{Discussion: synth.Discussion{Utterances: []synth.Utterance{{Speaker: "Host", Text: strings.Repeat("words ", 200)}}}}
	tr := &pilotTriplet{TextA: src.Discussion, TextB: src.Discussion}
	_, err = r.judgeAll(context.Background(), []*pilotTriplet{tr}, []*sourceRecord{src})
	if !errors.Is(err, errBudget) || f.batches != 0 || f.calls["judge"] != 0 {
		t.Fatalf("err %v, batches %d, calls %v", err, f.batches, f.calls)
	}
	// with the requests already cached nothing is projected and the batch goes out
	env.cached = func(llm.Client, llm.Request) bool { return true }
	if _, err := r.judgeAll(context.Background(), []*pilotTriplet{tr}, []*sourceRecord{src}); err != nil || f.batches != 1 {
		t.Fatalf("cached batch: err %v, batches %d", err, f.batches)
	}
}

func TestDefaultBudget(t *testing.T) {
	if b := defaultBudget(750); b != 260 {
		t.Fatalf("750 → %v", b)
	}
	if b := defaultBudget(75); b != 26 {
		t.Fatalf("75 → %v", b)
	}
}
