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
	"io"
	"strings"
	"flag"
	"errors"
	_ "embed"
	"sync"
	sn "golang.org/x/sync/singleflight"
)

type handler struct {
	root string
	gitTimeout, defaultRefresh, maxRefresh, jitterUnit time.Duration
	minJitter, maxJitter int
	git *cgi.Handler
	fetchGroup sn.Group
	repos map[string]bool
	reposLock sync.RWMutex
}

const gitboxVersion = "v0.2"
	
//go:embed html/400.html
var html400 []byte
//go:embed html/404.html
var html404 []byte
//go:embed html/500.html
var html500 []byte
//go:embed html/generic-begin.html
var htmlGenericBegin []byte
//go:embed html/generic-end.html
var htmlGenericEnd []byte

var infoRefs = "/info/refs"

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

// Internal server error
func (h *handler) serve500(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(500)

	_, err := w.Write(html500)

	if err != nil {
		log.Printf("500 write: %v", err)
	}
}

func (h *handler) serveDir(w http.ResponseWriter, path string) {
	entries, err := os.ReadDir(h.root + path)

	if err != nil {
		log.Printf("error at os.ReadDir(): %v", err)
		h.serve500(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	bw.Write(htmlGenericBegin)
	bw.WriteString(fmt.Sprintf(
		`
		 <style>
		 th, td, tr, table {
			 text-align: left;
		 }
		 </style>
		 <h2>Index of %s</h2><hr>
		 <a href="/">Root</a>
		 <a href="%s">Back</a>`,
		path,
		filepath.Dir(path),
	))
	bw.WriteString(`
	<table>
	<tr>
		<th>Size</th>
		<th>Is directory</th>
		<th>Last modified</th>
		<th>Name</th>
	</tr>`)

	for _, e := range entries {
		dir := "No"

		if e.IsDir() {
			dir = "Yes"
		}

		size, modtime := "", ""
		info, err := e.Info()

		if err == nil {
			size = fmt.Sprintf("%010d", info.Size())
			modtime = info.ModTime().Format("2006-01-02 15:04:05")
		}

		bw.WriteString(fmt.Sprintf(
				`<tr>
					<td>%s</td>
					<td>%s</td>
					<td>%s</td>
					<td><a href="%s">%s</a></td>
				</tr>`,
				size,
				dir,
				modtime,
				filepath.Clean(path + "/" + e.Name()),
				e.Name(),
		))
	}

	bw.WriteString("</table><hr>gitbox server " + gitboxVersion)
	bw.Write(htmlGenericEnd)
	bw.Flush()
}

func (h *handler) serveFile(w http.ResponseWriter, path string) {
	file, err := os.Open(filepath.Clean(h.root + path))

	if err != nil {
		log.Printf("error at os.Open(): %v", err)

		if errors.Is(err, os.ErrNotExist) {
			h.serve404(w)
			return
		}

		h.serve500(w)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	_, err = io.Copy(w, file)

	if err != nil {
		log.Printf("error at io.Copy(): %v", err)
		h.serve500(w)
		return
	}
}

func hasPostfix(str, postfix string) bool {
	if len(postfix) > len(str) {
		return false
	}

	return str[len(str)-len(postfix):] == postfix
}

func chopInfoRefs(path string) string {
	if hasPostfix(path, infoRefs) {
		path = path[:len(path)-len(infoRefs)]
	}
	return path
}

func refreshDefaultBranch(path string) bool {
	cmd := exec.Command("git", "ls-remote", "--symref", "origin", "HEAD")
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to refresh current default branch: %s, %v", path, err)
		return false
	}

	out, err := cmd.Output()

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
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to refresh repo: %s, %v", path, err)
		return false
	}

	return updateServerInfo(path)
}

func configureNewRepo(path string) bool {
	// Config for the future, so git doesn't lose refs/gitbox/*
	cmd := exec.Command("git", "--unset", "remote.origin.mirror")
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to configure repo: %s, %v", path, err)
		return false
	}

	cmd = exec.Command("git", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to configure repo: %s, %v", path, err)
		return false
	}

	cmd = exec.Command("git", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to configure repo: %s, %v", path, err)
		return false
	}

	return true
}

func updateServerInfo(path string) bool {
	cmd := exec.Command("git", "update-server-info")
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to run update-server-info: %s, %v", path, err)
		return false
	}

	return true
}

func (h *handler) fetchRepo(repo string) bool {

	success, _, _ := h.fetchGroup.Do(repo, func() (any, error) {
		url := "https:/" + repo

		// TODO: timeouts

		// First just ls...
		cmd := exec.Command("git", "ls-remote", "--exit-code", url)

		if err := cmd.Run(); err != nil {
			log.Printf("Failed to ls-remote '%s': %v", url, err)
			return false, nil
		}

		tmpDir := os.TempDir() 

		if tmpDir == "" {
			panic("NO TEMPORARY DIRECTORY")
		}

		tmpath := filepath.Clean(tmpDir + "/" + repo)
		cmd = exec.Command("git", "clone", "--mirror", url, tmpath)
		cmd.Dir = h.root

		if err := cmd.Run(); err != nil {
			log.Printf("Failed to mirror clone '%s': %d", url, err)
			return false, nil
		}

		if !configureNewRepo(tmpath) {
			return false, nil
		}

		if !updateServerInfo(tmpath) {
			return false, nil
		}

		path := filepath.Clean(h.root + "/" + repo)
		if err := os.Rename(tmpath, path); err != nil {
			log.Printf("Failed to move repo to permanent location: %v", err)
			os.Remove(tmpath)

			return false, nil
		}

		h.reposLock.Lock()
		h.repos[path] = true
		h.reposLock.Unlock()

		go h.refresher(path)

		return true, nil
	})

	return success.(bool)
}

func (h *handler) serveFS(w http.ResponseWriter, req *http.Request) {

	relpath := filepath.Clean("/" + req.URL.Path)
	path, err := expandPath(h.root + relpath)

	if err != nil {
		log.Printf("expandPath(): %v", err)
		h.serve500(w)
		return
	}

	// Outside of the root directory
	if !strings.HasPrefix(path, filepath.Clean(h.root + "/")) {
		log.Printf("External path '%s' was requested", path)
		h.serve400(w)
		return
	}

	info, err := os.Stat(path)

	if err != nil {
		if hasPostfix(path, infoRefs) {
			repo := chopInfoRefs(relpath)

			log.Printf("Running pullthrough on '%s'", repo)

			if !h.fetchRepo(repo) {
				h.serve404(w)
				return
			}

			h.serveFile(w, relpath)
			return
		}

		log.Printf("'%s' doesn't exist", relpath)
		h.serve404(w)
		return
	}

	if info.IsDir() {
		h.serveDir(w, relpath)
		return
	}

	h.serveFile(w, relpath)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	svc := req.URL.Query().Get("service")

	// Smart ref advertisement
	if req.Method == "GET" && hasPostfix(req.URL.Path, infoRefs) && svc == "git-upload-pack"{
		repo := chopInfoRefs(filepath.Clean("/" + req.URL.Path))

		if _, err := os.Stat(h.root + repo); err != nil {
			if !h.fetchRepo(repo) {
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

	var walk func(dir string, depth int) (repos map[string]bool)

	walk = func(dir string, depth int) (repos map[string]bool) {
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
				return map[string]bool{dir: true}
			}
		}

		// Walk subdirectories
		repos = map[string]bool{}
		for _, e := range entries {
			found := walk(dir + "/" + e.Name(), depth + 1)
			for r, _ := range found {
				repos[r] = true
			}
		}

		return
	}

	// Nothing runs at this point, so no lock
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
	if h.maxJitter == 0 {
		return h.defaultRefresh
	}

	jitter := rand.Int() % (h.maxJitter - h.minJitter) + h.minJitter
	return h.defaultRefresh + time.Duration(jitter) * h.jitterUnit
}

func (h *handler) refresher(path string) {
	duration := h.jitteredRefresh()

	for {
		time.Sleep(duration)

		if !refreshRepo(path) {

			if duration > h.maxRefresh {
				log.Printf("%s exceeded max refresh duration", path)
				return
			}

			duration *= 2
		}

		duration = h.jitteredRefresh()
	}
}

func main() {
	var port, root string

	flag.StringVar(&port, "port", "8080", "Specify the port to use")
	flag.StringVar(&root, "root", ".", "Specify the document root")
	flag.Parse()

	root, err := expandPath(root)

	if err != nil {
		log.Fatal(err)
	}

	html400 = []byte(strings.Replace(string(html400), "__GITBOX_VERSION", gitboxVersion, -1))
	html404 = []byte(strings.Replace(string(html404), "__GITBOX_VERSION", gitboxVersion, -1))
	html500 = []byte(strings.Replace(string(html500), "__GITBOX_VERSION", gitboxVersion, -1))

	log.Printf("Starting gitbox " + gitboxVersion)

	gitdir, err := exec.Command("git", "--exec-path").Output()

	if err != nil {
		log.Fatal(err)
	}

	backend := filepath.Join(strings.TrimSpace(string(gitdir)), "git-http-backend")

	h := &handler {
		root: root,
		gitTimeout: time.Minute * 5,	
		defaultRefresh: time.Hour * 12,
		maxRefresh: time.Hour * 24 * 20,
		minJitter: 20,
		maxJitter: 50,
		jitterUnit: time.Minute,
		git: &cgi.Handler {
			Path: backend,
			Dir: root,
			Env: []string {
				"GIT_PROJECT_ROOT=" + root,
				"GIT_HTTP_EXPORT_ALL=1",
			},
		},
	}

	// Populates h.repos from file system
	h.walkRepos()

	for r := range h.repos {
		go h.refresher(r)
	}

	serv := &http.Server {
		Addr: ":" + port,
		Handler: h,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout: 20 * time.Second,
		MaxHeaderBytes: 10 * 1024,
	}

	log.Fatal(serv.ListenAndServe())
}
