package utils

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CopyStringFile
func CopyStringFile(src string, dest string) (string, error) {
	str, err := ReadStringFile(src)
	if err != nil {
		return "", err
	}
	err = WriteStringFile(dest, str)
	if err != nil {
		return "", err
	}
	return str, nil
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

// WriteBytesFile writes the given file contents to the given path.
func WriteBytesFile(path string, content []byte) error {
	// fmt.Printf("Writing bytes to: %s\n", path)
	if err := os.MkdirAll(filepath.Dir(path), 0770); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(content)
	return err
}

// WriteJSONFile writes the given file contents to the given path.
func WriteJSONFile(path string, data any) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return WriteBytesFile(path, content)
}

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

// WriteTempStringFile writes the given file contents to the given path with a
// random id before the file type suffix (separared by a ".") if one is
// present, or appended at the end otherwise.
func WriteTempStringFile(path string, content string) (string, error) {
	bs := make([]byte, 8)
	_, err := rand.Read(bs)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %v", err)
	}

	if idx := strings.LastIndex(path, "."); idx > 0 {
		path = fmt.Sprintf("%s_%x.%s", path[0:idx], bs[:8], path[idx+1:])
	} else {
		path = fmt.Sprintf("%s_%x", path, bs[:8])
	}

	return path, WriteStringFile(path, content)
}
