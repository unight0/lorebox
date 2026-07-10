package main


import (
	"path/filepath"
	"net/http"
	"net/http/cgi"
	"math/rand/v2"
	"log"
	"fmt"
	"time"
	"bufio"
	"os"
	"os/exec"
	"strings"
	"flag"
	"strconv"
	"sync"
	"encoding/base64"
	"go.yaml.in/yaml/v4"
	"crypto/sha256"
	sn "golang.org/x/sync/singleflight"
	cr "crypto/rand"
	_ "embed"
)

type handler struct {
	root, auth string
	gitTimeout, defaultRefresh, maxRefresh, minJitter, maxJitter time.Duration
	git *cgi.Handler
	fetchGroup sn.Group
	tokens map[string]string
	repos map[string]repoDescription
	reposLock sync.RWMutex
	startup time.Time
	effectiveConfig *Config
}

type repoDescription struct {
	repo string
	size int64
}

const gitboxVersion = "v0.2"
	
var infoRefs = "/info/refs"

func refreshDefaultBranch(path string) bool {
	cmd := exec.Command("git", "ls-remote", "--symref", "origin", "HEAD")
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

	cmd = exec.Command("git", "symbolic-ref", "HEAD", "refs/heads/" + ref)
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}

	return true
}

func refreshRepo(path string) bool {
	// Non-critical if fails
	refreshDefaultBranch(path)

	cmd := exec.Command("git", "fetch", "--prune", "origin")
	cmd.Stdout = os.Stdout
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to refresh repo: %s, %v", path, err)
		return false
	}

	return updateServerInfo(path, log.Default())
}

func configureNewRepo(path string, logg *log.Logger) bool {
	// Config for the future, so git doesn't lose refs/gitbox/*
	cmd := exec.Command("git", "config", "--unset", "remote.origin.mirror")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (unset mirror): %s, %v", path, err)
		return false
	}

	cmd = exec.Command("git", "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (replace fetch): %s, %v", path, err)
		return false
	}

	cmd = exec.Command("git", "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	cmd.Dir = path
	cmd.Stdout = logg.Writer()
	cmd.Stderr = logg.Writer()

	if err := cmd.Run(); err != nil {
		logg.Printf("Failed to configure repo (add fetch): %s, %v", path, err)
		return false
	}

	return true
}

func updateServerInfo(path string, logg *log.Logger) bool {
	cmd := exec.Command("git", "update-server-info")
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

func (h *handler) fetchRepo(repo string, logg *log.Logger) bool {

	success, _, _ := h.fetchGroup.Do(repo, func() (any, error) {
		url := "https:/" + repo

		// TODO: timeouts

		// First just ls...
		cmd := exec.Command("git", "ls-remote", "--exit-code", url)

		if err := cmd.Run(); err != nil {
			logg.Printf("Failed to ls-remote '%s': %v", url, err)
			return false, nil
		}

		path := filepath.Clean(h.root + "/" + repo)

		cmd = exec.Command("git", "clone", "--mirror", url, path)
		cmd.Dir = h.root
		cmd.Stdout = logg.Writer()
		cmd.Stderr = logg.Writer()

		if err := cmd.Run(); err != nil {
			logg.Printf("Failed to mirror clone '%s': %v", url, err)
			return false, nil
		}

		if !configureNewRepo(path, logg) {
			return false, nil
		}

		if !updateServerInfo(path, logg) {
			return false, nil
		}

		size, err := dirSize(path)

		if err != nil {
			logg.Printf("Failed to calculate directory size: %d", path)
		}

		h.reposLock.Lock()
		h.repos[path] = repoDescription {
			repo: repo,
			size: size,
		}
		h.reposLock.Unlock()
		
		go h.refresher(path)

		return true, nil
	})

	return success.(bool)
}

func (h *handler) validateCredentials(id, token string) bool {
	log.Printf("Validating %s", id)

	hash := sha256.Sum256([]byte(token))
	enHash := base64.RawURLEncoding.EncodeToString(hash[:])

	servHash, ok := h.tokens[id]

	if !ok {
		log.Printf("Unknown token id %s", id)
		return false
	}

	if enHash != servHash {
		log.Printf("Hash doesn't match: %s", id)
		return false
	}

	log.Printf("%s validated", id)
	return true
}


func (h *handler) requireAuth(w http.ResponseWriter, req *http.Request) bool {
	if h.auth != "none" {
		id, token, ok := req.BasicAuth()
		if !ok || !h.validateCredentials(id, token) {
			h.serve401(w)
			return false
		}
	}
	return true
}

func (h *handler) apiList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	var totalSize int64

	bw.WriteString(fmt.Sprintf("Total %d repos\n", len(h.repos)))
	for _, d := range h.repos {
		bw.WriteString(fmt.Sprintf("%s %d\n", d.repo, d.size))
		totalSize += d.size
	}

	bw.WriteString(fmt.Sprintf("Total %d bytes\n", totalSize))

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

	bw.WriteString(fmt.Sprintf("gitbox version %s\nuptime %s\ntotal storage %d\n",
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
		bw.WriteString(fmt.Sprintf("Error: %w\n", err))
		return
	}

	bw.Write(out)
}

func (h *handler) apiFetch(w http.ResponseWriter, repo string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	logg := log.New(bw, "", log.LstdFlags)

	if h.fetchRepo(repo, logg) {
		logg.Printf("Success\n")
	} else {
		logg.Printf("Fail\n")
	}

	bw.Flush()
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

	if strings.HasPrefix(req.URL.Path, "/-/fetch/") {
		h.apiFetch(w, req.URL.Path[len("/-/fetch"):])
		return
	}

	// Invalid API point
	h.serve400(w)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	svc := req.URL.Query().Get("service")

	if h.auth == "all" {
		id, token, ok := req.BasicAuth()
		if !ok || !h.validateCredentials(id, token) {
			h.serve401(w)
			return
		}
	}

	if req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/-/") {
		//if !h.requireAuth(w, req) {
		//	return
		//}
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

		if _, err := os.Stat(h.root + repo); err != nil {

			// Authenticate
			if !h.requireAuth(w, req) {
				return
			}

			log.Printf("Running pullthrough on '%s'", repo)
			
			if !h.fetchRepo(repo, log.Default()) {
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

func (h *handler) refresher(path string) {
	duration := h.jitteredRefresh()

	for {
		time.Sleep(duration)

		if !refreshRepo(path) {

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
		return fmt.Errorf("bad duration %q: %w", s, err)
	}
	*d = D(dur)
	return nil
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
		Git Duration
		Refresh struct {
			Default Duration
			Max Duration
			Jitter struct {
				Max Duration
				Min Duration
			}
		}
	}

	Tokens []string

	//HttpCert string		`yaml:"https:cert"`
	//HttpKey string		`yaml:"https:key"`
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

	fmt.Printf("Successfully generated token credentials\n")
	fmt.Printf("Public (server) component:\n%s:%s\n\n", enTokenId, enHash)
	fmt.Printf("Private component:\n%s:%s\n", enTokenId, enToken)
}

func main() {
	var listen, root, configFile string
	var genToken bool

	flag.StringVar(&listen, "listen", "", "Override the bind port and address")
	flag.StringVar(&root, "root", "", "Override the document root")
	flag.StringVar(&configFile, "config", "", "Point to the config YAML file")
	flag.BoolVar(&genToken, "gen-token", false, "Generate authentication credentials")
	flag.Parse()

	if genToken {
		generateCredentials()
		return
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
	config.Timeouts.Git = 10 * Minute
	config.Timeouts.Refresh.Default = 12 * Hour
	config.Timeouts.Refresh.Max = 20 * 24 * Hour
	config.Timeouts.Refresh.Jitter.Min = 20 * Minute
	config.Timeouts.Refresh.Jitter.Max = 70 * Minute

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

	root, err = expandPath(root)

	if err != nil {
		log.Fatal(err)
	}

	html400 = []byte(strings.Replace(string(html400), "__GITBOX_VERSION", gitboxVersion, -1))
	html404 = []byte(strings.Replace(string(html404), "__GITBOX_VERSION", gitboxVersion, -1))
	html500 = []byte(strings.Replace(string(html500), "__GITBOX_VERSION", gitboxVersion, -1))

	gitdir, err := exec.Command("git", "--exec-path").Output()

	if err != nil {
		log.Fatal(err)
	}

	backend := filepath.Join(strings.TrimSpace(string(gitdir)), "git-http-backend")

	h := &handler {
		effectiveConfig: &config,
		root: root,
		auth: config.Auth,
		gitTimeout: config.Timeouts.Git.D(),	
		defaultRefresh: config.Timeouts.Refresh.Default.D(),
		maxRefresh: config.Timeouts.Refresh.Max.D(),
		minJitter: config.Timeouts.Refresh.Jitter.Min.D(),
		maxJitter: config.Timeouts.Refresh.Jitter.Max.D(),
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

	h.tokens = map[string]string{}
	for _, t := range config.Tokens {
		parts := strings.Split(t, ":")

		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
			log.Fatalf("Invalid token syntax: %s, expected something of form <id>:<hash>", t)
		}

		h.tokens[parts[0]] = parts[1]
	}

	// Populates h.repos
	h.walkRepos()

	for r := range h.repos {
		go h.refresher(r)
	}

	serv := &http.Server {
		Addr: config.Listen,
		Handler: h,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout: 20 * time.Second,
		MaxHeaderBytes: 10 * 1024,
	}

	log.Fatal(serv.ListenAndServe())
}
