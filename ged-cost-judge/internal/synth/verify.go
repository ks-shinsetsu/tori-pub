package synth

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// Content judgments of the verifier.
const (
	ContentSame       = "same"
	ContentParaphrase = "paraphrase"
	ContentChanged    = "changed"
	ContentNegated    = "negated"
	ContentAbsent     = "absent"
)

// Direction answers for a pair (first, second).
const (
	DirFirstToSecond = "first_to_second"
	DirSecondToFirst = "second_to_first"
	DirNone          = "none"
)

// Verification is the G4 verifier's blind local re-annotation (prompts/verify_v2.md, dataset-spec §4.3).
// Labels refer to the VerifyQuery the request was built from.
type Verification struct {
	Propositions []struct {
		Label   string `json:"label" jsonschema_description:"C1, C2, ... as given"`
		Content string `json:"content" jsonschema:"enum=same,enum=paraphrase,enum=changed,enum=negated,enum=absent" jsonschema_description:"How the discussion states this proposition relative to the given text"`
	} `json:"propositions"`
	Pairs []struct {
		Index     int    `json:"index" jsonschema_description:"Number of the pair as given"`
		Relation  string `json:"relation" jsonschema:"enum=RA,enum=CA,enum=MA,enum=none"`
		Direction string `json:"direction" jsonschema:"enum=first_to_second,enum=second_to_first,enum=none"`
	} `json:"pairs"`
	Fluency   string `json:"fluency" jsonschema:"enum=1,enum=2,enum=3,enum=4,enum=5" jsonschema_description:"Grammar, reference and flow"`
	Coherence string `json:"coherence" jsonschema:"enum=1,enum=2,enum=3,enum=4,enum=5" jsonschema_description:"Does the argument hang together"`
	Note      string `json:"note"`
}

// Fluent is fluency 1–5 (0 if unreadable).
func (v Verification) Fluent() int { return score15(v.Fluency) }

// Coherent is coherence 1–5 (0 if unreadable).
func (v Verification) Coherent() int { return score15(v.Coherence) }

// PropExpect is one proposition the verifier checks.
type PropExpect struct {
	Label string `json:"label"`
	ID    string `json:"id"`
	// Ref is the reference text: the realizer's text for a proposition whose content an edit must change
	// (P-para, P-shift, P-neg, P-add), the base text otherwise (for T1's B, A's realized text).
	Ref string `json:"ref"`
	// Present is false for a deleted proposition (P-del, P-del+R): it must be absent from the text.
	Present bool `json:"present"`
	// Edit is the edit kind that touched it ("" for an endpoint of a relation edit).
	Edit perturb.Kind `json:"edit,omitempty"`
}

// PairExpect is one pair of propositions whose relation the verifier reads.
type PairExpect struct {
	First  string `json:"first"` // proposition ids (shown as labels)
	Second string `json:"second"`
	// Kind is the intended relation in the variant graph (RA / CA / MA, or "none").
	Kind string `json:"kind"`
	// From is the intended premise / attacker / restating proposition ("" for none).
	From string `json:"from,omitempty"`
	// Sampled marks an untouched edge sampled for collateral damage (dataset-spec §4.3 step 3).
	Sampled bool `json:"sampled,omitempty"`
}

// VerifyQuery is what the verifier is shown besides the edited text, with the intended answers.
type VerifyQuery struct {
	Props []PropExpect `json:"props"`
	Pairs []PairExpect `json:"pairs"`
}

// BuildVerifyQuery derives the touched elements of a realization: every proposition named by edits, every
// touched pair with its intended relation in the variant graph, and up to nSample untouched variant edges
// (collateral check).
//
// The reference text of a proposition is the realizer's text only when an edit must change its content
// (P-para, P-shift, P-neg, P-add); every other proposition (the endpoints of relation edits, deleted ones and the
// endpoints of sampled edges) is referenced by its base text, whose wording must stay (dataset-spec §2.1, §4.3).
// So a realizer that negates an endpoint to make a relation fit, and reports the negated text, is read as
// "negated" against the base text and rejected (the §5.1 polarity-leak control).
func BuildVerifyQuery(base, out Discussion, target perturb.Variant, edits []perturb.Edit, rng *rand.Rand, nSample int) VerifyQuery {
	var q VerifyQuery
	inTarget := map[string]bool{}
	for _, p := range target.Graph.Props {
		inTarget[p.ID] = true
	}
	kindOf := map[string]perturb.Kind{}
	for _, e := range edits {
		if e.Prop != "" {
			kindOf[e.Prop] = e.Kind
		}
	}
	ref := func(id string) string {
		if t := base.PropText(id); t != "" && !mustChangeText(edits, id) {
			return t
		}
		return out.PropText(id)
	}
	label := map[string]string{}
	for _, id := range TouchedProps(edits) {
		pe := PropExpect{ID: id, Present: inTarget[id], Edit: kindOf[id]}
		if pe.Present {
			pe.Ref = ref(id)
		} else {
			pe.Ref = base.PropText(id)
		}
		if pe.Ref == "" {
			continue
		}
		pe.Label = fmt.Sprintf("C%d", len(q.Props)+1)
		label[id] = pe.Label
		q.Props = append(q.Props, pe)
	}
	pairSeen := map[[2]string]bool{}
	addPair := func(a, b string, sampled bool) {
		k := [2]string{min(a, b), max(a, b)}
		if pairSeen[k] || !inTarget[a] || !inTarget[b] {
			return
		}
		pairSeen[k] = true
		pe := PairExpect{First: k[0], Second: k[1], Kind: "none", Sampled: sampled}
		for _, e := range target.Graph.Edges {
			if (e.From == a && e.To == b) || (e.From == b && e.To == a) {
				pe.Kind, pe.From = e.Kind, e.From
			}
		}
		q.Pairs = append(q.Pairs, pe)
	}
	for _, e := range edits {
		if e.Edge != nil {
			addPair(e.Edge.From, e.Edge.To, false)
		}
	}
	// collateral sample among untouched edges
	var cands []ged.Edge
	for _, e := range target.Graph.Edges {
		k := [2]string{min(e.From, e.To), max(e.From, e.To)}
		if !pairSeen[k] {
			cands = append(cands, e)
		}
	}
	if rng != nil {
		rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	}
	for _, e := range cands[:min(nSample, len(cands))] {
		addPair(e.From, e.To, true)
	}
	// every pair endpoint needs a label (sampled edges add their endpoints as reference propositions)
	for _, p := range q.Pairs {
		for _, id := range []string{p.First, p.Second} {
			if _, ok := label[id]; !ok {
				pe := PropExpect{ID: id, Present: true, Ref: ref(id), Label: fmt.Sprintf("C%d", len(q.Props)+1)}
				label[id] = pe.Label
				q.Props = append(q.Props, pe)
			}
		}
	}
	return q
}

func (q VerifyQuery) labelOf(id string) string {
	for _, p := range q.Props {
		if p.ID == id {
			return p.Label
		}
	}
	return id
}

// userMessage is the blind verifier input: edited text, propositions and pairs (no instructions, no original).
func (q VerifyQuery) userMessage(d Discussion) string {
	var b strings.Builder
	b.WriteString("DISCUSSION\n\n")
	b.WriteString(d.renderNumbered())
	b.WriteString("\nPROPOSITIONS\n\n")
	for _, p := range q.Props {
		fmt.Fprintf(&b, "%s: %s\n", p.Label, p.Ref)
	}
	if len(q.Pairs) > 0 {
		b.WriteString("\nPAIRS\n\n")
		for i, p := range q.Pairs {
			fmt.Fprintf(&b, "%d. %s and %s\n", i+1, q.labelOf(p.First), q.labelOf(p.Second))
		}
	}
	return b.String()
}

// Problems compares the verification with the intended variant (dataset-spec §4.3 acceptance): every
// present proposition stated as same / paraphrase (a negation or change is a problem, an unrequested
// polarity flip in particular), every deleted one absent, every pair's relation and direction as in
// the intended variant graph (RA↔MA confusions count), sampled edges unchanged, fluency ≥ 3.
// Coherence is not a discard criterion.
func (v Verification) Problems(q VerifyQuery) []string {
	var out []string
	content := map[string]string{}
	for _, p := range v.Propositions {
		content[p.Label] = p.Content
	}
	for _, p := range q.Props {
		c, ok := content[p.Label]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("proposition %s was not checked", short(p.ID)))
		case !p.Present && c != ContentAbsent:
			out = append(out, fmt.Sprintf("deleted proposition %s is still stated (%s)", short(p.ID), c))
		case p.Present && c == ContentNegated:
			out = append(out, fmt.Sprintf("proposition %s is stated with reversed polarity (unrequested negation)", short(p.ID)))
		case p.Present && c != ContentSame && c != ContentParaphrase:
			out = append(out, fmt.Sprintf("proposition %s is %s relative to its intended text", short(p.ID), c))
		}
	}
	got := map[int]struct{ rel, dir string }{}
	for _, p := range v.Pairs {
		got[p.Index] = struct{ rel, dir string }{p.Relation, p.Direction}
	}
	for i, p := range q.Pairs {
		g, ok := got[i+1]
		if !ok {
			out = append(out, fmt.Sprintf("pair %s–%s was not checked", short(p.First), short(p.Second)))
			continue
		}
		wantDir := DirNone
		if p.Kind != "none" {
			wantDir = DirFirstToSecond
			if p.From == p.Second {
				wantDir = DirSecondToFirst
			}
		}
		if g.rel == p.Kind && (p.Kind == "none" || g.dir == wantDir) {
			continue
		}
		what := "touched"
		if p.Sampled {
			what = "untouched"
		}
		out = append(out, fmt.Sprintf("%s pair %s–%s: the text states %s, intended %s",
			what, short(p.First), short(p.Second), relString(g.rel, g.dir, p), relString(p.Kind, wantDir, p)))
	}
	if v.Fluent() < 3 {
		out = append(out, fmt.Sprintf("it reads badly (fluency %s of 5)", v.Fluency))
	}
	return out
}

func relString(rel, dir string, p PairExpect) string {
	if rel == "none" || rel == "" {
		return "no relation"
	}
	from, to := p.First, p.Second
	if dir == DirSecondToFirst {
		from, to = to, from
	}
	return fmt.Sprintf("%s %s %s", short(from), verbs[rel], short(to))
}

// Confusions lists RA↔MA confusions between the verifier and the intended graph (informative about
// inherent ambiguity; logged, dataset-spec §4.3).
func (v Verification) Confusions(q VerifyQuery) []string {
	var out []string
	for _, p := range v.Pairs {
		if p.Index < 1 || p.Index > len(q.Pairs) {
			continue
		}
		want := q.Pairs[p.Index-1].Kind
		if (want == ged.RA && p.Relation == ged.MA) || (want == ged.MA && p.Relation == ged.RA) {
			out = append(out, want+"→"+p.Relation)
		}
	}
	return out
}

// VerifierMeta identifies who verified (dataset-spec §4.1, §6: family and model id as returned).
type VerifierMeta struct {
	Family  string `json:"verifier_family"`
	Model   string `json:"verifier_model"`
	Setting string `json:"verifier_setting,omitempty"`
}

func metaOf(a Agent, resp *llm.Response) VerifierMeta {
	m := VerifierMeta{Family: a.Family, Model: a.Model, Setting: a.Setting()}
	if resp != nil && resp.Model != "" {
		m.Model = resp.Model
	}
	return m
}

// Verify checks a realization blind (prompts/verify_v2.md): the verifier (the other family) sees only the
// edited text d and the touched elements of q. It returns the verifier's family and model id.
func (g *Generator) Verify(ctx context.Context, d Discussion, q VerifyQuery) (Verification, VerifierMeta, *llm.Response, error) {
	a := g.verifier()
	req, err := g.verifyRequest(d, q)
	if err != nil {
		return Verification{}, metaOf(a, nil), nil, err
	}
	v, resp, err := llm.CompleteJSON[Verification](ctx, a.Client, req, "", "")
	return v, metaOf(a, resp), resp, err
}

// verifyRequest builds the verify_v2 request with its schema (exactly what is sent and cached).
func (g *Generator) verifyRequest(d Discussion, q VerifyQuery) (llm.Request, error) {
	req := g.verifier().request(g.VerifyPrompt.Body, q.userMessage(d), "verify")
	schema, err := llm.SchemaFor[Verification]("verification_v2", "Per-proposition content, per-pair relation, fluency and coherence")
	req.Schema = schema
	return req, err
}

// VerifyKey is the cache key of the verify request for d and q.
func (g *Generator) VerifyKey(d Discussion, q VerifyQuery) string {
	req, err := g.verifyRequest(d, q)
	if err != nil {
		return ""
	}
	return RequestKey(g.verifier().Client, req)
}

// SourceAnnotation is the verifier's full blind re-annotation of a source (prompts/verify_source_v1.md).
type SourceAnnotation struct {
	Propositions []struct {
		Label   string `json:"label" jsonschema_description:"P1, P2, ... as given"`
		Content string `json:"content" jsonschema:"enum=same,enum=paraphrase,enum=changed,enum=negated,enum=absent"`
	} `json:"propositions"`
	Relations []struct {
		Kind string `json:"kind" jsonschema:"enum=RA,enum=CA,enum=MA"`
		From string `json:"from" jsonschema_description:"Label of the premise / attacker / restating proposition"`
		To   string `json:"to" jsonschema_description:"Label of the conclusion / attacked / restated proposition"`
	} `json:"relations"`
}

// GraphAgreement compares a re-annotation with the generator's gold graph.
type GraphAgreement struct {
	Gold      int `json:"gold"`
	Read      int `json:"read"`
	Matched   int `json:"matched"` // same pair, type and direction
	TypeWrong int `json:"type_wrong"`
	DirWrong  int `json:"dir_wrong"`
	Missing   int `json:"missing"`
	Extra     int `json:"extra"`
	// ContentIssues counts propositions read as changed / negated / absent; Negated those read as negated.
	ContentIssues int     `json:"content_issues"`
	Negated       int     `json:"negated"`
	Recall        float64 `json:"recall"`
	// Pass: ≥ 65% of the relations recovered with type and direction (dataset-spec §4.3 as amended 2026-11-11).
	// Extra relations are counted but do not reject.
	Pass bool `json:"pass"`
}

// SourceRecallMin is the G1 acceptance threshold on relation recall. It was 0.9 together with "no extra relation";
// the first runs with real models accepted 1 source in 8 under that rule (recall 0.71 over 142 relations, 1.6 extra
// relations per source, mostly attacks the writer had not put in the graph), so the user set 0.8 and dropped the
// extra-relation condition (2026-11-11). The P2 pilot (25 sources) still accepted 9 of 25 at 0.8 after 49
// generations, with a median recall of 0.64 (Sonnet 5.5) and 0.65 (luna) over the re-annotations, so the user set
// 0.65 the same day.
const SourceRecallMin = 0.65

// sourceLabels maps proposition ids to P1, P2, … in graph order.
func sourceLabels(g ged.Graph) (map[string]string, map[string]string) {
	toLabel, toID := map[string]string{}, map[string]string{}
	for i, p := range g.Props {
		l := fmt.Sprintf("P%d", i+1)
		toLabel[p.ID], toID[l] = l, p.ID
	}
	return toLabel, toID
}

// Agreement scores ann against gold.
func Agreement(gold ged.Graph, ann SourceAnnotation) GraphAgreement {
	_, toID := sourceLabels(gold)
	a := GraphAgreement{Gold: len(gold.Edges), Read: len(ann.Relations)}
	type key = [2]string
	goldBy := map[key]ged.Edge{}
	for _, e := range gold.Edges {
		goldBy[key{min(e.From, e.To), max(e.From, e.To)}] = e
	}
	used := map[key]bool{}
	for _, r := range ann.Relations {
		f, t := toID[r.From], toID[r.To]
		k := key{min(f, t), max(f, t)}
		e, ok := goldBy[k]
		if f == "" || t == "" || f == t || !ok || used[k] {
			a.Extra++
			continue
		}
		used[k] = true
		switch {
		case e.Kind != r.Kind:
			a.TypeWrong++
		case e.From != f:
			a.DirWrong++
		default:
			a.Matched++
		}
	}
	a.Missing = a.Gold - len(used)
	for _, p := range ann.Propositions {
		switch p.Content {
		case ContentChanged, ContentAbsent:
			a.ContentIssues++
		case ContentNegated:
			a.ContentIssues++
			a.Negated++
		}
	}
	if a.Gold > 0 {
		a.Recall = float64(a.Matched) / float64(a.Gold)
	} else {
		a.Recall = 1
	}
	a.Pass = a.Recall >= SourceRecallMin
	return a
}

// VerifySource re-annotates a full discussion blind (G1, the other family; also used on the exemplars
// and in the label-correctness audit): the verifier sees the numbered utterances and the propositions
// (labels P1…) and returns every relation it can read, plus a content judgment per proposition.
// The agreement with d.Graph is returned with the verifier's family and model id.
func (g *Generator) VerifySource(ctx context.Context, d Discussion) (SourceAnnotation, GraphAgreement, VerifierMeta, *llm.Response, error) {
	a := g.verifier()
	toLabel, _ := sourceLabels(d.Graph)
	var b strings.Builder
	b.WriteString("DISCUSSION\n\n")
	b.WriteString(d.renderNumbered())
	b.WriteString("\nPROPOSITIONS\n\n")
	for _, p := range d.Graph.Props {
		fmt.Fprintf(&b, "%s: %s\n", toLabel[p.ID], p.Text)
	}
	ann, resp, err := llm.CompleteJSON[SourceAnnotation](ctx, a.Client, a.request(g.VerifySourcePrompt.Body, b.String(), "verify_source"),
		"source_annotation", "Every relation the text states between the given propositions")
	if err != nil {
		return ann, GraphAgreement{}, metaOf(a, resp), resp, err
	}
	return ann, Agreement(d.Graph, ann), metaOf(a, resp), resp, nil
}

// Relabel returns the relation set of ann with proposition ids (for reports).
func (ann SourceAnnotation) Relabel(gold ged.Graph) []ged.Edge {
	_, toID := sourceLabels(gold)
	var out []ged.Edge
	for _, r := range ann.Relations {
		if toID[r.From] != "" && toID[r.To] != "" {
			out = append(out, ged.Edge{From: toID[r.From], To: toID[r.To], Kind: r.Kind})
		}
	}
	slices.SortFunc(out, func(x, y ged.Edge) int { return strings.Compare(x.From+x.To, y.From+y.To) })
	return out
}
