package main

import (
	"testing"
	"path/filepath"
	"strings"
	"os"
)

func assertEq(t *testing.T, explain, want, got string) {
	if got != want {
		t.Errorf("%s: got %s, wanted %s", explain, got, want)
	}
}

func makeTestHandler(root string) *handler {
	config := defaultConfig()
	config.Root = root
	
	gitdir := config.getGitDir()
	backend := NewPath(filepath.Join(strings.TrimSpace(gitdir), "git-http-backend"))

 	return newHandler(&config, backend)	
}

func makeFakeRoot(t *testing.T) string {
	tmpDir := os.TempDir()
	fakeRoot, err := os.MkdirTemp(tmpDir, "lorebox-test-r-*")

	if err != nil {
		t.Fatalf("Failed to create a fake root: %v\n", err)
	}

	return fakeRoot
}

