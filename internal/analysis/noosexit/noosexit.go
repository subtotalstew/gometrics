// Package noosexit implements a static analyzer that forbids direct calls
// to os.Exit inside the main function of package main.
//
// Rationale: os.Exit terminates the process immediately, skipping all
// deferred calls, flushing of buffers, graceful shutdown of goroutines
// and other cleanup logic. Inside func main of package main this makes
// it impossible to guarantee controlled termination. Instead of calling
// os.Exit, the main function should delegate its work to a helper
// (e.g. run() error) and let the top-level code decide how to report
// the failure.
//
// Analyzer works only on packages of the current module: any generated
// packages from the Go build cache (go-build), packages of external
// modules (GOMODCACHE) and other main packages outside the module root
// are skipped. This keeps the diagnostics focused on the project code
// itself.
package noosexit

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// ModuleName is a substring that uniquely identifies the root of the
// analyzed module. It is used to filter out packages that do not belong
// to the project (for example, generated main packages in the Go build
// cache, which go/packages may add to the dependency graph).
//
// Если имя модуля в go.mod отличается — поменяйте константу.
const ModuleName = "gometrics"

// Analyzer is the "noosexit" analyzer instance.
//
// It reports any direct call to os.Exit that occurs lexically inside
// the body of func main in package main of the current module.
var Analyzer = &analysis.Analyzer{
	Name: "noosexit",
	Doc:  "forbids direct calls to os.Exit inside the main function of package main",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	// Нас интересуют только пакеты main.
	if pass.Pkg.Name() != "main" {
		return nil, nil
	}

	// Определяем корень модуля по пути первого файла пакета. Так
	// анализатор не будет трогать ни go-build, ни GOMODCACHE, ни
	// пакеты сторонних модулей.
	root := moduleRoot(pass)
	if root == "" {
		return nil, nil
	}

	for _, file := range pass.Files {
		pos := pass.Fset.Position(file.Pos())
		if !strings.HasPrefix(pos.Filename, root) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue // не функция или метод
			}
			if fn.Name.Name != "main" {
				continue
			}
			inspectFuncBody(pass, fn.Body)
		}
	}
	return nil, nil
}

// moduleRoot пытается найти корень модуля: поднимается по пути файла
// до директории, где встречается подстрока ModuleName. Если не находит —
// возвращает пустую строку, и анализатор молчит.
func moduleRoot(pass *analysis.Pass) string {
	for _, file := range pass.Files {
		pos := pass.Fset.Position(file.Pos())
		if idx := strings.Index(pos.Filename, ModuleName); idx >= 0 {
			return pos.Filename[:idx]
		}
	}
	return ""
}

func inspectFuncBody(pass *analysis.Pass, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Интересует только вызов вида os.Exit(...).
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Exit" {
			return true
		}

		// Проверяем, что sel.X действительно ссылается на пакет "os".
		// Это защищает от ложных срабатываний, если в коде есть
		// локальная переменная с именем os.
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		obj := pass.TypesInfo.Uses[ident]
		pkgName, ok := obj.(*types.PkgName)
		if !ok {
			return true
		}
		if pkgName.Imported().Path() != "os" {
			return true
		}

		pass.Reportf(call.Pos(),
			"direct call to os.Exit is forbidden in main function of package main; return an error instead")
		return true
	})
}
