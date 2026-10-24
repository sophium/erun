package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path"
	"slices"
	"strings"
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
		it, ok := iface.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
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
	// group is the offset of the grouped declaration a spec belongs to, and
	// tok its keyword; zero for a declaration that is not one spec of a group.
	group int
	tok   string
	// kept marks a test-only copy left in place because dest, where its file
	// goes, already declares it; drop removes it when its own package is
	// emptied, so references to it resolve to that twin.
	kept, drop bool
	dest       string
}

func (w *world) decide(m packageMap) decisions {
	d := decisions{dir: map[string]string{}, newName: map[string]string{}, decls: map[string][]declTarget{}}
	for _, name := range sortedKeys(w.files) {
		if sf := w.files[name]; sf.inModule {
			d.decls[name] = declTargets(sf, m)
		}
	}
	w.placeMethods(m, d)
	w.keepCopiesInPlace(d)
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
	o := w.objects[key]
	if decl := d.declAt(o.file, offsetOf(key)); decl != nil && decl.dups[from] {
		return from
	}
	if strings.HasSuffix(o.file, "_test.go") && w.alreadyDeclares(from, o) {
		return from
	}
	return d.dir[key]
}

// duplicateTestHelpers copies every test-only declaration a test in another
// package refers to into that package, together with what the copy refers to
// in turn and the methods of a copied type, until nothing more is needed.
func (w *world) duplicateTestHelpers(d decisions) {
	w.initsFollowTheirFile(d)
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
	w.methodsFollowTheirTypes(d)
	w.debugDecls(d)
}

// debugDecls prints where every declaration named debugName goes.
func (w *world) debugDecls(d decisions) {
	if debugName == "" {
		return
	}
	for file, decls := range d.decls {
		for _, decl := range decls {
			if sf := w.files[file]; sf != nil && strings.Contains(string(sf.src[decl.start:decl.end]), debugName) {
				_, _ = fmt.Fprintf(os.Stderr, "decl %s@%d-%d dir=%q dups=%v\n", sf.rel, decl.start, decl.end, decl.dir, sortedKeys(decl.dups))
			}
		}
	}
}

// initsFollowTheirFile copies a test file's init into every package that
// receives part of that file. An init refers to nothing the tests in its file
// name, so no reference carries it; but those tests rely on what it sets up,
// wherever they end up.
func (w *world) initsFollowTheirFile(d decisions) {
	for file, decls := range d.decls {
		sf := w.files[file]
		if sf == nil || !strings.HasSuffix(file, "_test.go") {
			continue
		}
		dirs := map[string]bool{}
		var inits []*declTarget
		for i := range decls {
			if w.isInit(sf, &decls[i]) {
				inits = append(inits, &decls[i])
				continue
			}
			dirs[decls[i].dir] = true
		}
		for _, od := range inits {
			for dir := range dirs {
				if dir == od.dir {
					continue
				}
				if od.dups == nil {
					od.dups = map[string]bool{}
				}
				od.dups[dir] = true
			}
		}
	}
}

// isInit says whether a declaration is a package init function.
func (w *world) isInit(sf *sourceFile, decl *declTarget) bool {
	for _, astDecl := range sf.ast.Decls {
		fn, ok := astDecl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "init" && sf.fset.Position(fn.Pos()).Offset >= decl.start && sf.fset.Position(fn.Pos()).Offset < decl.end {
			return true
		}
	}
	return false
}

// relocateUnusedTestHelpers moves, rather than copies, a test-only helper its
// own package no longer uses: every test that used it went elsewhere, so the
// original would be left behind unused.
func (w *world) relocateUnusedTestHelpers(d decisions) {
	for moved := true; moved; {
		moved = false
		usedAtHome := w.testHelpersUsedAtHome(d)
		for file, decls := range d.decls {
			for i := range decls {
				od := &decls[i]
				if len(od.dups) == 0 || usedAtHome[od] || od.method != "" || w.isInit(w.files[file], od) {
					continue
				}
				home := sortedKeys(od.dups)[0]
				delete(od.dups, home)
				od.dir = home
				w.redirectDeclObjects(od, d)
				moved = true
			}
		}
	}
}

// redirectDeclObjects records a relocated declaration's new directory for
// every object it declares.
func (w *world) redirectDeclObjects(od *declTarget, d decisions) {
	for key, o := range w.objects {
		if d.declAt(o.file, offsetOf(key)) == od {
			d.dir[key] = od.dir
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
		if w.alreadyDeclares(from, o) {
			continue
		}
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
		if od == nil || len(od.dups) == 0 || d.declAt(s.file, s.start) == od || w.inOwnMethod(s, od, d) {
			continue
		}
		if slices.Contains(w.siteDirs(s, d), od.dir) {
			used[od] = true
		}
	}
	return used
}

// alreadyDeclares reports whether a package already has its own declaration of
// o's name -- a copy an earlier step made -- in which case it needs no other.
func (w *world) alreadyDeclares(dir string, o *object) bool {
	if w.declared == nil {
		w.declared = map[string]map[string]bool{}
		for _, other := range w.objects {
			sf := w.files[other.file]
			if other.member || sf == nil || !sf.inModule {
				continue
			}
			home := path.Dir(sf.rel)
			if w.declared[home] == nil {
				w.declared[home] = map[string]bool{}
			}
			w.declared[home][other.name] = true
		}
	}
	sf := w.files[o.file]
	return sf != nil && path.Dir(sf.rel) != dir && w.declared[dir][o.name]
}

// keepCopiesInPlace leaves a test-only declaration where it is when the
// package its file's rule sends it to already declares every name it does:
// it is a copy an earlier step made, and that package has its own. This is
// what makes re-running a step's map after rebasing onto it change nothing.
func (w *world) keepCopiesInPlace(d decisions) {
	for file, decls := range d.decls {
		sf := w.files[file]
		if sf == nil || !strings.HasSuffix(file, "_test.go") {
			continue
		}
		home := path.Dir(sf.rel)
		for i := range decls {
			if decls[i].dir != home && w.declaresAllIn(&decls[i], file, decls[i].dir) {
				decls[i].dest = decls[i].dir
				decls[i].dir = home
				decls[i].kept = true
			}
		}
	}
	dropCopiesOfEmptiedPackages(w, d)
}

// dropCopiesOfEmptiedPackages drops a copy kept in place when nothing else of
// its package stays there: the package is going away, and the destination
// already declares the same thing.
func dropCopiesOfEmptiedPackages(w *world, d decisions) {
	stays := packagesKeepingDeclarations(w, d)
	for file, decls := range d.decls {
		sf := w.files[file]
		if sf == nil || stays[path.Dir(sf.rel)] {
			continue
		}
		for i := range decls {
			if decls[i].kept {
				decls[i].drop = true
				decls[i].dir = decls[i].dest
			}
		}
	}
}

// packagesKeepingDeclarations are the packages some declaration other than a
// kept copy stays in.
func packagesKeepingDeclarations(w *world, d decisions) map[string]bool {
	stays := map[string]bool{}
	for file, decls := range d.decls {
		sf := w.files[file]
		if sf == nil {
			continue
		}
		home := path.Dir(sf.rel)
		for _, decl := range decls {
			if decl.dir == home && !decl.kept {
				stays[home] = true
			}
		}
	}
	return stays
}

func (w *world) declaresAllIn(decl *declTarget, file, dir string) bool {
	found := false
	for key, o := range w.objects {
		if o.file != file || o.member {
			continue
		}
		if off := offsetOf(key); off < decl.start || off >= decl.end {
			continue
		}
		if !w.alreadyDeclares(dir, o) {
			return false
		}
		found = true
	}
	return found
}
