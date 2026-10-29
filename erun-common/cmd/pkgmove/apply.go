package main

import (
	"errors"
	"fmt"
	"go/ast"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

type edit struct {
	start, end int
	text       string
}

// fragment is the part of one source file that ends up in one package.
type fragment struct {
	dir     string
	ranges  [][2]int
	primary bool
	dup     bool
	needs   map[string]bool
	edits   []edit
	// resident marks an unchanged file moved code is appended to; it keeps all its imports.
	resident bool
	// out interleaves source ranges with glue that reopens a split group.
	out []piece
}

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
	suites, err := writeSuiteMains(w.rootDir, opts.timingEnv)
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

// pkgName is the name a directory's files declare, or its base name for a new package.
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
	// One fragment per package, so a moved declaration and a copied helper never
	// overwrite each other.
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
	if len(p.order) == 0 && allDropped(entries) {
		return nil
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

func allDropped(entries []declTarget) bool {
	for _, e := range entries {
		if !e.drop {
			return false
		}
	}
	return len(entries) > 0
}

func (p *fragmentPlan) place(e declTarget, rg [2]int) {
	if !e.drop {
		p.get(e.dir, false).addRange(rg)
	}
	for _, dir := range sortedKeys(e.dups) {
		p.get(dir, true).addRange(rg)
	}
}

// placeGroup splits a grouped declaration: its first spec's package keeps the
// opening and doc comment, others reopen the group.
func (p *fragmentPlan) placeGroup(sf *sourceFile, specs []declTarget, prev int) int {
	group := genDeclAt(sf, specs[0].group)
	end := sf.fset.Position(group.End()).Offset
	dirs := groupDirs(specs)
	if len(dirs) == 1 && !anyDropped(specs) {
		p.get(dirs[0], false).addRange([2]int{prev, end})
		return end
	}
	for _, dir := range dirs {
		f := p.get(dir, !specsIn(specs, dir))
		if dir == specs[0].dir && !specs[0].drop {
			f.addRange([2]int{prev, specs[0].start})
		} else {
			f.addLit("\n\n" + specs[0].tok + " (")
		}
		for _, spec := range specs {
			if (spec.dir == dir && !spec.drop) || spec.dups[dir] {
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

func anyDropped(specs []declTarget) bool {
	for _, spec := range specs {
		if spec.drop {
			return true
		}
	}
	return false
}

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
		if !spec.drop {
			add(spec.dir)
		}
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

func (r *rewriter) bodyStart(sf *sourceFile) int {
	end := sf.fset.Position(sf.ast.Name.End()).Offset
	for _, decl := range sf.ast.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok.String() == "import" {
			end = sf.fset.Position(g.End()).Offset
		}
	}
	return end
}

// fragmentsAt includes every copy of a duplicated test helper.
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

// qualifier is the package's name unless the file already uses that name.
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

// renameDoc renames the identifier a doc comment leads with.
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
