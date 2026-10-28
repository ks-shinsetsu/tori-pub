package synth

import (
	"math/rand/v2"
	"slices"
)

// NamePool is the speaker-name pool (dataset-spec §1.3: ≥ 120 names so that no name is used in more than
// 3% of 750 sources). Given names from many regions; none of the exemplar speaker names is included
// (Dev, Marianne, Tom, Bea, Callum, Hannah, Oskar, Adaeze, Ingrid, Tomasz, Kwame, Ruth, Mireille, …).
var NamePool = []string{
	"Abena", "Adrian", "Agnieszka", "Aiko", "Alejandro", "Alina", "Amara", "Amir", "Anders", "Anika",
	"Annelise", "Arjun", "Astrid", "Aurelio", "Ayesha", "Babajide", "Beatriz", "Bilal", "Birgit", "Bongani",
	"Bruno", "Camille", "Carmen", "Chidi", "Chloe", "Cillian", "Constance", "Dalia", "Damien", "Daniela",
	"Darius", "Deirdre", "Dimitri", "Dolores", "Eamon", "Efua", "Elif", "Elodie", "Emeka", "Enzo",
	"Esther", "Fatima", "Felix", "Femi", "Fiona", "Florin", "Gabriela", "Gareth", "Giulia", "Gwen",
	"Hamza", "Harriet", "Hector", "Helga", "Hiroshi", "Ifeoma", "Imogen", "Isaac", "Ivana", "Jasper",
	"Javier", "Jelena", "Jin", "Joaquin", "Jolene", "Josefa", "Kai", "Karima", "Keiko", "Kieran",
	"Laleh", "Lars", "Leilani", "Lorenzo", "Lucia", "Magnus", "Malik", "Margit", "Marisol", "Matteo",
	"Meera", "Milos", "Nadia", "Naveen", "Nkechi", "Noor", "Odette", "Olumide", "Orla", "Pablo",
	"Padraig", "Petra", "Pradeep", "Priya", "Quentin", "Rafael", "Rania", "Rashid", "Renata", "Rohan",
	"Rosalind", "Saanvi", "Samir", "Sanna", "Seamus", "Selin", "Siobhan", "Sipho", "Soren", "Sunita",
	"Tariq", "Thandiwe", "Theo", "Tobias", "Ulla", "Valentina", "Vikram", "Wanjiru", "Wilhelmina", "Xavier",
	"Yara", "Yusuf", "Zainab", "Zofia", "Bernard", "Clement", "Gideon", "Hollis", "Leopold", "Maeve",
}

// NameAllocator draws speaker names so that usage stays balanced (least-used first, random tie-break).
type NameAllocator struct {
	pool []string
	uses map[string]int
	rng  *rand.Rand
}

// NewNameAllocator returns an allocator over pool minus the excluded names (e.g. exemplar names).
func NewNameAllocator(pool, exclude []string, rng *rand.Rand) *NameAllocator {
	var p []string
	for _, n := range pool {
		if !slices.Contains(exclude, n) && !slices.Contains(p, n) {
			p = append(p, n)
		}
	}
	return &NameAllocator{pool: p, uses: map[string]int{}, rng: rng}
}

// Draw returns k distinct names among the least used ones.
func (a *NameAllocator) Draw(k int) []string {
	order := slices.Clone(a.pool)
	a.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	slices.SortStableFunc(order, func(x, y string) int { return a.uses[x] - a.uses[y] })
	k = min(k, len(order))
	out := slices.Clone(order[:k])
	for _, n := range out {
		a.uses[n]++
	}
	return out
}

// Uses returns the number of sources each name was drawn for.
func (a *NameAllocator) Uses() map[string]int { return a.uses }
