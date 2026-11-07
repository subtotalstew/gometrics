package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGoFile создаёт файл с исходником в dir.
func writeGoFile(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

// sampleSource — пакет, покрывающий все ветки генератора: примитивы,
// строка, bool, указатель, слайс, массив, мапа, sync-тип, тип из другого
// пакета, вложенная структура, embed и произвольный тип.
const sampleSource = `package sample

import (
	"sync"
	"time"
)

// generate:reset
type Foo struct {
	N       int
	S       string
	B       bool
	P       *int
	Sl      []string
	Arr     [2]int
	M       map[string]int
	Mu      sync.Mutex
	T       time.Time
	Inner   Bar
	Counter int64
	Any     interface{}
}

type Bar struct {
	V int
}

// generate:reset
type Baz struct {
	Foo Foo
}
`

// TestParseDir проверяет разбор пакета: находятся только структуры с
// директивой, а служебные файлы и тесты пропускаются.
func TestParseDir(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "sample.go", sampleSource)
	writeGoFile(t, dir, "sample_test.go", "package sample\n\n// generate:reset\ntype FromTest struct{ X int }\n")
	writeGoFile(t, dir, generatedFileName, "package sample\n\n// generate:reset\ntype Generated struct{ X int }\n")

	pkg, err := parseDir(dir)
	if err != nil {
		t.Fatalf("parseDir() error = %v", err)
	}
	if pkg == nil {
		t.Fatal("parseDir() = nil, want package")
	}
	if pkg.name != "sample" {
		t.Errorf("pkg.name = %q, want sample", pkg.name)
	}
	if len(pkg.structs) != 2 {
		t.Fatalf("len(pkg.structs) = %d, want 2 (Foo и Baz)", len(pkg.structs))
	}
	if pkg.structs[0].name != "Foo" || pkg.structs[1].name != "Baz" {
		t.Errorf("structs = %q, %q; want Foo, Baz", pkg.structs[0].name, pkg.structs[1].name)
	}
}

// TestParseDir_NoDirectives проверяет, что без директивы пакет не попадает
// в генерацию.
func TestParseDir_NoDirectives(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "plain.go", "package plain\n\ntype Plain struct{ X int }\n")

	pkg, err := parseDir(dir)
	if err != nil {
		t.Fatalf("parseDir() error = %v", err)
	}
	if pkg != nil {
		t.Errorf("parseDir() = %+v, want nil", pkg)
	}
}

// TestParseDir_ParseError проверяет, что битый исходник — ошибка.
func TestParseDir_ParseError(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "broken.go", "package broken\n\nfunc {\n")

	if _, err := parseDir(dir); err == nil {
		t.Fatal("parseDir() = nil, want error for invalid Go source")
	}
}

// TestScanPackages проверяет рекурсивный обход: служебные директории
// (vendor, testdata, скрытые) пропускаются, вложенные пакеты находятся.
func TestScanPackages(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "sub")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "testdata"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, root, "root.go", "package root\n")
	writeGoFile(t, pkgDir, "sub.go", "package sub\n\n// generate:reset\ntype S struct{ X int }\n")
	writeGoFile(t, filepath.Join(root, "testdata"), "ignored.go", "package testdata\n\n// generate:reset\ntype T struct{ X int }\n")

	pkgs, err := scanPackages(root)
	if err != nil {
		t.Fatalf("scanPackages() error = %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("len(pkgs) = %d, want 1", len(pkgs))
	}
	if pkgs[0].name != "sub" {
		t.Errorf("pkg.name = %q, want sub", pkgs[0].name)
	}

	if _, err := scanPackages(filepath.Join(root, "missing")); err == nil {
		t.Fatal("scanPackages() = nil, want error for missing root")
	}
}

// TestBuildTemplateData проверяет выражения сброса и список импортов.
func TestBuildTemplateData(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "sample.go", sampleSource)

	pkg, err := parseDir(dir)
	if err != nil || pkg == nil {
		t.Fatalf("parseDir() = %+v, %v", pkg, err)
	}

	data := buildTemplateData(pkg)
	if data.Package != "sample" {
		t.Errorf("Package = %q, want sample", data.Package)
	}
	if len(data.Imports) != 1 || data.Imports[0].Path != "time" {
		t.Errorf("Imports = %+v, want only time", data.Imports)
	}
	if len(data.Structs) != 2 || data.Structs[0].Name != "Baz" {
		t.Fatalf("Structs = %+v, want Baz первым", data.Structs)
	}

	exprs := make(map[string]string)
	for _, s := range data.Structs {
		if s.Name != "Foo" {
			continue
		}
		for _, f := range s.Fields {
			exprs[f.Name] = f.Expr
		}
	}

	want := map[string]string{
		"N":  "x.N = 0",
		"S":  `x.S = ""`,
		"B":  "x.B = false",
		"Sl": "x.Sl = x.Sl[:0]",
		"Arr": "x.Arr = x.Arr{}",
		"M":  "clear(x.M)",
	}
	for name, w := range want {
		if exprs[name] != w {
			t.Errorf("%s expr = %q, want %q", name, exprs[name], w)
		}
	}
	if _, ok := exprs["Mu"]; ok {
		t.Error("sync.Mutex field must be skipped")
	}
	if !strings.Contains(exprs["P"], "if x.P != nil") {
		t.Errorf("pointer expr = %q, want nil check", exprs["P"])
	}
	if !strings.Contains(exprs["T"], "Reset()") {
		t.Errorf("selector expr = %q, want Reset call", exprs["T"])
	}
	if !strings.Contains(exprs["Any"], "var zero = *new(") {
		t.Errorf("interface expr = %q, want zero value", exprs["Any"])
	}
}

// TestRenderPackage проверяет, что сгенерированный файл — валидный Go-код
// с методами Reset для каждой структуры.
func TestRenderPackage(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "sample.go", sampleSource)

	pkg, err := parseDir(dir)
	if err != nil || pkg == nil {
		t.Fatalf("parseDir() = %+v, %v", pkg, err)
	}
	if err := renderPackage(pkg); err != nil {
		t.Fatalf("renderPackage() error = %v", err)
	}

	out := filepath.Join(dir, generatedFileName)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	code := string(data)
	for _, want := range []string{
		"// Code generated by cmd/reset; DO NOT EDIT.",
		"func (x *Foo) Reset()",
		"func (x *Baz) Reset()",
		`"time"`,
		"clear(x.M)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("generated file does not contain %q", want)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), out, data, parser.ParseComments); err != nil {
		t.Errorf("generated file is not valid Go: %v", err)
	}
}

// TestRun генерирует методы от корня временного проекта.
func TestRun(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, filepath.Join(root, "pkg"), "sample.go", sampleSource)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("Chdir(%s) error = %v", oldWd, err)
		}
	}()

	if err := run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", generatedFileName)); err != nil {
		t.Errorf("generated file not created: %v", err)
	}
}

// TestResetExpr проверяет отдельные виды выражений сброса.
func TestResetExpr(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"int", "x.f = 0"},
		{"string", `x.f = ""`},
		{"bool", "x.f = false"},
		{"[]byte", "x.f = x.f[:0]"},
		{"[4]byte", "x.f = x.f{}"},
		{"map[string]int", "clear(x.f)"},
		{"*string", "if x.f != nil {"},
		{"time.Time", "Reset()"},
		{"chan int", "var zero = *new("},
	}

	for _, tt := range tests {
		expr, err := parser.ParseExpr(tt.src)
		if err != nil {
			t.Fatalf("ParseExpr(%q) error = %v", tt.src, err)
		}
		got := resetExpr("x.f", expr)
		if !strings.Contains(got, tt.want) {
			t.Errorf("resetExpr(%q) = %q, want contains %q", tt.src, got, tt.want)
		}
	}
}

// TestStdlibPath проверяет сопоставление имени пакета и пути импорта.
func TestStdlibPath(t *testing.T) {
	tests := map[string]string{
		"sync":    "sync",
		"time":    "time",
		"atomic":  "sync/atomic",
		"url":     "net/url",
		"http":    "net/http",
		"sql":     "database/sql",
		"json":    "encoding/json",
		"context": "context",
		"big":     "math/big",
		"io":      "io",
		"unknown": "",
	}
	for pkg, want := range tests {
		if got := stdlibPath(pkg); got != want {
			t.Errorf("stdlibPath(%q) = %q, want %q", pkg, got, want)
		}
	}
}

// TestIsSyncType проверяет, что sync-типы распознаются и не сбрасываются.
func TestIsSyncType(t *testing.T) {
	tests := []struct {
		src  string
		want bool
	}{
		{"sync.Mutex", true},
		{"sync.Once", true},
		{"time.Time", false},
		{"int", false},
		{"[]sync.Mutex", false},
	}
	for _, tt := range tests {
		expr, err := parser.ParseExpr(tt.src)
		if err != nil {
			t.Fatalf("ParseExpr(%q) error = %v", tt.src, err)
		}
		if got := isSyncType(expr); got != tt.want {
			t.Errorf("isSyncType(%q) = %v, want %v", tt.src, got, tt.want)
		}
	}
}

// TestHasDirective проверяет распознавание директивы в комментарии.
func TestHasDirective(t *testing.T) {
	src := "package p\n\n// generate:reset\ntype A struct{}\n\n// Обычный комментарий.\ntype B struct{}\n"
	fset, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var docs []*ast.CommentGroup
	for _, decl := range fset.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok {
			docs = append(docs, gd.Doc)
		}
	}
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d, want 2", len(docs))
	}
	if !hasDirective(docs[0]) {
		t.Error("hasDirective() = false, want true")
	}
	if hasDirective(docs[1]) {
		t.Error("hasDirective() = true for plain comment")
	}
	if hasDirective(nil) {
		t.Error("hasDirective(nil) = true, want false")
	}
}
