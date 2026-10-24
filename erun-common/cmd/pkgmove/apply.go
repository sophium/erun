package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// edit replaces src[start:end] of one file.
type edit struct {
	start, end int
	text       string
}

// fragment is the part of one source file that ends up in one package: the
// whole file when nothing in it moves apart.
type fragment struct {
	dir     string
	ranges  [][2]int
	primary bool
	dup     bool
	needs   map[string]bool
	edits   []edit
	// resident marks the whole of an unchanged file that moved code is
	// appended to: it keeps every import it had, module ones included.
	resident bool
	// out is the fragment's text in order: source ranges, with literal glue
	// where a grouped declaration is split and reopened in another package.
	out []piece
}

// piece is a source range of a fragment, or literal text between ranges.
type piece struct {
	rg  [2]int
	lit string
	raw bool
}

func (f *fragment) addRange(rg [2]int) {
	f.ranges = append(f.ranges, rg)
	f.out = append(f.out, piece{rg: rg})
}

func (f *fragment) addLit(text string) {
	f.out = append(f.out, piece{lit: text, raw: true})
}

func apply(w *world, m packageMap, opts options, out io.Writer) error {
	for _, dir := range opts.importers {
		if err := w.load(dir, []string{"./..."}); err != nil {
			return err
		}
	}
	d := w.decide(m)
	if !w.graph(d).report(out, d, opts.limit) {
		return errors.New("the map does not produce a sound package graph; run plan")
	}
	r := rewriter{w: w, d: d, m: m, frags: map[string][]*fragment{}}
	r.planFragments()
	r.planEdits()
	written, err := r.write()
	if err != nil {
		return err
	}
	suites, err := writeSuiteMains(w.rootDir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "rewrote %d files, gave %d packages a TestMain\n", written, suites)
	return nil
}

type rewriter struct {
	w     *world
	d     decisions
	m     packageMap
	frags map[string][]*fragment
}

// pkgName is the package name of a directory: the name its files already
// declare, or the directory's base name for a package the map creates.
func (r *rewriter) pkgName(dir string) string {
	for _, sf := range r.w.files {
		if sf.inModule && path.Dir(sf.rel) == dir && !strings.HasSuffix(sf.ast.Name.Name, "_test") {
			return sf.ast.Name.Name
		}
	}
	return path.Base(dir)
}

func (r *rewriter) importPath(dir string) string {
	if dir == rootDir {
		return r.w.module
	}
	return r.w.module + "/" + dir
}

// planFragments splits every module file by where its declarations go.
func (r *rewriter) planFragments() {
	for name, sf := range r.w.files {
		if !sf.inModule {
			f := &fragment{dir: "ext", primary: true, needs: map[string]bool{}}
			f.addRange([2]int{r.bodyStart(sf), len(sf.src)})
			r.frags[name] = []*fragment{f}
			continue
		}
		r.frags[name] = r.fileFragments(sf)
	}
}

func (r *rewriter) fileFragments(sf *sourceFile) []*fragment {
	home := r.m.fileDir(sf.rel)
	// One fragment per package: a declaration that moves there and a test
	// helper copied there end up in the same file, never two that overwrite
	// each other.
	p := &fragmentPlan{byDir: map[string]*fragment{}}
	prev := r.bodyStart(sf)
	entries := r.d.decls[sf.path]
	for i := 0; i < len(entries); {
		e := entries[i]
		if e.group == 0 {
			p.place(e, [2]int{prev, e.end})
			prev = e.end
			i++
			continue
		}
		j := i
		for j < len(entries) && entries[j].group == e.group {
			j++
		}
		prev = p.placeGroup(sf, entries[i:j], prev)
		i = j
	}
	primary := p.byDir[home]
	if primary == nil {
		if len(p.order) == 0 {
			primary = p.get(home, false)
		} else {
			primary = p.order[0]
		}
	}
	primary.primary = true
	primary.addRange([2]int{prev, len(sf.src)})
	return p.order
}

// fragmentPlan collects one file's fragments, one per package directory, in
// the order their first text appears.
type fragmentPlan struct {
	byDir map[string]*fragment
	order []*fragment
}

func (p *fragmentPlan) get(dir string, dup bool) *fragment {
	f := p.byDir[dir]
	if f == nil {
		f = &fragment{dir: dir, dup: dup, needs: map[string]bool{}}
		p.byDir[dir] = f
		p.order = append(p.order, f)
	}
	f.dup = f.dup && dup
	return f
}

// place puts one declaration's text in its package and in every package a
// copy of it goes to.
func (p *fragmentPlan) place(e declTarget, rg [2]int) {
	p.get(e.dir, false).addRange(rg)
	for _, dir := range sortedKeys(e.dups) {
		p.get(dir, true).addRange(rg)
	}
}

// placeGroup places a grouped declaration whose specs may go to different
// packages: the package of its first spec keeps the group's own opening and
// doc comment, every other package reopens the group around its specs. It
// returns where the group's text ends.
func (p *fragmentPlan) placeGroup(sf *sourceFile, specs []declTarget, prev int) int {
	group := genDeclAt(sf, specs[0].group)
	end := sf.fset.Position(group.End()).Offset
	dirs := groupDirs(specs)
	if len(dirs) == 1 {
		p.get(dirs[0], false).addRange([2]int{prev, end})
		return end
	}
	for _, dir := range dirs {
		f := p.get(dir, !specsIn(specs, dir))
		if dir == specs[0].dir {
			f.addRange([2]int{prev, specs[0].start})
		} else {
			f.addLit("\n\n" + specs[0].tok + " (")
		}
		for _, spec := range specs {
			if spec.dir == dir || spec.dups[dir] {
				f.addRange([2]int{spec.start, spec.end})
			}
		}
		f.addLit("\n)")
	}
	return end
}

func specsIn(specs []declTarget, dir string) bool {
	for _, spec := range specs {
		if spec.dir == dir {
			return true
		}
	}
	return false
}

// groupDirs are the packages a group's specs and their copies go to, in the
// order they first appear.
func groupDirs(specs []declTarget) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	for _, spec := range specs {
		add(spec.dir)
		for _, dir := range sortedKeys(spec.dups) {
			add(dir)
		}
	}
	return dirs
}

func genDeclAt(sf *sourceFile, offset int) *ast.GenDecl {
	for _, decl := range sf.ast.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && sf.fset.Position(g.Pos()).Offset == offset {
			return g
		}
	}
	return nil
}

// bodyStart is where a file's declarations begin: after its imports, or
// after its package clause when it has none.
func (r *rewriter) bodyStart(sf *sourceFile) int {
	end := sf.fset.Position(sf.ast.Name.End()).Offset
	for _, decl := range sf.ast.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok.String() == "import" {
			end = sf.fset.Position(g.End()).Offset
		}
	}
	return end
}

// fragmentsAt are the fragments holding offset: the code's own, and every
// copy of a duplicated test helper.
func (r *rewriter) fragmentsAt(file string, offset int) []*fragment {
	var out []*fragment
	for _, f := range r.frags[file] {
		for _, rg := range f.ranges {
			if offset >= rg[0] && offset < rg[1] {
				out = append(out, f)
			}
		}
	}
	return out
}

// planEdits turns every site into the text it must read in its new home, and
// records the module packages each fragment now imports.
func (r *rewriter) planEdits() {
	for _, s := range r.w.sites {
		o := r.w.objects[s.objKey]
		name := o.name
		if n, ok := r.d.newName[s.objKey]; ok {
			name = n
		}
		for _, frag := range r.fragmentsAt(s.file, s.start) {
			r.planSite(s, o, name, frag)
		}
	}
}

func (r *rewriter) planSite(s site, o *object, name string, frag *fragment) {
	if o.member || s.def {
		if name != o.name {
			frag.edits = append(frag.edits, edit{start: s.end - len(o.name), end: s.end, text: name})
			if s.def {
				r.renameDoc(s, o, name, frag)
			}
		}
		return
	}
	to := r.w.visibleDir(s.objKey, frag.dir, r.d)
	text := name
	if to != frag.dir {
		frag.needs[to] = true
		text = r.qualifier(s.file, frag, to) + "." + name
	}
	if string(r.w.files[s.file].src[s.start:s.end]) != text {
		frag.edits = append(frag.edits, edit{start: s.start, end: s.end, text: text})
	}
}

// qualifier is the name a fragment refers to a module package by: the
// package's own name, unless something in the file already uses that name.
func (r *rewriter) qualifier(file string, frag *fragment, dir string) string {
	name := r.pkgName(dir)
	sf := r.w.files[file]
	if existing, ok := importedAs(sf, r.importPath(dir)); ok {
		return existing
	}
	if sf.names[name] || r.declaresIn(frag.dir, name) || r.otherImport(sf, name) {
		return name + "pkg"
	}
	return name
}

func (r *rewriter) declaresIn(dir, name string) bool {
	for key, o := range r.w.objects {
		if o.member || r.d.dir[key] != dir {
			continue
		}
		n := o.name
		if renamed, ok := r.d.newName[key]; ok {
			n = renamed
		}
		if n == name {
			return true
		}
	}
	return false
}

func (r *rewriter) otherImport(sf *sourceFile, name string) bool {
	for _, spec := range sf.ast.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		if r.w.inModulePath(p) {
			continue
		}
		local, ok := sf.importNames[p]
		if !ok {
			local = path.Base(p)
		}
		if local == name {
			return true
		}
	}
	return false
}

// write renders every changed file into its new place and removes the
// originals that moved away.
// part is one fragment bound for a file.
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

// collectParts groups every changed fragment by the file it is written to, and
// lists the source files no fragment stays in.
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

// withResident orders the parts bound for one file, the file already living
// at that path first: declarations moved into a file of a package that
// already has one are appended to it, never written over it.
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

// writeParts renders the parts bound for one file into it: the first part's
// header and package clause, every part's imports, every part's text.
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
	if len(frags) > 1 || len(frags[0].edits) > 0 {
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

// header is everything before the package clause: build constraints and the
// package's doc comment, which only the primary fragment keeps.
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

// importLines are a fragment's imports: every import of the original file its
// code still names, minus module packages it no longer refers to, plus the
// module packages it refers to now.
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

// keepImport says whether a fragment still names an import. Blank and dot
// imports stay with the primary fragment; a named import stays where its
// name is used.
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

// importedAs is the name a file already imports a package by, which a
// rewrite keeps.
func importedAs(sf *sourceFile, importPath string) (string, bool) {
	for _, spec := range sf.ast.Imports {
		if p, _ := strconv.Unquote(spec.Path.Value); p == importPath && spec.Name != nil {
			return spec.Name.Name, true
		}
	}
	return "", false
}

// renameDoc renames the identifier a renamed declaration's doc comment leads
// with, so the comment keeps describing the declaration by its name.
func (r *rewriter) renameDoc(s site, o *object, name string, frag *fragment) {
	sf := r.w.files[s.file]
	doc, ok := sf.docs[s.start]
	if !ok {
		return
	}
	lead := "// " + o.name
	text := string(sf.src[doc:min(len(sf.src), doc+len(lead)+1)])
	if !strings.HasPrefix(text, lead) || len(text) <= len(lead) || isIdentRune(rune(text[len(lead)])) {
		return
	}
	frag.edits = append(frag.edits, edit{start: doc + 3, end: doc + 3 + len(o.name), text: name})
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
