package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type part struct {
	sf *sourceFile
	f  *fragment
}

func (r *rewriter) write() (int, error) {
	byTarget, removed, err := r.collectParts()
	if err != nil {
		return 0, err
	}
	written := 0
	for _, target := range sortedKeys(byTarget) {
		parts, err := r.withResident(target, byTarget[target])
		if err != nil {
			return written, err
		}
		if err := r.writeParts(target, parts); err != nil {
			return written, err
		}
		written++
	}
	for _, name := range removed {
		if _, rewritten := byTarget[name]; !rewritten {
			if err := os.Remove(name); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (r *rewriter) collectParts() (map[string][]part, []string, error) {
	byTarget := map[string][]part{}
	var removed []string
	for _, name := range sortedKeys(r.frags) {
		sf := r.w.files[name]
		frags := r.frags[name]
		if !r.changed(sf, frags) {
			continue
		}
		if usesCgo(sf) {
			return nil, nil, fmt.Errorf("%s uses cgo, whose import preamble a rewrite would drop; move its references by hand", sf.path)
		}
		for _, f := range frags {
			target := r.targetPath(sf, f)
			byTarget[target] = append(byTarget[target], part{sf: sf, f: f})
		}
		if sf.inModule && !r.keepsOriginal(sf, frags) {
			removed = append(removed, sf.path)
		}
	}
	return byTarget, removed, nil
}

// withResident puts the file already at target first, so moved code is appended, never overwrites it.
func (r *rewriter) withResident(target string, parts []part) ([]part, error) {
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].sf.path == target && parts[j].sf.path != target })
	if parts[0].sf.path == target {
		return parts, nil
	}
	resident, ok := r.w.files[target]
	if !ok {
		if _, err := os.Stat(target); err == nil {
			return nil, fmt.Errorf("%s already exists and no load saw it; add a rename to the map", target)
		}
		return parts, nil
	}
	whole := &fragment{dir: path.Dir(resident.rel), primary: true, resident: true, needs: map[string]bool{}}
	whole.addRange([2]int{r.bodyStart(resident), len(resident.src)})
	return append([]part{{sf: resident, f: whole}}, parts...), nil
}

func (r *rewriter) writeParts(target string, parts []part) error {
	first := parts[0]
	for _, p := range parts[1:] {
		if buildConstraint(p.sf) != buildConstraint(first.sf) {
			return fmt.Errorf("%s and %s carry different build constraints and cannot share %s; add a rename to the map", first.sf.path, p.sf.path, target)
		}
	}
	var std, other []string
	var body strings.Builder
	for _, p := range parts {
		s, o := r.importLines(p.sf, p.f)
		std, other = append(std, s...), append(other, o...)
		for _, pc := range p.f.out {
			if pc.raw {
				body.WriteString(pc.lit)
				continue
			}
			body.WriteString(applyEdits(p.sf, p.f, pc.rg))
		}
	}
	text := r.header(first.sf, first.f) + "package " + r.packageClause(first.sf, first.f) +
		renderImports(dedupe(std), dedupe(other)) + body.String()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	src, err := format.Source([]byte(text))
	if err != nil {
		return fmt.Errorf("format %s: %w", target, err)
	}
	return os.WriteFile(target, src, 0o644)
}

// buildConstraint is a file's //go:build line, which every part sharing a
// file has to agree on.
func buildConstraint(sf *sourceFile) string {
	header := string(sf.src[:sf.fset.Position(sf.ast.Package).Offset])
	for _, line := range strings.Split(header, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//go:build") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func dedupe(lines []string) []string {
	seen := map[string]bool{}
	out := lines[:0]
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func (r *rewriter) changed(sf *sourceFile, frags []*fragment) bool {
	if len(frags) != 1 || len(frags[0].edits) > 0 {
		return true
	}
	return sf.inModule && frags[0].dir != path.Dir(sf.rel)
}

func (r *rewriter) keepsOriginal(sf *sourceFile, frags []*fragment) bool {
	for _, f := range frags {
		if r.targetPath(sf, f) == sf.path {
			return true
		}
	}
	return false
}

func (r *rewriter) targetPath(sf *sourceFile, f *fragment) string {
	if !sf.inModule {
		return sf.path
	}
	return filepath.Join(r.w.rootDir, filepath.FromSlash(f.dir), filepath.Base(sf.path))
}

func (r *rewriter) packageClause(sf *sourceFile, f *fragment) string {
	if !sf.inModule || f.dir == path.Dir(sf.rel) {
		return sf.ast.Name.Name
	}
	return r.pkgName(f.dir)
}

// header is everything before the package clause; only the primary fragment keeps it.
func (r *rewriter) header(sf *sourceFile, f *fragment) string {
	pkgPos := sf.fset.Position(sf.ast.Package).Offset
	if f.primary || sf.ast.Doc == nil {
		return string(sf.src[:pkgPos])
	}
	docStart := sf.fset.Position(sf.ast.Doc.Pos()).Offset
	docEnd := sf.fset.Position(sf.ast.Doc.End()).Offset
	return string(sf.src[:docStart]) + strings.TrimLeft(string(sf.src[docEnd:pkgPos]), "\n")
}

func applyEdits(sf *sourceFile, f *fragment, rg [2]int) string {
	var in []edit
	for _, e := range f.edits {
		if e.start >= rg[0] && e.end <= rg[1] {
			in = append(in, e)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].start < in[j].start })
	var b strings.Builder
	at := rg[0]
	for _, e := range in {
		if e.start < at {
			continue
		}
		b.Write(sf.src[at:e.start])
		b.WriteString(e.text)
		at = e.end
	}
	b.Write(sf.src[at:rg[1]])
	return b.String()
}

// importLines are the original imports the fragment still names, plus the
// module packages it now refers to.
func (r *rewriter) importLines(sf *sourceFile, f *fragment) (std, other []string) {
	add := func(line, p string) {
		if strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
			other = append(other, line)
		} else {
			std = append(std, line)
		}
	}
	for _, spec := range sf.ast.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		if f.resident {
			add(r.specText(sf, spec), p)
			continue
		}
		if r.w.inModulePath(p) {
			continue
		}
		if r.keepImport(sf, f, spec, p) {
			add(r.specText(sf, spec), p)
		}
	}
	for _, dir := range sortedKeys(f.needs) {
		q := r.qualifier(sf.path, f, dir)
		line := strconv.Quote(r.importPath(dir))
		if q != r.pkgName(dir) {
			line = q + " " + line
		}
		add(line, r.importPath(dir))
	}
	return std, other
}

func (r *rewriter) specText(sf *sourceFile, spec *ast.ImportSpec) string {
	start := sf.fset.Position(spec.Pos()).Offset
	end := sf.fset.Position(spec.End()).Offset
	return string(sf.src[start:end])
}

// keepImport keeps blank and dot imports with the primary fragment, and named
// imports where their name is used.
func (r *rewriter) keepImport(sf *sourceFile, f *fragment, spec *ast.ImportSpec, p string) bool {
	if spec.Name != nil && (spec.Name.Name == "_" || spec.Name.Name == ".") {
		return f.primary
	}
	if len(r.frags[sf.path]) == 1 {
		return true
	}
	local, ok := sf.importNames[p]
	if !ok {
		local = path.Base(p)
	}
	return r.fragmentNames(sf, f, local)
}

func (r *rewriter) fragmentNames(sf *sourceFile, f *fragment, local string) bool {
	found := false
	ast.Inspect(sf.ast, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || found {
			return !found
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == local {
			off := sf.fset.Position(x.Pos()).Offset
			if !sf.pkgRefs[off] {
				return true
			}
			for _, rg := range f.ranges {
				if off >= rg[0] && off < rg[1] {
					found = true
				}
			}
		}
		return true
	})
	return found
}

func renderImports(std, other []string) string {
	if len(std)+len(other) == 0 {
		return ""
	}
	sort.Strings(std)
	sort.Strings(other)
	var b strings.Builder
	b.WriteString("\n\nimport (\n")
	for _, l := range std {
		b.WriteString("\t" + l + "\n")
	}
	if len(std) > 0 && len(other) > 0 {
		b.WriteString("\n")
	}
	for _, l := range other {
		b.WriteString("\t" + l + "\n")
	}
	b.WriteString(")")
	return b.String()
}

func usesCgo(sf *sourceFile) bool {
	for _, spec := range sf.ast.Imports {
		if spec.Path.Value == `"C"` {
			return true
		}
	}
	return false
}

func importedAs(sf *sourceFile, importPath string) (string, bool) {
	for _, spec := range sf.ast.Imports {
		if p, _ := strconv.Unquote(spec.Path.Value); p == importPath && spec.Name != nil {
			return spec.Name.Name, true
		}
	}
	return "", false
}
