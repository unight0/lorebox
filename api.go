package main

import (
	//"bufio"
	"net/http"
	"fmt"
	"log"
	"strings"
	"path/filepath"
	"time"
	"bytes"
	"os"
	"encoding/json"
	"go.yaml.in/yaml/v4"
)

func jsonFailure(description, status string) string {
	return fmt.Sprintf(`{"status":"%s; Status: %s"}`, description, status)
}

func (h *handler) apiList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	repos := jsonableRepos{Status: "success"}
	repos.Repos = map[string]jsonableRepo{}

	for p, d := range h.repos {
		repo := jsonableRepo {
			Name: d.repo,
			LastError: d.lastErr,
			Pinned: h.repoPinned(d.repo, log.Default()),
			Size: d.size,
			Requests: d.requests,
			SelfHosted: d.selfHosted,
			Hidden: h.repoHidden(d.repo, log.Default()),
		}

		repos.Repos[p] = repo
		repos.TotalSize += d.size
	}

	marsh, err := json.Marshal(repos)

	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(jsonFailure("Could not marshal repo list into JSON", repos.Status)))
		return
	}

	w.Write(marsh)
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

	status := jsonableStatus {
		Banner: "lorebox server",
		Status: "success",
		ID: fullSelfID(),
		Version: loreboxVersion,
		UptimeSec: int(time.Since(h.startup).Seconds()),
		TotalHTTPReqs: int(h.totalRequests.Load()),
	}
	status.Disk.Usage = size
	status.Disk.Max = h.maxDiskUsage
	status.Disk.Policy = h.diskUsagePolicy
	status.Cache.Hits = int(h.cacheHits.Load())
	status.Cache.Misses = int(h.cacheMisses.Load())

	marsh, err := json.Marshal(status)

	if err != nil {
		log.Print(err)
		status = jsonableStatus{}

		bw.WriteString(jsonFailure("Could not marshal status into JSON", status.Status))

		w.WriteHeader(500)
		w.Write(bw.Bytes())
		return
	}

	bw.Write(marsh)

	w.Write(bw.Bytes())
}

func (h *handler) apiEffectiveConfig(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/json; charset=utf-8")

	ec := jsonableEffectiveConfig{Status:"success"}

	out, err := yaml.Marshal(h.effectiveConfig)
	if err != nil {
		ec.Status = fmt.Sprintf("Error: %v\n", err)
	}

	ec.Config = string(out)

	marsh, err := json.Marshal(ec)

	if err != nil {
		w.WriteHeader(500)
		marsh = []byte(jsonFailure("Could not marshal response into JSON", ec.Status))
	}
	
	w.Write(marsh)
}

func (h *handler) apiFetch(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		return h.fetchRepo(repo, logg, "https")
	})
}

func (h *handler) apiFetchHttp(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		return h.fetchRepo(repo, logg, "http")
	})
}

func (h *handler) apiSimpleTr(w http.ResponseWriter, f func(*log.Logger) bool) {
	w.Header().Set("Content-Type", "text/json; charset=utf-8")

	op := jsonableOperation{Status:"success"}
	tr := &bytes.Buffer{}
	logg := log.New(tr, "", log.LstdFlags)

	if !f(logg) {
		op.Status = "Internal error"
	}

	op.Transcript = tr.String()

	marsh, err := json.Marshal(op)

	if err != nil {
		w.WriteHeader(500)
		marsh = []byte(jsonFailure("Could not marshal response into JSON", op.Status))
	}
	
	w.Write(marsh)
}

func (h *handler) apiPin(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.pinRepo(repo, logg)
	})
}

func (h *handler) apiUnpin(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.unpinRepo(repo, logg)
	})
}

func (h *handler) apiRefresh(w http.ResponseWriter, repo string) {
	path := filepath.Clean(h.root + "/" + repo)

	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.refreshRepo(path, logg)
	})
}

func (h *handler) apiEvict(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.evictRepo(repo, logg)
	})
}

func (h *handler) apiRefreshAll(w http.ResponseWriter) {

	repos := jsonableRefreshAll{Repos: map[string]jsonableRefresh{}}

	h.reposLock.RLock()
	for path, d := range h.repos {
		// Can't refresh a self-hosted repo
		if d.selfHosted {
			continue
		}

		repos.Repos[path] = jsonableRefresh{Name: d.repo, Status: "success"}
	}
	h.reposLock.RUnlock()


	fails := 0

	tr := &bytes.Buffer{}
	for p, r := range repos.Repos {
		tr.Reset()
		logg := log.New(tr, "", log.LstdFlags)
		if !h.refreshRepo(p, logg) {
			r.Status = "Refresh failed"
			fails++
		}
		r.Transcript = tr.String()
		repos.Repos[p] = r
	}

	if fails != 0 {
		w.WriteHeader(500)
	}

	repos.Fails = fails
	marsh, err := json.Marshal(repos)

	if err != nil {
		marsh = []byte(jsonFailure(
			"Failed to marshal refresh results into JSON",
			fmt.Sprintf("Fails: %d", fails),
		))
	}

	w.Write(marsh)
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

	// Invalid API endpoint
	h.serve400(w)
}

func (h *handler) apiSHCreate(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		repo = "/~" + repo

		if _, err := os.Stat(h.root + repo); err == nil {
			logg.Printf("Can't init %s: already exists", repo)
			return false
		}

		return h.gitInit(h.root + repo, logg)
	})
}

func (h *handler) apiSHDelete(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		repo = "/~" + repo
		logg.Printf("Removing %s...", repo)

		if _, err := os.Stat(h.root + repo); err != nil {
			logg.Printf("Could not stat %s: %v", repo, err)
			return false
		}
		
		err := os.RemoveAll(h.root + repo)
		if err != nil {
			logg.Printf("Could not remote %s: %v", repo, err)
			return false
		}
		return true
	})
}

func (h *handler) apiSHHide(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		repo = "/~" + repo
		return h.hideRepo(repo, logg)
	})
}

func (h *handler) apiSHUnhide(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func (logg *log.Logger) bool {
		repo = "/~" + repo
		return h.unhideRepo(repo, logg)
	})
}

func (h *handler) apiSelfHosted(w http.ResponseWriter, req *http.Request) {
	if strings.HasPrefix(req.URL.Path, "/+/create/") {
		h.apiSHCreate(w, req.URL.Path[len("/-/create"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/+/delete/") {
		h.apiSHDelete(w, req.URL.Path[len("/-/delete"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/+/hide/") {
		h.apiSHHide(w, req.URL.Path[len("/+/hide"):])
		return
	}

	if strings.HasPrefix(req.URL.Path, "/+/unhide/") {
		h.apiSHUnhide(w, req.URL.Path[len("/+/unhide"):])
		return
	}

	// Invalid API endpoint
	h.serve400(w)
}
