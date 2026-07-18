package main

import (
	"context"
	"time"
	"io"
	"os/exec"
	"os"
)

type gitRunner struct {
	path string
	timeout time.Duration
	out io.Writer
}

func (g *gitRunner) run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.path
	cmd.Stdout = g.out
	cmd.Stderr = g.out
	return cmd.Run()
}

func (g *gitRunner) output(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.path
	cmd.Stdout = g.out
	cmd.Stderr = g.out
	return cmd.Output()
}

func (h *handler) gitInit(path string) error {
	git := gitRunner{h.root, h.gitTimeout, os.Stdout}

	return git.run("init", "--bare", path)
}
