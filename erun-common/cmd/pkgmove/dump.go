package main

import (
	"fmt"
	"io"
	"sort"
)

// dump writes one row per reference from a top-level declaration to a
// package-level object: the referring file and declaration, the referenced
// file and name, and how many times. Declarations are named as declName
// names them, so a method row names its receiver type.
func dump(w *world, m packageMap, out io.Writer) error {
	d := w.decide(m)
	rows := map[string]int{}
	for _, s := range w.sites {
		o := w.objects[s.objKey]
		sf := w.files[s.file]
		if s.def || o.member || sf == nil || !sf.inModule {
			continue
		}
		target := w.files[o.file]
		if target == nil || !target.inModule {
			continue
		}
		rows[fmt.Sprintf("%s\t%s\t%s\t%s", sf.rel, w.enclosingName(sf, s.start, d), target.rel, o.name)]++
	}
	keys := sortedKeys(rows)
	sort.Strings(keys)
	_, _ = fmt.Fprintln(out, "from_file\tfrom_decl\tto_file\tto_name\tcount")
	for _, k := range keys {
		_, _ = fmt.Fprintf(out, "%s\t%d\n", k, rows[k])
	}
	return nil
}

func (w *world) enclosingName(sf *sourceFile, offset int, d decisions) string {
	for _, decl := range sf.ast.Decls {
		start := sf.fset.Position(decl.Pos()).Offset
		end := sf.fset.Position(decl.End()).Offset
		if offset >= start && offset < end {
			return declName(decl)
		}
	}
	return ""
}
