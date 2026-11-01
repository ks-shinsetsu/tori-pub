package ged

import (
	"errors"
	"math"
)

// bipartiteMatch finds an optimal node assignment with the bipartite approximation of Riesen & Bunke (2009).
//
// It builds an (n+m)×(n+m) cost matrix:
//
//	top-left     n×m  substitution costs sub[i][j]
//	top-right    n×n  deletion costs del[i] on the diagonal, forbidden elsewhere
//	bottom-left  m×m  insertion costs ins[j] on the diagonal, forbidden elsewhere
//	bottom-right m×n  dummy to dummy (0)
//
// It returns the (i → j) pairs assigned as substitutions. Deleted and inserted nodes are not included.
// The same matrix used to be solved by a Python (scipy) service. The solver matches scipy's
// (see linearSumAssignment), so for the matrix it is given (the snapped one, below) the same assignment is
// returned, including how ties are broken.
//
// The costs are rounded to the grid 2^−costBits before solving. Equal costs (two propositions below the last
// substitution knot, say) are then equal in every sum and difference the solver forms, so a tie between two
// assignments is broken by the solver's fixed rule and not by rounding noise that moves with θ (decisions §11.5:
// the matching must be a deterministic, piecewise-constant function of θ; the estimation loop cannot converge
// if it is not). That rule visits columns in an order that depends on the other entries, so an exact tie can still
// be resolved differently at two θ; Compute therefore separates ties by cosine (tieEps1, tieEps2) and only entries
// with identical cos remain with the solver's rule. The features returned by Compute are those of the chosen
// assignment, so the GED value itself is not rounded; only a genuine difference below the grid can change which of
// two near-equal assignments is taken.
// The forbidden sentinel 1e9 lies outside the exact range (see costBits), so reduced costs that involve it are
// inexact; that is harmless because a forbidden entry is never selected on a feasible instance (the feasible
// entries always contain a perfect matching, so Dijkstra reaches a free column through them first) and the dual
// updates only touch selected rows and columns.
func bipartiteMatch(sub [][]float64, del, ins []float64) (map[int]int, error) {
	n, m := len(del), len(ins)
	if n == 0 && m == 0 {
		return map[int]int{}, nil
	}
	const forbidden = 1e9
	size := n + m
	cost := make([][]float64, size)
	for i := range cost {
		row := make([]float64, size)
		for j := range row {
			row[j] = forbidden
		}
		cost[i] = row
	}
	for i := range n {
		for j := range m {
			cost[i][j] = snap(sub[i][j])
		}
		cost[i][m+i] = snap(del[i])
	}
	for j := range m {
		cost[n+j][j] = snap(ins[j])
		for k := m; k < size; k++ {
			cost[n+j][k] = 0
		}
	}
	col4row, err := linearSumAssignment(cost)
	if err != nil {
		return nil, err
	}
	out := make(map[int]int, min(n, m))
	for i := range n {
		if j := col4row[i]; j < m {
			out[i] = j
		}
	}
	return out, nil
}

// costBits is the number of fractional bits of the assignment cost grid. A sum or difference of two grid values is
// exact in a float64 while both are below 2^12 in magnitude (40 + 12 + 1 ≤ 53 bits). A cost is at most
// 4·ThetaMax plus the tie key (≈ 40) and the duals are sums of at most n + m costs, far below 2^12 at this
// experiment's sizes (≤ 18 propositions per graph; 55 was the largest magnitude seen in 3,000 random instances).
// The 1e9 sentinel of forbidden entries is not snapped; reduced costs involving it are inexact but never selected.
// The grid is fine enough for the tie-breaking keys of Compute (see tieEps1) to resolve cos differences of 2^−18.
const costBits = 40

// snap rounds c to the nearest multiple of 2^−costBits.
func snap(c float64) float64 {
	const scale = 1 << costBits
	return math.Round(c*scale) / scale
}

// linearSumAssignment solves the minimum-cost perfect assignment of a square cost matrix and returns the column assigned to each row i.
//
// It is a port of scipy.optimize.linear_sum_assignment (rectangular_lsap.cpp), the
// shortest augmenting path method of Crouse (2016). Tie-breaking (ordering remaining columns in reverse,
// preferring unassigned columns) matches scipy too. Floating-point operations are performed in the same order,
// so the same matrix yields the same assignment as scipy.
func linearSumAssignment(cost [][]float64) ([]int, error) {
	n := len(cost)
	for _, row := range cost {
		if len(row) != n {
			return nil, errors.New("evaluation: 割当のコスト行列が正方でない")
		}
		for _, c := range row {
			if math.IsNaN(c) || math.IsInf(c, -1) {
				return nil, errors.New("evaluation: 割当のコスト行列に NaN か -Inf がある")
			}
		}
	}
	u := make([]float64, n)
	v := make([]float64, n)
	shortest := make([]float64, n)
	path := make([]int, n)
	col4row := make([]int, n)
	row4col := make([]int, n)
	sr := make([]bool, n)
	sc := make([]bool, n)
	remaining := make([]int, n)
	for i := range n {
		path[i], col4row[i], row4col[i] = -1, -1, -1
	}

	for cur := range n {
		// ---- Find the shortest augmenting path ----
		minVal := 0.0
		for it := range n {
			remaining[it] = n - it - 1
		}
		numRemaining := n
		clear(sr)
		clear(sc)
		for j := range shortest {
			shortest[j] = math.Inf(1)
		}
		sink, i := -1, cur
		for sink == -1 {
			index, lowest := -1, math.Inf(1)
			sr[i] = true
			for it := range numRemaining {
				j := remaining[it]
				r := minVal + cost[i][j] - u[i] - v[j]
				if r < shortest[j] {
					path[j] = i
					shortest[j] = r
				}
				if shortest[j] < lowest || (shortest[j] == lowest && row4col[j] == -1) {
					lowest = shortest[j]
					index = it
				}
			}
			minVal = lowest
			if math.IsInf(minVal, 1) {
				return nil, errors.New("evaluation: 割当が存在しない")
			}
			j := remaining[index]
			if row4col[j] == -1 {
				sink = j
			} else {
				i = row4col[j]
			}
			sc[j] = true
			numRemaining--
			remaining[index] = remaining[numRemaining]
		}

		// ---- Update the dual variables ----
		u[cur] += minVal
		for i := range n {
			if sr[i] && i != cur {
				u[i] += minVal - shortest[col4row[i]]
			}
		}
		for j := range n {
			if sc[j] {
				v[j] -= minVal - shortest[j]
			}
		}

		// ---- Augment the solution along the path ----
		for j := sink; ; {
			i := path[j]
			row4col[j] = i
			col4row[i], j = j, col4row[i]
			if i == cur {
				break
			}
		}
	}
	return col4row, nil
}
