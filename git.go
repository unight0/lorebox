package main

import (
	"context"
	"time"
	"io"
	"os/exec"
	"log"
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

func (h *handler) gitInit(path string) bool {
	git := gitRunner{h.root, h.gitTimeout, os.Stdout}

	err := git.run("init", "--bare", path)

	if err != nil {
		return false
	}

	git.path = path
	err = git.run("config", "http.receivepack", "true")
	if err != nil {
		return false
	}

	return h.updateServerInfo(path, log.Default())
}

func (h *handler) updateServerInfo(path string, logg *log.Logger) bool {
	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	if err := git.run("update-server-info"); err != nil {
		logg.Printf("Failed to run update-server-info: %s, %v", path, err)
		return false
	}

	return true
}

