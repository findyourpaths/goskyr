package utils

import (
	"os"
	"path/filepath"
)

// WriteStringFile writes the given file contents to the given path.
func WriteStringFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0770); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}

// ReadStringFile returns a string with the data at the given path declared in a
// "data" attribute of a BUILD.bazel rule.
func ReadStringFile(path string) (string, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}
