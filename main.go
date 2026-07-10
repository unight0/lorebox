package main



import (
	"path/filepath"
	"net/http"
	"net/http/cgi"
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
)

type handler struct {
	root string
	gitTimeout time.Duration
	git *cgi.Handler
}

	
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
	w.WriteHeader(404)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	_, err := w.Write(html404)

	if err != nil {
		log.Printf("404 write: %v", err)
	}
}

// Client error; invalid request
func (h *handler) serve400(w http.ResponseWriter) {
	w.WriteHeader(400)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	_, err := w.Write(html400)

	if err != nil {
		log.Printf("400 write: %v", err)
	}
}

// Internal server error
func (h *handler) serve500(w http.ResponseWriter) {
	w.WriteHeader(500)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

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

	w.WriteHeader(200)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

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

		size := ""
		info, err := e.Info()

		if err == nil {
			size = fmt.Sprintf("%010d", info.Size())
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
				info.ModTime().Format("2006-01-02 15:04:05"),
				filepath.Clean(path + "/" + e.Name()),
				e.Name(),
		))
	}

	bw.WriteString("</table><hr>gitbox server v0.1")
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

	w.WriteHeader(200)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

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

func (h *handler) fetchRepo(repo string) bool {

	url := "https:/" + repo
	// First just ls...
	cmd := exec.Command("git", "ls-remote", "--exit-code", url)
	cmd.WaitDelay = h.gitTimeout

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to ls-remote '%s': %v", url, err)
		return false
	}

	path := filepath.Clean(h.root + repo)
	cmd = exec.Command("git", "clone", "--mirror", url, path)
	cmd.WaitDelay = h.gitTimeout
	cmd.Dir = h.root

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to mirror clone '%s': %d", url, err)
		return false
	}

	cmd = exec.Command("git", "update-server-info")
	cmd.WaitDelay = h.gitTimeout
	cmd.Dir = path

	if err := cmd.Run(); err != nil {
		log.Printf("Failed to run update-server-info: %s, %v", repo, err)
		return false
	}

	//registerRepo()

	return true
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
	if !strings.HasPrefix(path, h.root) {
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

			h.serveFile(w, path)
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

func expandPath(path string) (string, error) {
	path, err := filepath.Abs(path)

	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(path)

	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return path, nil
		}

		return "", err
	}

	return resolved, nil
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

	log.Printf("Starting gitbox v0.1")

	gitdir, err := exec.Command("git", "--exec-path").Output()

	if err != nil {
		log.Fatal(err)
	}

	backend := filepath.Join(strings.TrimSpace(string(gitdir)), "git-http-backend")

	h := &handler {
		root: root,
		gitTimeout: time.Minute * 5,	
		git: &cgi.Handler {
			Path: backend,
			Dir: root,
			Env: []string {
				"GIT_PROJECT_ROOT=" + root,
				"GIT_HTTP_EXPORT_ALL=1",
			},
		},
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
