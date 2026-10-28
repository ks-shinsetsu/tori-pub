package synth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// Verbatim copies of the pilot-2 (commit 029bbf0) structured-output types. The v1 path must send
// exactly these schemas, so that `-prompt-version v1` replays pilot 2 from the response cache.
type v1SourceOut struct {
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

type v1RealizeOut struct {
	Changes []struct {
		Utterance int    `json:"utterance"`
		Op        string `json:"op" jsonschema:"enum=replace,enum=insert_after,enum=delete"`
		Speaker   string `json:"speaker"`
		Text      string `json:"text"`
		Role      string `json:"role" jsonschema:"enum=edit,enum=connective"`
	} `json:"changes"`
	Propositions []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"propositions"`
}

type v1Verification struct {
	Edits []struct {
		Index    int  `json:"index" jsonschema_description:"Number of the requested edit"`
		Realized bool `json:"realized"`
	} `json:"edits"`
	UnintendedChange bool   `json:"unintended_change"`
	Note             string `json:"note"`
	Naturalness      string `json:"naturalness" jsonschema:"enum=1,enum=2,enum=3,enum=4,enum=5"`
}

type v1JudgeOut struct {
	Answer string `json:"answer" jsonschema:"enum=1,enum=2,enum=same"`
}

func v1Request[T any](t *testing.T, model, system, user, tag, name, desc string) llm.Request {
	t.Helper()
	s, err := llm.SchemaFor[T](name, desc)
	if err != nil {
		t.Fatal(err)
	}
	return llm.Request{Model: model, Messages: []llm.Message{llm.System(system), llm.User(user)}, Schema: s, Tag: tag}
}

// The v1 requests (source, realize, verify, judge) have the pilot-2 cache keys.
func TestV1RequestsUnchanged(t *testing.T) {
	var got []llm.Request
	reply := map[string]string{
		"synth_source": `{"utterances":[{"speaker":"A","text":"x"},{"speaker":"B","text":"y"}],"propositions":[{"id":"p1","utterance":0,"text":"a"},{"id":"p2","utterance":0,"text":"b"},{"id":"p3","utterance":1,"text":"c"},{"id":"p4","utterance":1,"text":"d"}],"relations":[{"kind":"RA","from":"p1","to":"p2"},{"kind":"CA","from":"p3","to":"p2"}]}`,
		"realize":      `{"changes":[],"propositions":[]}`,
		"verify":       `{"edits":[],"unintended_change":false,"note":"","naturalness":"5"}`,
		"judge":        `{"answer":"1"}`,
	}
	c := &llm.FuncClient{Fn: func(_ context.Context, req llm.Request) (*llm.Response, error) {
		got = append(got, req)
		return &llm.Response{Text: reply[req.Tag]}, nil
	}}
	prompts := map[string]Prompt{}
	for _, n := range []string{"synth_source_v1", "realize_v2", "verify_v1", "judge_v1"} {
		p, err := LoadPrompt("../../prompts", n)
		if err != nil {
			t.Fatal(err)
		}
		prompts[n] = p
	}
	g := &Generator{Client: c, Model: "gpt-5.6-luna", SourcePrompt: prompts["synth_source_v1"], RealizePrompt: prompts["realize_v2"],
		VerifyPrompt: prompts["verify_v1"], JudgePrompt: prompts["judge_v1"]}
	ctx := context.Background()
	spec := Spec{Format: "meeting", Domain: "economy", Speakers: 3, Props: 12, Structure: "chain (each claim supports the next)"}
	src, _, err := g.GenerateSource(ctx, spec, "s0.", 1)
	if err != nil {
		t.Fatal(err)
	}
	e := src.Graph.Edges[0]
	v := perturb.Variant{Graph: src.Graph.Clone(), Edits: []perturb.Edit{{Kind: perturb.RDel, Edge: &e}}}
	v.Graph.Edges = v.Graph.Edges[1:]
	d, _, _, err := g.RealizeV1(ctx, src, v, "s0.", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.VerifyV1(ctx, src, d, v); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.JudgeAnswer(ctx, src, d, d); err != nil {
		t.Fatal(err)
	}

	var rb strings.Builder
	describeSource(&rb, src.Utterances, src)
	rb.WriteString("\nEdits:\n" + strings.Join(Instructions(v), "\n") + "\n")
	var vb strings.Builder
	vb.WriteString("ORIGINAL DISCUSSION\n\n")
	describeSource(&vb, src.Utterances, src)
	vb.WriteString("\nEDITED DISCUSSION\n\n" + d.Render() + "\nREQUESTED EDITS\n\n" + strings.Join(Instructions(v), "\n") + "\n")
	want := []llm.Request{
		v1Request[v1SourceOut](t, g.Model, prompts["synth_source_v1"].Body,
			fmt.Sprintf("Format: meeting\nTopic area: economy\nNumber of speakers: 3\nTarget number of propositions: 12\nStructure: %s\n(attempt 2)\n", spec.Structure),
			"synth_source", "discussion", "A discussion with its argument structure"),
		v1Request[v1RealizeOut](t, g.Model, prompts["realize_v2"].Body, rb.String(), "realize", "edited_discussion", "Changed utterances and propositions"),
		v1Request[v1Verification](t, g.Model, prompts["verify_v1"].Body, vb.String(), "verify", "verification", "Whether each requested edit is visible, unintended changes, naturalness"),
		v1Request[v1JudgeOut](t, g.Model, prompts["judge_v1"].Body,
			"Original discussion S:\n"+src.Render()+"\nVersion 1:\n"+d.Render()+"\nVersion 2:\n"+d.Render(), "judge", "judgment", "Which version better preserves the original"),
	}
	if len(got) != len(want) {
		t.Fatalf("%d requests", len(got))
	}
	for i := range want {
		if llm.CacheKey(got[i]) != llm.CacheKey(want[i]) || got[i].Tag != want[i].Tag {
			a, _ := json.Marshal(got[i])
			b, _ := json.Marshal(want[i])
			t.Errorf("request %d (%s) differs from pilot 2:\n got %s\nwant %s", i, want[i].Tag, a, b)
		}
	}
}
