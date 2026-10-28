package main

import (
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
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
	}
}
