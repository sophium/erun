package main

import (
	"fmt"
	"os"
	"path"
	"sort"
	"unicode"
	"unicode/utf8"
)

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
		if o.member || neverCollides(o.name) || w.dropped(key, d) {
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

// dropped says whether an object is declared by a copy that is dropped.
func (w *world) dropped(key string, d decisions) bool {
	decl := d.declAt(w.objects[key].file, offsetOf(key))
	return decl != nil && decl.drop
}

// neverCollides says whether a package may declare a name any number of
// times: init functions and the blank identifier.
func neverCollides(name string) bool {
	return name == "init" || name == "_"
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
