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
		UptimeSec: int(time.Now().Sub(h.startup).Seconds()),
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
		if err != nil {
			bw.WriteString(jsonFailure("Could not marshal status into JSON", status.Status))

			w.WriteHeader(500)
			w.Write(bw.Bytes())
			return
		}
	}

	bw.Write(marsh)

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

func (h *handler) apiSimpleTr(w http.ResponseWriter, f func(*log.Logger) bool) {
	w.Header().Set("Content-Type", "text/json; charset=utf-8")

	op := jsonableOperation{Status:"success"}
	tr := &bytes.Buffer{}
	logg := log.New(tr, "", log.LstdFlags)

	if !f(logg) {
		op.Status = "Internal error"
	}

	op.Transcript = string(tr.Bytes())

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
	//w.Header().Set("Content-Type", "text/json; charset=utf-8")

	//op := jsonableOperation{Status:"success"}

	//tr := &bytes.Buffer{}
	//logg := log.New(tr, "", log.LstdFlags)

	//if !h.pinRepo(repo, logg) {
	//	op.Status = "Error pinning repo"
	//}

	//op.Transcript = string(tr.Bytes())

	//marsh, err := json.Marshal(op)

	//if err != nil {
	//	w.WriteHeader(500)
	//	w.Write([]byte(jsonFailure("Could not marshal pin info into JSON")))
	//	return
	//}
	//
	//w.Write(marsh)
}

func (h *handler) apiUnpin(w http.ResponseWriter, repo string) {
	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.unpinRepo(repo, logg)
	})
	//w.Header().Set("Content-Type", "text/json; charset=utf-8")

	//op := jsonableOperation{Status:"success"}

	//tr := &bytes.Buffer{}
	//logg := log.New(tr, "", log.LstdFlags)

	//if !h.unpinRepo(repo, logg) {
	//	op.Status = "Error unpinning repo"
	//}

	//op.Transcript = string(tr.Bytes())

	//marsh, err := json.Marshal(op)

	//if err != nil {
	//	w.WriteHeader(500)
	//	w.Write([]byte(jsonFailure("Could not marshal pin info into JSON")))
	//	return
	//}
	//
	//w.Write(marsh)
}

func (h *handler) apiRefresh(w http.ResponseWriter, repo string) {
	path := filepath.Clean(h.root + "/" + repo)

	h.apiSimpleTr(w, func(logg *log.Logger) bool {
		return h.refreshRepo(path, logg)
	})
	//w.Header().Set("Content-Type", "text/json; charset=utf-8")

	//op := jsonableOperation{Status:"success"}

	//tr := &bytes.Buffer{}
	//logg := log.New(tr, "", log.LstdFlags)

	//if !h.unpinRepo(repo, logg) {
	//	op.Status = "Error unpinning repo"
	//}

	//op.Transcript = string(tr.Bytes())

	//marsh, err := json.Marshal(op)

	//if err != nil {
	//	w.WriteHeader(500)
	//	w.Write([]byte(jsonFailure("Could not marshal pin info into JSON")))
	//	return
	//}
	//
	//w.Write(marsh)
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
		repos.Repos[path] = jsonableRefresh{Name: d.repo, Status: "success"}
	}
	h.reposLock.RUnlock()


	fails := 0

	for p, r := range repos.Repos {
		tr := &bytes.Buffer{}
		logg := log.New(tr, "", log.LstdFlags)
		if !h.refreshRepo(p, logg) {
			r.Status = "Refresh failed"
			fails++
		}
		r.Transcript = string(tr.Bytes())
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
			fmt.Sprintf("Fails: %s", fails),
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

	// Invalid API point
	h.serve400(w)
}

