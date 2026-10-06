package main

import ( 
	"testing"
	"net/http"
	"bufio"
	"bytes"
)

// implements http.ResponseWriter
type dummyHTTPWriter struct {
	header http.Header
	buffer bytes.Buffer
	headerWritten bool
	statusCode int
}

func (d *dummyHTTPWriter) Header() http.Header {
	return d.header
}

func (d *dummyHTTPWriter) Write(b []byte) (int, error) {
	if (!d.headerWritten) {
		d.WriteHeader(200)
	}
	return d.buffer.Write(b)
}

func (d *dummyHTTPWriter) WriteHeader(code int) {
	d.headerWritten = true
	d.statusCode = code
}

func makeDummyHTTPWriter() dummyHTTPWriter {
	d := dummyHTTPWriter{}
	d.header = make(http.Header)
	d.buffer = bytes.Buffer{}
	return d
}

// TestRepoTxt tests that /repos.txt is accurate to the actual present repos. This does
// not check if hidden repos are shown or not (they shouldn't be)
func TestRepoTxt(t *testing.T) {
	h := makeTestHandler("/R/")
	h.repos = make(map[Path]repoDescription)
	h.repos[NewPath("/R/repo1/")] = repoDescription{repo: RepoPath("/repo1")}
	h.repos[NewPath("/R/repo2/")] = repoDescription{repo: RepoPath("/repo2")}
	h.repos[NewPath("/R/repo3/")] = repoDescription{repo: RepoPath("/repo3")}

	wr := makeDummyHTTPWriter()

	h.serveRepoIndex(&wr)

	listed := map[string]bool{}

	// Initialize
	for _, d := range h.repos {
		listed[d.repo.S()] = false
	}

	sc := bufio.NewScanner(&wr.buffer)

	// Tick off the ones that are present
	for sc.Scan() && sc.Err() == nil {
		repo := sc.Text()
		listed[repo] = true
		if repo == "" {
			break
		}
	}

	// Find any that are not 'ticked off'
	for r, v := range listed {
		if !v {
			t.Errorf("Repo is not listed in /repos.txt: %s\n", r)
		}
	}
}
