package main


import (
	"path/filepath"
	"net/http"
	"net/http/cgi"
	"math/rand/v2"
	"crypto/tls"
	"log"
	"fmt"
	"time"
	"os"
	"os/exec"
	"strings"
	"flag"
	"strconv"
	"sync"
	"maps"
	"sort"
	"bufio"
	"context"
	"errors"
	"sync/atomic"
	"encoding/base64"
	"go.yaml.in/yaml/v4"
	"crypto/sha512"
	"crypto/subtle"
	sn "golang.org/x/sync/singleflight"
	cr "crypto/rand"
	_ "embed"
)

type handler struct {
	root Path
	auth string
	gitTimeout, gitCloneTimeout, defaultRefresh, maxRefresh, minJitter, maxJitter time.Duration
	git *cgi.Handler
	fetchGroup sn.Group
	tokens map[string]tokenInfo
	repos map[Path]repoDescription
	reposLock sync.RWMutex
	startup time.Time
	effectiveConfig *Config
	maxDiskUsage int64
	diskUsagePolicy string
	allowPush bool

	totalRequests, cacheMisses, cacheHits atomic.Uint64
}

type tokenInfo struct {
	hash string
	level int
}

type repoDescription struct {
	repo RepoPath
	size, requests int64
	selfHosted bool
	lastErr time.Time
	cancelRefresher func()
}

func (h *handler) refreshDefaultBranch(path Path, logg *log.Logger) bool {
	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	out, err := git.output("ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}


	// Parsing below
	// We are searching for a line that looks like 'ref: refs/heads/XXX\tHEAD'

	ref := ""

	for line := range strings.Lines(string(out)) {
		_, err := fmt.Sscanf(line, "ref: refs/heads/%s\tHEAD", &ref)
		if err == nil {
			break
		}
	}

	if ref == "" {
		log.Printf("Failed to refresh current default branch: found no 'ref: ' upstream: %s", path)
		return false
	}


	if err := git.run("symbolic-ref", "HEAD", "refs/heads/" + ref); err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}

	return true
}

//func (h *handler) getRepos() map[Path]repoDescription {
//	h.reposLock.RLock()
//	defer h.reposLock.RUnlock()
//
//	repos := make(map[Path]repoDescription)
//
//	maps.Copy(repos, h.repos)
//
//	return repos
//}

func (h *handler) refreshRepo(path Path, logg *log.Logger) bool {
	repo := path.RepoPath(h)

	if selfHosted(repo) {
		logg.Printf("Cannot refresh %s: repo is self-hosted", repo.S())
		return false
	}

	// Non-critical if fails
	h.refreshDefaultBranch(path, logg)

	git := gitRunner{path, h.gitCloneTimeout, logg.Writer()}

	if err := git.run("fetch", "--prune", "origin"); err != nil {
		logg.Printf("Failed to refresh repo %s: %v", path, err)
		return false
	}

	updRes := h.updateServerInfo(path, logg)

	return h.applyDiskUsagePolicy(logg) && updRes
}

func (h *handler) evictRepo(repo RepoPath, logg *log.Logger) bool {

	path := repo.Path(h)

	h.reposLock.RLock()
	if _, ok := h.repos[path]; !ok {
		logg.Printf("Repo %s doesn't exist", repo)
		h.reposLock.RUnlock()
		return false
	}
	h.reposLock.RUnlock()

	if h.repoPinned(repo, logg) {
		logg.Printf("Can't evict %s: repo pinned, unpin first", repo)
		return false
	}

	h.reposLock.Lock()
	defer h.reposLock.Unlock()

	if err := os.RemoveAll(path.S()); err != nil {
		logg.Printf("Could not remove '%s'", path)
		return false
	}

	// Self-hosted repos don't have a refresher in the first place
	if !h.repos[path].selfHosted {
		h.repos[path].cancelRefresher()
	}

	delete(h.repos, path)

	return true
}

// Config for the future, so git doesn't lose refs/lorebox/*
func (h *handler) configureNewRepo(path Path, logg *log.Logger) bool {
	git := gitRunner{path, h.gitTimeout, logg.Writer()}

	if err := git.run("config", "--unset", "remote.origin.mirror"); err != nil {
		logg.Printf("Failed to configure repo (unset mirror): %s, %v", path, err)
		return false
	}

	if err := git.run("config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*"); err != nil {
		logg.Printf("Failed to configure repo (replace fetch): %s, %v", path, err)
		return false
	}

	if err := git.run("config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*"); err != nil {
		logg.Printf("Failed to configure repo (add fetch): %s, %v", path, err)
		return false
	}

	return true
}

func dirSize(path Path) (size int64, err error) {
	err = filepath.WalkDir(path.S(), func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		size += info.Size()
		return nil
	})

	return
}

func (h *handler) totalRepoSize() (size int64) {
	for _, d := range h.repos {
		size += d.size
	}
	return
}

func (h *handler) diskUsageExceeded() bool {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()
	return h.totalRepoSize() >= h.maxDiskUsage
}

func (h *handler) diskUsageExceededLocked() bool {
	return h.totalRepoSize() >= h.maxDiskUsage
}

func (h *handler) LRU(logg *log.Logger) bool {
	h.reposLock.Lock()
	defer h.reposLock.Unlock()

	if len(h.repos) == 0 {
		return true
	}

	type index struct {
		mt time.Time
		path Path
	}

	idx := make([]index, 0, len(h.repos))

	for p := range h.repos {
		acc := readAccess(p, logg)
		idx = append(idx, index{acc, p})
	}

	sort.Slice(idx, func (i, j int) bool {
		return idx[i].mt.Before(idx[j].mt)
	})

	// Delete until have enough space/only pinned repos left
	for _, i := range idx { 
		if !h.diskUsageExceededLocked() {
			break
		}

		repo := h.repos[i.path].repo

		// Can't evict a self-hosted repo
		if selfHosted(repo) {
			continue
		}

		if h.repoPinned(repo, logg) {
			logg.Printf("Can't LRU evict %s: repo is pinned", repo)
			continue
		}

		h.repos[i.path].cancelRefresher()
		delete(h.repos, i.path)

		if err := os.RemoveAll(i.path.S()); err != nil {
			logg.Printf("Couldn't LRU evict %s: %v", repo, err)
			continue
		}

		logg.Printf("Evicted %s because of disk usage", repo)
	}

	if h.diskUsageExceededLocked() {
		logg.Printf("Disk usage is exceeded, but only pinned repos are left")
		return false
	}

	return true
}

func readAccess(path Path, logg *log.Logger) time.Time {
	accPath := path.Concat("lorebox.access").S()

	info, err := os.Stat(accPath)

	if err != nil {
		logg.Printf("Could not read access for %s: %v", path, err)
		return time.Now()
	}

	return info.ModTime()
}


func recordAccess(path Path, logg *log.Logger) {
	accPath := path.Concat("lorebox.access").S()

	// Doesn't exist; touch
	if _, err := os.Stat(accPath); err != nil {
		f, err := os.OpenFile(accPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			logg.Printf("Could not create %s", accPath)
			return
		}
		f.Close()
	}

	now := time.Now()
	err := os.Chtimes(accPath, now, now)

	if err != nil {
		logg.Printf("Could not update mtime on %s", accPath)
		return
	}
}

func (h *handler) applyDiskUsagePolicy(logg *log.Logger) bool {
	if h.diskUsageExceeded() {
		logg.Printf("Max disk usage exceeded")
		switch h.diskUsagePolicy {
		case "deny":
			logg.Printf("Repo fetch denied\n")
			return false
		case "warn":
		case "lru":
			return h.LRU(logg)
		default:
			panic("Unknown disk usage policy")
		}
	}
	return true
}

func (h *handler) fetchRepo(repo RepoPath, logg *log.Logger, scheme string) bool {

	success, _, _ := h.fetchGroup.Do(repo.S(), func() (any, error) {
	
		if !h.applyDiskUsagePolicy(logg) {
			return false, nil
		}

		url := scheme + ":/" + repo.S()
		
		git := gitRunner{h.root, h.gitTimeout, logg.Writer()}

		// First just ls...
		if err := git.run("ls-remote", "--exit-code", url); err != nil {
			logg.Printf("Failed to ls-remote '%s': %v", url, err)
			return false, nil
		}

		path := repo.Path(h)

		stempPath, err := os.MkdirTemp(h.tmpDir().S(), "repo-fetch-*")
		defer os.RemoveAll(stempPath)

		tempPath := Path(stempPath)

		if err != nil {
			logg.Printf("Failed to make a temporary directory for '%s' fetch: %v", url, err)
			tempPath = path
		}

		git.timeout = h.gitCloneTimeout

		if err := git.run("clone", "--mirror", url, tempPath.S()); err != nil {
			logg.Printf("Failed to mirror clone '%s': %v", url, err)
			return false, nil
		}

		if !h.configureNewRepo(tempPath, logg) {
			return false, nil
		}

		if !h.updateServerInfo(tempPath, logg) {
			return false, nil
		}

		size, err := dirSize(tempPath)

		if err != nil {
			logg.Printf("Failed to calculate directory size: %s", path)
		}
		
		if err := os.MkdirAll(filepath.Dir(path.S()), 0755); err != nil {
			logg.Print(err)
			return false, nil
		}

		if err := os.Rename(tempPath.S(), path.S()); err != nil {
			logg.Printf("Failed to move %s to %s", tempPath, path)
			return false, nil
		}

		recordAccess(path, logg)

		ctx, cancel := context.WithCancel(context.Background())

		h.reposLock.Lock()
		h.repos[path] = repoDescription {
			repo: repo,
			size: size,
			cancelRefresher: cancel,
		}
		h.reposLock.Unlock()
		
		go h.refresher(ctx, path)

		return true, nil
	})

	return success.(bool)
}

func (h *handler) validateCredentials(id, token string, requiredAuthLevel int) (int, bool) {
	log.Printf("Validating '%s'", id)

	hash := sha512.Sum512([]byte(id + "|" + token))
	enHash := base64.RawURLEncoding.EncodeToString(hash[:])

	servToken, ok := h.tokens[id]

	if !ok {
		log.Printf("Unknown token id '%s'", id)
		return authLevelNone, false
	}

	if subtle.ConstantTimeCompare([]byte(enHash), []byte(servToken.hash)) == 0 {
		log.Printf("Hash doesn't match: '%s'", id)
		return authLevelNone, false
	}

	if servToken.level < requiredAuthLevel {
		log.Printf("%s doesn't have enough permissions. %d < %d", id, servToken.level, requiredAuthLevel)
		return authLevelNone, false
	}

	return servToken.level, true
}


func stringToAuthLevel(s string) int {
	switch s {
	case "admin":
		return authLevelAdmin
	case "push":
		return authLevelPush
	case "fetch":
		return authLevelFetch
	default:
		return authLevelNone
	}
}

func (h *handler) requireAuth(req *http.Request, authLevel int) (string, int, bool) {
	if h.auth == "none" {
		return "", authLevelNone, true
	}

	id, token, ok := req.BasicAuth()
	if !ok {
		return id, authLevelNone, false
	}

	level, ok := h.validateCredentials(id, token, authLevel) 
	if !ok {
		return id, authLevelNone, false
	}

	return id, level, true
}

func (h *handler) updateRepoSize(repo RepoPath) {
	path := repo.Path(h)

	size, err := dirSize(path)

	if err != nil {
		log.Printf("Could not calculate size of %s: %v", repo, err)
	}

	h.reposLock.Lock()
	r := h.repos[path]
	r.size = size
	h.repos[path] = r
	h.reposLock.Unlock()
}

func (h *handler) handlePush(w http.ResponseWriter, req *http.Request) {
	fullrpath := NewRepoPath(req.URL.Path)	

	if !selfHosted(fullrpath) {
		serve400(w)
		return
	}

	owner, repoName := parseSelfHosted(fullrpath)

	// Auth required, obviously
	who, level, ok := h.requireAuth(req, authLevelPush)
	if !ok {
		serve401(w)
		log.Printf("Invalid auth: %s", who)
		return
	}

	// You can only push to your own repo, unless you are an admin
	if who != owner && level != authLevelAdmin {
		serve400(w)
		log.Printf("%s tried to push into repo they don't own: %s/%s", who, owner, repoName)
		return
	}

	repo := NewRepoPath(chopPostfix(req.URL.Path, "/git-receive-pack"))
	path := repo.Path(h)

	// Exists
	if _, err := os.Stat(repo.Path(h).S()); err == nil {
		h.git.ServeHTTP(w, req)
		h.updateRepoSize(repo)
		return
	}

	// Doesn't exist, so create
	if !h.gitInit(path, log.Default()) {
		log.Printf("Couldn't init %s", repo)
		serve500(w)
		return
	}
	
	size, err := dirSize(path)

	if err != nil {
		log.Printf("Could not calculate size of %s: %v", repo, err)
	}

	h.reposLock.Lock()
	h.repos[path] = repoDescription {
		repo: repo,
		selfHosted: true,
		size: size,
	}
	h.reposLock.Unlock()

	h.git.ServeHTTP(w, req)
	h.updateRepoSize(repo)
}

func (h *handler) handlePull(w http.ResponseWriter, req *http.Request, svc string) {
	repo := NewRepoPath(chopInfoRefs(filepath.Clean("/" + req.URL.Path)))
	path := repo.Path(h)

	miss := false
	// Doesn't exist, so pull
	if _, err := os.Stat(path.S()); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("Can't stat '%s': %v", repo, err)
			serve500(w)
			return
		}

		// Authenticate
		who, level, ok := h.requireAuth(req, authLevelFetch)
		if !ok {
			serve401(w)
			return
		}

		// For a self-hosted repo, we create it if we have the permissions
		if h.allowPush && selfHosted(repo) && level >= authLevelPush {
			owner, repoName := parseSelfHosted(NewRepoPath(req.URL.Path))

			// You can only push to your own repo, unless you are an admin
			if who != owner && level != authLevelAdmin {
				log.Printf("%s tried to push-create into repo they don't own: %s/%s", who, owner, repoName)
				serve400(w)
				return
			}

			if !h.gitInit(path, log.Default()) {
				log.Printf("Couldn't init %s: %v", repo, err)
				serve500(w)
				return
			}

			size, err := dirSize(path)

			if err != nil {
				log.Printf("Could not calculate size of %s: %v", repo, err)
			}

			h.reposLock.Lock()
			h.repos[path] = repoDescription {
				repo: repo,
				selfHosted: true,
				size: size,
			}
			h.reposLock.Unlock()

			h.git.ServeHTTP(w, req)

			return
		} 

		// Fetch repo
		log.Printf("Running pullthrough on '%s'", repo)
		
		if !h.fetchRepo(repo, log.Default(), "https") {
			serve404(w)
			return
		}
		h.cacheMisses.Add(1)
		miss = true
	}

	if !miss {
		h.cacheHits.Add(1)
	}

	// Statistics
	h.reposLock.Lock()
	r := h.repos[path]
	r.requests++
	h.repos[path] = r
	h.reposLock.Unlock()

	// Smart client
	if svc == "git-upload-pack" {
		h.git.ServeHTTP(w, req)

	// Dumb client
	} else {
		h.serveFS(w, req)
	}

}

func (h *handler) requireSHAuth(req *http.Request) (string, string, bool) {
	owner, repoName := parseSelfHosted(NewRepoPath(req.URL.Path))

	// Invalid syntax
	if owner == "" || repoName == "" {
		return "", "", false
	}

	repo := owner + "/" + repoName

	// Auth required, obviously
	who, level, ok := h.requireAuth(req, authLevelPush)
	if !ok {
		log.Printf("Invalid SH auth: %s", who)
		return who, repo, false
	}

	return who, repo, who == owner || level == authLevelAdmin
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	h.totalRequests.Add(1)

	svc := req.URL.Query().Get("service")

	// Always has to be available
	if (req.Method == "GET" || req.Method == "HEAD") && req.URL.Path == "/robots.txt" {
		serveRobots(w)
		return
	}

	// Auth
	if h.auth == "all" {
		if _, _, ok := h.requireAuth(req, authLevelFetch); !ok {
			serve401(w)
			log.Printf("Invalid auth")
			return
		}
	}

	// Inject repo index
	if (req.Method == "GET" || req.Method == "HEAD") && req.URL.Path == "/repos.txt" {
		h.serveRepoIndex(w)
		return
	}

	// Admin remote control panel API
	if (req.Method == "GET" || req.Method == "HEAD") && strings.HasPrefix(req.URL.Path, "/-/") {
		who, _, ok := h.requireAuth(req, authLevelAdmin)
		if !ok {
			serve401(w)
			log.Printf("Invalid auth: %s", who)
			return
		}
		// Prevent cross-site nastiness
		if req.Header.Get("X-Lorebox-Api") != "On" {
			log.Printf("Valid auth, but no X-Lorebox-Api header: %s", who)
			serve400(w)
			return
		}
		h.api(w, req)
		return
	}

	// Self-hosted repo control panel API
	if (req.Method == "GET" || req.Method == "HEAD") && strings.HasPrefix(req.URL.Path, "/+/") {

		who, level, ok := h.requireAuth(req, authLevelPush)
		if !ok {
			serve401(w)
			log.Printf("Invalid auth: %s", who)
			return
		}

		_, srepo, ok := strings.Cut(req.URL.Path[len("/+/"):], "/")

		if !ok {
			log.Printf("Invalid path for GET /+/: '%s'", req.URL.Path)
			serve400(w)
			return
		}

		owner, name := parseSelfHosted(RepoPath("/~/").Concat(srepo))

		if who != owner && level != authLevelAdmin {
			log.Printf("%s tried to access api for controlling self-hosted %s/%s (they don't own it)", who, owner, name)
			serve404(w)
			return
		}

		// Prevent cross-site nastiness
		if req.Header.Get("X-Lorebox-Api") != "On" {
			log.Printf("Valid auth, but no X-Lorebox-Api header: %s", who)
			serve400(w)
			return
		}
		h.apiSelfHosted(w, req)
		return
	}

	// Smart ref advertisement
	// Request for .../info/refs, a start of the git repo transmission. Either
	// pull (if not available) or just serve
	// Note: HEAD is not allowed here, because we do not want to trigger a
	// pullthrough on HEAD...
	if req.Method == "GET" && hasPostfix(req.URL.Path, infoRefs) {

		// Hidden repos can only be seen by their owner and admin
		if h.hiddenRepoPath(NewRepoPath(req.URL.Path), log.Default()) {
			if who, repo, ok := h.requireSHAuth(req); !ok {
				serve404(w)
				log.Printf("%s tried to fetch a hidden repo they don't own: %s", who, repo)
				return
			}
		}

		h.handlePull(w, req, svc)
		return
	}

	// HEAD is not allowed here
	if req.Method == "HEAD" && hasPostfix(req.URL.Path, infoRefs) {
		serve405NoHead(w);
		return
	}
	
	// Smart pack transfer
	if req.Method == "POST" && hasPostfix(req.URL.Path, "/git-upload-pack") {
		h.git.ServeHTTP(w, req)
		return
	}

	// Push. Only allowed for self-hosted repos
	if h.allowPush && req.Method == "POST" && hasPostfix(req.URL.Path, "/git-receive-pack") {
		h.handlePush(w, req)
		return
	}

	// Unknown service. Maybe remove this check?
	if svc != "" {
		serve400(w)
		return
	}

	// Dumb client
	if req.Method == "GET" || req.Method == "HEAD" {
		h.serveFS(w, req)	
		return
	}

	serve405(w)
}

func (h *handler) walkRepos() {
	// No repo root deeper than 10
	maxDepth := 10

	log.Printf("Walking document root to find already existing repos...")

	var walk func(dir Path, depth int) (repos map[Path]repoDescription)

	walk = func(dir Path, depth int) (repos map[Path]repoDescription) {
		if depth > maxDepth {
			return
		}
		// Don't walk into /.tmp
		if dir == h.tmpDir() {
			return
		}

		info, err := os.Stat(dir.S())

		if err != nil {
			log.Printf("Error while walking: %v", err)
			return
		}

		if !info.IsDir() {
			return
		}

		entries, err := os.ReadDir(dir.S())

		if err != nil {
			log.Printf("Error while walking: %v", err)
			return
		}

		// Check if a repo
		for _, e := range entries {
			if e.Name() == "HEAD" {
				log.Printf("Found %s", dir)
				size, err := dirSize(dir)

				if err != nil {
					log.Printf("Could not measure size of %s", dir)
				}

				return map[Path]repoDescription {
					dir: {
						repo: dir.RepoPath(h),
						size: size,
					},
				}
			}
		}

		// Walk subdirectories
		repos = map[Path]repoDescription{}
		for _, e := range entries {
			found := walk(dir.Concat(e.Name()), depth + 1)
			maps.Copy(repos, found)
		}

		return
	}

	// No lock, nothing runs at this point yet
	h.repos = walk(h.root, 0)

	// Mark self-hosted repos as such
	for p, d := range h.repos {
		if selfHosted(d.repo) {
			d.selfHosted = true
			h.repos[p] = d
		}
	}

	log.Printf("Done walking repos")
}

func (h *handler) jitteredRefresh() time.Duration {
	jitter := time.Duration(0)
	if span := h.maxJitter - h.minJitter; span > 0 {
		jitter = rand.N(span)
	}
	return h.defaultRefresh + h.minJitter + jitter
}

func (h *handler) refresher(ctx context.Context, path Path) {
	duration := h.jitteredRefresh()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Refresher on %s is killed", path)
			return
		case <-time.After(duration):
		}

		if !h.refreshRepo(path, log.Default()) {
			h.reposLock.Lock()

			d, ok := h.repos[path]

			// Doesn't exist anymore
			if !ok {
				log.Printf("Refresher: repo %s is no longer present, dying", path)
				h.reposLock.Unlock()
				return
			}

			d.lastErr = time.Now()
			h.repos[path] = d
			h.reposLock.Unlock()

			if duration >= h.maxRefresh {
				duration = h.maxRefresh
				log.Printf("%s reached max refresh duration", path)
				continue
			}

			duration *= 2
			continue
		}

		duration = h.jitteredRefresh()
	}
}

type Duration time.Duration

func (d Duration) D() time.Duration {
	return time.Duration(d)
}

func D(d time.Duration) Duration {
	return Duration(d)
}

var Hour = D(time.Hour)
var Minute = D(time.Minute)

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	dur, err := parseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q: %v", s, err)
	}
	*d = D(dur)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.D().String(), nil
}

func parseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}

type Config struct {
	Root string
	Name string
	Listen string

	Auth string

	Push bool

	Timeouts struct {
		Git struct {
			Regular Duration
			Clone Duration
		}
		Refresh struct {
			Default Duration
			Max Duration
			Jitter struct {
				Max Duration
				Min Duration
			}
		}
	}

	Tokens []struct {
		Id, Hash, Level string
	}

	Https struct {
		Certificate string
		Key string
	}

	Disk struct {
		Max string
		Policy string
	}
}

func generateCredentials(username string) {
	if username == "" {
		fmt.Printf("Username must not be an empty string\n")
		usage()
		return
	}

	token := make([]byte, 32)

	_, err := cr.Read(token)
	if err != nil {
		log.Fatalf("Could not generate token: %v", err)
	}

	enToken := base64.RawURLEncoding.EncodeToString(token)

	// No need for salt, token is already completely random
	hash := sha512.Sum512([]byte(username + ":" + enToken))
	enHash := base64.RawURLEncoding.EncodeToString(hash[:])

	fmt.Printf("# Successfully generated token credentials\n")
	fmt.Printf("# Public (server) component\n")
	fmt.Printf("# Paste this into your lorebox.yml:\n")
	fmt.Printf("tokens:\n")
	fmt.Printf("  - id: \"%s\"\n", username)
	fmt.Printf("    hash: \"%s\"\n", enHash)
	fmt.Printf("    level: <select \"fetch\", \"push\", or \"admin\">\n")

	fmt.Printf("\n# Private component\n")
	fmt.Printf("# Use this as user:pass when using git, e.g.:\n")
	fmt.Printf("# git clone https://%s:%s@box.bob.net/alice.net/alice/repo\n", username, enToken)
	fmt.Printf("# Or you can paste this into your ~/.config/lorebox/client.yml:\n")
	fmt.Printf("tokens:\n")
	fmt.Printf("  \"<your box>\": \"%s:%s\"\n", username, enToken)
	fmt.Printf("# Then run 'lorebox register' to register lorebox as your git's auth provider for this box\n")

	fmt.Printf("\n%s:%s\n", username, enToken)
}

func registerWithGit() {
	config := ClientConfig{}

	if err := yaml.Unmarshal(getConfigData(defaultConfigFile), &config); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Configuring your git client...\n")

	if len(config.Tokens) == 0 {
		fmt.Printf("No tokens configured for ~/.config/lorebox/client.yml")
		return
	}

	lorebox, err := os.Executable()

	if err != nil {
		fmt.Printf("Couldn't obtain executable path of self: %v", err)
		return
	}

	git := gitRunner{".", time.Second * 5, os.Stdout}

	for host := range config.Tokens {
		fmt.Printf("Registering auth for %s...\n", host)

		err = git.run("config", "--global", "credential.http://" + host + ".helper", "!" + lorebox + " credential")

		if err != nil {
			fmt.Println(err)
		}

		fmt.Printf("    http success\n")

		err = git.run("config", "--global", "credential.https://" + host + ".helper", "!" + lorebox + " credential")

		if err != nil {
			fmt.Println(err)
		}

		fmt.Printf("    https success\n")
	}

	fmt.Printf("Done\n")
}

func credentialHelper() {
	config := ClientConfig{}

	if err := yaml.Unmarshal(getConfigData(defaultConfigFile), &config); err != nil {
		log.Fatal(err)
	}

	// We need to stay silent unless answering the query

	attrs := map[string]string{}

	sc := bufio.NewScanner(os.Stdin)

	for sc.Scan() && sc.Err() == nil {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			attrs[k] = v
		}
	}

	if attrs["protocol"] != "https" && attrs["protocol"] != "http" {
		return
	}

	host := attrs["host"]

	if host == "" {
		return
	}

	if token, ok := config.Tokens[host]; ok {
		id, secret, ok := strings.Cut(token, ":")

		if !ok {
			return
		}

		fmt.Printf("username=%s\npassword=%s\n\n", id, secret)
	}
}

func defaultConfig() Config {
	config := Config {
		Root: ".",
		Name: "unnamed",
		Listen: ":8080",
		Auth: "new",
	}
	config.Disk.Max = "10G"
	config.Disk.Policy = "lru"
	config.Timeouts.Git.Regular = 5 * Minute
	config.Timeouts.Git.Clone = 30 * Minute
	config.Timeouts.Refresh.Default = 12 * Hour
	config.Timeouts.Refresh.Max = 20 * 24 * Hour
	config.Timeouts.Refresh.Jitter.Min = 20 * Minute
	config.Timeouts.Refresh.Jitter.Max = 70 * Minute

	return config
}

func (config *Config) check() {
	if config.Timeouts.Refresh.Jitter.Max < config.Timeouts.Refresh.Jitter.Min {
		log.Fatal("Error: min jitter > max jitter")
	}
	if config.Timeouts.Refresh.Default > config.Timeouts.Refresh.Max {
		log.Fatal("Error: default repo refresh time > max refresh time")
	}
	if config.Auth != "new" && config.Auth != "all" && config.Auth != "none" {
		log.Fatal("Error: invalid authentication mode. Select new/all/none")
	}
	// XOR
	if (config.Https.Certificate == "") != (config.Https.Key == "") {
		log.Fatal("Error: specify _both_ the certificate and key to use HTTPS")
	}
	switch config.Disk.Policy {
	case "lru", "deny", "warn":
	default:
		log.Fatal("disk:policy must be either 'lru', 'deny', or 'warn'")
	}

	maxDiskUsage, err := parseDiskSize(config.Disk.Max)
	if err != nil {
		log.Fatalf("Error: could not parse '%s': %v\n", config.Disk.Max, err)
	}
	if maxDiskUsage == 0 {
		log.Fatal("disk:max may not be zero")
	}
	if maxDiskUsage <= 32 * 1024 {
		log.Printf("Warning: disk:max set to less than 32K, that might be too little")
	}
}

func (config *Config) load(configFile string) {
	var configData []byte
	var err error

	if configFile != "" {
		configData, err = os.ReadFile(configFile)
		if err != nil {
			log.Fatalf("Could not read config: %v", err)
		}
	}

	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Fatalf("Could not read YAML: %v", err)
	}
}

// newHandler constructs a new handler from config, path to document root, and
// path to the git backend. It will call log.Fatalf() if it fails to parse
// config.Disk.Max
func newHandler(config *Config, root, backend Path) *handler {
	maxDisk, err := parseDiskSize(config.Disk.Max)
	if err != nil {
		log.Fatalf("Error: could not parse '%s': %v\n", config.Disk.Max, err)
	}

	h := &handler {
		effectiveConfig: config,
		root: root,
		auth: config.Auth,
		gitTimeout: config.Timeouts.Git.Regular.D(),	
		gitCloneTimeout: config.Timeouts.Git.Clone.D(),
		defaultRefresh: config.Timeouts.Refresh.Default.D(),
		maxRefresh: config.Timeouts.Refresh.Max.D(),
		minJitter: config.Timeouts.Refresh.Jitter.Min.D(),
		maxJitter: config.Timeouts.Refresh.Jitter.Max.D(),
		diskUsagePolicy: config.Disk.Policy,
		maxDiskUsage: maxDisk,
		allowPush: config.Push,
		git: &cgi.Handler {
			Path: backend.S(),
			Dir: root.S(),
			Env: []string {
				"GIT_PROJECT_ROOT=" + root.S(),
				"GIT_HTTP_EXPORT_ALL=1",
			},
		},
	}

	h.startup = time.Now()

	return h
}

// processTokens read tokens from the config and populates the h.tokens
// dictionary
func (h *handler) processTokens() {
	for _, t := range h.effectiveConfig.Tokens {
		if t.Id == "" || t.Hash == "" {
			log.Fatalf("For each token, id and hash must be set")
		}

		if t.Level == "" {
			t.Level = "fetch"
		}

		level := stringToAuthLevel(t.Level)
		// Invalid level
		if level == authLevelNone {
			log.Fatalf("For each token, level must be set to either 'fetch', 'push', or 'admin'")
		}

		h.tokens[t.Id] = tokenInfo {
			t.Hash, level,
		}
	}

}

// launchRefresh launches h.refresher for each of the cached repos in h.repos.
// Self-hosted repos will not be refreshed
func (h *handler) launchRefresh() {
	for r, d := range h.repos {
		// Can't refresh self-hosted repos
		if d.selfHosted {
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		info := h.repos[r]
		info.cancelRefresher = cancel
		h.repos[r] = info
		go h.refresher(ctx, r)
	}
}

func main() {

	if len(os.Args) < 2 {
		fmt.Printf("Select a command verb\n")
		usage()
		return
	}

	switch os.Args[1] {
	case "gen-token", "gen-tok", "gen":
		if len(os.Args) != 3 {
			fmt.Printf("lorebox gen-token requires 1 argument: username\n")
			usage()
			return
		}
		generateCredentials(os.Args[2])
		return
	case "register", "reg":
		registerWithGit()
		return
	case "credential":
		if len(os.Args) < 3 || os.Args[2] != "get" {
			fmt.Printf("Syntax: 'lorebox credential get', all parts mandatory\n")
			usage()
			return
		}
		credentialHelper()
		return
	case "synonyms", "syns", "syn":
		synonyms()
		return
	case "serve", "srv":
		break
	default:
		client()
		return
	}

	var listen, sroot, configFile string
	var insecure bool

	flag.StringVar(&listen, "listen", "", "Override the bind port and address")
	flag.StringVar(&sroot, "root", "", "Override the document root")
	flag.StringVar(&configFile, "config", "", "Point to the config YAML file")
	flag.BoolVar(&insecure, "insecure", false, "Point to the config YAML file")
	if err := flag.CommandLine.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}

	root := Path(sroot)

	log.Printf("Starting lorebox " + loreboxVersion)

	config := defaultConfig()

	if configFile != "" {
		config.load(configFile)
		log.Println("Config read")
	}

	// Override config values
	if root != "" {
		config.Root = root.S()
	}
	if listen != "" {
		config.Listen = listen
	}

	config.check();

	root, err := Path(config.Root).expand()

	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(root.S(), 0700); err != nil {
		log.Fatal(err)
	}

	// Used by fullSelfID()
	// Ugly global variable, but does not have a significant impact on anything,
	// so fine for now (whatever 'now' is)
	loreboxName = config.Name

	// Replace __LOREBOX_VERSION with fullSelfID()
	processStaticPages()
	
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeouts.Git.Regular.D())
	defer cancel()
	gitdir, err := exec.CommandContext(ctx, "git", "--exec-path").Output()

	if err != nil {
		log.Fatal(err)
	}

	backend := NewPath(filepath.Join(strings.TrimSpace(string(gitdir)), "git-http-backend"))

	h := newHandler(&config, root, backend)

	tmpDir := h.tmpDir()
	// Wipe
	os.RemoveAll(tmpDir.S())
	// Create
	if err := os.Mkdir(tmpDir.S(), 0700); err != nil {
		log.Fatal(err)
	}

	// Self-hosted repos go here
	if err := os.MkdirAll(root.Concat("~").S(), 0700); err != nil {
		log.Fatal(err)
	}

	h.tokens = map[string]tokenInfo{}

	// Read tokens from the config, check if they are correctly configured, then
	// add them to the h.tokens
	h.processTokens()

	// Populates h.repos
	h.walkRepos()

	// Launch refresher goroutines to fetch cached repos periodically
	h.launchRefresh()

	serv := &http.Server {
		Addr: config.Listen,
		Handler: h,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout: 20 * time.Second,
		MaxHeaderBytes: 10 * 1024,
	}

	if config.Https.Key != "" && !insecure {
		kpr, err := NewKeypairReloader(config.Https.Certificate, config.Https.Key)

		if err != nil {
			log.Fatalf("Could not create a key pair reloader: %v", err)
		}

		serv.TLSConfig = &tls.Config{}
		serv.TLSConfig.GetCertificate = kpr.GetCertificateFunc()

		log.Fatal(serv.ListenAndServeTLS("", ""))
	}

	log.Fatal(serv.ListenAndServe())
}
