package main

import (
	"context"
	"time"
	"io"
	"os/exec"
	"log"
	"fmt"
	"path/filepath"
	"errors"
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

func (h *handler) gitInit(path string, logg *log.Logger) bool {
	git := gitRunner{h.root, h.gitTimeout, logg.Writer()}

	err := git.run("init", "--bare", path)

	if err != nil {
		return false
	}

	git.path = path
	
	if err := git.run("config", "http.receivepack", "true"); err != nil {
		return false
	}

	if err := git.run("config", "lorebox.hidden", "true"); err != nil {
		logg.Printf("Failed to hide %s: %v", path, err)
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

func (h *handler) pinRepo(repo string, logg *log.Logger) bool {
	if selfHosted(repo) {
		logg.Printf("Cannot pin %s: repo is self-hosted", repo)
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	if err := git.run("config", "lorebox.pinned", "true"); err != nil {
		logg.Printf("Failed to pin %s: %v", repo, err)
		return false
	}

	return true
}

func (h *handler) unpinRepo(repo string, logg *log.Logger) bool {
	if selfHosted(repo) {
		logg.Printf("Cannot unpin %s: repo is self-hosted", repo)
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	if err := git.run("config", "--unset", "lorebox.pinned"); err != nil {
		logg.Printf("Failed to pin '%s': %v", repo, err)
		return false
	}

	return true
}

func (h *handler) repoPinned(repo string, logg *log.Logger) bool {
	if selfHosted(repo) {
		logg.Printf("Pinned/unpinned status is not applicable to self-hosted repo %s", repo)
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	if err := git.run("config", "--bool", "lorebox.pinned"); err != nil {
		if _, yes := errors.AsType[*exec.ExitError](err); yes {
			return false
		}

		logg.Printf("Failed to check if repo is pinned '%s': %v", repo, err)
		return false
	}

	// 0 exit code means it exists, in our case === true
	return true
}

func (h *handler) hideRepo(repo string, logg *log.Logger) bool {
	if !selfHosted(repo) {
		logg.Printf("Can't hide %s: repo is not self-hosted", repo)
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, logg.Writer()}	

	if err := git.run("config", "lorebox.hidden", "true"); err != nil {
		logg.Printf("Failed to hide %s: %v", repo, err)
		return false
	}

	return true
}

func (h *handler) unhideRepo(repo string, logg *log.Logger) bool {
	if !selfHosted(repo) {
		logg.Printf("Can't unhide %s: repo is not self-hosted", repo)
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, logg.Writer()}	

	if err := git.run("config", "--unset", "lorebox.hidden"); err != nil {
		logg.Printf("Failed to hide %s: %v", repo, err)
		return false
	}

	return true
}

func (h *handler) repoHidden(repo string, logg *log.Logger) bool {
	if !selfHosted(repo) {
		return false
	}

	path := filepath.Clean(h.root + "/" + repo)

	git := gitRunner{path, h.gitTimeout, io.Discard}

	if err := git.run("config", "--bool", "lorebox.hidden"); err != nil {
		if _, yes := errors.AsType[*exec.ExitError](err); yes {
			return false
		}

		logg.Printf("Failed to check if repo is hidden '%s': %v", repo, err)
		return false
	}

	// 0 exit code means it exists, in our case === true
	return true
}

func (h *handler) hiddenRepoPath(rpath string, logg *log.Logger) bool {
	if !selfHosted(rpath) {
		return false
	}

	owner, name := parseSelfHosted(rpath)
	
	// Could not parse
	if owner == "" || name == "" {
		return false
	}

	return h.repoHidden(fmt.Sprintf("/~/%s/%s", owner, name), logg)
}
