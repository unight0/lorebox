package main

import (
	"log"
	"net/http"
	"bufio"
	"sort"
	"fmt"
	_ "embed"
)

//go:embed static/400.html
var html400 []byte
//go:embed static/401.html
var html401 []byte
//go:embed static/404.html
var html404 []byte
//go:embed static/405.html
var html405 []byte
//go:embed static/500.html
var html500 []byte
//go:embed static/robots.txt
var robotstxt []byte
//go:embed static/generic-begin.html
var htmlGenericBegin []byte
//go:embed static/generic-end.html
var htmlGenericEnd []byte
//go:embed static/style.css
var cssStyle string

func genericPage(code int, page []byte, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)

	_, err := w.Write(page)

	if err != nil {
		log.Printf("%d write: %v", code, err)
	}
}

// Doesn't exist
func serve404(w http.ResponseWriter) {
	genericPage(404, html404, w);
}

// Method not allowed
func serve405(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST, HEAD")
	genericPage(405, html405, w);
}

// Only GET and POST are allowed
func serve405NoHead(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST")
	genericPage(405, html405, w);
}

// Client error; invalid request
func serve400(w http.ResponseWriter) {
	genericPage(400, html400, w);
}

// Authentication requred 
func serve401(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Basic realm=\"lorebox\"")
	genericPage(401, html401, w);
}

// Internal server error
func serve500(w http.ResponseWriter) {
	genericPage(500, html500, w);
}

// /robots.txt 
func serveRobots(w http.ResponseWriter) {
	genericPage(200, robotstxt, w);
}

// /repos.txt
func (h *handler) serveRepoIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	h.reposLock.RLock()
	repos := make([]string, 0, len(h.repos))
	for _, d := range h.repos {
		if !h.repoHidden(d.repo, log.Default()) {
			repos = append(repos, d.repo.S())
		}
	}
	h.reposLock.RUnlock()

	sort.Strings(repos)

	for _, p := range repos {
		fmt.Fprintf(bw, "%s\n", p)
	}
}
