package main

import "testing"

func assertEq(t *testing.T, explain, want, got string) {
	if got != want {
		t.Errorf("%s: got %s, wanted %s", explain, got, want)
	}
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
