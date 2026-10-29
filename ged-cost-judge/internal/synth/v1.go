package synth

// The pilot-2 (v1) realization and verification. Kept unchanged so that a pilot-2 run replays from the
// response cache (`-prompt-version v1`); the struct bodies below are part of the cache keys.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

type realizeOutV1 struct {
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

// RealizeStats is the result of mechanically checking a realization.
type RealizeStats struct {
	Changed     int `json:"changed"`
	Connective  int `json:"connective"`
	MissingText int `json:"missing_text"`
	// Collateral counts replaced/deleted utterances outside the edited utterances and their neighbours (v2).
	Collateral int `json:"collateral,omitempty"`
}

// RealizeV1 realizes variant v as text based on src (pilot-2 behaviour). Unchanged utterances are
// assembled here verbatim (plan §4.4). A touched proposition without returned text falls back to the
// source text (counted in MissingText); v2's Realize treats that as an error instead.
func (g *Generator) RealizeV1(ctx context.Context, src Discussion, v perturb.Variant, prefix string, attempt int, feedback string) (Discussion, RealizeStats, *llm.Response, error) {
	out := Discussion{Graph: v.Graph.Clone()}
	instr := Instructions(v)
	needLLM := len(instr) > 0
	utts := slices.Clone(src.Utterances)
	if swapsSpeakers(v.Edits) {
		utts = swapSpeakers(utts)
	}
	srcText := map[string]string{}
	for _, p := range src.Graph.Props {
		srcText[p.ID] = p.Text
	}
	if !needLLM {
		out.Utterances = utts
		for i, p := range out.Graph.Props {
			out.Graph.Props[i].Text = srcText[p.ID]
		}
		return out, RealizeStats{}, nil, nil
	}

	var b strings.Builder
	describeSource(&b, utts, src)
	b.WriteString("\nEdits:\n" + strings.Join(instr, "\n") + "\n")
	if feedback != "" {
		fmt.Fprintf(&b, "\nA previous attempt (%d) was rejected: %s\n", attempt, feedback)
	}

	res, resp, err := llm.CompleteJSON[realizeOutV1](ctx, g.Client, llm.Request{
		Model:    g.Model,
		Messages: []llm.Message{llm.System(g.RealizePrompt.Body), llm.User(b.String())},
		Tag:      "realize",
	}, "edited_discussion", "Changed utterances and propositions")
	if err != nil {
		return Discussion{}, RealizeStats{}, resp, err
	}

	var st RealizeStats
	replace := map[int]Utterance{}
	deleted := map[int]bool{}
	inserts := map[int][]Utterance{}
	for _, c := range res.Changes {
		if c.Utterance < -1 || c.Utterance >= len(utts) {
			return Discussion{}, st, resp, fmt.Errorf("synth: change refers to utterance %d", c.Utterance)
		}
		st.Changed++
		if c.Role == "connective" {
			st.Connective++
		}
		u := Utterance{Speaker: c.Speaker, Text: c.Text}
		switch c.Op {
		case "replace":
			if c.Utterance < 0 {
				return Discussion{}, st, resp, errors.New("synth: replace of utterance -1")
			}
			if u.Speaker == "" {
				u.Speaker = utts[c.Utterance].Speaker
			}
			replace[c.Utterance] = u
		case "delete":
			if c.Utterance >= 0 {
				deleted[c.Utterance] = true
			}
		case "insert_after":
			inserts[c.Utterance] = append(inserts[c.Utterance], u)
		}
	}
	out.Utterances = append(out.Utterances, inserts[-1]...)
	for i, u := range utts {
		if !deleted[i] {
			if r, ok := replace[i]; ok {
				u = r
			}
			out.Utterances = append(out.Utterances, u)
		}
		out.Utterances = append(out.Utterances, inserts[i]...)
	}

	newText := map[string]string{}
	for _, p := range res.Propositions {
		newText[prefix+p.ID] = p.Text
		newText[p.ID] = p.Text
	}
	for i, p := range out.Graph.Props {
		t, changed := newText[p.ID]
		if !changed {
			t, changed = newText[short(p.ID)]
		}
		_, mustChange := v.Changed[p.ID]
		isNew := !strings.HasPrefix(p.ID, prefix)
		switch {
		case (mustChange || isNew) && changed && strings.TrimSpace(t) != "":
			out.Graph.Props[i].Text = t
		case mustChange || isNew:
			st.MissingText++
			out.Graph.Props[i].Text = srcText[p.ID]
		default:
			out.Graph.Props[i].Text = srcText[p.ID]
		}
	}
	return out, st, resp, nil
}

// VerificationV1 is the pilot-2 verification result (yes/no per edit; the same luna as generation).
type VerificationV1 struct {
	Edits []struct {
		Index    int  `json:"index" jsonschema_description:"Number of the requested edit"`
		Realized bool `json:"realized"`
	} `json:"edits"`
	UnintendedChange bool   `json:"unintended_change"`
	Note             string `json:"note"`
	Naturalness      string `json:"naturalness" jsonschema:"enum=1,enum=2,enum=3,enum=4,enum=5"`
}

// Natural is naturalness (1–5, or 0 if unreadable).
func (v VerificationV1) Natural() int { return score15(v.Naturalness) }

func score15(s string) int {
	if len(s) == 1 && s[0] >= '1' && s[0] <= '5' {
		return int(s[0] - '0')
	}
	return 0
}

// Problems returns the reasons for failing (empty if passed). n is the number of instructed edits.
// Criteria: every edit appears in the text, no uninstructed changes, naturalness 3 or more (plan §4.4).
func (v VerificationV1) Problems(n int) []string {
	var out []string
	seen := map[int]bool{}
	for _, e := range v.Edits {
		seen[e.Index] = true
		if !e.Realized {
			out = append(out, fmt.Sprintf("edit %d is not clearly visible in the text", e.Index))
		}
	}
	for i := 1; i <= n; i++ {
		if !seen[i] {
			out = append(out, fmt.Sprintf("edit %d was not checked", i))
		}
	}
	if v.UnintendedChange {
		out = append(out, "it changes something no edit asked for ("+v.Note+")")
	}
	if v.Natural() < 3 {
		out = append(out, fmt.Sprintf("it reads unnaturally (naturalness %s of 5)", v.Naturalness))
	}
	return out
}

// VerifyV1 checks, in a separate request, whether the realized variant d implements variant v's edits (pilot 2).
func (g *Generator) VerifyV1(ctx context.Context, src Discussion, d Discussion, v perturb.Variant) (VerificationV1, *llm.Response, error) {
	var b strings.Builder
	b.WriteString("ORIGINAL DISCUSSION\n\n")
	describeSource(&b, src.Utterances, src)
	b.WriteString("\nEDITED DISCUSSION\n\n")
	b.WriteString(d.Render())
	b.WriteString("\nREQUESTED EDITS\n\n" + strings.Join(Instructions(v), "\n") + "\n")
	if swapsSpeakers(v.Edits) {
		b.WriteString("\nSpeaker names were swapped on purpose.\n")
	}
	return llm.CompleteJSON[VerificationV1](ctx, g.Client, llm.Request{
		Model:    g.Model,
		Messages: []llm.Message{llm.System(g.VerifyPrompt.Body), llm.User(b.String())},
		Tag:      "verify",
	}, "verification", "Whether each requested edit is visible, unintended changes, naturalness")
}
