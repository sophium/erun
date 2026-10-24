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

// inOwnMethod: a type's own methods move with it, so they do not keep it home.
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

// methodsFollowTheirTypes places each method with its receiver type. A test
// type's method not called through an interface goes only where it is called.
func (w *world) methodsFollowTheirTypes(d decisions) {
	calledIn := w.methodCallers(d)
	linked := w.linkedKeys()
	for file, decls := range d.decls {
		for i := range decls {
			w.placeMethod(&decls[i], d, strings.HasSuffix(file, "_test.go") && !linked[decls[i].method], calledIn)
		}
	}
}

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

// linkedKeys are methods linked to an interface method; they always follow their type.
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
