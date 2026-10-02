package main

// This defines three types: Path, RepoPath, and IRPath. All three have
// underlying type string, but add clean semantics of conversion
//   Path: absolute path within a fileystem
// 	 RepoPath: path relative to document root 
//   IRPath (IntraRepoPath): path relative to a repo directory

import (
	"path/filepath"
	"strings"
)

type Path string
type RepoPath string
type IRPath string

// NewPath constructs new Path form a string, cleaning up the filepath
func NewPath(s string) Path {
	return Path(filepath.Clean(s))
}

// NewRepoPath constructs new RepoPath, cleaning up the filepath and ensuring
// it starts with '/'
func NewRepoPath(s string) RepoPath {
	return RepoPath(filepath.Clean("/" + s))
}

// RepoPath converts a Path into RepoPath by removing the document root
// prefix. For example, if document root is /srv/www, and the Path is
// /srv/www/example.com/user1/repo1, the resultant RepoPath will
// /example.com/user1/repo
func (p Path) RepoPath(h *handler) RepoPath {
	if strings.HasPrefix(p.S(), h.root.S() + "/") {
		return RepoPath(p[len(h.root):])
	}
	return RepoPath(p)
}

// Path converts a RepoPath to Path by prepending the document root. If the
// RepoPath is /example.com/user1/repo1, and the document root is /srv/www, the
// resultant Path is /srv/www/example.com/user1/repo1
func (rp RepoPath) Path(h *handler) Path {
	return h.root.Concat(rp.S())
}

// Concat appends string s and returns the resultant Path. s may start both with
// a '/' or without one, the result will be the same
func (p Path) Concat(s string) Path {
	return Path(filepath.Clean(p.S() + "/" + s))
}

// Concat appends string s and returns the resultant RepoPath. s may start both
// with a '/' and without one, the result will be the same
func (p RepoPath) Concat(s string) RepoPath {
	return RepoPath(filepath.Clean(p.S() + "/" + s))
}

// S() converts RepoPath into a string
func (rp RepoPath) S() string {
	return string(rp)
}

// S() converts Path into a string
func (p Path) S() string {
	return string(p)
}

// S() converts IRPath into a string
func (p IRPath) S() string {
	return string(p)
}

// expand tries to resolve all symlinks and relative paths (i.e. '.', '..')
// within a Path. If unsuccessfull, it returns ("", error). On success, returns
// (Path, nil)
func (p Path) expand() (Path, error) {
	spath, err := filepath.Abs(p.S())

	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(spath)

	if err != nil {
		return NewPath(spath), nil
	}

	return NewPath(resolved), nil
}

// intraRepoPath tries to transform a path in relation to the document root (the
// RepoPath) into a path in relation to a repo itself (the IRPath). It does that
// by looking through the list of detected repos and checking if one of them is
// a prefix of the path
func (path Path) intraRepoPath(h *handler) (IRPath, bool) {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	for p := range h.repos {
		ps := p.S() + "/"
		if strings.HasPrefix(path.S(), ps) {
			return IRPath(path.S()[len(p):]), true
		}
	}

	return IRPath(path.S()), false
}


// selfHosted transforms a RepoPath into a self-hosted repo path by adding a
// '/~/' prefix, if it is not present
func (p RepoPath) selfHosted() RepoPath {
	if !strings.HasPrefix(p.S(), "/~/") {
		return RepoPath("/~/").Concat(p.S())
	}
	return p
}

// tmpDir returns the absolute filepath of the temporary directory docroot/.tmp
func (h *handler) tmpDir() Path {
	return h.root.Concat("/.tmp")
}
