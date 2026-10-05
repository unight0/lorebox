package main

import (
	"os"
	"testing"
	"path/filepath"
	"strings"
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

func TestNewPaths(t *testing.T) {
	assertEq(t,
	"NewPath() should clean up the filepath",
	"/hello/world/c",
	NewPath("//hello/world/c/d/../").S())

	assertEq(t, 
	"NewRepoPath() should clean up the filepath and prepend '/'",
	"/1/2/3",
	NewRepoPath("1/2/3/").S())
}

func TestConcat(t *testing.T) {
	assertEq(t,
	"Path.Concat() should concatenate a string to a Path through '/' and clean up",
    "/a/a/b/b",
	NewPath("//a/a").Concat("b/b/").S())

	assertEq(t,
	"RepoPath.Concat() should concatenate a string to a RepoPath through '/' and clean up",
    "/a/a/b/b",
	NewRepoPath("//a/a").Concat("b/b/").S())
}

func TestSelfHosted(t *testing.T) {
	assertEq(t,
	"RepoPath.selfHosted() should append a '/~/' prefix",
    "/~/a/b/c/d",
	NewRepoPath("a/b/c/d/").selfHosted().S())

	assertEq(t,
	"RepoPath.selfHosted() should not append the '/~/' prefix if already present",
    "/~/a/b/c/d",
	NewRepoPath("~/a/b/c/d").selfHosted().S())
}

// TODO: conversion functions, anything that deals with handler

func TestTmpDir(t *testing.T) {
	h := makeTestHandler("/a/b/c")

	assertEq(t,
	"tmpDir() should return the $(h.root)/.tmp",
    "/a/b/c/.tmp",
	h.tmpDir().S())
}

func TestExpand(t *testing.T) {
	fakeRoot := makeFakeRoot(t)
	defer os.RemoveAll(fakeRoot)

	fakeRootPath := NewPath(fakeRoot)

	file1, err := os.Create(fakeRootPath.Concat("file").S())
	if err != nil {
		t.Fatalf("Could not create temporary file: %v\n", err)
	}
	defer file1.Close()

	dir1 := fakeRootPath.Concat("dir1").S()
	err = os.Mkdir(dir1, 0o700)
	if err != nil {
		t.Fatalf("Could not create temporary directory: %v\n", err)
	}

	dir2 := NewPath(dir1).Concat("dir2").S()
	err = os.Mkdir(dir2, 0o700)
	if err != nil {
		t.Fatalf("Could not create temporary directory: %v\n", err)
	}

	lnName := "link1"
	lnPath := NewPath(dir1).Concat(lnName)

	err = os.Symlink(file1.Name(), lnPath.S())
	if err != nil {
		t.Fatalf("Could not create link: %v\n", err)
	}

	// Directory structure at this point:
	// tmp
	//  fakeRoot
	//   file1
	//   dir1
	//    link1 -> /tmp/fakeRoot/file1
	//    dir2

	// Path tested:
	// /tmp/fakeRoot/dir1/dir2/../link1 === /tmp/fakeRoot/file1

	exp, err := NewPath(dir2).Concat("..").Concat(lnName).expand()
	if err != nil {
		t.Errorf("Failed to run Path.expand(): %v", err)
	}

	assertEq(t,
	"Path.expand() should expand symlinks and relative paths",
	file1.Name(),
	exp.S())
}

func TestIntraRepoPath(t *testing.T) {
	h := makeTestHandler("/R/")
	h.repos = make(map[Path]repoDescription)
	h.repos[NewPath("/R/repo1/")] = repoDescription{}
	h.repos[NewPath("/R/repo2/")] = repoDescription{}
	h.repos[NewPath("/R/repo3/")] = repoDescription{}

	for p := range h.repos {
		irp, s := p.Concat("path/in/repo/").intraRepoPath(h)
		if !s {
			t.Errorf("%s should have something to chop", irp.S())
		}
		assertEq(t,
		"Path.intraRepoPath() should yield a path inside a repo",
		"/path/in/repo",
		irp.S())
	}

	irp, s := NewPath("/repo1/path/in/repo/").intraRepoPath(h)
	if s {
		t.Errorf("%s should not have anything to chop", irp.S())
	}

	irp, s = NewPath("/repo2/path/in/repo/").intraRepoPath(h)
	if s {
		t.Errorf("%s should not have anything to chop", irp.S())
	}
}
