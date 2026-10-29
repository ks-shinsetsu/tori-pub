// Package synth uses an LLM to build source arguments (graph + text) (plan G1),
// realize edit plans as variant text (G3), verify them (G4), and judge triplets (J).
//
// Generation and judgment always use separate requests with no conversation history (plan §2.2).
//
// Two prompt versions coexist:
//
//   - "v1" is the pilot-2 pipeline (synth_source_v1, realize_v1/v2, verify_v1, judge_v1; one model).
//     Its requests are kept byte-identical so that a pilot-2 run replays from the response cache.
//   - "v2" is the revised pipeline of dataset-spec §9 (synth_source_v2 with exemplars, realize_v3,
//     verify_v2, verify_source_v1, synth_topics_v1; two generator families, cross-family verifier).
package synth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// Prompt versions.
const (
	V1 = "v1"
	V2 = "v2"
)

// Prompt is a prompt file's body and hash.
type Prompt struct {
	Name string
	Body string
	Hash string
}

// LoadPrompt reads dir/name.md.
func LoadPrompt(dir, name string) (Prompt, error) {
	b, err := os.ReadFile(filepath.Join(dir, name+".md"))
	if err != nil {
		return Prompt{}, err
	}
	return newPrompt(name, string(b)), nil
}

func newPrompt(name, body string) Prompt {
	h := sha256.Sum256([]byte(body))
	return Prompt{Name: name, Body: body, Hash: hex.EncodeToString(h[:6])}
}

// Utterance is an utterance.
type Utterance struct {
	Speaker string `json:"speaker" jsonschema_description:"Speaker name or role"`
	Text    string `json:"text" jsonschema_description:"What the speaker says"`
}

// Discussion is a source argument or a variant (text and graph).
type Discussion struct {
	Utterances []Utterance `json:"utterances"`
	Graph      ged.Graph   `json:"graph"`
	// PropUtterance maps proposition → index of the utterance stating it.
	// Source arguments always carry it; v2 realizations carry it too (needed for incremental T1).
	PropUtterance map[string]int `json:"prop_utterance,omitempty"`
}

// Render is the text shown for judgment.
func (d Discussion) Render() string {
	var b strings.Builder
	for _, u := range d.Utterances {
		fmt.Fprintf(&b, "%s: %s\n", u.Speaker, u.Text)
	}
	return b.String()
}

// renderNumbered is the text with utterance numbers (verifier input).
func (d Discussion) renderNumbered() string {
	var b strings.Builder
	for i, u := range d.Utterances {
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, u.Speaker, u.Text)
	}
	return b.String()
}

// PropText returns the text of proposition id ("" if absent).
func (d Discussion) PropText(id string) string {
	for _, p := range d.Graph.Props {
		if p.ID == id {
			return p.Text
		}
	}
	return ""
}

// Spec is the allocation of a source argument (plan §4.1, dataset-spec §1.2).
type Spec struct {
	Format   string `json:"format"`
	Domain   string `json:"domain"`
	Speakers int    `json:"speakers"`
	Props    int    `json:"props"`
	// Structure is the v1 structure label (chain / convergent / …). v2 replaces it with the numeric targets.
	Structure string `json:"structure,omitempty"`

	// Numeric targets (v2, dataset-spec §1.2).
	RA           int  `json:"ra,omitempty"`
	CA           int  `json:"ca,omitempty"`
	MA           int  `json:"ma,omitempty"`
	Isolated     int  `json:"isolated,omitempty"`
	MultiPremise bool `json:"multi_premise,omitempty"`
	Chain        bool `json:"chain,omitempty"`
	// SizeBin is the n_P bin ("8-10", "11-15", "16-20").
	SizeBin string `json:"size_bin,omitempty"`

	TopicID string   `json:"topic_id,omitempty"`
	Topic   string   `json:"topic,omitempty"`
	Names   []string `json:"names,omitempty"`
	// ExemplarPair is the two exemplars shown in the prompt (dataset-spec §1.5.2).
	ExemplarPair ExemplarPair `json:"exemplar_pair,omitzero"`
	// Generator is the model id assigned to write this source and all its variants.
	Generator string `json:"generator,omitempty"`
	// Seed is the per-source seed (exemplar order, plan draws).
	Seed uint64 `json:"seed,omitempty"`
}

// Edges is the target number of relations.
func (s Spec) Edges() int { return s.RA + s.CA + s.MA }

// Agent is one model endpoint with its fixed reasoning settings.
type Agent struct {
	Client llm.Client
	Model  string
	// Family is the provider family ("openai" / "anthropic"); see FamilyOf.
	Family   string
	Effort   string
	Thinking string
}

// FamilyOf maps a model id to its provider family.
func FamilyOf(model string) string {
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	}
	return "unknown"
}

// NewAgent builds an agent for model with its family filled in.
func NewAgent(c llm.Client, model, effort, thinking string) Agent {
	return Agent{Client: c, Model: model, Family: FamilyOf(model), Effort: effort, Thinking: thinking}
}

// Setting is the reasoning setting as recorded ("effort=low,thinking=adaptive").
func (a Agent) Setting() string {
	var parts []string
	if a.Effort != "" {
		parts = append(parts, "effort="+a.Effort)
	}
	if a.Thinking != "" {
		parts = append(parts, "thinking="+a.Thinking)
	}
	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, ",")
}

func (a Agent) request(system, user, tag string) llm.Request {
	return llm.Request{
		Model:    a.Model,
		Messages: []llm.Message{llm.System(system), llm.User(user)},
		Effort:   a.Effort,
		Thinking: a.Thinking,
		Tag:      tag,
	}
}

// Generator bundles LLM calls.
type Generator struct {
	// Client / Model are the writer (generator family). For v1 they are also the verifier and the judge.
	Client   llm.Client
	Model    string
	Effort   string
	Thinking string

	// Verifier is the other-family model of v2 (nil in v1: the writer verifies itself).
	Verifier *Agent

	Version            string // V1 (default) or V2
	SourcePrompt       Prompt
	RealizePrompt      Prompt
	JudgePrompt        Prompt
	VerifyPrompt       Prompt
	VerifySourcePrompt Prompt
	TopicPrompt        Prompt
	// Exemplars is the exemplar bank rendered into the v2 source prompt (looked up by id).
	Exemplars []Exemplar
}

// Writer returns the generator model as an Agent.
func (g *Generator) Writer() Agent {
	return Agent{Client: g.Client, Model: g.Model, Family: FamilyOf(g.Model), Effort: g.Effort, Thinking: g.Thinking}
}

// verifier returns the verifier agent (the writer itself when none is set, as in v1).
func (g *Generator) verifier() Agent {
	if g.Verifier != nil {
		return *g.Verifier
	}
	return g.Writer()
}

// Usage aggregates cost (per call purpose).
type Usage map[string]llm.Usage

// sourceOut is the structured output of source argument generation.
// Its shape (field order, tags) is part of the cache key of every v1 and v2 source request.
type sourceOut struct {
	Utterances   []Utterance `json:"utterances"`
	Propositions []struct {
		ID        string `json:"id" jsonschema_description:"p1, p2, ..."`
		Utterance int    `json:"utterance" jsonschema_description:"0-based index of the utterance that states it"`
		Text      string `json:"text" jsonschema_description:"Self-contained declarative sentence"`
	} `json:"propositions"`
	Relations []struct {
		Kind string `json:"kind" jsonschema:"enum=RA,enum=CA,enum=MA"`
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"relations"`
}

// SourceUserMessage is the user message of source generation for the generator's prompt version.
func (g *Generator) SourceUserMessage(spec Spec, attempt int) string {
	var user string
	if g.Version == V2 {
		yn := map[bool]string{false: "no", true: "yes"}
		names := strings.Join(spec.Names, ", ")
		if names == "" {
			names = "(none; use the role only)"
		}
		user = fmt.Sprintf("Format: %s\nTopic: %s\nSpeaker names: %s\nTargets: propositions %d, RA %d, CA %d, MA %d, isolated %d, multi-premise %s, chain %s\n",
			spec.Format, spec.Topic, names, spec.Props, spec.RA, spec.CA, spec.MA, spec.Isolated, yn[spec.MultiPremise], yn[spec.Chain])
	} else {
		user = fmt.Sprintf("Format: %s\nTopic area: %s\nNumber of speakers: %d\nTarget number of propositions: %d\nStructure: %s\n",
			spec.Format, spec.Domain, spec.Speakers, spec.Props, spec.Structure)
	}
	if attempt > 0 {
		user += fmt.Sprintf("(attempt %d)\n", attempt+1)
	}
	return user
}

// SourceSystemPrompt is the system prompt of source generation: v1 verbatim; v2 with the two exemplars
// of spec.ExemplarPair rendered in place of {{exemplars}} (in the pair's display order).
// withExemplars=false renders the v2 prompt without examples (homogeneity monitor, dataset-spec §1.5.2 (4)).
func (g *Generator) SourceSystemPrompt(spec Spec, withExemplars bool) (string, error) {
	if g.Version != V2 {
		return g.SourcePrompt.Body, nil
	}
	if !withExemplars {
		return RenderExemplarsInto(g.SourcePrompt.Body, nil), nil
	}
	var exs []Exemplar
	for _, id := range spec.ExemplarPair.Order {
		ex, ok := FindExemplar(g.Exemplars, id)
		if !ok {
			return "", fmt.Errorf("synth: exemplar %q not loaded", id)
		}
		exs = append(exs, ex)
	}
	if len(exs) != 2 {
		return "", fmt.Errorf("synth: spec has %d exemplars, want 2", len(exs))
	}
	return RenderExemplarsInto(g.SourcePrompt.Body, exs), nil
}

// GenerateSource builds a source argument. prefix is the proposition ID prefix (unique per source argument).
// v2 renders the exemplar pair of spec into the system prompt; the pair is part of the prompt text and
// therefore of the LLM cache key.
func (g *Generator) GenerateSource(ctx context.Context, spec Spec, prefix string, attempt int) (Discussion, *llm.Response, error) {
	return g.generateSource(ctx, spec, prefix, attempt, true)
}

// GenerateSourceNoExemplars is GenerateSource with the v2 prompt but no examples (homogeneity monitor only).
func (g *Generator) GenerateSourceNoExemplars(ctx context.Context, spec Spec, prefix string) (Discussion, *llm.Response, error) {
	return g.generateSource(ctx, spec, prefix, 0, false)
}

func (g *Generator) generateSource(ctx context.Context, spec Spec, prefix string, attempt int, withEx bool) (Discussion, *llm.Response, error) {
	sys, err := g.SourceSystemPrompt(spec, withEx)
	if err != nil {
		return Discussion{}, nil, err
	}
	req := llm.Request{
		Model:    g.Model,
		Messages: []llm.Message{llm.System(sys), llm.User(g.SourceUserMessage(spec, attempt))},
		Tag:      "synth_source",
	}
	if g.Version == V2 {
		req.Effort, req.Thinking = g.Effort, g.Thinking
	}
	out, resp, err := llm.CompleteJSON[sourceOut](ctx, g.Client, req, "discussion", "A discussion with its argument structure")
	if err != nil {
		return Discussion{}, resp, err
	}
	d, err := out.discussion(prefix)
	return d, resp, err
}

// discussion converts the structured output to a Discussion, checking the references.
func (out sourceOut) discussion(prefix string) (Discussion, error) {
	d := Discussion{Utterances: out.Utterances, PropUtterance: map[string]int{}}
	ids := map[string]string{}
	for _, p := range out.Propositions {
		if _, dup := ids[p.ID]; dup || p.Utterance < 0 || p.Utterance >= len(out.Utterances) || strings.TrimSpace(p.Text) == "" {
			return Discussion{}, fmt.Errorf("synth: invalid proposition %q", p.ID)
		}
		id := prefix + p.ID
		ids[p.ID] = id
		d.Graph.Props = append(d.Graph.Props, ged.Prop{ID: id, Text: p.Text})
		d.PropUtterance[id] = p.Utterance
	}
	seen := map[[2]string]bool{}
	for _, r := range out.Relations {
		f, ok1 := ids[r.From]
		t, ok2 := ids[r.To]
		if !ok1 || !ok2 || f == t {
			return Discussion{}, fmt.Errorf("synth: invalid relation %s→%s", r.From, r.To)
		}
		k := [2]string{min(f, t), max(f, t)}
		if seen[k] {
			continue // keep only one relation per pair
		}
		seen[k] = true
		d.Graph.Edges = append(d.Graph.Edges, ged.Edge{From: f, To: t, Kind: r.Kind})
	}
	if len(d.Graph.Props) < 4 || len(d.Graph.Edges) < 2 {
		return Discussion{}, errors.New("synth: too few propositions or relations")
	}
	return d, nil
}

// GraphStats are the shape statistics of a source graph (dataset-spec §1.2 / §1.3 report).
type GraphStats struct {
	Props, Edges, RA, CA, MA, Isolated int
	// MultiPremise: some proposition has ≥ 2 incoming RA. Chain: a directed RA/CA/MA path of length ≥ 2 edges.
	MultiPremise, Chain bool
	// MaxPerUtterance is the largest number of propositions stated in one utterance.
	MaxPerUtterance int
	// Distances counts edges by utterance distance (|u(from) − u(to)|).
	Distances map[int]int
}

// Stats computes GraphStats.
func Stats(d Discussion) GraphStats {
	g := d.Graph
	st := GraphStats{Props: len(g.Props), Edges: len(g.Edges), Distances: map[int]int{}}
	deg := map[string]int{}
	inRA := map[string]int{}
	succ := map[string][]string{}
	for _, e := range g.Edges {
		switch e.Kind {
		case ged.RA:
			st.RA++
			inRA[e.To]++
		case ged.CA:
			st.CA++
		case ged.MA:
			st.MA++
		}
		deg[e.From]++
		deg[e.To]++
		succ[e.From] = append(succ[e.From], e.To)
		if d.PropUtterance != nil {
			st.Distances[abs(d.PropUtterance[e.From]-d.PropUtterance[e.To])]++
		}
	}
	for _, p := range g.Props {
		if deg[p.ID] == 0 {
			st.Isolated++
		}
		if inRA[p.ID] >= 2 {
			st.MultiPremise = true
		}
		for _, q := range succ[p.ID] {
			if len(succ[q]) > 0 {
				st.Chain = true
			}
		}
	}
	per := map[int]int{}
	for _, u := range d.PropUtterance {
		per[u]++
		st.MaxPerUtterance = max(st.MaxPerUtterance, per[u])
	}
	return st
}

// TargetProblems compares a generated source with its numeric targets (dataset-spec §7 "source
// acceptance": relation counts within ±1 per type, isolated ≥ 2) and returns the violations.
func TargetProblems(d Discussion, spec Spec) []string {
	st := Stats(d)
	var out []string
	check := func(name string, got, want int) {
		if abs(got-want) > 1 {
			out = append(out, fmt.Sprintf("%s: %d relations, target %d (±1)", name, got, want))
		}
	}
	check("RA", st.RA, spec.RA)
	check("CA", st.CA, spec.CA)
	check("MA", st.MA, spec.MA)
	if st.CA < 1 {
		out = append(out, "no CA relation (≥ 1 required)")
	}
	if st.Isolated < 2 {
		out = append(out, fmt.Sprintf("%d isolated propositions (≥ 2 required)", st.Isolated))
	}
	if abs(st.Props-spec.Props) > 1 {
		out = append(out, fmt.Sprintf("%d propositions, target %d (±1)", st.Props, spec.Props))
	}
	if st.MaxPerUtterance > 2 {
		out = append(out, fmt.Sprintf("an utterance states %d propositions (≤ 2)", st.MaxPerUtterance))
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var verbs = map[string]string{ged.RA: "supports", ged.CA: "attacks", ged.MA: "restates"}

func short(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// Short is the prompt-facing proposition id ("s3.p4" → "p4").
func Short(id string) string { return short(id) }

// describe turns an edit into an English instruction for realization (v1 wording; part of the v1 cache keys).
func describe(e perturb.Edit) string {
	switch e.Kind {
	case perturb.PPara:
		how := map[string]string{"weak": "light rewording", "medium": "clear rewording", "strong": "substantial rewording"}[e.Strength]
		return fmt.Sprintf("Paraphrase proposition %s (%s) without changing its meaning.", short(e.Prop), how)
	case perturb.PShift:
		how := map[string]string{"small": "a small change", "medium": "a moderate change", "large": "a large change (it should become a clearly different claim)"}[e.Strength]
		return fmt.Sprintf("Change the content of proposition %s to a different claim on the same topic that keeps all its relations (%s).", short(e.Prop), how)
	case perturb.PNeg:
		return fmt.Sprintf("Negate proposition %s: reverse its polarity or quantifier (e.g. \"should\" → \"should not\", \"all\" → \"some\"), keeping everything else, including its relations as stated.", short(e.Prop))
	case perturb.PDel, perturb.PDelR:
		return fmt.Sprintf("Remove proposition %s from the discussion entirely, together with any relation it takes part in.", short(e.Prop))
	case perturb.PAdd:
		return fmt.Sprintf("Add a new proposition %s: a claim on the same topic that has no relation to any other proposition (it neither supports, attacks nor restates anything).", short(e.Prop))
	case perturb.RDel:
		return fmt.Sprintf("Remove the relation \"%s %s %s\": both propositions stay stated, but %s is no longer presented as related to %s.",
			short(e.Edge.From), verbs[e.Edge.Kind], short(e.Edge.To), short(e.Edge.From), short(e.Edge.To))
	case perturb.RAdd:
		return fmt.Sprintf("Add the relation \"%s %s %s\" without changing what either proposition claims.",
			short(e.Edge.From), verbs[e.Edge.Kind], short(e.Edge.To))
	case perturb.RType:
		return fmt.Sprintf("Change the relation \"%s %s %s\" so that %s now %s %s, keeping what each proposition claims as far as possible.",
			short(e.Edge.From), verbs[e.Edge.Kind], short(e.Edge.To), short(e.Edge.From), verbs[e.NewKind], short(e.Edge.To))
	case perturb.RTypeCA:
		// The diagnostic CA-involving relabel (dataset-spec §2.1–§2.2); only the v3 realizer is given it.
		// New case, so the v1 wording (and cache keys) of the other kinds is unchanged.
		return fmt.Sprintf("Change the relation \"%s %s %s\" so that %s now %s %s, keeping the wording of both propositions.",
			short(e.Edge.From), verbs[e.Edge.Kind], short(e.Edge.To), short(e.Edge.From), verbs[e.NewKind], short(e.Edge.To))
	case perturb.RRev:
		return fmt.Sprintf("Reverse the relation \"%s %s %s\" so that %s now %s %s.",
			short(e.Edge.From), verbs[e.Edge.Kind], short(e.Edge.To), short(e.Edge.To), verbs[e.Edge.Kind], short(e.Edge.From))
	case perturb.XSurf:
		return "Reword one utterance of your choice without changing any proposition or relation."
	}
	return ""
}

// Instructions are the edit instructions passed to realization and verification (speaker swaps are done by the harness, so they are excluded).
func Instructions(v perturb.Variant) []string {
	var instr []string
	for _, e := range v.Edits {
		if e.Kind != perturb.XSpk {
			instr = append(instr, fmt.Sprintf("%d. %s", len(instr)+1, describe(e)))
		}
	}
	return instr
}

// NeedsLLM reports whether the variant needs LLM realization (speaker swaps alone are handled by the harness).
func NeedsLLM(v perturb.Variant) bool { return len(Instructions(v)) > 0 }

func swapsSpeakers(edits []perturb.Edit) bool {
	for _, e := range edits {
		if e.Kind == perturb.XSpk {
			return true
		}
	}
	return false
}

// describeSource writes the source argument's utterances, propositions and relations with numbers.
func describeSource(b *strings.Builder, utts []Utterance, src Discussion) {
	b.WriteString("Utterances:\n")
	for i, u := range utts {
		fmt.Fprintf(b, "[%d] %s: %s\n", i, u.Speaker, u.Text)
	}
	b.WriteString("\nPropositions:\n")
	for _, p := range src.Graph.Props {
		fmt.Fprintf(b, "%s (utterance %d): %s\n", short(p.ID), src.PropUtterance[p.ID], p.Text)
	}
	b.WriteString("\nRelations:\n")
	for _, e := range src.Graph.Edges {
		fmt.Fprintf(b, "%s %s %s\n", short(e.From), verbs[e.Kind], short(e.To))
	}
}

func swapSpeakers(us []Utterance) []Utterance {
	out := slices.Clone(us)
	var names []string
	for _, u := range us {
		if !slices.Contains(names, u.Speaker) {
			names = append(names, u.Speaker)
		}
	}
	if len(names) < 2 {
		return out
	}
	a, b := names[0], names[1]
	for i, u := range out {
		switch u.Speaker {
		case a:
			out[i].Speaker = b
		case b:
			out[i].Speaker = a
		}
	}
	return out
}

type judgeOut struct {
	Answer string `json:"answer" jsonschema:"enum=1,enum=2,enum=same"`
}

// Judge is one judge model with the judge prompt.
type Judge struct {
	Agent
	Prompt Prompt
}

// Request builds the judgment request for (S, Version 1, Version 2). The request is the same for v1 and
// v2 except for the agent's reasoning settings (empty for the pilot-2 luna judge).
func (j Judge) Request(s, first, second Discussion) (llm.Request, error) {
	user := "Original discussion S:\n" + s.Render() +
		"\nVersion 1:\n" + first.Render() +
		"\nVersion 2:\n" + second.Render()
	req := j.request(j.Prompt.Body, user, "judge")
	schema, err := llm.SchemaFor[judgeOut]("judgment", "Which version better preserves the original")
	if err != nil {
		return llm.Request{}, err
	}
	req.Schema = schema
	return req, nil
}

// ParseJudgeAnswer decodes a judge response ("1" / "2" / "same").
func ParseJudgeAnswer(resp *llm.Response) (string, error) {
	if resp == nil {
		return "", errors.New("synth: no judge response")
	}
	var out judgeOut
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		return "", fmt.Errorf("synth: judge answer: %w (text=%.200q)", err, resp.Text)
	}
	switch out.Answer {
	case "1", "2", "same":
		return out.Answer, nil
	}
	return "", fmt.Errorf("synth: judge answer %q", out.Answer)
}

// JudgeAnswer is the result of one judgment ("1" / "2" / "same") by the generator model (v1 path).
func (g *Generator) JudgeAnswer(ctx context.Context, s, first, second Discussion) (string, *llm.Response, error) {
	j := Judge{Agent: Agent{Client: g.Client, Model: g.Model}, Prompt: g.JudgePrompt}
	req, err := j.Request(s, first, second)
	if err != nil {
		return "", nil, err
	}
	resp, err := g.Client.Complete(ctx, req)
	if err != nil {
		return "", nil, err
	}
	ans, err := ParseJudgeAnswer(resp)
	return ans, resp, err
}

// MarshalIndent is for writing JSON.
func MarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
