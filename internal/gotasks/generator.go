package gotasks

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/format"
	"io"
	"os"
	"strings"
	"text/template"
)

// GenerateDispatcher creates a dispatcher file that imports qtasks as a subpackage
func GenerateDispatcher(tasks []TaskFunc, qtasksDir string) (string, error) {
	if len(tasks) == 0 {
		return "", fmt.Errorf("no tasks to generate")
	}

	// Create a unique temp file in the qtasks directory so it can be compiled together
	tempFile, err := os.CreateTemp(qtasksDir, "quake_dispatcher_*.go")
	if err != nil {
		return "", err
	}
	tempPath := tempFile.Name()
	tempFile.Close()

	// Generate the main.go content
	content, err := generateMainContent(tasks, qtasksDir)
	if err != nil {
		os.Remove(tempPath)
		return "", err
	}

	// Format the generated code
	formatted, err := format.Source([]byte(content))
	if err != nil {
		// If formatting fails, write unformatted for debugging
		os.WriteFile(tempPath, []byte(content), 0644)
		return "", fmt.Errorf("failed to format generated code: %w", err)
	}

	// Write the formatted main.go
	if err := os.WriteFile(tempPath, formatted, 0644); err != nil {
		os.Remove(tempPath)
		return "", err
	}

	return tempPath, nil
}

// generateMainContent creates the main.go content
func generateMainContent(tasks []TaskFunc, qtasksDir string) (string, error) {
	// Generate a main function that will be compiled with other package main files
	tmpl := `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Error: task name required\n")
		os.Exit(1)
	}

	taskName := os.Args[1]
	args := os.Args[2:]

	switch taskName {
{{range .Tasks}}
	case "{{.Name}}":
		{{.CallCode}}
{{end}}
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown Go task '%s'\n", taskName)
		os.Exit(1)
	}
}
`

	// Create template data
	type TaskTemplate struct {
		Name           string
		ExportedName   string
		ParamSignature string
		HasError       bool
		CallCode       string
	}

	data := struct {
		Tasks []TaskTemplate
	}{
		Tasks: make([]TaskTemplate, len(tasks)),
	}

	for i, task := range tasks {
		// Build the full task name including namespace
		taskName := task.Name
		if task.Namespace != "" {
			taskName = task.Namespace + ":" + task.Name
		}

		// The exported function name is always the original Go function name
		exportedName := task.FunctionName

		data.Tasks[i] = TaskTemplate{
			Name:           taskName,
			ExportedName:   exportedName,
			ParamSignature: generateParamSignature(task.Params),
			HasError:       task.HasError,
			CallCode:       generateTaskCall(&task, exportedName),
		}
	}

	// Execute template
	var buf bytes.Buffer
	t := template.Must(template.New("main").Parse(tmpl))
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// generateParamSignature generates the parameter signature for forward declaration
func generateParamSignature(params []string) string {
	if len(params) == 0 {
		return ""
	}

	var parts []string
	for _, param := range params {
		if strings.HasSuffix(param, "...") {
			parts = append(parts, param[:len(param)-3]+" ...string")
		} else {
			parts = append(parts, param+" string")
		}
	}
	return strings.Join(parts, ", ")
}

// generateTaskCall generates the code to call a task function
func generateTaskCall(task *TaskFunc, fnCall string) string {
	var code strings.Builder

	// Handle parameters
	var argHandling string
	if len(task.Params) == 0 {
		// No parameters
		argHandling = ""
		fnCall += "()"
	} else if len(task.Params) > 0 && strings.HasSuffix(task.Params[0], "...") {
		// Variadic parameter
		argHandling = ""
		fnCall += "(args...)"
	} else {
		// Fixed parameters
		argChecks := []string{}
		argPassing := []string{}
		for i, param := range task.Params {
			argChecks = append(argChecks, fmt.Sprintf(`
		if len(args) <= %d {
			fmt.Fprintf(os.Stderr, "Error: task '%s' requires parameter '%s'\n")
			os.Exit(1)
		}`, i, task.Name, param))
			argPassing = append(argPassing, fmt.Sprintf("args[%d]", i))
		}
		argHandling = strings.Join(argChecks, "\n")
		fnCall += "(" + strings.Join(argPassing, ", ") + ")"
	}

	// Build the complete call code
	if argHandling != "" {
		code.WriteString(argHandling)
		code.WriteString("\n\t\t")
	}

	if task.HasError {
		code.WriteString(fmt.Sprintf(`if err := %s; err != nil {
			fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
			os.Exit(1)
		}`, fnCall))
	} else {
		code.WriteString(fnCall)
	}

	return code.String()
}

// CalculateSourceHash calculates a hash of all source files for caching
func CalculateSourceHash(tasks []TaskFunc) (string, error) {
	h := sha256.New()

	// Hash all unique source files
	seen := make(map[string]bool)
	for _, task := range tasks {
		if seen[task.SourceFile] {
			continue
		}
		seen[task.SourceFile] = true

		file, err := os.Open(task.SourceFile)
		if err != nil {
			return "", err
		}
		defer file.Close()

		if _, err := io.Copy(h, file); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
