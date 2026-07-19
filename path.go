package main


import (
	"path/filepath"
	"strings"
)

type Path string
type RepoPath string
type IRPath string

func NewPath(s string) Path {
	return Path(filepath.Clean(s))
}

func NewRepoPath(s string) RepoPath {
	return RepoPath(filepath.Clean("/" + s))
}

// Identical to handler.chopRoot at the moment, but moved here to ensure
// behaviour stays constant
func (p Path) RepoPath(h *handler) RepoPath {
	if strings.HasPrefix(p.S(), filepath.Clean(h.root.S()) + "/") {
		return RepoPath(p[len(h.root):])
	}
	return RepoPath(p)
}

func (rp RepoPath) Path(h *handler) Path {
	return h.root.Concat(rp.S())
}

func (p Path) Concat(s string) Path {
	return Path(filepath.Clean(p.S() + "/" + s))
}

func (p RepoPath) Concat(s string) RepoPath {
	return RepoPath(filepath.Clean(p.S() + "/" + s))
}

func (rp RepoPath) S() string {
	return string(rp)
}

func (p Path) S() string {
	return string(p)
}

func (p IRPath) S() string {
	return string(p)
}

func (p Path) expand() (Path, error) {
	spath, err := filepath.Abs(p.S())

	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(spath)

	if err != nil {
		return p, nil
	}

	return Path(resolved), nil
}


func (h *handler) tmpDir() Path {
	return h.root.Concat("/.tmp")
}
