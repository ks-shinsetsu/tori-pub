package synth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// realizeOut is the structured output of realization (prompts/realize_v3.md).
type realizeOut struct {
	Infeasible bool   `json:"infeasible" jsonschema_description:"True only if a relation edit cannot be made without changing what a proposition claims; then leave changes and propositions empty"`
	Reason     string `json:"reason" jsonschema_description:"Why the edit is infeasible (empty otherwise)"`
	// InfeasibleEdit attributes an infeasible return to one edit (review-synth-impl M3).
	InfeasibleEdit int `json:"infeasible_edit" jsonschema_description:"When infeasible: the number of the edit (as numbered in Edits) that cannot be made; 0 otherwise"`
	Changes        []struct {
		Utterance int    `json:"utterance"`
		Op        string `json:"op" jsonschema:"enum=replace,enum=insert_after,enum=delete"`
		Speaker   string `json:"speaker"`
		Text      string `json:"text"`
		Role      string `json:"role" jsonschema:"enum=edit,enum=connective"`
	} `json:"changes"`
	Propositions []struct {
		ID        string `json:"id"`
		Unchanged bool   `json:"unchanged" jsonschema_description:"True if the proposition's content is exactly as given; then text may be empty"`
		Text      string `json:"text" jsonschema_description:"The proposition's final self-contained text"`
	} `json:"propositions"`
}

// InfeasibleKinds are the kinds for which the realizer may declare an edit infeasible (dataset-spec §4.2):
// the relation edits that change a relation's kind (including the diagnostic CA relabel of the t3_diag
// sub-stratum, §2.2), reverse it, or add it.
var InfeasibleKinds = []perturb.Kind{perturb.RType, perturb.RTypeCA, perturb.RRev, perturb.RAdd}

// RealizeInput is one realization request.
type RealizeInput struct {
	// Base is the discussion being edited: the source S, or A's realized discussion for T1's B
	// (incremental realization, dataset-spec §3.1). It must carry PropUtterance.
	Base Discussion
	// Target is the frozen variant (graph after all its edits).
	Target perturb.Variant
	// Edits are the edits to make relative to Base: Target.Edits for an ordinary variant, B \ A for T1's B.
	Edits []perturb.Edit
	// Incremental requires every base utterance outside the edited utterances and their direct
	// neighbours to stay byte-equal (T1's B).
	Incremental bool
	Prefix      string
	Attempt     int
	Feedback    string
}

// RealizeResult is the outcome of Realize.
type RealizeResult struct {
	Discussion Discussion
	Stats      RealizeStats
	// Infeasible is the realizer's "this edit cannot be realized without a content change" (no Discussion);
	// InfeasibleEdit is the index into RealizeInput.Edits of the edit it named (-1 when not infeasible).
	Infeasible     bool
	InfeasibleEdit int
	Reason         string
	// Touched are the edit-named propositions present in the variant (their text is from the realizer).
	Touched []string
	// ChangedUtterances are the base indices replaced or deleted.
	ChangedUtterances []int
}

// TouchedProps returns the propositions named by edits (P-* targets, both ends of R-* edges), in order.
func TouchedProps(edits []perturb.Edit) []string {
	var out []string
	add := func(id string) {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, e := range edits {
		add(e.Prop)
		if e.Edge != nil {
			add(e.Edge.From)
			add(e.Edge.To)
		}
	}
	return out
}

// mustChangeText reports whether edits require proposition id to get new content.
func mustChangeText(edits []perturb.Edit, id string) bool {
	for _, e := range edits {
		if e.Prop == id {
			switch e.Kind {
			case perturb.PPara, perturb.PShift, perturb.PNeg, perturb.PAdd:
				return true
			}
		}
	}
	return false
}

// describeV3 turns an edit into the v3 instruction (R-del separation, R-type wording preserved, infeasible).
func describeV3(e perturb.Edit) string {
	rel := func(from, kind, to string) string {
		return fmt.Sprintf("\"%s %s %s\"", short(from), verbs[kind], short(to))
	}
	switch e.Kind {
	case perturb.RDel:
		return fmt.Sprintf("Remove the relation %s. Both propositions stay stated with their wording, but a reader must no longer be able to read %s as related to %s. Separate the two claims: put them in different sentences with no signalling word that links them (\"so\", \"because\", \"that is why\", \"but\", \"in other words\", …); if they are in the same or adjacent utterances you may move %s one utterance away. Deleting only the connective is not enough when the order of the sentences still implies the link.",
			rel(e.Edge.From, e.Edge.Kind, e.Edge.To), short(e.Edge.From), short(e.Edge.To), short(e.Edge.From))
	case perturb.RAdd:
		return fmt.Sprintf("Add the relation %s: add explicit signalling words that present it, without changing what either proposition claims. If this cannot be done without changing what a proposition claims, declare the edit infeasible.",
			rel(e.Edge.From, e.Edge.Kind, e.Edge.To))
	case perturb.RType:
		return fmt.Sprintf("Change the relation %s so that %s now %s %s. Keep the wording of both propositions; change only the signalling words or the framing around them. Do not negate or weaken either proposition to make the new relation fit. If this cannot be done without changing what a proposition claims, declare the edit infeasible.",
			rel(e.Edge.From, e.Edge.Kind, e.Edge.To), short(e.Edge.From), verbs[e.NewKind], short(e.Edge.To))
	case perturb.RTypeCA:
		return fmt.Sprintf("Change the relation %s so that %s now %s %s (%s). Keep the wording of both propositions; change only the signalling words or the framing around them. Do not negate, weaken or qualify either proposition to make the new relation fit: %s. If this cannot be done without changing what a proposition claims, declare the edit infeasible; that is an expected answer for this edit, not a failure.",
			rel(e.Edge.From, e.Edge.Kind, e.Edge.To), short(e.Edge.From), verbs[e.NewKind], short(e.Edge.To), caDirectionGloss(e), caDirectionHint(e))
	case perturb.RRev:
		return fmt.Sprintf("Reverse the relation %s so that %s now %s %s. Keep what each proposition claims; change only how they are linked. If this cannot be done without changing what a proposition claims, declare the edit infeasible.",
			rel(e.Edge.From, e.Edge.Kind, e.Edge.To), short(e.Edge.To), verbs[e.Edge.Kind], short(e.Edge.From))
	}
	return describe(e)
}

// relationNoun names a relation kind for the realizer.
var relationNoun = map[string]string{ged.RA: "a support", ged.CA: "an attack", ged.MA: "a restatement"}

// caDirection returns the relabel's old and new kinds: from Edit.Direction ("RA→CA") when set, else from the edge.
func caDirection(e perturb.Edit) (from, to string) {
	if a, b, ok := strings.Cut(e.Direction, "→"); ok {
		return a, b
	}
	return e.Edge.Kind, e.NewKind
}

// caDirectionGloss is the plain-words direction of a CA relabel ("an attack becomes a support").
func caDirectionGloss(e perturb.Edit) string {
	from, to := caDirection(e)
	return relationNoun[from] + " becomes " + relationNoun[to]
}

// caDirectionHint tells the realizer what the new relation's signalling looks like in that direction.
func caDirectionHint(e perturb.Edit) string {
	from, to := caDirection(e)
	switch {
	case to == ged.CA:
		return "present the first claim as an objection to the second (\"but\", \"that ignores\", \"on the contrary\") while it still says exactly what it said"
	case from == ged.CA && to == ged.RA:
		return "present the first claim as a reason for the second (\"because\", \"so\", \"that is why\") while it still says exactly what it said"
	default: // CA → MA
		return "present the first claim as a restatement of the second (\"in other words\", \"that is to say\") while it still says exactly what it said"
	}
}

// instructionsV3 numbers the v3 instructions (speaker swaps are done by the harness).
func instructionsV3(edits []perturb.Edit) []string {
	var instr []string
	for _, e := range edits {
		if e.Kind != perturb.XSpk {
			instr = append(instr, fmt.Sprintf("%d. %s", len(instr)+1, describeV3(e)))
		}
	}
	return instr
}

// realizePrep is a prepared realization request.
type realizePrep struct {
	req       llm.Request
	utts      []Utterance
	baseText  map[string]string
	present   []string
	instrEdit []int // instruction number − 1 → index into RealizeInput.Edits
	// done is the result when no LLM call is needed (speaker swap only).
	done *RealizeResult
}

// prepareRealize builds the realize_v3 request (with its schema, so the request is exactly what is sent and cached).
func (g *Generator) prepareRealize(in RealizeInput) (realizePrep, error) {
	var pr realizePrep
	base := in.Base
	utts := slices.Clone(base.Utterances)
	if i, j, ok := speakerSwap(in.Edits); ok {
		if in.Incremental {
			return pr, errors.New("synth: speaker swap in an incremental realization")
		}
		utts = swapSpeakers(utts, i, j)
	}
	baseText := map[string]string{}
	for _, p := range base.Graph.Props {
		baseText[p.ID] = p.Text
	}
	pr.utts, pr.baseText = utts, baseText
	instr := instructionsV3(in.Edits)
	for i, e := range in.Edits {
		if e.Kind != perturb.XSpk {
			pr.instrEdit = append(pr.instrEdit, i)
		}
	}
	if len(instr) == 0 {
		d := Discussion{Utterances: utts, Graph: in.Target.Graph.Clone(), PropUtterance: map[string]int{}}
		for i, p := range d.Graph.Props {
			t, ok := baseText[p.ID]
			if !ok {
				return pr, fmt.Errorf("synth: proposition %s is not in the base", p.ID)
			}
			d.Graph.Props[i].Text = t
			d.PropUtterance[p.ID] = base.PropUtterance[p.ID]
		}
		pr.done = &RealizeResult{Discussion: d, InfeasibleEdit: -1}
		return pr, nil
	}

	touched := TouchedProps(in.Edits)
	inTarget := map[string]bool{}
	for _, p := range in.Target.Graph.Props {
		inTarget[p.ID] = true
	}
	for _, id := range touched {
		if inTarget[id] {
			pr.present = append(pr.present, id)
		}
	}

	var b strings.Builder
	if in.Incremental {
		b.WriteString("This discussion is itself an edited version. Make only the edits listed below; every other utterance must stay exactly as it is.\n\n")
	}
	describeSource(&b, utts, base)
	b.WriteString("\nEdits:\n" + strings.Join(instr, "\n") + "\n")
	var shorts []string
	for _, id := range pr.present {
		shorts = append(shorts, short(id))
	}
	fmt.Fprintf(&b, "\nPropositions you must return in `propositions`: %s\n", strings.Join(shorts, ", "))
	if in.Feedback != "" {
		fmt.Fprintf(&b, "\nA previous attempt (%d) was rejected: %s\n", in.Attempt, in.Feedback)
	}
	w := g.Writer()
	pr.req = w.request(g.RealizePrompt.Body, b.String(), "realize")
	schema, err := llm.SchemaFor[realizeOut]("edited_discussion_v3", "Changed utterances, final proposition texts, or an infeasible flag")
	if err != nil {
		return pr, err
	}
	pr.req.Schema = schema
	return pr, nil
}

// RequestKey is the cache key of req as client c sends it (after its defaults), as recorded for judgments.
func RequestKey(c llm.Client, req llm.Request) string {
	if d, ok := c.(llm.RequestDefaulter); ok {
		req = d.ApplyDefaults(req)
	}
	return llm.CacheKey(req)
}

// RealizeKey is the cache key of the realize request for in ("" when no LLM call is needed).
func (g *Generator) RealizeKey(in RealizeInput) string {
	pr, err := g.prepareRealize(in)
	if err != nil || pr.done != nil {
		return ""
	}
	return RequestKey(g.Writer().Client, pr.req)
}

// Realize realizes in.Edits on in.Base as text (prompts/realize_v3.md). Unchanged utterances are
// assembled here verbatim. Errors (as opposed to Infeasible) mean the output cannot be used:
// a touched proposition without returned text, a required content change returned as "unchanged",
// an infeasible flag for a kind that does not allow it, a surviving proposition whose utterance was
// deleted, or — for incremental realization — a change to an utterance that is neither edited nor a
// direct neighbour of an edited one.
func (g *Generator) Realize(ctx context.Context, in RealizeInput) (RealizeResult, *llm.Response, error) {
	pr, err := g.prepareRealize(in)
	if err != nil || pr.done != nil {
		if pr.done != nil {
			return *pr.done, nil, nil
		}
		return RealizeResult{}, nil, err
	}
	base, utts, baseText, present := in.Base, pr.utts, pr.baseText, pr.present
	w := g.Writer()
	res, resp, err := llm.CompleteJSON[realizeOut](ctx, w.Client, pr.req, "", "")
	if err != nil {
		return RealizeResult{}, resp, err
	}
	if res.Infeasible {
		// The named edit must be one the realizer may refuse; without a valid number, the side's only such edit.
		var eligible []int
		for _, i := range pr.instrEdit {
			if slices.Contains(InfeasibleKinds, in.Edits[i].Kind) {
				eligible = append(eligible, i)
			}
		}
		if len(eligible) == 0 {
			return RealizeResult{}, resp, errors.New("synth: infeasible declared for edits that do not allow it (only R-type, R-type-CA, R-rev, R-add)")
		}
		idx := -1
		if n := res.InfeasibleEdit; n >= 1 && n <= len(pr.instrEdit) && slices.Contains(eligible, pr.instrEdit[n-1]) {
			idx = pr.instrEdit[n-1]
		} else if len(eligible) == 1 {
			idx = eligible[0]
		} else {
			return RealizeResult{}, resp, fmt.Errorf("synth: infeasible_edit %d does not name a relation edit that may be infeasible", res.InfeasibleEdit)
		}
		return RealizeResult{Infeasible: true, Reason: res.Reason, InfeasibleEdit: idx}, resp, nil
	}

	// Utterances allowed to change: those stating a touched proposition and their direct neighbours.
	// P-add and an untargeted X-surf may change any utterance; a targeted X-surf its utterance and the neighbours.
	// A change elsewhere is counted as collateral (and is an error in an incremental realization).
	anyOK := false
	allowed := map[int]bool{}
	for _, e := range in.Edits {
		switch {
		case e.Kind == perturb.XSurf && e.Utterance != nil:
			u := *e.Utterance
			allowed[u-1], allowed[u], allowed[u+1] = true, true, true
		case e.Kind == perturb.PAdd || e.Kind == perturb.XSurf:
			anyOK = true
		}
	}
	for _, id := range TouchedProps(in.Edits) {
		if u, ok := base.PropUtterance[id]; ok {
			allowed[u-1], allowed[u], allowed[u+1] = true, true, true
		}
	}

	var st RealizeStats
	replace := map[int]Utterance{}
	deleted := map[int]bool{}
	inserts := map[int][]Utterance{}
	for _, c := range res.Changes {
		if c.Utterance < -1 || c.Utterance >= len(utts) {
			return RealizeResult{}, resp, fmt.Errorf("synth: change refers to utterance %d", c.Utterance)
		}
		st.Changed++
		if c.Role == "connective" {
			st.Connective++
		}
		u := Utterance{Speaker: c.Speaker, Text: c.Text}
		switch c.Op {
		case "replace":
			if c.Utterance < 0 {
				return RealizeResult{}, resp, errors.New("synth: replace of utterance -1")
			}
			if u.Speaker == "" {
				u.Speaker = utts[c.Utterance].Speaker
			}
			if u == utts[c.Utterance] {
				st.Changed-- // a no-op replace
				continue
			}
			replace[c.Utterance] = u
		case "delete":
			if c.Utterance >= 0 {
				deleted[c.Utterance] = true
			}
		case "insert_after":
			inserts[c.Utterance] = append(inserts[c.Utterance], u)
		default:
			return RealizeResult{}, resp, fmt.Errorf("synth: unknown op %q", c.Op)
		}
		if c.Op != "insert_after" && !anyOK && !allowed[c.Utterance] {
			st.Collateral++
			if in.Incremental {
				return RealizeResult{}, resp, fmt.Errorf("synth: incremental realization changed untouched utterance %d", c.Utterance)
			}
		}
	}

	// Assemble: newIdx maps base index → output index (−1 if deleted); edited lists output indices written by the realizer.
	var out Discussion
	newIdx := make([]int, len(utts))
	var edited []int
	push := func(u Utterance, fromRealizer bool) {
		if fromRealizer {
			edited = append(edited, len(out.Utterances))
		}
		out.Utterances = append(out.Utterances, u)
	}
	for _, u := range inserts[-1] {
		push(u, true)
	}
	for i, u := range utts {
		newIdx[i] = -1
		if !deleted[i] {
			newIdx[i] = len(out.Utterances)
			r, ok := replace[i]
			if ok {
				u = r
			}
			push(u, ok)
		}
		for _, x := range inserts[i] {
			push(x, true)
		}
	}
	var changedIdx []int
	for i := range utts {
		if deleted[i] {
			changedIdx = append(changedIdx, i)
		} else if _, ok := replace[i]; ok {
			changedIdx = append(changedIdx, i)
		}
	}
	if err := CheckUntouched(utts, out.Utterances, newIdx, changedIdx); err != nil {
		return RealizeResult{}, resp, err
	}

	// Proposition texts.
	returned := map[string]struct {
		unchanged bool
		text      string
	}{}
	for _, p := range res.Propositions {
		v := struct {
			unchanged bool
			text      string
		}{p.Unchanged, strings.TrimSpace(p.Text)}
		returned[p.ID] = v
		returned[short(p.ID)] = v
	}
	out.Graph = in.Target.Graph.Clone()
	out.PropUtterance = map[string]int{}
	for i, p := range out.Graph.Props {
		r, got := returned[p.ID]
		if !got {
			r, got = returned[short(p.ID)]
		}
		named := slices.Contains(present, p.ID)
		switch {
		case named && !got:
			return RealizeResult{}, resp, fmt.Errorf("synth: touched proposition %s has no returned text", short(p.ID))
		case named && mustChangeText(in.Edits, p.ID) && (r.unchanged || r.text == ""):
			return RealizeResult{}, resp, fmt.Errorf("synth: proposition %s must get new content but was returned unchanged", short(p.ID))
		case got && !r.unchanged && r.text != "":
			out.Graph.Props[i].Text = r.text
		default:
			t, ok := baseText[p.ID]
			if !ok {
				return RealizeResult{}, resp, fmt.Errorf("synth: proposition %s has no text", short(p.ID))
			}
			if named && r.text == "" && !r.unchanged {
				st.MissingText++
			}
			out.Graph.Props[i].Text = t
		}
		// location
		if u, ok := base.PropUtterance[p.ID]; ok && u >= 0 && u < len(newIdx) && newIdx[u] >= 0 {
			out.PropUtterance[p.ID] = newIdx[u]
			continue
		}
		loc := locate(out.Graph.Props[i].Text, out.Utterances, edited)
		if loc < 0 {
			return RealizeResult{}, resp, fmt.Errorf("synth: proposition %s is no longer stated in any utterance", short(p.ID))
		}
		out.PropUtterance[p.ID] = loc
	}
	slices.Sort(changedIdx)
	return RealizeResult{Discussion: out, Stats: st, Touched: present, ChangedUtterances: changedIdx, InfeasibleEdit: -1}, resp, nil
}

// CheckUntouched verifies that every base utterance that was not replaced or deleted appears
// byte-for-byte at its new position (dataset-spec §3.1: B's untouched utterances equal A's).
func CheckUntouched(base, out []Utterance, newIdx []int, changed []int) error {
	for i, u := range base {
		if slices.Contains(changed, i) {
			continue
		}
		j := newIdx[i]
		if j < 0 || j >= len(out) || out[j] != u {
			return fmt.Errorf("synth: untouched utterance %d differs from the base", i)
		}
	}
	return nil
}

// locate returns the output utterance (among cand, the realizer-written ones) sharing the most tokens with text (−1 if none).
func locate(text string, utts []Utterance, cand []int) int {
	want := map[string]bool{}
	for _, t := range tokens(text) {
		want[t] = true
	}
	best, bestN := -1, 0
	for _, i := range cand {
		n := 0
		for _, t := range tokens(utts[i].Text) {
			if want[t] {
				n++
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	return best
}
