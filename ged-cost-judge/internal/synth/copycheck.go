package synth

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
)

// Copy-detection thresholds (dataset-spec §1.5.2, detection 1–3).
const (
	CopyMaxShared5 = 3   // ≥ 3 shared 5-grams with one exemplar rejects
	CopyMaxCos     = 0.6 // embedding cos > 0.6 with any exemplar rejects
)

// SignalStoplist are signalling phrases excluded from the n-gram overlap (dataset-spec §1.5.2 (1)).
// Tokens inside an occurrence of a stoplist phrase break the n-grams, so an n-gram that spans a
// signalling phrase is never counted as copied.
var SignalStoplist = []string{
	"in other words", "that is to say", "put another way", "which is another reason",
	"because of that", "so you are saying",
}

var tokenRe = regexp.MustCompile(`[a-z0-9]+(?:'[a-z]+)?`)

func tokens(s string) []string { return tokenRe.FindAllString(strings.ToLower(s), -1) }

// ngramSet returns the set of n-grams of text, not crossing stoplist phrase occurrences.
func ngramSet(text string, n int) map[string]bool {
	toks := tokens(text)
	masked := make([]bool, len(toks))
	for _, ph := range SignalStoplist {
		pt := tokens(ph)
		for i := 0; i+len(pt) <= len(toks); i++ {
			if slices.Equal(toks[i:i+len(pt)], pt) {
				for k := range pt {
					masked[i+k] = true
				}
			}
		}
	}
	out := map[string]bool{}
	for i := 0; i+n <= len(toks); i++ {
		ok := true
		for k := range n {
			if masked[i+k] {
				ok = false
				break
			}
		}
		if ok {
			out[strings.Join(toks[i:i+n], " ")] = true
		}
	}
	return out
}

// SharedNgrams returns the n-grams shared by a and b (stoplist-aware), sorted.
func SharedNgrams(a, b string, n int) []string {
	sa, sb := ngramSet(a, n), ngramSet(b, n)
	var out []string
	for g := range sa {
		if sb[g] {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out
}

// CopyResult is the outcome of CopyCheck. Reject is true when any rule fires.
type CopyResult struct {
	Reject  bool     `json:"reject"`
	Reasons []string `json:"reasons,omitempty"`
	// Shared6 / Shared5 are the shared n-grams with the exemplar that has the most of them.
	Shared6 []string `json:"shared6,omitempty"`
	Shared5 []string `json:"shared5,omitempty"`
	Names   []string `json:"names,omitempty"`
	// MaxCos is the largest embedding cos with an exemplar text (0 and CosComputed=false without an embedder).
	MaxCos      float64 `json:"max_cos"`
	MaxCosWith  string  `json:"max_cos_with,omitempty"`
	CosComputed bool    `json:"cos_computed"`
}

// CopyCheck implements the mechanical exemplar-copy detection of dataset-spec §1.5.2 (1)–(3), run before
// the LLM-based G1 verification:
//
//  1. any shared token 6-gram (lower-cased, stoplist-aware) with an exemplar, or ≥ 3 shared 5-grams with one exemplar;
//  2. any exemplar speaker name (not a role) or reserved place / institution name in the source (speakers,
//     utterances or proposition texts; case-sensitive whole word);
//  3. embedding cos between the source text and an exemplar text > 0.6 (skipped when emb is nil).
func CopyCheck(ctx context.Context, src Discussion, exemplars []Exemplar, emb llm.Embedder) (CopyResult, error) {
	res := CopyResult{}
	text := utteranceText(src)
	for _, ex := range exemplars {
		s6 := SharedNgrams(text, ex.Text(), 6)
		s5 := SharedNgrams(text, ex.Text(), 5)
		if len(s6) > 0 {
			res.Reasons = append(res.Reasons, fmt.Sprintf("shares %d 6-gram(s) with %s (%q)", len(s6), ex.ID, s6[0]))
		}
		if len(s5) >= CopyMaxShared5 {
			res.Reasons = append(res.Reasons, fmt.Sprintf("shares %d 5-grams with %s", len(s5), ex.ID))
		}
		if len(s6) > len(res.Shared6) || (len(s6) == len(res.Shared6) && len(s5) > len(res.Shared5)) {
			res.Shared6, res.Shared5 = s6, s5
		}
	}
	var all strings.Builder
	for _, u := range src.Utterances {
		all.WriteString(u.Speaker + "\n" + u.Text + "\n")
	}
	for _, p := range src.Graph.Props {
		all.WriteString(p.Text + "\n")
	}
	for _, ex := range exemplars {
		for _, n := range ex.Names() {
			if containsWord(all.String(), n) && !slices.Contains(res.Names, n) {
				res.Names = append(res.Names, n)
				res.Reasons = append(res.Reasons, fmt.Sprintf("uses the name %q from %s", n, ex.ID))
			}
		}
	}
	if emb != nil && len(exemplars) > 0 {
		texts := []string{text}
		for _, ex := range exemplars {
			texts = append(texts, ex.Text())
		}
		vecs, err := emb.Embed(ctx, texts)
		if err != nil {
			return res, fmt.Errorf("synth: copy check embedding: %w", err)
		}
		res.CosComputed = true
		res.MaxCos = -1
		for i, ex := range exemplars {
			c := Cosine(vecs[0], vecs[i+1])
			if c > res.MaxCos {
				res.MaxCos, res.MaxCosWith = c, ex.ID
			}
		}
		if res.MaxCos > CopyMaxCos {
			res.Reasons = append(res.Reasons, fmt.Sprintf("text cos %.2f with %s (> %.1f)", res.MaxCos, res.MaxCosWith, CopyMaxCos))
		}
	}
	res.Reject = len(res.Reasons) > 0
	return res, nil
}

// containsWord reports whether name occurs in s as a whole word (case-sensitive).
func containsWord(s, name string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return false
		}
		at := i + j
		before := at == 0 || !isWordByte(s[at-1])
		after := at+len(name) >= len(s) || !isWordByte(s[at+len(name)])
		if before && after {
			return true
		}
		i = at + 1
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Cosine is the cosine similarity of two vectors (0 if either is zero).
func Cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range min(len(a), len(b)) {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
