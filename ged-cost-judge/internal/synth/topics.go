package synth

import (
	"context"
	"fmt"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/llm"
)

// TopicDedupCos is the dedup threshold of the topic bank: a topic is kept only if its cos with every
// kept topic and every reserved exemplar topic is below it (dataset-spec §1.3).
const TopicDedupCos = 0.85

// TopicsPerCall is the number of questions asked for per stage-0 call (two calls per domain ≈ 100 topics).
const TopicsPerCall = 50

type topicsOut struct {
	Questions []string `json:"questions" jsonschema_description:"Concrete, distinct debate questions"`
}

// GenerateTopics asks agent for n concrete debate questions in domain (stage 0, prompts/synth_topics_v1.md).
// batch numbers the calls for one domain (0, 1, …); avoid lists questions from earlier batches so the
// model does not repeat them. Topics are returned unnumbered; ids are given by DedupTopics.
func GenerateTopics(ctx context.Context, agent Agent, prompt Prompt, domain string, n, batch int, avoid []string) ([]Topic, *llm.Response, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Domain: %s\nNumber of questions: %d\nBatch: %d\n", domain, n, batch+1)
	if len(avoid) > 0 {
		b.WriteString("\nQuestions already in the bank (do not repeat or rephrase them):\n")
		for _, q := range avoid {
			b.WriteString("- " + q + "\n")
		}
	}
	out, resp, err := llm.CompleteJSON[topicsOut](ctx, agent.Client, agent.request(prompt.Body, b.String(), "synth_topics"),
		"topics", "Debate questions for one domain")
	if err != nil {
		return nil, resp, err
	}
	var ts []Topic
	for _, q := range out.Questions {
		if q = strings.TrimSpace(q); q != "" {
			ts = append(ts, Topic{Domain: domain, Question: q, Generator: agent.Model})
		}
	}
	return ts, resp, nil
}

// DedupTopics keeps, in order, every topic whose embedding cos with all kept topics and all reserved
// topics (the exemplar topics) is below TopicDedupCos, and numbers the kept ones per domain
// ("d03-t017"). It returns the kept topics and the number dropped.
func DedupTopics(ctx context.Context, emb llm.Embedder, topics []Topic, reserved []string) ([]Topic, int, error) {
	texts := make([]string, 0, len(topics)+len(reserved))
	for _, t := range topics {
		texts = append(texts, t.Question)
	}
	texts = append(texts, reserved...)
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return nil, 0, fmt.Errorf("synth: topic dedup: %w", err)
	}
	resVecs := vecs[len(topics):]
	var keptVecs [][]float32
	var kept []Topic
	perDomain := map[string]int{}
	for i, t := range topics {
		dup := false
		for _, v := range append(keptVecs, resVecs...) {
			if Cosine(vecs[i], v) >= TopicDedupCos {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		keptVecs = append(keptVecs, vecs[i])
		perDomain[t.Domain]++
		t.ID = fmt.Sprintf("d%02d-t%03d", domainIndex(t.Domain)+1, perDomain[t.Domain])
		kept = append(kept, t)
	}
	return kept, len(topics) - len(kept), nil
}

func domainIndex(d string) int {
	for i, x := range Domains {
		if x == d {
			return i
		}
	}
	return len(Domains)
}
