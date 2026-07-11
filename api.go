package main

import (
	"bufio"
	"net/http"
	"fmt"
	"log"
	"strings"
	"path/filepath"
	"time"
	"bytes"
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
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	size, err := dirSize(h.root)
	if err != nil {
		log.Printf("Could not measure size of root dir")
		bw.WriteString("Could not measure size of root dir\n")

		w.WriteHeader(500)
		w.Write(bw.Bytes())

		return
	}

	bw.WriteString(fmt.Sprintf(
		"gitbox server\n" +
		"version: %s\n" + 
		"uptime: %s\n" +
		"total storage: %d\n" +
		"total http requests: %d\n",
		gitboxVersion,
		time.Now().Sub(h.startup),
		size,
		h.totalRequests.Load(),
	))

	w.Write(bw.Bytes())
}

func (h *handler) apiEffectiveConfig(w http.ResponseWriter) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	out, err := yaml.Marshal(h.effectiveConfig)

	if err != nil {
		bw.WriteString(fmt.Sprintf("Error: %v\n", err))
		w.WriteHeader(500)
		w.Write(bw.Bytes())
		return
	}

	bw.Write(out)
	w.Write(bw.Bytes())
}

func (h *handler) apiFetch(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	if h.fetchRepo(repo, logg, "https") {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}
	
	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
}

func (h *handler) apiFetchHttp(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	if h.fetchRepo(repo, logg, "http") {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}
	
	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
}

func (h *handler) apiPin(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	if h.pinRepo(repo, logg) {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}
	
	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
}

func (h *handler) apiUnpin(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	if h.unpinRepo(repo, logg) {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}
	
	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
}

func (h *handler) apiRefresh(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	path := filepath.Clean(h.root + "/" + repo)

	if h.refreshRepo(path, logg) {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}

	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
}

func (h *handler) apiRefreshAll(w http.ResponseWriter) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	repos := h.getRepos()

	fail := false

	for path, d := range repos {
		logg.Printf("Refreshing %s", d.repo)
		if h.refreshRepo(path, logg) {
			logg.Printf("Success\n")
			continue
		}
		logg.Printf("Fail\n")
		fail = true
	}

	if fail {
		w.WriteHeader(500)
	}
	w.Write(bw.Bytes())
}

func (h *handler) apiEvict(w http.ResponseWriter, repo string) {
	bw := &bytes.Buffer{}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	logg := log.New(bw, "", log.LstdFlags)

	if h.evictRepo(repo, logg) {
		logg.Printf("Success\n")
		w.Write(bw.Bytes())
		return
	}

	logg.Printf("Fail\n")
	w.WriteHeader(500)
	w.Write(bw.Bytes())
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

	if strings.HasPrefix(req.URL.Path, "/-/fetch-http/") {
		h.apiFetchHttp(w, req.URL.Path[len("/-/fetch-http"):])
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

