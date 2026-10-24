package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

type edge struct {
	from, to string
	refs     map[string]int
}

type graph struct {
	edges map[[2]string]*edge
	nodes map[string]bool
}

func (w *world) graph(d decisions) graph {
	g := graph{edges: map[[2]string]*edge{}, nodes: map[string]bool{}}
	for _, dir := range d.dir {
		if dir != "" {
			g.nodes[dir] = true
		}
	}
	for _, s := range w.sites {
		o := w.objects[s.objKey]
		if s.def || o.member {
			continue
		}
		for _, from := range w.siteDirs(s, d) {
			to := w.visibleDir(s.objKey, from, d)
			if strings.HasPrefix(from, "ext:") || from == to || to == "" {
				continue
			}
			g.add(from, to, w.files[s.file].rel+" -> "+w.describeKey(s.objKey))
		}
	}
	return g
}

// cycles returns the import cycles, largest first.
func (g graph) cycles() [][]string {
	adj := map[string][]string{}
	for k := range g.edges {
		adj[k[0]] = append(adj[k[0]], k[1])
	}
	t := tarjan{adj: adj, index: map[string]int{}, low: map[string]int{}, onStack: map[string]bool{}}
	for _, n := range sortedKeys(g.nodes) {
		if _, seen := t.index[n]; !seen {
			t.visit(n)
		}
	}
	var out [][]string
	for _, c := range t.sccs {
		if len(c) > 1 {
			sort.Strings(c)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

type tarjan struct {
	adj     map[string][]string
	index   map[string]int
	low     map[string]int
	onStack map[string]bool
	stack   []string
	next    int
	sccs    [][]string
}

func (t *tarjan) visit(n string) {
	t.index[n], t.low[n] = t.next, t.next
	t.next++
	t.stack = append(t.stack, n)
	t.onStack[n] = true
	for _, m := range t.adj[n] {
		if _, seen := t.index[m]; !seen {
			t.visit(m)
			t.low[n] = min(t.low[n], t.low[m])
		} else if t.onStack[m] {
			t.low[n] = min(t.low[n], t.index[m])
		}
	}
	if t.low[n] != t.index[n] {
		return
	}
	var c []string
	for {
		m := t.stack[len(t.stack)-1]
		t.stack = t.stack[:len(t.stack)-1]
		t.onStack[m] = false
		c = append(c, m)
		if m == n {
			break
		}
	}
	t.sccs = append(t.sccs, c)
}

// report prints the graph, cycles and collisions, and returns whether the mapping is sound.
func (g graph) report(out io.Writer, d decisions, limit int) bool {
	keys := make([][2]string, 0, len(g.edges))
	for k := range g.edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	_, _ = fmt.Fprintln(out, "edges:")
	for _, k := range keys {
		_, _ = fmt.Fprintf(out, "  %s -> %s (%d refs)\n", k[0], k[1], total(g.edges[k].refs))
	}
	cycles := g.cycles()
	for _, c := range cycles {
		_, _ = fmt.Fprintf(out, "cycle: %s\n", strings.Join(c, ", "))
		g.printCycleEdges(out, c, limit)
	}
	for _, e := range d.errs {
		_, _ = fmt.Fprintln(out, "collision:", e)
	}
	_, _ = fmt.Fprintf(out, "renamed: %d identifiers\n", len(d.newName))
	g.reportExportedFields(out, d)
	return len(cycles) == 0 && len(d.errs) == 0
}

func (g graph) printCycleEdges(out io.Writer, cycle []string, limit int) {
	in := map[string]bool{}
	for _, n := range cycle {
		in[n] = true
	}
	for _, k := range sortedEdgeKeys(g.edges) {
		if !in[k[0]] || !in[k[1]] {
			continue
		}
		e := g.edges[k]
		_, _ = fmt.Fprintf(out, "  %s -> %s:\n", k[0], k[1])
		refs := sortedKeys(e.refs)
		for i, r := range refs {
			if i == limit {
				_, _ = fmt.Fprintf(out, "    ... %d more\n", len(refs)-limit)
				break
			}
			_, _ = fmt.Fprintf(out, "    %s (%d)\n", r, e.refs[r])
		}
	}
}

func sortedEdgeKeys(edges map[[2]string]*edge) [][2]string {
	keys := make([][2]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+" "+keys[i][1] < keys[j][0]+" "+keys[j][1] })
	return keys
}

func total(refs map[string]int) int {
	n := 0
	for _, c := range refs {
		n += c
	}
	return n
}

func (g graph) add(from, to, ref string) {
	k := [2]string{from, to}
	e := g.edges[k]
	if e == nil {
		e = &edge{from: from, to: to, refs: map[string]int{}}
		g.edges[k] = e
	}
	e.refs[ref]++
}

// reportExportedFields lists newly exported fields, which encoding/json and
// yaml now see.
func (g graph) reportExportedFields(out io.Writer, d decisions) {
	for _, line := range d.exportedFields {
		_, _ = fmt.Fprintln(out, "exported field:", line)
	}
}
