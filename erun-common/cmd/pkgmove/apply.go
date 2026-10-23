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
	_, _ = fmt.Fprintf(out, "rewrote %d files\n", written)
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
			body := [][2]int{{r.bodyStart(sf), len(sf.src)}}
			r.frags[name] = []*fragment{{dir: "ext", ranges: body, primary: true, needs: map[string]bool{}}}
			continue
		}
		r.frags[name] = r.fileFragments(sf)
	}
}

func (r *rewriter) fileFragments(sf *sourceFile) []*fragment {
	home := r.m.fileDir(sf.rel)
	byDir := map[string]*fragment{}
	dupByDir := map[string]*fragment{}
	var order, dups []*fragment
	prev := r.bodyStart(sf)
	for _, decl := range r.d.decls[sf.path] {
		if genImport(sf, decl.start) {
			continue
		}
		rg := [2]int{prev, decl.end}
		prev = decl.end
		order = addRange(byDir, order, decl.dir, rg, false)
		for _, dir := range sortedKeys(decl.dups) {
			dups = addRange(dupByDir, dups, dir, rg, true)
		}
	}
	primary := byDir[home]
	if primary == nil {
		if len(order) == 0 {
			primary = &fragment{dir: home, needs: map[string]bool{}}
			order = append(order, primary)
		} else {
			primary = order[0]
		}
	}
	primary.primary = true
	primary.ranges = append(primary.ranges, [2]int{prev, len(sf.src)})
	return append(order, dups...)
}

func addRange(byDir map[string]*fragment, order []*fragment, dir string, rg [2]int, dup bool) []*fragment {
	f := byDir[dir]
	if f == nil {
		f = &fragment{dir: dir, dup: dup, needs: map[string]bool{}}
		byDir[dir] = f
		order = append(order, f)
	}
	f.ranges = append(f.ranges, rg)
	return order
}

func genImport(sf *sourceFile, start int) bool {
	for _, decl := range sf.ast.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && sf.fset.Position(g.Pos()).Offset == start {
			return g.Tok.String() == "import"
		}
	}
	return false
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
func (r *rewriter) write() (int, error) {
	written := 0
	for _, name := range sortedKeys(r.frags) {
		sf := r.w.files[name]
		frags := r.frags[name]
		if !r.changed(sf, frags) {
			continue
		}
		if usesCgo(sf) {
			return written, fmt.Errorf("%s uses cgo, whose import preamble a rewrite would drop; move its references by hand", sf.path)
		}
		moved := false
		for _, f := range frags {
			target := r.targetPath(sf, f)
			if target != sf.path {
				moved = true
			}
			if err := r.writeFragment(sf, f, target); err != nil {
				return written, err
			}
			written++
		}
		if moved && !r.keepsOriginal(sf, frags) {
			if err := os.Remove(sf.path); err != nil {
				return written, err
			}
		}
	}
	return written, nil
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

func (r *rewriter) writeFragment(sf *sourceFile, f *fragment, target string) error {
	var b strings.Builder
	b.WriteString(r.header(sf, f))
	b.WriteString("package ")
	b.WriteString(r.packageClause(sf, f))
	b.WriteString(r.imports(sf, f))
	for _, rg := range f.ranges {
		b.WriteString(applyEdits(sf, f, rg))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(target); err == nil && target != sf.path {
		return fmt.Errorf("%s already exists (%d bytes); add a rename to the map", target, len(existing))
	}
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format %s: %w", target, err)
	}
	return os.WriteFile(target, src, 0o644)
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

// imports renders a fragment's import block: every import of the original
// file its code still names, minus module packages it no longer refers to,
// plus the module packages it refers to now.
func (r *rewriter) imports(sf *sourceFile, f *fragment) string {
	var std, other []string
	add := func(line, p string) {
		if strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
			other = append(other, line)
		} else {
			std = append(std, line)
		}
	}
	for _, spec := range sf.ast.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
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
	return renderImports(std, other)
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
