package main

import (
	"strings"
)

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

// inOwnMethod says whether a site is inside a method of the type od declares:
// a type's own methods go wherever the type goes, so they are not a use that
// keeps it at home.
func (w *world) inOwnMethod(s site, od *declTarget, d decisions) bool {
	at := d.declAt(s.file, s.start)
	if at == nil || at.method == "" {
		return false
	}
	m, ok := w.objects[at.method]
	if !ok || m.owner == "" {
		return false
	}
	owner, ok := w.objects[m.owner]
	return ok && d.declAt(owner.file, offsetOf(m.owner)) == od
}

// methodsFollowTheirTypes puts every method where its receiver type ended up,
// copied wherever the type is copied: Go keeps methods with their type, so a
// method is never placed on its own. A test type's method that nothing calls
// through an interface is the one exception: it goes only to the packages
// that call it, so a copy never carries a method none of its users call.
func (w *world) methodsFollowTheirTypes(d decisions) {
	calledIn := w.methodCallers(d)
	linked := w.linkedKeys()
	for file, decls := range d.decls {
		for i := range decls {
			w.placeMethod(&decls[i], d, strings.HasSuffix(file, "_test.go") && !linked[decls[i].method], calledIn)
		}
	}
}

// placeMethod puts one method declaration with its type, narrowed to the
// packages that call it when narrowing is allowed.
func (w *world) placeMethod(decl *declTarget, d decisions, narrow bool, calledIn map[string]map[string]bool) {
	o, ok := w.objects[decl.method]
	if !ok || o.owner == "" {
		return
	}
	ownerObj, ok := w.objects[o.owner]
	if !ok {
		return
	}
	owner := d.declAt(ownerObj.file, offsetOf(o.owner))
	if owner == nil {
		return
	}
	dirs := append([]string{owner.dir}, sortedKeys(owner.dups)...)
	if narrow && len(calledIn[decl.method]) > 0 {
		dirs = keepCalled(dirs, calledIn[decl.method])
	}
	decl.dir = dirs[0]
	decl.dups = map[string]bool{}
	for _, dir := range dirs[1:] {
		decl.dups[dir] = true
	}
	w.redirectDeclObjects(decl, d)
}

// methodCallers are the packages each method is called from directly.
func (w *world) methodCallers(d decisions) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range w.sites {
		if s.def || !w.objects[s.objKey].method {
			continue
		}
		if out[s.objKey] == nil {
			out[s.objKey] = map[string]bool{}
		}
		for _, dir := range w.siteDirs(s, d) {
			out[s.objKey][dir] = true
		}
	}
	return out
}

// linkedKeys are the methods that share a name with an interface method they
// satisfy, or with an implementation of theirs: they stay with their type
// wherever it goes.
func (w *world) linkedKeys() map[string]bool {
	classSize := map[string]int{}
	for key := range w.links {
		classSize[w.find(key)]++
	}
	out := map[string]bool{}
	for key := range w.objects {
		root := w.find(key)
		if root != key || classSize[root] > 0 {
			out[key] = true
		}
	}
	return out
}

// keepCalled narrows the packages a method goes to down to the ones that call
// it, keeping them in the type's order.
func keepCalled(dirs []string, called map[string]bool) []string {
	var out []string
	for _, dir := range dirs {
		if called[dir] {
			out = append(out, dir)
		}
	}
	if len(out) == 0 {
		return dirs
	}
	return out
}
