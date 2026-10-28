package synth

// QT30 transfer-set import (dataset-spec §1.4). Reads the nodeset documents that aif-with-llm stored in
// ../aif-with-llm/data/aifdb (nodesetNNN.json: {nodeset_id, corpus, text, graph{nodes, edges},
// text_located, text_locutions}) without importing that module: the AIF node types, the
// normalization and the metamodel rules below follow aif-with-llm/internal/aif (aif.go, validate.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// QT30TestEpisodes are the three test episodes (28 May, 2 Sep, 22 Oct 2020) — the only ones imported.
var QT30TestEpisodes = []string{"cutietestrun28May2020", "cutietestrun2September2020", "cutietestrun22October2020"}

type aifNode struct {
	ID      string `json:"nodeID"`
	Type    string `json:"type"`
	Text    string `json:"text"`
	Scheme  string `json:"scheme,omitempty"`
	Speaker string `json:"speaker,omitempty"`
}

type aifEdge struct {
	From string `json:"fromID"`
	To   string `json:"toID"`
}

type aifDoc struct {
	NodesetID string `json:"nodeset_id"`
	Corpus    string `json:"corpus"`
	Graph     struct {
		Nodes []aifNode `json:"nodes"`
		Edges []aifEdge `json:"edges"`
	} `json:"graph"`
	TextSource    string `json:"text_source"`
	TextLocated   int    `json:"text_located"`
	TextLocutions int    `json:"text_locutions"`
}

// QT30Source is one imported nodeset as a source discussion.
type QT30Source struct {
	NodesetID  string     `json:"nodeset_id"`
	Episode    string     `json:"episode"`
	Discussion Discussion `json:"discussion"`
	// Unanchored counts I-nodes with no L-node anchor (placed at a related proposition's utterance).
	Unanchored int `json:"unanchored,omitempty"`
}

// QT30Filter counts why nodesets were not imported (dataset-spec §1.4 filters, applied in this order).
type QT30Filter struct {
	Files        int `json:"files"`
	NotTest      int `json:"not_test_episode"`
	Metamodel    int `json:"metamodel_violation"`
	Size         int `json:"i_nodes_outside_8_20"`
	NotLocated   int `json:"not_all_located"`
	FewEdges     int `json:"fewer_than_4_edges"`
	Eligible     int `json:"eligible"`
	DupRelations int `json:"duplicate_pair_relations_dropped"`
}

// ImportQT30 reads every nodeset*.json in dir and returns the eligible test-episode nodesets: metamodel
// clean after normalization, 8–20 I-nodes, all locutions located in the transcript
// (text_located == text_locutions), ≥ 4 folded edges.
func ImportQT30(dir string) ([]QT30Source, QT30Filter, error) {
	files, err := filepath.Glob(filepath.Join(dir, "nodeset*.json"))
	if err != nil {
		return nil, QT30Filter{}, err
	}
	slices.Sort(files)
	var out []QT30Source
	var f QT30Filter
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, f, err
		}
		var doc aifDoc
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, f, fmt.Errorf("synth: %s: %w", filepath.Base(path), err)
		}
		f.Files++
		if !slices.Contains(QT30TestEpisodes, doc.Corpus) {
			f.NotTest++
			continue
		}
		nodes, edges := normalizeAIF(doc.Graph.Nodes, doc.Graph.Edges)
		if n := len(validateAIF(nodes, edges)); n > 0 {
			f.Metamodel++
			continue
		}
		nI := 0
		for _, n := range nodes {
			if n.Type == "I" {
				nI++
			}
		}
		if nI < 8 || nI > 20 {
			f.Size++
			continue
		}
		if doc.TextLocutions == 0 || doc.TextLocated != doc.TextLocutions {
			f.NotLocated++
			continue
		}
		src, dups := qt30Discussion(doc, nodes, edges)
		f.DupRelations += dups
		if len(src.Discussion.Graph.Edges) < 4 {
			f.FewEdges++
			continue
		}
		f.Eligible++
		out = append(out, src)
	}
	return out, f, nil
}

func allowedAIFEdge(from, to string) bool {
	isS := func(t string) bool { return t == "RA" || t == "CA" || t == "MA" }
	switch {
	case from == "I":
		return isS(to)
	case isS(from):
		return to == "I" || isS(to)
	case from == "L":
		return to == "YA" || to == "TA"
	case from == "YA":
		return to == "I" || isS(to)
	case from == "TA":
		return to == "L" || to == "YA"
	}
	return false
}

// normalizeAIF drops edges with missing ends, shapes the metamodel does not have, self-loops and
// duplicates, then nodes of degree 0 (aif-with-llm aif.Normalize).
func normalizeAIF(nodes []aifNode, edges []aifEdge) ([]aifNode, []aifEdge) {
	typ := map[string]string{}
	for _, n := range nodes {
		typ[n.ID] = n.Type
	}
	deg := map[string]int{}
	seen := map[aifEdge]bool{}
	var es []aifEdge
	for _, e := range edges {
		ft, tt := typ[e.From], typ[e.To]
		if ft == "" || tt == "" || e.From == e.To || !allowedAIFEdge(ft, tt) || seen[e] {
			continue
		}
		seen[e] = true
		es = append(es, e)
		deg[e.From]++
		deg[e.To]++
	}
	var ns []aifNode
	for _, n := range nodes {
		if deg[n.ID] > 0 {
			n.Text = strings.TrimSpace(n.Text)
			ns = append(ns, n)
		}
	}
	return ns, es
}

// validateAIF returns metamodel violations (rules R1–R7, R9, R10 of aif-with-llm aif.Validate; the
// illocutionary-force check R8 is not needed for the proposition graph).
func validateAIF(nodes []aifNode, edges []aifEdge) []string {
	typ := map[string]string{}
	var vs []string
	for _, n := range nodes {
		switch n.Type {
		case "I", "L", "RA", "CA", "MA", "YA", "TA":
		default:
			vs = append(vs, "R9 unknown type "+n.ID)
		}
		if _, dup := typ[n.ID]; dup {
			vs = append(vs, "R9 duplicate id "+n.ID)
		}
		typ[n.ID] = n.Type
	}
	in, out := map[string][]string{}, map[string][]string{}
	for _, e := range edges {
		if typ[e.From] == "" || typ[e.To] == "" {
			vs = append(vs, "R9 dangling edge")
			continue
		}
		out[e.From] = append(out[e.From], e.To)
		in[e.To] = append(in[e.To], e.From)
	}
	isS := func(t string) bool { return t == "RA" || t == "CA" || t == "MA" }
	for _, n := range nodes {
		switch t := n.Type; {
		case t == "I":
			for _, x := range out[n.ID] {
				if typ[x] == "I" {
					vs = append(vs, "R1 "+n.ID)
				}
			}
		case isS(t):
			prem, conc := 0, 0
			dst := ""
			for _, x := range in[n.ID] {
				if typ[x] == "I" {
					prem++
				}
			}
			for _, x := range out[n.ID] {
				if typ[x] == "I" || isS(typ[x]) {
					conc++
					dst = x
				}
			}
			if prem == 0 || (t != "RA" && prem != 1) || conc != 1 {
				vs = append(vs, "R2-4 "+n.ID)
			}
			if dst != "" && slices.Contains(in[n.ID], dst) {
				vs = append(vs, "R10 "+n.ID)
			}
		case t == "YA":
			anchors, targets := 0, 0
			for _, x := range in[n.ID] {
				if typ[x] == "L" || typ[x] == "TA" {
					anchors++
				}
			}
			for _, x := range out[n.ID] {
				if typ[x] == "I" || isS(typ[x]) {
					targets++
				}
			}
			if anchors != 1 || targets != 1 {
				vs = append(vs, "R5 "+n.ID)
			}
		case t == "TA":
			from, to := 0, 0
			for _, x := range in[n.ID] {
				if typ[x] == "L" {
					from++
				}
			}
			for _, x := range out[n.ID] {
				if typ[x] == "L" {
					to++
				}
			}
			if from != 1 || to != 1 {
				vs = append(vs, "R6 "+n.ID)
			}
		case t == "L":
			for _, x := range out[n.ID] {
				if typ[x] != "YA" && typ[x] != "TA" {
					vs = append(vs, "R7 "+n.ID)
				}
			}
		}
	}
	return vs
}

// qt30Discussion converts a normalized nodeset to a Discussion: utterances are the L-nodes in node
// order (speaker prefix "Name : " removed), propositions the I-nodes, edges the S-nodes folded into one
// edge per premise (RA with several premises gives several edges; S-node targets — undercuts — are dropped).
func qt30Discussion(doc aifDoc, nodes []aifNode, edges []aifEdge) (QT30Source, int) {
	prefix := "q" + doc.NodesetID + "."
	typ := map[string]string{}
	for _, n := range nodes {
		typ[n.ID] = n.Type
	}
	in, out := map[string][]string{}, map[string][]string{}
	for _, e := range edges {
		out[e.From] = append(out[e.From], e.To)
		in[e.To] = append(in[e.To], e.From)
	}
	src := QT30Source{NodesetID: doc.NodesetID, Episode: doc.Corpus}
	d := Discussion{PropUtterance: map[string]int{}}
	lIdx := map[string]int{}
	for _, n := range nodes {
		if n.Type != "L" {
			continue
		}
		text := n.Text
		if n.Speaker != "" {
			text = strings.TrimSpace(strings.TrimPrefix(text, n.Speaker+" :"))
		}
		lIdx[n.ID] = len(d.Utterances)
		d.Utterances = append(d.Utterances, Utterance{Speaker: n.Speaker, Text: text})
	}
	pid := map[string]string{}
	for _, n := range nodes {
		if n.Type == "I" {
			pid[n.ID] = fmt.Sprintf("%sp%d", prefix, len(d.Graph.Props)+1)
			d.Graph.Props = append(d.Graph.Props, ged.Prop{ID: pid[n.ID], Text: n.Text})
		}
	}
	// anchors: L → YA → I
	for _, n := range nodes {
		if n.Type != "YA" {
			continue
		}
		var l, i string
		for _, x := range in[n.ID] {
			if typ[x] == "L" {
				l = x
			}
		}
		for _, x := range out[n.ID] {
			if typ[x] == "I" {
				i = x
			}
		}
		if l != "" && i != "" {
			if _, done := d.PropUtterance[pid[i]]; !done {
				d.PropUtterance[pid[i]] = lIdx[l]
			}
		}
	}
	dups := 0
	seen := map[[2]string]bool{}
	for _, n := range nodes {
		if n.Type != "RA" && n.Type != "CA" && n.Type != "MA" {
			continue
		}
		var dst string
		for _, x := range out[n.ID] {
			if typ[x] == "I" {
				dst = x
			}
		}
		if dst == "" {
			continue
		}
		for _, x := range in[n.ID] {
			if typ[x] != "I" {
				continue
			}
			f, t := pid[x], pid[dst]
			k := [2]string{min(f, t), max(f, t)}
			if seen[k] {
				dups++
				continue
			}
			seen[k] = true
			d.Graph.Edges = append(d.Graph.Edges, ged.Edge{From: f, To: t, Kind: n.Type})
		}
	}
	// unanchored propositions: put them at a related proposition's utterance (or 0)
	for _, p := range d.Graph.Props {
		if _, ok := d.PropUtterance[p.ID]; ok {
			continue
		}
		src.Unanchored++
		u := 0
		for _, e := range d.Graph.Edges {
			var other string
			switch p.ID {
			case e.From:
				other = e.To
			case e.To:
				other = e.From
			}
			if v, ok := d.PropUtterance[other]; ok && other != "" {
				u = v
				break
			}
		}
		d.PropUtterance[p.ID] = u
	}
	src.Discussion = d
	return src, dups
}
