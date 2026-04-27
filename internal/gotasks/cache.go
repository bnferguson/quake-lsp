package gotasks

import (
	"fmt"
	"os"
)

// TaskCache manages generated Go task dispatcher code
type TaskCache struct {
	tempFiles map[string]string // hash -> temp file path
}

// NewTaskCache creates a new task cache
func NewTaskCache() (*TaskCache, error) {
	return &TaskCache{
		tempFiles: make(map[string]string),
	}, nil
}

// GetDispatcherPath returns the path to the dispatcher file for go run
func (c *TaskCache) GetDispatcherPath(tasks []TaskFunc, qtasksDir string) (string, error) {
	if len(tasks) == 0 {
		return "", fmt.Errorf("no tasks to generate")
	}

	// Calculate hash of source files
	hash, err := CalculateSourceHash(tasks)
	if err != nil {
		return "", err
	}

	// Check if we already have this dispatcher generated
	if tempFile, exists := c.tempFiles[hash]; exists {
		if _, err := os.Stat(tempFile); err == nil {
			return tempFile, nil
		}
	}

	// Generate the dispatcher code
	tempFile, err := GenerateDispatcher(tasks, qtasksDir)
	if err != nil {
		return "", err
	}

	// Store the temp file for this hash
	c.tempFiles[hash] = tempFile

	return tempFile, nil
}

// Cleanup removes all temporary files
func (c *TaskCache) Cleanup() error {
	for _, file := range c.tempFiles {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			// Log but don't fail on cleanup errors
			fmt.Fprintf(os.Stderr, "Warning: failed to clean up %s: %v\n", file, err)
		}
	}
	c.tempFiles = make(map[string]string)
	return nil
}
