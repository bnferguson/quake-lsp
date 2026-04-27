package gotasks

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// TaskFunc represents a discovered Go function that can be used as a task
type TaskFunc struct {
	Name         string   // Task name (custom or lowercase function name)
	FunctionName string   // Original Go function name
	Namespace    string   // Optional namespace from comment
	Description  string   // Description from comment
	SourceFile   string   // Source file path
	Package      string   // Package name
	Params       []string // Parameter names
	HasError     bool     // Whether function returns error
}

// DiscoverTasks finds all exported functions in Go files within the given directory
func DiscoverTasks(dir string) ([]TaskFunc, error) {
	var tasks []TaskFunc

	// Walk the directory to find .go files
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-Go files
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Parse the Go file
		fileTasks, err := parseGoFile(path)
		if err != nil {
			// Skip files that can't be parsed
			fmt.Printf("Warning: failed to parse %s: %v\n", path, err)
			return nil
		}

		tasks = append(tasks, fileTasks...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// parseGoFile parses a single Go file and extracts exported functions
func parseGoFile(filename string) ([]TaskFunc, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Only process files with package main
	if node.Name.Name != "main" {
		return nil, nil
	}

	var tasks []TaskFunc

	// Visit all declarations
	for _, decl := range node.Decls {
		// Look for function declarations
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Skip methods (functions with receivers)
		if fn.Recv != nil {
			continue
		}

		// Skip the main function itself
		if fn.Name.Name == "main" {
			continue
		}

		// Skip non-exported functions
		if !ast.IsExported(fn.Name.Name) {
			continue
		}

		// Check if this is a valid task function signature
		task := analyzeFunction(fn, filename, node.Name.Name)
		if task != nil {
			// Extract comment and parse for custom name/namespace
			if fn.Doc != nil {
				parseTaskComment(fn.Doc, task)
			}
			tasks = append(tasks, *task)
		}
	}

	return tasks, nil
}

// analyzeFunction checks if a function has a valid task signature
func analyzeFunction(fn *ast.FuncDecl, filename, pkgName string) *TaskFunc {
	task := &TaskFunc{
		Name:         strings.ToLower(fn.Name.Name),
		FunctionName: fn.Name.Name, // Store the original function name
		Description:  "",
		SourceFile:   filename,
		Package:      pkgName,
		Params:       []string{},
		HasError:     false,
	}

	// Check parameters - we support:
	// 1. No parameters: func()
	// 2. String parameters: func(arg1 string, arg2 string, ...)
	// 3. Variadic string: func(args ...string)
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
		for _, param := range fn.Type.Params.List {
			// Check if it's a string or ...string type
			if !isStringParam(param.Type) {
				// Invalid parameter type for a task
				return nil
			}

			// Add parameter names
			if isVariadicString(param.Type) {
				// For variadic parameters, mark with special suffix
				for _, name := range param.Names {
					task.Params = append(task.Params, name.Name+"...")
				}
			} else {
				for _, name := range param.Names {
					task.Params = append(task.Params, name.Name)
				}
			}
		}
	}

	// Check return type - we support:
	// 1. No return: func()
	// 2. Error return: func() error
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		// Only support single error return
		if len(fn.Type.Results.List) != 1 {
			return nil
		}

		result := fn.Type.Results.List[0]
		if !isErrorType(result.Type) {
			return nil
		}

		task.HasError = true
	}

	return task
}

// isStringParam checks if a parameter type is string or ...string
func isStringParam(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "string"
	case *ast.Ellipsis:
		// Check if it's ...string
		if ident, ok := t.Elt.(*ast.Ident); ok {
			return ident.Name == "string"
		}
	}
	return false
}

// isVariadicString checks if a parameter type is ...string
func isVariadicString(expr ast.Expr) bool {
	if ellipsis, ok := expr.(*ast.Ellipsis); ok {
		if ident, ok := ellipsis.Elt.(*ast.Ident); ok {
			return ident.Name == "string"
		}
	}
	return false
}

// isErrorType checks if a type is error
func isErrorType(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name == "error"
	}
	return false
}

// parseTaskComment parses the comment for custom name/namespace and description
func parseTaskComment(doc *ast.CommentGroup, task *TaskFunc) {
	if doc == nil || len(doc.List) == 0 {
		return
	}

	// Get the full comment text
	var lines []string
	for _, comment := range doc.List {
		text := comment.Text
		// Remove // or /* */ markers
		text = strings.TrimPrefix(text, "//")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimSuffix(text, "*/")
		text = strings.TrimSpace(text)
		if text != "" {
			lines = append(lines, text)
		}
	}

	if len(lines) == 0 {
		return
	}

	firstLine := lines[0]

	// Check if the first line matches the pattern [name] :: or [namespace:name] ::
	// We're looking for a pattern like: word or word:word followed by ::
	if idx := strings.Index(firstLine, "::"); idx > 0 {
		// Extract the part before ::
		nameSpec := strings.TrimSpace(firstLine[:idx])

		// Check if it starts with [ and ends with ]
		if strings.HasPrefix(nameSpec, "[") && strings.HasSuffix(nameSpec, "]") {
			// Remove the brackets
			nameSpec = nameSpec[1 : len(nameSpec)-1]

			// Split on : to check for namespace
			if colonIdx := strings.Index(nameSpec, ":"); colonIdx > 0 {
				// Has namespace
				task.Namespace = strings.TrimSpace(nameSpec[:colonIdx])
				task.Name = strings.ToLower(strings.TrimSpace(nameSpec[colonIdx+1:]))
			} else {
				// No namespace, just custom name
				task.Name = strings.ToLower(strings.TrimSpace(nameSpec))
			}

			// The description is everything after ::
			task.Description = strings.TrimSpace(firstLine[idx+2:])

			// If description is empty but there are more lines, use the next line
			if task.Description == "" && len(lines) > 1 {
				task.Description = lines[1]
			}
		} else {
			// No brackets, just use the whole first line as description
			task.Description = firstLine
		}
	} else {
		// No :: pattern, just use the first line as description
		task.Description = firstLine
	}
}
