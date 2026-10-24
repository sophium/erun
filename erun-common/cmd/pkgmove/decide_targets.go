package main

import (
	"fmt"
	"go/ast"
	"go/token"
)

// declTargets lists a file's top-level declarations with the directory each
// goes to. A method goes where its receiver's type goes, which objectDir
// resolves; here it is placed by its receiver's name, which is the same
// answer for a receiver declared in the module root.
func declTargets(sf *sourceFile, m packageMap) []declTarget {
	var out []declTarget
	for _, decl := range sf.ast.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			continue
		}
		start := sf.fset.Position(decl.Pos()).Offset
		end := sf.fset.Position(decl.End()).Offset
		if g, ok := decl.(*ast.GenDecl); ok && g.Lparen.IsValid() && len(g.Specs) > 1 {
			out = append(out, specTargets(sf, m, g, start)...)
			continue
		}
		target := declTarget{start: start, end: end, dir: m.declDir(sf.rel, declName(decl))}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
			target.method = fmt.Sprintf("%s:%d", sf.path, sf.fset.Position(fn.Name.Pos()).Offset)
		}
		out = append(out, target)
	}
	return out
}

// specTargets places each spec of a grouped declaration on its own, so one
// `type ( ... )` block can be split between packages. A spec's range starts
// where the previous one ended, carrying the comments between them, and runs
// to the end of its last line. A const group that repeats an expression
// implicitly or counts with iota stays whole: splitting it would change the
// values the later specs get.
func specTargets(sf *sourceFile, m packageMap, g *ast.GenDecl, group int) []declTarget {
	keepWhole := g.Tok == token.CONST && constGroupIsPositional(g)
	firstDir := m.declDir(sf.rel, specName(g.Specs[0]))
	prev := sf.fset.Position(g.Lparen).Offset + 1
	out := make([]declTarget, 0, len(g.Specs))
	for _, spec := range g.Specs {
		end := lineEnd(sf.src, sf.fset.Position(spec.End()).Offset)
		dir := m.declDir(sf.rel, specName(spec))
		if keepWhole {
			dir = firstDir
		}
		out = append(out, declTarget{start: prev, end: end, dir: dir, group: group, tok: g.Tok.String()})
		prev = end
	}
	return out
}

func specName(spec ast.Spec) string {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Name.Name
	case *ast.ValueSpec:
		return s.Names[0].Name
	}
	return ""
}

func constGroupIsPositional(g *ast.GenDecl) bool {
	for _, spec := range g.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if len(vs.Values) == 0 {
			return true
		}
		for _, v := range vs.Values {
			if mentionsIota(v) {
				return true
			}
		}
	}
	return false
}

func mentionsIota(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "iota" {
			found = true
		}
		return !found
	})
	return found
}

// lineEnd is the offset of the newline ending the line offset is on, or the
// end of src.
func lineEnd(src []byte, offset int) int {
	for i := offset; i < len(src); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return len(src)
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
