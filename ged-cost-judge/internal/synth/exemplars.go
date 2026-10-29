package synth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Exemplar is one created few-shot exemplar (prompts/exemplars/exNN-*.md, dataset-spec §1.5).
// The file is YAML-like front matter followed by a ```json block in the exact source output schema.
type Exemplar struct {
	ID     string `json:"id"`
	Format string `json:"format"`
	Domain string `json:"domain"`
	// Topic is the front-matter topic without the "(reserved; …)" note.
	Topic        string `json:"topic"`
	Props        int    `json:"propositions"`
	Edges        int    `json:"relations"`
	RA           int    `json:"ra"`
	CA           int    `json:"ca"`
	MA           int    `json:"ma"`
	Isolated     int    `json:"isolated"`
	MultiPremise bool   `json:"multi_premise"`
	Chain        bool   `json:"chain"`
	Words        int    `json:"words"`
	// ReservedNames are place / institution names that must not appear in a generated source
	// (front-matter key reserved_names; speaker names are added from the utterances).
	ReservedNames []string `json:"reserved_names,omitempty"`
	// JSON is the raw json block, rendered verbatim into the prompt.
	JSON string `json:"-"`
	// Discussion is the parsed gold graph (proposition ids as in the file: p1, p2, …).
	Discussion Discussion `json:"-"`
}

// Roles are speaker labels that are roles, not names (allowed in sources; not reserved).
var Roles = []string{"Host", "Interviewer", "Author", "Chair", "Moderator", "Audience member", "Caller"}

// LoadExemplars reads every ex*.md in dir (sorted by file name) and validates each gold graph.
func LoadExemplars(dir string) ([]Exemplar, error) {
	files, err := filepath.Glob(filepath.Join(dir, "ex*.md"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	var out []Exemplar
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		ex, err := ParseExemplar(string(b))
		if err != nil {
			return nil, fmt.Errorf("synth: exemplar %s: %w", filepath.Base(f), err)
		}
		if err := ex.Validate(); err != nil {
			return nil, fmt.Errorf("synth: exemplar %s: %w", filepath.Base(f), err)
		}
		out = append(out, ex)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("synth: no exemplars in %s", dir)
	}
	return out, nil
}

// ParseExemplar parses one exemplar file.
func ParseExemplar(s string) (Exemplar, error) {
	var ex Exemplar
	if !strings.HasPrefix(s, "---\n") {
		return ex, errors.New("no front matter")
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return ex, errors.New("unterminated front matter")
	}
	front, body := s[4:4+end], s[4+end+5:]
	for line := range strings.SplitSeq(front, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			ex.ID = v
		case "format":
			ex.Format = v
		case "domain":
			ex.Domain = v
		case "topic":
			if i := strings.Index(v, " (reserved"); i >= 0 {
				v = v[:i]
			}
			ex.Topic = v
		case "propositions":
			ex.Props = leadingInt(v)
		case "relations":
			// "7 (RA 4, CA 1, MA 2)"
			ex.Edges = leadingInt(v)
			for _, part := range strings.Split(strings.Trim(v[strings.IndexByte(v, '(')+1:], ")"), ",") {
				f := strings.Fields(part)
				if len(f) != 2 {
					continue
				}
				n, _ := strconv.Atoi(f[1])
				switch f[0] {
				case "RA":
					ex.RA = n
				case "CA":
					ex.CA = n
				case "MA":
					ex.MA = n
				}
			}
		case "isolated":
			ex.Isolated = leadingInt(v)
		case "multi_premise":
			ex.MultiPremise = strings.HasPrefix(v, "true")
		case "chain":
			ex.Chain = strings.HasPrefix(v, "true")
		case "words":
			ex.Words = leadingInt(v)
		case "reserved_names":
			for n := range strings.SplitSeq(v, ",") {
				if n = strings.TrimSpace(n); n != "" {
					ex.ReservedNames = append(ex.ReservedNames, n)
				}
			}
		}
	}
	start := strings.Index(body, "```json\n")
	if start < 0 {
		return ex, errors.New("no ```json block")
	}
	rest := body[start+len("```json\n"):]
	stop := strings.Index(rest, "\n```")
	if stop < 0 {
		return ex, errors.New("unterminated ```json block")
	}
	ex.JSON = rest[:stop]
	var out sourceOut
	dec := json.NewDecoder(strings.NewReader(ex.JSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return ex, fmt.Errorf("json block: %w", err)
	}
	d, err := out.discussion("")
	if err != nil {
		return ex, err
	}
	if len(out.Relations) != len(d.Graph.Edges) {
		return ex, errors.New("duplicate relation between one pair")
	}
	ex.Discussion = d
	return ex, nil
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// Validate checks the gold graph against the front matter (and so that the numeric targets are satisfiable).
func (ex Exemplar) Validate() error {
	if ex.ID == "" || ex.Format == "" || ex.Topic == "" {
		return errors.New("front matter lacks id, format or topic")
	}
	st := Stats(ex.Discussion)
	var errs []error
	cmp := func(name string, got, want int) {
		if got != want {
			errs = append(errs, fmt.Errorf("%s: graph has %d, front matter says %d", name, got, want))
		}
	}
	cmp("propositions", st.Props, ex.Props)
	cmp("relations", st.Edges, ex.Edges)
	cmp("RA", st.RA, ex.RA)
	cmp("CA", st.CA, ex.CA)
	cmp("MA", st.MA, ex.MA)
	cmp("isolated", st.Isolated, ex.Isolated)
	if st.MultiPremise != ex.MultiPremise {
		errs = append(errs, fmt.Errorf("multi_premise: graph %v, front matter %v", st.MultiPremise, ex.MultiPremise))
	}
	if st.Chain != ex.Chain {
		errs = append(errs, fmt.Errorf("chain: graph %v, front matter %v", st.Chain, ex.Chain))
	}
	if st.MaxPerUtterance > 2 {
		errs = append(errs, fmt.Errorf("an utterance states %d propositions", st.MaxPerUtterance))
	}
	return errors.Join(errs...)
}

// SpeakerNames are the exemplar's speaker names that are not roles.
func (ex Exemplar) SpeakerNames() []string {
	var out []string
	for _, u := range ex.Discussion.Utterances {
		if !slices.Contains(Roles, u.Speaker) && !slices.Contains(out, u.Speaker) {
			out = append(out, u.Speaker)
		}
	}
	return out
}

// Names are all reserved names of the exemplar: speakers (not roles) and places / institutions.
func (ex Exemplar) Names() []string {
	return append(ex.SpeakerNames(), ex.ReservedNames...)
}

// Text is the exemplar's discussion text (utterances only, no speakers).
func (ex Exemplar) Text() string { return utteranceText(ex.Discussion) }

func utteranceText(d Discussion) string {
	var b strings.Builder
	for _, u := range d.Utterances {
		b.WriteString(u.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// Render is the prompt rendering of the exemplar: Format / Topic / Targets, then the JSON block.
func (ex Exemplar) Render() string {
	yn := map[bool]string{false: "no", true: "yes"}
	return fmt.Sprintf("Format: %s\nTopic: %s\nTargets: propositions %d, RA %d, CA %d, MA %d, isolated %d, multi-premise %s, chain %s\n```json\n%s\n```\n",
		ex.Format, ex.Topic, ex.Props, ex.RA, ex.CA, ex.MA, ex.Isolated, yn[ex.MultiPremise], yn[ex.Chain], ex.JSON)
}

// RenderExemplarsInto fills the {{exemplars}} placeholder of the v2 source prompt. With no exemplars the
// whole "About the examples …" section is removed (the homogeneity monitor's no-exemplar arm).
func RenderExemplarsInto(body string, exs []Exemplar) string {
	if len(exs) == 0 {
		if i := strings.Index(body, "About the examples"); i >= 0 {
			return strings.TrimRight(body[:i], "\n") + "\n"
		}
		return strings.ReplaceAll(body, "{{exemplars}}", "")
	}
	var b strings.Builder
	for i, ex := range exs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "Example %d\n%s", i+1, ex.Render())
	}
	return strings.ReplaceAll(body, "{{exemplars}}", strings.TrimRight(b.String(), "\n"))
}

// FindExemplar looks an exemplar up by id.
func FindExemplar(exs []Exemplar, id string) (Exemplar, bool) {
	for _, ex := range exs {
		if ex.ID == id {
			return ex, true
		}
	}
	return Exemplar{}, false
}

// ExemplarPair is the two exemplars of one source prompt.
type ExemplarPair struct {
	// ID is the unordered pair id 1…C(n,2) over the exemplar bank sorted by id (1–21 for seven exemplars).
	ID int `json:"id"`
	// Order is the two exemplar ids in the order shown in the prompt.
	Order []string `json:"order"`
}

// PairID is the unordered pair id (1-based) of exemplars a and b within ids (sorted bank ids).
func PairID(ids []string, a, b string) int {
	i, j := slices.Index(ids, a), slices.Index(ids, b)
	if i < 0 || j < 0 || i == j {
		return 0
	}
	if i > j {
		i, j = j, i
	}
	n := len(ids)
	// Pairs enumerated (0,1), (0,2), …, (0,n-1), (1,2), …
	return i*n - i*(i+1)/2 + (j - i - 1) + 1
}

// ExemplarIDs returns the ids of the bank in sorted order.
func ExemplarIDs(exs []Exemplar) []string {
	ids := make([]string, len(exs))
	for i, ex := range exs {
		ids[i] = ex.ID
	}
	slices.Sort(ids)
	return ids
}
