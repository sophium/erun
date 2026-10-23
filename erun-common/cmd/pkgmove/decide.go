package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// linkImplementations ties every method of a module interface to the methods
// that implement it, and an embedded field to its type, so a rename of one
// renames the others: an implementation whose method changed name would stop
// implementing.
func (w *world) linkImplementations(fset *token.FileSet, named []*types.TypeName) {
	var ifaces, concrete []*types.TypeName
	for _, tn := range named {
		if types.IsInterface(tn.Type()) {
			ifaces = append(ifaces, tn)
		} else {
			concrete = append(concrete, tn)
		}
	}
	for _, iface := range ifaces {
		it := iface.Type().Underlying().(*types.Interface)
		for _, impl := range concrete {
			w.linkIfImplements(fset, it, impl)
		}
	}
}

func (w *world) linkIfImplements(fset *token.FileSet, it *types.Interface, impl *types.TypeName) {
	t := impl.Type()
	if !types.Implements(t, it) && !types.Implements(types.NewPointer(t), it) {
		return
	}
	for i := range it.NumMethods() {
		m := it.Method(i)
		found, _, _ := types.LookupFieldOrMethod(t, true, m.Pkg(), m.Name())
		if found == nil || !w.inModuleObj(m) || !w.inModuleObj(found) {
			continue
		}
		w.union(objKey(fset, m), objKey(fset, found))
	}
}

func (w *world) inModuleObj(obj types.Object) bool {
	return obj.Pkg() != nil && w.inModulePath(obj.Pkg().Path()) && obj.Pos().IsValid()
}

func (w *world) find(key string) string {
	root := key
	for {
		parent, ok := w.links[root]
		if !ok || parent == root {
			break
		}
		root = parent
	}
	for key != root {
		next := w.links[key]
		w.links[key] = root
		key = next
	}
	return root
}

func (w *world) union(a, b string) {
	ra, rb := w.find(a), w.find(b)
	if ra != rb {
		w.links[ra] = rb
	}
}

// decisions is what a mapping means for every module object: the package
// directory it ends up in, and its name there.
type decisions struct {
	dir     map[string]string
	newName map[string]string
	// exportedFields names every struct field the mapping exports.
	exportedFields []string
	decls          map[string][]declTarget
	errs           []string
}

// declTarget is one top-level declaration of a module file and where it goes.
type declTarget struct {
	start, end int
	dir        string
	// method is the key of the method a FuncDecl declares, which Go requires
	// to stay with its receiver type wherever that type is declared.
	method string
	// dups are the other packages a test-only declaration is copied into,
	// because tests there use it: a _test.go declaration cannot be exported.
	dups map[string]bool
}

func (w *world) decide(m packageMap) decisions {
	d := decisions{dir: map[string]string{}, newName: map[string]string{}, decls: map[string][]declTarget{}}
	for _, name := range sortedKeys(w.files) {
		if sf := w.files[name]; sf.inModule {
			d.decls[name] = declTargets(sf, m)
		}
	}
	w.placeMethods(m, d)
	for _, key := range sortedKeys(w.objects) {
		d.dir[key] = w.objectDir(key, m, d)
	}
	w.duplicateTestHelpers(d)
	w.decideNames(m, &d)
	return d
}

// declAt is the top-level declaration of a module file containing offset.
func (d decisions) declAt(file string, offset int) *declTarget {
	decls := d.decls[file]
	for i := range decls {
		if offset >= decls[i].start && offset < decls[i].end {
			return &decls[i]
		}
	}
	return nil
}

// visibleDir is where code in package directory from finds an object: its
// own copy when the object is a test helper duplicated there.
func (w *world) visibleDir(key, from string, d decisions) string {
	if decl := d.declAt(w.objects[key].file, offsetOf(key)); decl != nil && decl.dups[from] {
		return from
	}
	return d.dir[key]
}

// duplicateTestHelpers copies every test-only declaration a test in another
// package refers to into that package, together with what the copy refers to
// in turn and the methods of a copied type, until nothing more is needed.
func (w *world) duplicateTestHelpers(d decisions) {
	for changed := true; changed; {
		changed = false
		for _, s := range w.sites {
			if !s.def && w.dupForSite(s, d) {
				changed = true
			}
		}
		if w.dupMethods(d) {
			changed = true
		}
	}
	w.relocateUnusedTestHelpers(d)
}

// relocateUnusedTestHelpers moves, rather than copies, a test-only helper its
// own package no longer uses: every test that used it went elsewhere, so the
// original would be left behind unused.
func (w *world) relocateUnusedTestHelpers(d decisions) {
	usedAtHome := w.testHelpersUsedAtHome(d)
	for _, decls := range d.decls {
		for i := range decls {
			od := &decls[i]
			if len(od.dups) == 0 || usedAtHome[od] {
				continue
			}
			home := sortedKeys(od.dups)[0]
			delete(od.dups, home)
			od.dir = home
		}
	}
}

func (w *world) dupForSite(s site, d decisions) bool {
	o := w.objects[s.objKey]
	if !strings.HasSuffix(o.file, "_test.go") {
		return false
	}
	od := d.declAt(o.file, offsetOf(s.objKey))
	if od == nil {
		return false
	}
	changed := false
	for _, from := range w.siteDirs(s, d) {
		if from != od.dir && !strings.HasPrefix(from, "ext:") && !od.dups[from] {
			if od.dups == nil {
				od.dups = map[string]bool{}
			}
			od.dups[from] = true
			changed = true
		}
	}
	return changed
}

func (w *world) dupMethods(d decisions) bool {
	changed := false
	for _, decls := range d.decls {
		for i := range decls {
			o, ok := w.objects[decls[i].method]
			if !ok || o.owner == "" {
				continue
			}
			owner := d.declAt(w.objects[o.owner].file, offsetOf(o.owner))
			if owner == nil {
				continue
			}
			for dir := range owner.dups {
				if !decls[i].dups[dir] {
					if decls[i].dups == nil {
						decls[i].dups = map[string]bool{}
					}
					decls[i].dups[dir] = true
					changed = true
				}
			}
		}
	}
	return changed
}

func (w *world) placeMethods(m packageMap, d decisions) {
	for _, decls := range d.decls {
		for i := range decls {
			if o, ok := w.objects[decls[i].method]; ok && o.owner != "" {
				if _, ok := w.objects[o.owner]; ok {
					decls[i].dir = w.objectDir(o.owner, m, d)
				}
			}
		}
	}
}

// objectDir is the directory an object's declaration ends up in: a method
// goes with its receiver type, a field with the declaration it is written in.
func (w *world) objectDir(key string, m packageMap, d decisions) string {
	o := w.objects[key]
	if o.method && o.owner != "" {
		if owner, ok := w.objects[o.owner]; ok && owner != o {
			return w.objectDir(o.owner, m, d)
		}
	}
	offset := offsetOf(key)
	for _, decl := range d.decls[o.file] {
		if offset >= decl.start && offset < decl.end {
			return decl.dir
		}
	}
	sf := w.files[o.file]
	if sf == nil || !sf.inModule {
		return ""
	}
	return m.fileDir(sf.rel)
}

func offsetOf(key string) int {
	i := strings.LastIndex(key, ":")
	var n int
	_, _ = fmt.Sscanf(key[i+1:], "%d", &n)
	return n
}

// declTargets lists a file's top-level declarations with the directory each
// goes to. A method goes where its receiver's type goes, which objectDir
// resolves; here it is placed by its receiver's name, which is the same
// answer for a receiver declared in the module root.
func declTargets(sf *sourceFile, m packageMap) []declTarget {
	var out []declTarget
	for _, decl := range sf.ast.Decls {
		start := sf.fset.Position(decl.Pos()).Offset
		end := sf.fset.Position(decl.End()).Offset
		target := declTarget{start: start, end: end, dir: m.declDir(sf.rel, declName(decl))}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
			target.method = fmt.Sprintf("%s:%d", sf.path, sf.fset.Position(fn.Name.Pos()).Offset)
		}
		out = append(out, target)
	}
	return out
}

func declName(decl ast.Decl) string {
	switch x := decl.(type) {
	case *ast.FuncDecl:
		if x.Recv != nil && len(x.Recv.List) > 0 {
			return receiverName(x.Recv.List[0].Type)
		}
		return x.Name.Name
	case *ast.GenDecl:
		for _, spec := range x.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				return s.Name.Name
			case *ast.ValueSpec:
				return s.Names[0].Name
			}
		}
	}
	return ""
}

func receiverName(expr ast.Expr) string {
	for {
		switch x := expr.(type) {
		case *ast.StarExpr:
			expr = x.X
		case *ast.IndexExpr:
			expr = x.X
		case *ast.IndexListExpr:
			expr = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// decideNames exports every unexported object some file in another package
// refers to, together with every object linked to it, and applies the map's
// explicit renames.
func (w *world) decideNames(m packageMap, d *decisions) {
	crossing := w.crossingClasses(*d)
	for _, key := range sortedKeys(w.objects) {
		w.nameObject(key, m, d, crossing)
	}
	for key, o := range w.objects {
		if o.embedded != "" {
			if name, ok := d.newName[o.embedded]; ok {
				d.newName[key] = name
			}
		}
	}
	d.errs = append(d.errs, w.collisions(*d)...)
	d.errs = append(d.errs, w.memberCollisions(*d)...)
}

// crossingClasses are the linked classes of objects some code in another
// package refers to.
func (w *world) crossingClasses(d decisions) map[string]bool {
	crossing := map[string]bool{}
	for _, s := range w.sites {
		if s.def {
			continue
		}
		for _, from := range w.siteDirs(s, d) {
			if w.visibleDir(s.objKey, from, d) == from {
				continue
			}
			crossing[w.find(s.objKey)] = true
			if debugName != "" && w.objects[s.objKey].name == debugName {
				_, _ = fmt.Fprintf(os.Stderr, "crossing %s: site %s@%d in %q, object %s in %q\n", debugName, s.file, s.start, from, s.objKey, d.dir[s.objKey])
			}
		}
	}
	return crossing
}

// nameObject decides one object's name: the map's explicit rename, or its
// exported form when another package refers to it.
func (w *world) nameObject(key string, m packageMap, d *decisions, crossing map[string]bool) {
	o := w.objects[key]
	name := o.name
	if renamed, ok := m.renames[w.renameKey(o)]; ok {
		name = renamed
	} else if crossing[w.find(key)] || w.embeddedTypeCrossing(o, crossing) {
		name = exported(name)
	}
	if name == o.name {
		return
	}
	d.newName[key] = name
	if o.member && !o.method && o.embedded == "" {
		d.exportedFields = append(d.exportedFields, w.describeKey(key)+" -> "+name)
	}
}

// renameKey is how the map names an object in a rename: a package-level
// object by its name, a field or method as Owner.name.
func (w *world) renameKey(o *object) string {
	if o.member {
		if owner, ok := w.objects[o.owner]; ok {
			return owner.name + "." + o.name
		}
	}
	return o.name
}

// memberCollisions reports two fields or methods of one type that would share
// a name once one of them is exported, which the map resolves with a rename.
func (w *world) memberCollisions(d decisions) []string {
	byOwner := map[string]map[string][]string{}
	for _, key := range sortedKeys(w.objects) {
		o := w.objects[key]
		if !o.member || o.owner == "" {
			continue
		}
		name := o.name
		if n, ok := d.newName[key]; ok {
			name = n
		}
		if byOwner[o.owner] == nil {
			byOwner[o.owner] = map[string][]string{}
		}
		byOwner[o.owner][name] = append(byOwner[o.owner][name], key)
	}
	var errs []string
	for owner, names := range byOwner {
		for name, keys := range names {
			if len(keys) > 1 && w.renamedAny(keys, d) {
				errs = append(errs, fmt.Sprintf("%s.%s: %d members would share the name; add `rename %s.<member> <NewName>`",
					w.objects[owner].name, name, len(keys), w.objects[owner].name))
			}
		}
	}
	sort.Strings(errs)
	return errs
}

func (w *world) renamedAny(keys []string, d decisions) bool {
	for _, k := range keys {
		if _, ok := d.newName[k]; ok {
			return true
		}
	}
	return false
}

func (w *world) embeddedTypeCrossing(o *object, crossing map[string]bool) bool {
	return o.embedded != "" && crossing[w.find(o.embedded)]
}

func exported(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(r) || r == '_' {
		return name
	}
	return string(unicode.ToUpper(r)) + name[size:]
}

// siteDirs are the package directories the code containing a site ends up
// in: its declaration's own, and every package a test helper is copied to.
// A file outside the module is its own importing package.
func (w *world) siteDirs(s site, d decisions) []string {
	sf := w.files[s.file]
	if sf == nil || !sf.inModule {
		return []string{"ext:" + s.file}
	}
	decl := d.declAt(s.file, s.start)
	if decl == nil {
		return []string{path.Dir(sf.rel)}
	}
	return append([]string{decl.dir}, sortedKeys(decl.dups)...)
}

// collisions reports two package-level objects that would share a name in
// one package, which the map has to resolve with a rename.
func (w *world) collisions(d decisions) []string {
	seen := map[string][]string{}
	var errs []string
	for _, key := range sortedKeys(w.objects) {
		o := w.objects[key]
		if o.member {
			continue
		}
		name := o.name
		if n, ok := d.newName[key]; ok {
			name = n
		}
		slot := d.dir[key] + "." + name
		for _, other := range seen[slot] {
			if sharesOS(w.objects[other], o) {
				errs = append(errs, fmt.Sprintf("%s: %s and %s", slot, w.describeKey(other), w.describeKey(key)))
			}
		}
		seen[slot] = append(seen[slot], key)
	}
	sort.Strings(errs)
	return errs
}

func (w *world) describeKey(key string) string {
	o := w.objects[key]
	if sf := w.files[o.file]; sf != nil && sf.rel != "" {
		return sf.rel + ":" + o.name
	}
	return o.file + ":" + o.name
}

func sharesOS(a, b *object) bool {
	for goos := range a.oses {
		if b.oses[goos] {
			return true
		}
	}
	return false
}

// debugName, when set, explains why that identifier is exported.
var debugName = os.Getenv("PKGMOVE_DEBUG")

// testHelpersUsedAtHome are the duplicated test-only declarations some code in
// their own package still refers to.
func (w *world) testHelpersUsedAtHome(d decisions) map[*declTarget]bool {
	used := map[*declTarget]bool{}
	for _, s := range w.sites {
		o := w.objects[s.objKey]
		if s.def || !strings.HasSuffix(o.file, "_test.go") {
			continue
		}
		od := d.declAt(o.file, offsetOf(s.objKey))
		if od == nil || len(od.dups) == 0 || d.declAt(s.file, s.start) == od {
			continue
		}
		if slices.Contains(w.siteDirs(s, d), od.dir) {
			used[od] = true
		}
	}
	return used
}
