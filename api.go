package main

import (
	"bufio"
	"net/http"
	"fmt"
	"log"
	"strings"
	"path/filepath"
	"time"
	"go.yaml.in/yaml/v4"
)

func (h *handler) apiList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	var totalSize int64

	bw.WriteString(fmt.Sprintf("Total %d repos\n", len(h.repos)))
	bw.WriteString(fmt.Sprintf("% -36s % -16s % -28s Pinned\n",
		"Repository",
		"Size on disk",
		"Last refresh error timestamp",
	))

	for _, d := range h.repos {
		lastErr := fmt.Sprintf("%s", d.lastErr)
		if d.lastErr.IsZero() {
			lastErr = "-"
		}
		pinned := "No"
		if h.repoPinned(d.repo, log.Default()) {
			pinned = "Yes"
		}
		bw.WriteString(fmt.Sprintf("% -36s %0-16d % -28s %s\n", d.repo, d.size, lastErr, pinned))
		totalSize += d.size
	}

	bw.WriteString(fmt.Sprintf("Total %d bytes (%d kilobytes)\n", totalSize, totalSize/1024))

	bw.Flush()
}

func (h *handler) apiStatus(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	size, err := dirSize(h.root)
	if err != nil {
		log.Printf("Could not measure size of root dir")
		return
	}

	bw.WriteString(fmt.Sprintf("gitbox server\nversion: %s\nuptime: %s\ntotal storage: %d\n",
		gitboxVersion,
		time.Now().Sub(h.startup),
		size,
	))

	bw.Flush()
}

func (h *handler) apiEffectiveConfig(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	out, err := yaml.Marshal(h.effectiveConfig)

	if err != nil {
		bw.WriteString(fmt.Sprintf("Error: %v\n", err))
		return
	}

	bw.Write(out)
}

func (h *handler) apiFetch(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	if h.fetchRepo(repo, logg) {
		logg.Printf("Success\n")
		return
	}
	
	logg.Printf("Fail\n")
}

func (h *handler) apiPin(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	if h.pinRepo(repo, logg) {
		logg.Printf("Success\n")
		return
	}
	
	logg.Printf("Fail\n")
}

func (h *handler) apiUnpin(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	if h.unpinRepo(repo, logg) {
		logg.Printf("Success\n")
		return
	}
	
	logg.Printf("Fail\n")
}

func (h *handler) apiRefresh(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	path := filepath.Clean(h.root + "/" + repo)

	if h.refreshRepo(path, logg) {
		logg.Printf("Success\n")
		return
	}

	logg.Printf("Fail\n")
}

func (h *handler) apiRefreshAll(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	repos := h.getRepos()

	for path, d := range repos {
		logg.Printf("Refreshing %s", d.repo)
		if h.refreshRepo(path, logg) {
			logg.Printf("Success\n")
			continue
		}
		logg.Printf("Fail\n")
	}

}

func (h *handler) apiEvict(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	logg := log.New(bw, "", log.LstdFlags)

	if h.evictRepo(repo, logg) {
		logg.Printf("Success\n")
		return
	}

	logg.Printf("Fail\n")
}

func (h *handler) api(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/-/list" {
		h.apiList(w)
		return
	}

	if req.URL.Path == "/-/status" {
		h.apiStatus(w)
		return
	}

	if req.URL.Path == "/-/effective-config" {
		h.apiEffectiveConfig(w)
		return
	}

	if req.URL.Path == "/-/refresh-all" {
		h.apiRefreshAll(w)
		return
	}

	if strings.HasPrefix(req.URL.Path, "/-/fetch/") {
		h.apiFetch(w, req.URL.Path[len("/-/fetch"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/-/pin/") {
		h.apiPin(w, req.URL.Path[len("/-/pin"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/-/unpin/") {
		h.apiUnpin(w, req.URL.Path[len("/-/unpin"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/-/refresh/") {
		h.apiRefresh(w, req.URL.Path[len("/-/refresh"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/-/evict/") {
		h.apiEvict(w, req.URL.Path[len("/-/evict"):])
		return
	}

	// Invalid API point
	h.serve400(w)
}

