package strictcli

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

// The framework-use lint (effects contract §28), Go's scanner. It reads the
// whole program the flag was run through -- every package main directory of
// the module that imports strictcli, plus every package of the module those
// import, transitively -- and refuses the constructs that bypass the
// framework's ownership of exits, output, argv, and environment input.

// The Go spellings the finding messages name (§12.17's spelling rows).
const (
	lintGoManifest = "go.mod"
	lintGoEarly    = "strictcli.ExitNow(code, message)"
	lintGoOut      = "ctx.Out"
	lintGoPayload  = "ctx.Payload"
	lintGoDocument = "ctx.Document()"
	lintGoWarn     = "ctx.Warn"
	lintGoError    = "ctx.Error"
)

// lintRule is one closed list of §28.3: a construct's canonical spelling maps
// to its rule identifier.
var lintGoSelectors = map[string]string{
	"os.Exit":         "process-exit",
	"syscall.Exit":    "process-exit",
	"log.Fatal":       "process-exit",
	"log.Fatalf":      "process-exit",
	"log.Fatalln":     "process-exit",
	"fmt.Print":       "stdout-write",
	"fmt.Printf":      "stdout-write",
	"fmt.Println":     "stdout-write",
	"os.Stdout":       "stdout-write",
	"os.Stderr":       "stderr-write",
	"log.Print":       "stderr-write",
	"log.Printf":      "stderr-write",
	"log.Println":     "stderr-write",
	"log.Panic":       "stderr-write",
	"log.Panicf":      "stderr-write",
	"log.Panicln":     "stderr-write",
	"os.Args":         "argv-access",
	"os.Getenv":       "environment-read",
	"os.LookupEnv":    "environment-read",
	"os.Environ":      "environment-read",
	"os.ExpandEnv":    "environment-read",
	"syscall.Getenv":  "environment-read",
	"syscall.Environ": "environment-read",
}

// lintGoBuiltins are the builtins that write to stderr.
var lintGoBuiltins = map[string]bool{"print": true, "println": true}

// lintGoImports are the imports that are findings at their import line.
var lintGoImports = map[string]string{"flag": "argv-access"}

// lintMessage is a rule's message for a construct.
func lintMessage(rule, construct string) string {
	switch rule {
	case "process-exit":
		return errLintProcessExit(construct, lintGoEarly)
	case "stdout-write":
		return errLintStdoutWrite(construct, lintGoOut, lintGoPayload, lintGoDocument)
	case "stderr-write":
		return errLintStderrWrite(construct, lintGoWarn, lintGoError)
	case "argv-access":
		return errLintArgvAccess(construct)
	case "environment-read":
		return errLintEnvironmentRead(construct)
	case "exit-now-in-goroutine":
		return errLintExitNowInGoroutine
	}
	panic("strictcli: unknown framework-use rule " + rule)
}

// strictcliPackagePath is this package's import path; its module path is the
// directory above it.
var strictcliPackagePath = reflect.TypeOf(App{}).PkgPath()

func strictcliModulePath() string {
	return path.Dir(strictcliPackagePath)
}

// runningMainModule is the running binary's main module path, from its build
// information.
func runningMainModule() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "" {
		return "", false
	}
	return info.Main.Path, true
}

// runLintFrameworkUse runs the scan on the working directory and writes the
// report (§28.1): findings on stdout, exit 1 when there is one; a refusal as
// one error line on stderr, exit 1.
func (a *App) runLintFrameworkUse(stdout, stderr *strings.Builder) int {
	root, err := os.Getwd()
	if err != nil {
		stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	identity := a.lintMainModule
	if identity == nil {
		identity = runningMainModule
	}
	module, ok := identity()
	if !ok {
		module = ""
	}
	findings, refusal := lintGoProgram(root, module)
	if refusal != "" {
		stderr.WriteString("error: " + refusal + "\n")
		return 1
	}
	for _, f := range findings {
		stdout.WriteString(f + "\n")
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// lintGoProgram scans the Go program rooted at root whose running main module
// is mainModule. It returns the finding lines in §28.1's order, or a refusal.
func lintGoProgram(root, mainModule string) ([]string, string) {
	modPath, ok := readModulePath(filepath.Join(root, lintGoManifest))
	if !ok {
		return nil, errLintFrameworkUseNoManifest(lintGoManifest, root)
	}
	if modPath != mainModule {
		return nil, errLintFrameworkUseManifestMismatch(lintGoManifest, root)
	}
	files, ok := lintRepoFiles(root)
	if !ok {
		return nil, errLintFrameworkUseNotWorkTree(root)
	}

	// Group the module's own non-test Go files by directory.
	dirs := map[string][]string{}
	for _, rel := range files {
		dirs[path.Dir(rel)] = append(dirs[path.Dir(rel)], rel)
	}
	// Every file of the module is read up to its imports, which is what
	// deciding the program's packages needs; a scanned file is then parsed
	// whole. A file the scan must read and cannot parse is refused (§28.2).
	fset := token.NewFileSet()
	headers := map[string]*ast.File{}
	for _, rel := range files {
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ImportsOnly)
		if err != nil {
			return nil, errLintFrameworkUseUnparsable(rel, parseErrorDetail(err))
		}
		headers[rel] = f
	}
	parse := func(rel string) *ast.File { return headers[rel] }

	// Roots: every package main directory whose files import strictcli.
	type pkgKey struct {
		dir  string
		main bool
	}
	scanned := map[pkgKey]bool{}
	var queue []pkgKey
	enqueue := func(k pkgKey) {
		if !scanned[k] {
			scanned[k] = true
			queue = append(queue, k)
		}
	}
	strictcliModule := strictcliModulePath()
	dirNames := make([]string, 0, len(dirs))
	for d := range dirs {
		dirNames = append(dirNames, d)
	}
	sort.Strings(dirNames)
	for _, d := range dirNames {
		for _, rel := range dirs[d] {
			f := parse(rel)
			if f.Name.Name != "main" {
				continue
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p == strictcliModule || strings.HasPrefix(p, strictcliModule+"/") {
					enqueue(pkgKey{dir: d, main: true})
				}
			}
		}
	}

	// Closure over the module's own imports.
	var scanFiles []string
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, rel := range dirs[k.dir] {
			f := parse(rel)
			if (f.Name.Name == "main") != k.main {
				continue
			}
			scanFiles = append(scanFiles, rel)
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				var dir string
				switch {
				case p == modPath:
					dir = "."
				case strings.HasPrefix(p, modPath+"/"):
					dir = strings.TrimPrefix(p, modPath+"/")
				default:
					continue
				}
				if _, ok := dirs[dir]; ok {
					enqueue(pkgKey{dir: dir, main: false})
				}
			}
		}
	}

	var findings []lintFinding
	for _, rel := range scanFiles {
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			return nil, errLintFrameworkUseUnparsable(rel, parseErrorDetail(err))
		}
		findings = append(findings, lintGoFile(fset, rel, f)...)
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if a.line != b.line {
			return a.line < b.line
		}
		if a.rule != b.rule {
			return a.rule < b.rule
		}
		return a.message < b.message
	})
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = fmt.Sprintf("%s:%d: %s: %s", f.path, f.line, f.rule, f.message)
	}
	return lines, ""
}

// parseErrorDetail is the parser's own message for a file's first syntax
// error, with its line and column, without the file name the refusal already
// carries.
func parseErrorDetail(err error) string {
	if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
		e := list[0]
		return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Column, e.Msg)
	}
	return err.Error()
}

// lintFinding is one finding line's parts.
type lintFinding struct {
	path    string
	line    int
	rule    string
	message string
}

// readModulePath reads the module path from a go.mod file.
func readModulePath(p string) (string, bool) {
	f, err := os.Open(p)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			if unq, err := strconv.Unquote(fields[1]); err == nil {
				return unq, true
			}
			return fields[1], true
		}
	}
	return "", true
}

// lintRepoFiles lists the repository-owned, non-test Go files of the module
// rooted at root (§28.2, §11.2's input rule), relative to root with "/"
// separators. vendor and testdata directories and nested modules are not the
// module's own packages. It reports false when root is not in a git work tree.
func lintRepoFiles(root string) ([]string, bool) {
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	all := strings.Split(string(out), "\x00")
	var nested []string
	for _, rel := range all {
		if path.Base(rel) == lintGoManifest && path.Dir(rel) != "." {
			nested = append(nested, path.Dir(rel)+"/")
		}
	}
	seen := map[string]bool{}
	var files []string
	for _, rel := range all {
		if rel == "" || seen[rel] || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		seen[rel] = true
		skip := false
		for _, seg := range strings.Split(path.Dir(rel), "/") {
			if seg == "vendor" || seg == "testdata" {
				skip = true
			}
		}
		for _, n := range nested {
			if strings.HasPrefix(rel, n) {
				skip = true
			}
		}
		if !skip {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files, true
}

// lintGoFile finds the refused constructs in one file. Package identifiers are
// resolved through the file's imports (§28.3): the default name, an explicit
// alias, and a dot import; a blank import binds nothing. Resolution is by
// import, not by scope.
func lintGoFile(fset *token.FileSet, rel string, f *ast.File) []lintFinding {
	var out []lintFinding
	add := func(pos token.Pos, rule, construct string) {
		out = append(out, lintFinding{path: rel, line: fset.Position(pos).Line, rule: rule, message: lintMessage(rule, construct)})
	}

	named := map[string]string{} // local name -> import path
	var dotted []string          // import paths imported with "."
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if rule, ok := lintGoImports[p]; ok {
			add(imp.Pos(), rule, p)
		}
		switch {
		case imp.Name == nil:
			named[importDefaultName(p)] = p
		case imp.Name.Name == "_":
		case imp.Name.Name == ".":
			dotted = append(dotted, p)
		default:
			named[imp.Name.Name] = p
		}
	}

	// Identifiers that are not references to a package-level name: the
	// selected half of a selector, declared names, field names, and struct
	// literal keys.
	notRefs := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			notRefs[x.Sel] = true
		case *ast.FuncDecl:
			notRefs[x.Name] = true
		case *ast.ValueSpec:
			for _, id := range x.Names {
				notRefs[id] = true
			}
		case *ast.TypeSpec:
			notRefs[x.Name] = true
		case *ast.Field:
			for _, id := range x.Names {
				notRefs[id] = true
			}
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				notRefs[id] = true
			}
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						notRefs[id] = true
					}
				}
			}
		case *ast.LabeledStmt:
			notRefs[x.Label] = true
		case *ast.BranchStmt:
			if x.Label != nil {
				notRefs[x.Label] = true
			}
		}
		return true
	})
	if f.Name != nil {
		notRefs[f.Name] = true
	}
	for _, imp := range f.Imports {
		if imp.Name != nil {
			notRefs[imp.Name] = true
		}
	}

	// isExitNow reports whether a call's function is strictcli's ExitNow.
	isExitNow := func(fun ast.Expr) bool {
		switch x := fun.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			return ok && x.Sel.Name == "ExitNow" && named[id.Name] == strictcliPackagePath
		case *ast.Ident:
			if x.Name != "ExitNow" {
				return false
			}
			for _, p := range dotted {
				if p == strictcliPackagePath {
					return true
				}
			}
		}
		return false
	}

	var walk func(n ast.Node, inGoLit bool)
	walk = func(n ast.Node, inGoLit bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				if lit, ok := x.Call.Fun.(*ast.FuncLit); ok {
					walk(lit, true)
					for _, arg := range x.Call.Args {
						walk(arg, inGoLit)
					}
					return false
				}
			case *ast.CallExpr:
				if inGoLit && isExitNow(x.Fun) {
					add(x.Pos(), "exit-now-in-goroutine", "")
				}
				if id, ok := x.Fun.(*ast.Ident); ok && lintGoBuiltins[id.Name] {
					add(id.Pos(), "stderr-write", id.Name)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					if p, ok := named[id.Name]; ok {
						construct := p + "." + x.Sel.Name
						if rule, ok := lintGoSelectors[construct]; ok {
							add(x.Pos(), rule, construct)
						}
					}
				}
			case *ast.Ident:
				if notRefs[x] || len(dotted) == 0 {
					return true
				}
				for _, p := range dotted {
					construct := p + "." + x.Name
					if rule, ok := lintGoSelectors[construct]; ok {
						add(x.Pos(), rule, construct)
					}
				}
			}
			return true
		})
	}
	walk(f, false)
	return out
}

// importDefaultName is the name an import binds without an alias: the last
// path element, skipping a major-version suffix.
func importDefaultName(p string) string {
	base := path.Base(p)
	if len(base) > 1 && base[0] == 'v' {
		if _, err := strconv.Atoi(base[1:]); err == nil {
			base = path.Base(path.Dir(p))
		}
	}
	return base
}
