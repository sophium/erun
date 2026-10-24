package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// targetOSes are type-checked so platform-specific files are seen.
var targetOSes = []string{"darwin", "linux", "windows"}

// world merges what every load says about the module's declarations, keyed
// by declaration position.
type world struct {
	module  string
	rootDir string
	objects map[string]*object
	files   map[string]*sourceFile
	sites   map[siteKey]site
	links   map[string]string
	goos    string
	// declared is filled lazily, per package, when a copy is considered.
	declared map[string]map[string]bool
	// twins maps a name to its first declaration, so a rename reaches every
	// platform's declaration of it.
	twins map[string]string
}

// object is a module declaration referable by name.
type object struct {
	key      string
	name     string
	file     string
	member   bool
	method   bool
	owner    string
	embedded string
	// oses: two declarations collide only if compiled together.
	oses map[string]bool
}

type sourceFile struct {
	path        string
	rel         string
	inModule    bool
	pkgPath     string
	fset        *token.FileSet
	ast         *ast.File
	src         []byte
	names       map[string]bool
	imports     map[string]bool
	importNames map[string]string
	// docs maps a name's offset to its doc comment's, so renames update the comment.
	docs map[int]int
	// pkgRefs lets a local that shadows an import name not count as a use.
	pkgRefs map[int]bool
}

type siteKey struct {
	file  string
	start int
}

// site is one identifier naming a module object; for pkg.Name, start covers
// the qualifier and qualPath is its package.
type site struct {
	file     string
	start    int
	end      int
	objKey   string
	def      bool
	qualPath string
}

func newWorld(module, rootDir string) *world {
	return &world{
		module:  module,
		rootDir: rootDir,
		objects: map[string]*object{},
		files:   map[string]*sourceFile{},
		sites:   map[siteKey]site{},
		links:   map[string]string{},
		twins:   map[string]string{},
	}
}

func (w *world) inModulePath(pkgPath string) bool {
	return pkgPath == w.module || strings.HasPrefix(pkgPath, w.module+"/")
}

// load type-checks patterns in dir, tests included, on every target OS.
func (w *world) load(dir string, patterns []string) error {
	for _, goos := range targetOSes {
		w.goos = goos
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
			Dir:   dir,
			Tests: true,
			Env:   append(os.Environ(), "GOOS="+goos, cgoFor(goos)),
		}
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			return fmt.Errorf("load %s (%s): %w", dir, goos, err)
		}
		if err := w.collectAll(pkgs, goos); err != nil {
			return err
		}
	}
	return nil
}

func cgoFor(goos string) string {
	if goos == hostOS() {
		return "CGO_ENABLED=1"
	}
	return "CGO_ENABLED=0"
}

func (w *world) collectAll(pkgs []*packages.Package, goos string) error {
	var failed []string
	var named []*types.TypeName
	var fset *token.FileSet
	for _, pkg := range pkgs {
		if strings.HasSuffix(pkg.ID, ".test") {
			continue
		}
		for _, e := range pkg.Errors {
			failed = append(failed, fmt.Sprintf("%s (%s): %s", pkg.ID, goos, e))
		}
		w.collect(pkg)
		if w.inModulePath(pkg.PkgPath) {
			named = append(named, moduleNamed(pkg.Types)...)
			fset = pkg.Fset
		}
	}
	if fset != nil {
		w.linkImplementations(fset, named)
		w.recordFieldOwners(fset, named)
	}
	if len(failed) > 0 {
		return fmt.Errorf("type errors:\n%s", strings.Join(failed, "\n"))
	}
	return nil
}

func (w *world) collect(pkg *packages.Package) {
	inModule := w.inModulePath(pkg.PkgPath)
	for _, file := range pkg.Syntax {
		sf := w.registerFile(pkg, file, inModule)
		if sf == nil {
			continue
		}
		w.collectFile(pkg, sf)
	}
}

func (w *world) registerFile(pkg *packages.Package, file *ast.File, inModule bool) *sourceFile {
	name := pkg.Fset.Position(file.Pos()).Filename
	if !strings.HasSuffix(name, ".go") {
		return nil
	}
	if sf, ok := w.files[name]; ok {
		return sf
	}
	src, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	sf := &sourceFile{
		path: name, inModule: inModule, pkgPath: pkg.PkgPath, fset: pkg.Fset, ast: file, src: src,
		names: map[string]bool{}, imports: map[string]bool{}, importNames: map[string]string{}, docs: map[int]int{}, pkgRefs: map[int]bool{},
	}
	if inModule {
		rel, _ := filepath.Rel(w.rootDir, name)
		sf.rel = filepath.ToSlash(rel)
	}
	recordDocs(sf)
	w.files[name] = sf
	return sf
}

func recordDocs(sf *sourceFile) {
	note := func(names []*ast.Ident, doc *ast.CommentGroup) {
		if doc == nil {
			return
		}
		for _, id := range names {
			sf.docs[sf.fset.Position(id.Pos()).Offset] = sf.fset.Position(doc.Pos()).Offset
		}
	}
	ast.Inspect(sf.ast, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			note([]*ast.Ident{x.Name}, x.Doc)
		case *ast.GenDecl:
			if len(x.Specs) == 1 {
				recordSpecDoc(x.Specs[0], x.Doc, note)
			}
		case *ast.TypeSpec:
			note([]*ast.Ident{x.Name}, x.Doc)
		case *ast.ValueSpec:
			note(x.Names, x.Doc)
		case *ast.Field:
			note(x.Names, x.Doc)
		}
		return true
	})
}

func recordSpecDoc(spec ast.Spec, doc *ast.CommentGroup, note func([]*ast.Ident, *ast.CommentGroup)) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		if s.Doc == nil {
			note([]*ast.Ident{s.Name}, doc)
		}
	case *ast.ValueSpec:
		if s.Doc == nil {
			note(s.Names, doc)
		}
	}
}

func (w *world) collectFile(pkg *packages.Package, sf *sourceFile) {
	file := pkg.Syntax[indexOfFile(pkg, sf.path)]
	qualified := qualifiedSelectors(file, pkg.TypesInfo)
	collectImportNames(pkg, file, sf)
	for id, obj := range pkg.TypesInfo.Defs {
		if !w.sameFile(pkg, id, sf) {
			continue
		}
		if _, isImport := obj.(*types.PkgName); obj != nil && !isImport {
			sf.names[id.Name] = true
		}
		w.recordDef(pkg, id, obj)
	}
	for id, obj := range pkg.TypesInfo.Uses {
		if !w.sameFile(pkg, id, sf) {
			continue
		}
		if _, isImport := obj.(*types.PkgName); isImport {
			sf.pkgRefs[pkg.Fset.Position(id.Pos()).Offset] = true
		}
		w.recordUse(pkg, id, obj, qualified)
	}
}

func collectImportNames(pkg *packages.Package, file *ast.File, sf *sourceFile) {
	for _, spec := range file.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		if spec.Name != nil {
			sf.imports[spec.Name.Name] = true
			sf.importNames[p] = spec.Name.Name
		} else if pn, ok := pkg.TypesInfo.Implicits[spec].(*types.PkgName); ok {
			sf.imports[pn.Name()] = true
			sf.importNames[p] = pn.Name()
		}
	}
}

func indexOfFile(pkg *packages.Package, name string) int {
	for i, file := range pkg.Syntax {
		if pkg.Fset.Position(file.Pos()).Filename == name {
			return i
		}
	}
	return 0
}

func (w *world) sameFile(pkg *packages.Package, id *ast.Ident, sf *sourceFile) bool {
	return pkg.Fset.Position(id.Pos()).Filename == sf.path
}

// qualifiedSelectors maps each pkg.Name's Sel to the whole selector, so a
// rewrite replaces the qualifier too.
func qualifiedSelectors(file *ast.File, info *types.Info) map[*ast.Ident]*ast.SelectorExpr {
	out := map[*ast.Ident]*ast.SelectorExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			if _, ok := info.Uses[x].(*types.PkgName); ok {
				out[sel.Sel] = sel
			}
		}
		return true
	})
	return out
}

func (w *world) recordDef(pkg *packages.Package, id *ast.Ident, obj types.Object) {
	o := w.objectFor(pkg.Fset, obj)
	if o == nil {
		return
	}
	if v, ok := obj.(*types.Var); ok && v.Embedded() {
		// The embedded type's own reference carries this identifier.
		return
	}
	pos := pkg.Fset.Position(id.Pos())
	w.addSite(site{file: pos.Filename, start: pos.Offset, end: pos.Offset + len(id.Name), objKey: o.key, def: true})
}

func (w *world) recordUse(pkg *packages.Package, id *ast.Ident, obj types.Object, qualified map[*ast.Ident]*ast.SelectorExpr) {
	o := w.objectFor(pkg.Fset, obj)
	if o == nil {
		return
	}
	pos := pkg.Fset.Position(id.Pos())
	s := site{file: pos.Filename, start: pos.Offset, end: pos.Offset + len(id.Name), objKey: o.key}
	if sel, ok := qualified[id]; ok && !o.member {
		if x, pkgName, ok := selectorQualifier(pkg, sel); ok {
			s.start = pkg.Fset.Position(x.Pos()).Offset
			s.qualPath = pkgName.Imported().Path()
		}
	}
	w.addSite(s)
}

func selectorQualifier(pkg *packages.Package, sel *ast.SelectorExpr) (*ast.Ident, *types.PkgName, bool) {
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	pkgName, ok := pkg.TypesInfo.Uses[x].(*types.PkgName)
	return x, pkgName, ok
}

func (w *world) addSite(s site) {
	w.sites[siteKey{file: s.file, start: s.start}] = s
}

// objectFor returns obj's module object, registering it on first sight, or
// nil for anything rewriting never touches.
func (w *world) objectFor(fset *token.FileSet, obj types.Object) *object {
	if obj == nil || obj.Pkg() == nil || !w.inModulePath(obj.Pkg().Path()) || !obj.Pos().IsValid() {
		return nil
	}
	key := objKey(fset, obj)
	o, ok := w.objects[key]
	if !ok {
		if o = w.register(fset, obj, key); o == nil {
			return nil
		}
	}
	o.oses[w.goos] = true
	return o
}

func (w *world) register(fset *token.FileSet, obj types.Object, key string) *object {
	o := describe(fset, obj, key)
	if o == nil {
		return nil
	}
	o.oses = map[string]bool{}
	w.objects[key] = o
	if !o.member {
		twin := obj.Pkg().Path() + "." + o.name
		if first, ok := w.twins[twin]; ok {
			w.union(first, key)
		} else {
			w.twins[twin] = key
		}
	}
	return o
}

func describe(fset *token.FileSet, obj types.Object, key string) *object {
	o := &object{key: key, name: obj.Name(), file: fset.Position(obj.Pos()).Filename}
	switch x := obj.(type) {
	case *types.PkgName, *types.Label, *types.Builtin, *types.Nil:
		return nil
	case *types.Func:
		if recv := x.Signature().Recv(); recv != nil {
			o.member, o.method = true, true
			if named := namedOf(recv.Type()); named != nil {
				o.owner = objKey(fset, named.Obj())
			}
			return o
		}
	case *types.Var:
		if x.IsField() {
			o.member = true
			if x.Embedded() {
				if named := namedOf(x.Type()); named != nil {
					o.embedded = objKey(fset, named.Obj())
				}
			}
			return o
		}
	}
	if obj.Parent() != obj.Pkg().Scope() {
		return nil
	}
	return o
}

func namedOf(t types.Type) *types.Named {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if a, ok := t.(*types.Alias); ok {
		t = types.Unalias(a)
	}
	named, _ := t.(*types.Named)
	return named
}

func objKey(fset *token.FileSet, obj types.Object) string {
	pos := fset.Position(obj.Pos())
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Offset)
}

func moduleNamed(pkg *types.Package) []*types.TypeName {
	if pkg == nil {
		return nil
	}
	var out []*types.TypeName
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if tn, ok := scope.Lookup(name).(*types.TypeName); ok {
			if _, ok := tn.Type().(*types.Named); ok {
				out = append(out, tn)
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// recordFieldOwners lets a renamed field be checked against its type's other members.
func (w *world) recordFieldOwners(fset *token.FileSet, named []*types.TypeName) {
	for _, tn := range named {
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			if o, ok := w.objects[objKey(fset, st.Field(i))]; ok && o.owner == "" {
				o.owner = objKey(fset, tn)
			}
		}
	}
}
