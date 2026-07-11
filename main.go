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
	"sort"
	"bufio"
	"context"
	"errors"
	"sync/atomic"
	"encoding/base64"
	"go.yaml.in/yaml/v4"
	"crypto/sha256"
	sn "golang.org/x/sync/singleflight"
	cr "crypto/rand"
	_ "embed"
)

type handler struct {
	root, auth string
	gitTimeout, gitCloneTimeout, defaultRefresh, maxRefresh, minJitter, maxJitter time.Duration
	git *cgi.Handler
	fetchGroup sn.Group
	tokens map[string]tokenInfo
	repos map[string]repoDescription
	reposLock sync.RWMutex
	startup time.Time
	effectiveConfig *Config
	maxDiskUsage int64
	diskUsagePolicy string

	totalRequests atomic.Uint64
}

type tokenInfo struct {
	hash string
	level string
}

type repoDescription struct {
	repo string
	size int64
	lastErr time.Time
	cancelRefresher func()
}

const gitboxVersion = "v0.3"
	
var infoRefs = "/info/refs"

func (h *handler) refreshDefaultBranch(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--symref", "origin", "HEAD")
	cmd.Dir = path

	out, err := cmd.Output()
	if err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}

	if err != nil {
		log.Printf("Failed to get output: %v", err)
		return false
	}


	// Parsing below
	// We are searching for a line that looks like 'ref: <...>\tHEAD'

	ref := ""

	for line := range strings.Lines(string(out)) {
		_, err := fmt.Sscanf(line, "ref: refs/heads/%s\tHEAD", &ref)
		if err == nil {
			break
		}
	}

	if ref == "" {
		log.Printf("Failed to refresh current default branch: found no 'ref: ': %s", path)
		return false
	}

	ctx, cancel = context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd = exec.CommandContext(ctx, "git", "symbolic-ref", "HEAD", "refs/heads/" + ref)
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}

	return true
}

func (h *handler) getRepos() map[string]repoDescription {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	repos := make(map[string]repoDescription)

	for k, v := range h.repos {
		repos[k] = v
	}

	return repos
}

func (h *handler) refreshRepo(path string, logg *log.Logger) bool {
	// Non-critical if fails
	h.refreshDefaultBranch(path)

	ctx, cancel := context.WithTimeout(context.Background(), h.gitCloneTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--prune", "origin")
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to refresh repo: %s, %v", path, err)
		return false
	}

	updRes := h.updateServerInfo(path, logg)

	return h.applyDiskUsagePolicy(logg) && updRes
}

func (h *handler) evictRepo(repo string, logg *log.Logger) bool {
	path := filepath.Clean(h.root + "/" + repo)

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

	if err := os.RemoveAll(path); err != nil {
		logg.Printf("Could not remove '%s'", path)
		return false
	}

	h.repos[path].cancelRefresher()
	delete(h.repos, path)

	return true
}

func (h *handler) configureNewRepo(path string, logg *log.Logger) bool {
	// Config for the future, so git doesn't lose refs/gitbox/*
	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--unset", "remote.origin.mirror")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (unset mirror): %s, %v", path, err)
		return false
	}

	ctx, cancel = context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd = exec.CommandContext(ctx, "git", "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (replace fetch): %s, %v", path, err)
		return false
	}

	ctx, cancel = context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd = exec.CommandContext(ctx, "git", "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (add fetch): %s, %v", path, err)
		return false
	}

	return true
}

func (h *handler) updateServerInfo(path string, logg *log.Logger) bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "update-server-info")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to run update-server-info: %s, %v", path, err)
		return false
	}

	return true
}

func dirSize(path string) (size int64, err error) {
	err = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
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
		path string
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

		if h.repoPinned(repo, logg) {
			logg.Printf("Can't LRU evict %s: repo is pinned", repo)
			continue
		}

		h.repos[i.path].cancelRefresher()
		delete(h.repos, i.path)

		if err := os.RemoveAll(i.path); err != nil {
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

func readAccess(path string, logg *log.Logger) time.Time {
	accPath := filepath.Clean(path + "/gitbox.access")

	info, err := os.Stat(accPath)

	if err != nil {
		logg.Printf("Could not read access for %s: %v", path, err)
		return time.Now()
	}

	return info.ModTime()
}

func (h *handler) pinRepo(repo string, logg *log.Logger) bool {
	path := filepath.Clean(h.root + "/" + repo)

	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "gitbox.pinned", "true")
	cmd.Stderr = logg.Writer()
	cmd.Stdout = logg.Writer()
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to pin '%s': %v", repo, err)
		return false
	}

	return true
}

func (h *handler) unpinRepo(repo string, logg *log.Logger) bool {
	path := filepath.Clean(h.root + "/" + repo)

	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--unset", "gitbox.pinned")
	cmd.Stderr = logg.Writer()
	cmd.Stdout = logg.Writer()
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to pin '%s': %v", repo, err)
		return false
	}

	return true
}

func (h *handler) repoPinned(repo string, logg *log.Logger) bool {
	path := filepath.Clean(h.root + "/" + repo)

	ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--bool", "gitbox.pinned")
	cmd.Dir = path

	err := cmd.Run()

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false
		}

		logg.Printf("Failed to check if repo is pinned '%s': %v", repo, err)
		return false
	}

	// 0 exit code means it exists, in our case === true
	return true
}

func recordAccess(path string, logg *log.Logger) {
	accPath := filepath.Clean(path + "/gitbox.access")

	// Doesn't exist; touch
	if _, err := os.Stat(accPath); err != nil {
		f, err := os.OpenFile(accPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("Could not create %s", accPath)
			return
		}
		f.Close()
	}

	now := time.Now()
	err := os.Chtimes(accPath, now, now)

	if err != nil {
		log.Printf("Could not update mtime on %s", accPath)
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

func (h *handler) fetchRepo(repo string, logg *log.Logger, scheme string) bool {

	success, _, _ := h.fetchGroup.Do(repo, func() (any, error) {
	
		if !h.applyDiskUsagePolicy(logg) {
			return false, nil
		}

		url := scheme + ":/" + repo

		// TODO: timeouts

		// First just ls...
		ctx, cancel := context.WithTimeout(context.Background(), h.gitTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", url)

		if err := cmd.Run(); err != nil {
			logg.Printf("Failed to ls-remote '%s': %v", url, err)
			return false, nil
		}

		path := filepath.Clean(h.root + "/" + repo)

		tempPath, err := os.MkdirTemp(h.root + "/.tmp", "repo-fetch-*")
		defer os.RemoveAll(tempPath)

		if err != nil {
			logg.Printf("Failed to make a temporary directory for '%s' fetch: %v", url, err)
			tempPath = path
		}

		ctx, cancel = context.WithTimeout(context.Background(), h.gitCloneTimeout)
		defer cancel()
		cmd = exec.CommandContext(ctx, "git", "clone", "--mirror", url, tempPath)
		cmd.Dir = h.root
		cmd.Stdout = logg.Writer()
		cmd.Stderr = logg.Writer()

		if err := cmd.Run(); err != nil {
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
		
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			logg.Print(err)
			return false, nil
		}

		if err := os.Rename(tempPath, path); err != nil {
			logg.Printf("Failed to move %s to %s", tempPath, path)
			return false, nil
		}

		recordAccess(path, logg)

		ctx, cancel = context.WithCancel(context.Background())

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

func (h *handler) validateCredentials(id, token string, admin bool) bool {
	log.Printf("Validating '%s'", id)

	hash := sha256.Sum256([]byte(token))
	enHash := base64.RawURLEncoding.EncodeToString(hash[:])

	servToken, ok := h.tokens[id]

	if !ok {
		log.Printf("Unknown token id '%s'", id)
		return false
	}

	if enHash != servToken.hash {
		log.Printf("Hash doesn't match: '%s'", id)
		return false
	}

	if admin && servToken.level != "admin" {
		log.Printf("Doesn't have admin permissions: '%s'", id)
		return false
	}

	log.Printf("%s validated", id)
	return true
}


func (h *handler) requireAuth(w http.ResponseWriter, req *http.Request, admin bool) bool {
	if h.auth != "none" {
		id, token, ok := req.BasicAuth()
		if !ok || !h.validateCredentials(id, token, admin) {
			h.serve401(w)
			return false
		}
	}
	return true
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	h.totalRequests.Add(1)

	svc := req.URL.Query().Get("service")

	// Always has to be available
	if req.Method == "GET" && req.URL.Path == "/robots.txt" {
		h.serveRobots(w)
		return
	}

	if h.auth == "all" {
		id, token, ok := req.BasicAuth()
		if !ok || !h.validateCredentials(id, token, false) {
			h.serve401(w)
			return
		}
	}

	if req.Method == "GET" && req.URL.Path == "/repos.txt" {
		h.serveRepoIndex(w)
		return
	}

	if req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/-/") {
		if !h.requireAuth(w, req, true) {
			log.Printf("Invalid auth")
			return
		}
		// Prevent cross-site nastiness
		if req.Header.Get("X-Gitbox-Api") != "On" {
			log.Printf("Valid auth, but no X-Gitbox-Api header")
			h.serve400(w)
			return
		}
		h.api(w, req)
		return
	}

	// Smart ref advertisement
	if req.Method == "GET" && hasPostfix(req.URL.Path, infoRefs) && svc == "git-upload-pack"{
		repo := chopInfoRefs(filepath.Clean("/" + req.URL.Path))

		// Doesn't exist, so pull
		if _, err := os.Stat(h.root + repo); err != nil {

			// Authenticate
			if !h.requireAuth(w, req, false) {
				return
			}

			log.Printf("Running pullthrough on '%s'", repo)
			
			if !h.fetchRepo(repo, log.Default(), "https") {
				h.serve404(w)
				return
			}
		}

		h.git.ServeHTTP(w, req)
		return
	}
	
	// Smart pack transfer
	if req.Method == "POST" && hasPostfix(req.URL.Path, "/git-upload-pack") {
		h.git.ServeHTTP(w, req)
		return
	}

	// Push and anything else
	if svc != "" || hasPostfix(req.URL.Path, "/git-receive-pack") {
		h.serve400(w)
		return
	}

	if req.Method == "GET" {
		h.serveFS(w, req)	
		return
	}

	h.serve400(w)
}

func (h *handler) walkRepos() {
	// No repo root deeper than 10
	maxDepth := 10

	log.Printf("Walking document root to find already existing repos...")

	var walk func(dir string, depth int) (repos map[string]repoDescription)

	walk = func(dir string, depth int) (repos map[string]repoDescription) {
		if depth > maxDepth {
			return
		}
		// Don't walk into /.tmp
		if dir == h.root + "/.tmp" {
			return
		}

		info, err := os.Stat(dir)

		if err != nil {
			log.Printf("Error while walking: %v", err)
			return
		}

		if !info.IsDir() {
			return
		}

		entries, err := os.ReadDir(dir)

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

				return map[string]repoDescription {
					dir: repoDescription {
						repo: h.chopRoot(dir),
						size: size,
					},
				}
			}
		}

		// Walk subdirectories
		repos = map[string]repoDescription{}
		for _, e := range entries {
			found := walk(dir + "/" + e.Name(), depth + 1)
			for r, v := range found {
				repos[r] = v
			}
		}

		return
	}

	// No lock, nothing runs at this point yet
	h.repos = walk(h.root, 0)

	log.Printf("Done walking repos")
}

func expandPath(path string) (string, error) {
	path, err := filepath.Abs(path)

	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(path)

	if err != nil {
		return path, nil
	}

	return resolved, nil
}

func (h *handler) jitteredRefresh() time.Duration {
	jitter := time.Duration(0)
	if span := h.maxJitter - h.minJitter; span > 0 {
		jitter = rand.N(span)
	}
	return h.defaultRefresh + h.minJitter + jitter
}

func (h *handler) refresher(ctx context.Context, path string) {
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
	Listen string

	Auth string

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

func generateCredentials() {
	token := make([]byte, 32)
	tokenId := make([]byte, 4)

	_, err := cr.Read(token)
	if err != nil {
		log.Fatalf("Could not generate token: %v", err)
	}

	_, err = cr.Read(tokenId)
	if err != nil {
		log.Fatalf("Could not generate token id: %v", err)
	}

	enToken := base64.RawURLEncoding.EncodeToString(token)
	enTokenId := "id-" + base64.RawURLEncoding.EncodeToString(tokenId)

	// No need for salt, token is already completely random
	hash := sha256.Sum256([]byte(enToken))
	enHash := base64.RawURLEncoding.EncodeToString(hash[:])

	fmt.Printf("# Successfully generated token credentials\n")
	fmt.Printf("# Public (server) component\n")
	fmt.Printf("# Paste this into your gitbox.yml:\n")
	fmt.Printf("tokens:\n")
	fmt.Printf("  - id: \"%s\"\n", enTokenId)
	fmt.Printf("    hash: \"%s\"\n", enHash)
	fmt.Printf("    level: <select \"fetch\" or \"admin\">\n")

	fmt.Printf("\n# Private component\n")
	fmt.Printf("# Use this as user:pass when using git, e.g.:\n")
	fmt.Printf("# git clone https://%s:%s@box.bob.net/alice.net/alice/repo\n", enTokenId, enToken)
	fmt.Printf("# Or you can paste this into your ~/.config/gitbox/client.yml:\n")
	fmt.Printf("tokens:\n")
	fmt.Printf("  \"%s\": \"%s\"\n", enTokenId, enToken)
	fmt.Printf("# Then run 'gitbox autoconf' to automatically configure your git client to use this token\n")

	fmt.Printf("\n%s:%s\n", enTokenId, enToken)
}

func registerWithGit() {
	config := ClientConfig{}

	if err := yaml.Unmarshal(getConfigData(defaultConfigFile), &config); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Configuring your git client...\n")

	if len(config.Tokens) == 0 {
		fmt.Printf("No tokens configured for ~/.config/gitbox/client.yml")
		return
	}

	gitbox, err := os.Executable()

	if err != nil {
		fmt.Printf("Couldn't obtain executable path of self: %v", err)
		return
	}

	for host, _ := range config.Tokens {
		fmt.Printf("Registering auth for %s...\n", host)

		err = exec.Command("git", "config", "--global", "credential.http://" + host + ".helper", "!" + gitbox + " credential").Run()

		if err != nil {
			fmt.Println(err)
		}

		fmt.Printf("    http success\n")

		err = exec.Command("git", "config", "--global", "credential.https://" + host + ".helper", "!" + gitbox + " credential").Run()

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

	for sc.Scan() {
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

	return
}

func main() {

	if len(os.Args) < 2 {
		fmt.Printf("Select a command verb\n")
		usage()
		return
	}


	switch os.Args[1] {
	case "gen-token":
		generateCredentials()
		return
	case "register":
		registerWithGit()
		return
	case "credential":
		if len(os.Args) < 3 || os.Args[2] != "get" {
			usage()
			return
		}
		credentialHelper()
		return
	case "serve":
		break
	default:
		client()
		return
	}

	var listen, root, configFile string
	var insecure bool

	flag.StringVar(&listen, "listen", "", "Override the bind port and address")
	flag.StringVar(&root, "root", "", "Override the document root")
	flag.StringVar(&configFile, "config", "", "Point to the config YAML file")
	flag.BoolVar(&insecure, "insecure", false, "Point to the config YAML file")
	if err := flag.CommandLine.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting gitbox " + gitboxVersion)

	var configData []byte
	var err error

	if configFile != "" {
		configData, err = os.ReadFile(configFile)
		if err != nil {
			log.Fatalf("Could not read config: %v", err)
		}
	}

	config := Config {
		Root: ".",
		Listen: ":8080",
		Auth: "new",
	}
	config.Timeouts.Git.Regular = 5 * Minute
	config.Timeouts.Git.Clone = 30 * Minute
	config.Timeouts.Refresh.Default = 12 * Hour
	config.Timeouts.Refresh.Max = 20 * 24 * Hour
	config.Timeouts.Refresh.Jitter.Min = 20 * Minute
	config.Timeouts.Refresh.Jitter.Max = 70 * Minute
	config.Disk.Max = "10G"

	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Fatalf("Could not read YAML: %v", err)
	}
	log.Printf("Config read")

	if root != "" {
		config.Root = root
	}
	if listen != "" {
		config.Listen = listen
	}

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
		log.Fatal("disk:policy: must be either 'lru', 'deny', or 'warn'")
	}

	root, err = expandPath(config.Root)

	if err != nil {
		log.Fatal(err)
	}


	tmpDir := root + "/.tmp"
	// Wipe
	os.RemoveAll(tmpDir)
	// Create
	if err := os.Mkdir(tmpDir, 0700); err != nil {
		log.Fatal(err)
	}

	html400 = []byte(strings.Replace(string(html400), "__GITBOX_VERSION", gitboxVersion, -1))
	html404 = []byte(strings.Replace(string(html404), "__GITBOX_VERSION", gitboxVersion, -1))
	html500 = []byte(strings.Replace(string(html500), "__GITBOX_VERSION", gitboxVersion, -1))

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeouts.Git.Regular.D())
	defer cancel()
	gitdir, err := exec.CommandContext(ctx, "git", "--exec-path").Output()

	if err != nil {
		log.Fatal(err)
	}

	backend := filepath.Join(strings.TrimSpace(string(gitdir)), "git-http-backend")

	h := &handler {
		effectiveConfig: &config,
		root: root,
		auth: config.Auth,
		gitTimeout: config.Timeouts.Git.Regular.D(),	
		gitCloneTimeout: config.Timeouts.Git.Clone.D(),
		defaultRefresh: config.Timeouts.Refresh.Default.D(),
		maxRefresh: config.Timeouts.Refresh.Max.D(),
		minJitter: config.Timeouts.Refresh.Jitter.Min.D(),
		maxJitter: config.Timeouts.Refresh.Jitter.Max.D(),
		diskUsagePolicy: config.Disk.Policy,
		maxDiskUsage: parseDiskSize(config.Disk.Max),
		git: &cgi.Handler {
			Path: backend,
			Dir: root,
			Env: []string {
				"GIT_PROJECT_ROOT=" + root,
				"GIT_HTTP_EXPORT_ALL=1",
			},
		},
	}

	h.startup = time.Now()

	h.tokens = map[string]tokenInfo{}

	for _, t := range config.Tokens {
		if t.Id == "" || t.Hash == "" {
			log.Fatalf("For each token, id:, hash:, and level: must be set")
		}

		if t.Level == "" {
			t.Level = "fetch"
		}

		if t.Level != "admin" && t.Level != "fetch" {
			log.Fatalf("For each token, level: must be set to either 'fetch' or 'admin'")
		}

		h.tokens[t.Id] = tokenInfo {
			t.Hash, t.Level,
		}
	}

	// Populates h.repos
	h.walkRepos()

	for r := range h.repos {
		ctx, cancel := context.WithCancel(context.Background())
		info := h.repos[r]
		info.cancelRefresher = cancel
		h.repos[r] = info
		go h.refresher(ctx, r)
	}

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
