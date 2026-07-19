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

// Doesn't exist
func (h *handler) serve404(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(404)

	_, err := w.Write(html404)

	if err != nil {
		log.Printf("404 write: %v", err)
	}
}

// Client error; invalid request
func (h *handler) serve400(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(400)

	_, err := w.Write(html400)

	if err != nil {
		log.Printf("400 write: %v", err)
	}
}

// Authentication requred 
func (h *handler) serve401(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("WWW-Authenticate", "Basic realm=\"lorebox\"")
	w.WriteHeader(401)

	_, err := w.Write(html401)

	if err != nil {
		log.Printf("401 write: %v", err)
	}
}

// Internal server error
func (h *handler) serve500(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(500)

	_, err := w.Write(html500)

	if err != nil {
		log.Printf("500 write: %v", err)
	}
}

// /robots.txt 
func (h *handler) serveRobots(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	_, err := w.Write(robotstxt)

	if err != nil {
		log.Printf("robots.txt write: %v", err)
	}
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
			repos = append(repos, d.repo)
		}
	}
	h.reposLock.RUnlock()

	sort.Strings(repos)

	for _, p := range repos {
		fmt.Fprintf(bw, "%s\n", p)
	}
}
