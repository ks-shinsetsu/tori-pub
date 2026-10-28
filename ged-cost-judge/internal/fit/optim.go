package fit

import (
	"math"
)

// boxOptions configures the projected L-BFGS solver.
type boxOptions struct {
	Memory   int     // number of (s, y) pairs kept (0 → 10)
	MaxIter  int     // iteration cap (0 → 5000)
	GradTol  float64 // stop when ‖projected gradient‖_∞ < GradTol (0 → 1e-6)
	FuncTol  float64 // stop when the relative objective change is < FuncTol on two consecutive iterations (0 → 1e-10)
	MaxEvals int     // objective evaluation cap (0 → 20000)
}

// boxResult is the output of projLBFGS.
type boxResult struct {
	X         []float64
	F         float64
	Iter      int
	Evals     int
	Converged bool
}

func (o boxOptions) withDefaults() boxOptions {
	if o.Memory <= 0 {
		o.Memory = 10
	}
	if o.MaxIter <= 0 {
		o.MaxIter = 5000
	}
	if o.GradTol <= 0 {
		o.GradTol = 1e-6
	}
	if o.FuncTol <= 0 {
		o.FuncTol = 1e-10
	}
	if o.MaxEvals <= 0 {
		o.MaxEvals = 20000
	}
	return o
}

func clampTo(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func project(x, lo, hi []float64) {
	for i := range x {
		x[i] = clampTo(x[i], lo[i], hi[i])
	}
}

// projLBFGS minimises f over the box lo ≤ x ≤ hi with a limited-memory quasi-Newton method and
// projected backtracking line searches (decisions §11.1, review B1: the constraint θ ≥ 0 is imposed
// directly, so a coordinate that reaches the bound keeps a non-vanishing gradient and can leave it again).
//
// At each iteration the active set is the coordinates at a bound whose gradient points outward; their
// gradient components are masked, the L-BFGS two-loop recursion gives a direction on the free coordinates,
// and the step x(α) = P(x + α d) is accepted under the Armijo condition f(x(α)) ≤ f(x) + c₁ g·(x(α) − x).
// If the quasi-Newton direction fails the line search the memory is reset and a projected gradient step is tried.
// f writes the gradient into g when g is non-nil and returns the objective. lo[i] = hi[i] fixes a coordinate.
func projLBFGS(f func(x, g []float64) float64, x0, lo, hi []float64, opt boxOptions) boxResult {
	opt = opt.withDefaults()
	n := len(x0)
	x := make([]float64, n)
	copy(x, x0)
	project(x, lo, hi)
	g := make([]float64, n)
	evals := 0
	eval := func(x, g []float64) float64 {
		evals++
		return f(x, g)
	}
	fx := eval(x, g)
	if math.IsNaN(fx) || math.IsInf(fx, 0) {
		return boxResult{X: x, F: fx, Evals: evals}
	}

	type pair struct{ s, y []float64 }
	var mem []pair
	free := make([]bool, n)
	d := make([]float64, n)
	gm := make([]float64, n)
	xNew := make([]float64, n)
	gNew := make([]float64, n)
	alphas := make([]float64, opt.Memory)
	stall := 0
	res := boxResult{X: x, F: fx}

	for it := 1; it <= opt.MaxIter; it++ {
		// Projected gradient and active set.
		pgMax := 0.0
		for i := range n {
			if lo[i] == hi[i] {
				free[i] = false
				continue
			}
			pg := x[i] - clampTo(x[i]-g[i], lo[i], hi[i])
			pgMax = math.Max(pgMax, math.Abs(pg))
			free[i] = !((x[i] <= lo[i] && g[i] > 0) || (x[i] >= hi[i] && g[i] < 0))
		}
		if pgMax < opt.GradTol {
			res.Converged = true
			break
		}
		for i := range n {
			if free[i] {
				gm[i] = g[i]
			} else {
				gm[i] = 0
			}
		}

		// Two-loop recursion on the masked gradient.
		copy(d, gm)
		for k := len(mem) - 1; k >= 0; k-- {
			p := mem[k]
			rho := 1 / dot(p.y, p.s)
			alphas[k] = rho * dot(p.s, d)
			axpy(-alphas[k], p.y, d)
		}
		if len(mem) > 0 {
			p := mem[len(mem)-1]
			gammaK := dot(p.s, p.y) / dot(p.y, p.y)
			scale(gammaK, d)
		} else {
			gn := norm(gm)
			if gn > 0 {
				scale(math.Min(1, 1/gn), d)
			}
		}
		for k := range mem {
			p := mem[k]
			rho := 1 / dot(p.y, p.s)
			b := rho * dot(p.y, d)
			axpy(alphas[k]-b, p.s, d)
		}
		for i := range n {
			if free[i] {
				d[i] = -d[i]
			} else {
				d[i] = 0
			}
		}
		if dot(d, gm) >= 0 {
			mem = mem[:0]
			for i := range n {
				d[i] = -gm[i]
			}
			if gn := norm(gm); gn > 0 {
				scale(math.Min(1, 1/gn), d)
			}
		}

		// Projected backtracking line search.
		accepted := false
		var fNew float64
		for attempt := 0; attempt < 2 && !accepted; attempt++ {
			alpha := 1.0
			for ls := 0; ls < 40; ls++ {
				for i := range n {
					xNew[i] = clampTo(x[i]+alpha*d[i], lo[i], hi[i])
				}
				fNew = eval(xNew, gNew)
				dec := 0.0
				for i := range n {
					dec += g[i] * (xNew[i] - x[i])
				}
				if !math.IsNaN(fNew) && fNew <= fx+1e-4*dec {
					accepted = true
					break
				}
				if evals >= opt.MaxEvals {
					break
				}
				alpha *= 0.5
			}
			if !accepted && attempt == 0 && len(mem) > 0 {
				mem = mem[:0]
				for i := range n {
					d[i] = -gm[i]
				}
				if gn := norm(gm); gn > 0 {
					scale(math.Min(1, 1/gn), d)
				}
			}
		}
		if !accepted {
			break
		}

		// Curvature pair and update.
		s := make([]float64, n)
		y := make([]float64, n)
		for i := range n {
			s[i] = xNew[i] - x[i]
			y[i] = gNew[i] - g[i]
		}
		if sy := dot(s, y); sy > 1e-12*norm(s)*norm(y) && sy > 0 {
			mem = append(mem, pair{s, y})
			if len(mem) > opt.Memory {
				mem = mem[1:]
			}
		}
		rel := math.Abs(fx-fNew) / math.Max(1, math.Abs(fx))
		copy(x, xNew)
		copy(g, gNew)
		fx = fNew
		res.Iter = it
		if rel < opt.FuncTol {
			stall++
			if stall >= 2 {
				res.Converged = true
				break
			}
		} else {
			stall = 0
		}
		if evals >= opt.MaxEvals {
			break
		}
	}
	res.X, res.F, res.Evals = x, fx, evals
	return res
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func axpy(a float64, x, y []float64) {
	for i := range x {
		y[i] += a * x[i]
	}
}

func scale(a float64, x []float64) {
	for i := range x {
		x[i] *= a
	}
}

func norm(x []float64) float64 { return math.Sqrt(dot(x, x)) }
