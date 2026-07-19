package main

import (
	"net/http"
	"html"
	"os"
	"errors"
	"io"
	"strings"
	"log"
	"fmt"
	"bufio"
	"time"
	"path/filepath"
)

func (h *handler) serveDir(w http.ResponseWriter, path string) {
	entries, err := os.ReadDir(h.root + path)

	if err != nil {
		log.Printf("error at os.ReadDir(): %v", err)
		h.serve500(w)
		return
	}

	w.Header().Set("Cache-Control", "max-age=60, no-transform")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)

	bw := bufio.NewWriter(w)

	bw.Write(htmlGenericBegin)
	fmt.Fprintf(bw,
		`
		 <style>
		 %s
		 th, td, tr, table {
			 text-align: left;
		 }
		 table {
			 border-spacing: 0 2px;
			 width: 100%%;
			 padding: 0 3%% 0 3%%;
		 }
		 tbody tr:nth-child(even) {
			 background-color: #1a1a1a;
		 }
		 tbody tr:nth-child(odd) {
			 background-color: #000000;
		 }
		 tbody tr:hover {
		 	background-color: #262626;
		 }
		 </style>
		 <h1>(<a href="%s">back</a>) <a href="/">index</a>: %s</h1>
		 <hr>
		 <table>
		 <thead>
		 <tr>
		 	<th>Name</th>
		 	<th>Size</th>
		 	<th>Is directory</th>
		 	<th>Last modified</th>
		 </tr>
		 </thead><tbody>
		 `,
		cssStyle,
		filepath.Dir(path),
		path,
	)

	// Inject /repos.txt
	if path == "/" {
		fmt.Fprintf(bw,
				`<tr>
					<td><a href="%s">%s</a></td>
					<td></td>
					<td><span class="no">No</span></td>
					<td><span class="meta">%s</span></td>
				</tr>`,
				"/repos.txt",
				"repos.txt",
				time.Now().Format("2006-01-02 15:04:05"),
		)
	}

	for _, e := range entries {
		if h.excludedPath(filepath.Clean(h.root + "/" + path + "/" + e.Name())) {
			continue
		}
		if h.hiddenRepoPath(path + "/" + e.Name(), log.Default()) {
			continue
		}

		dir := `<span class="no">No</span>`

		if e.IsDir() {
			dir = `<span class="yes">Yes</span>`
		}

		size, modtime := "", ""
		info, err := e.Info()
		name := e.Name()

		if err == nil {
			size = fmt.Sprintf("%010d", info.Size())
			modtime = info.ModTime().Format("2006-01-02 15:04:05")
			if info.IsDir() {
				name += "/"
			}
		}

		fmt.Fprintf(bw,
				`<tr>
					<td><a href="%s">%s</a></td>
					<td><span class="meta">%s</span></td>
					<td>%s</td>
					<td><span class="meta">%s</span></td>
				</tr>`,
				filepath.Clean(path + "/" + e.Name()),
				html.EscapeString(name),
				size,
				dir,
				modtime,
		)
	}

	fmt.Fprintf(bw, "</tbody></table><hr><i>%s</i>", fullSelfID())
	bw.Write(htmlGenericEnd)
	bw.Flush()
}

func (h *handler) intraRepoPath(path string) (string, bool) {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	for p := range h.repos {
		p = filepath.Clean(p) + "/"
		if strings.HasPrefix(path, p) {
			return "/" + path[len(p):], true
		}
	}

	return path, false
}

func (h *handler) cacheablePath(path string) bool {

	path, ok := h.intraRepoPath(path)

	if !ok {
		return false
	}

	if hasPostfix(path, "/HEAD") ||
		hasPostfix(path, "/info/refs") ||
		strings.Contains(path, "/objects/info/") {
		return false
	}

	if strings.Contains(path, "/objects/") {
		return true
	}

	return false
}

func (h *handler) excludedPath(path string) bool {

	// /.tmp dir should not be accessible
	if strings.HasPrefix(path, filepath.Clean(h.root + "/.tmp")) {
		return true
	}

	path, ok := h.intraRepoPath(path)

	if !ok {
		return false
	}

	return hasPostfix(path, "/lorebox.access") ||
		hasPostfix(path, "/config") ||
		hasPostfix(path, "/description") ||
		strings.Contains(path, "/hooks/") ||
		hasPostfix(path, "/hooks") ||
		hasPostfix(path, "/FETCH_HEAD")
}

func (h *handler) serveFile(w http.ResponseWriter, path string) {

	abspath := filepath.Clean(h.root + path)

	if hasPostfix(abspath, infoRefs) {
		dirpath := chopInfoRefs(abspath)
		recordAccess(dirpath, log.Default())
	}

	file, err := os.Open(abspath)

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

	w.Header().Set("Cache-Control", "max-age=60, no-transform")
	if h.cacheablePath(path) {
		w.Header().Set("Cache-Control", "max-age=31536000, immutable, no-transform")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)

	_, err = io.Copy(w, file)

	if err != nil {
		log.Printf("error at io.Copy(): %v", err)
		h.serve500(w)
		return
	}
}

func (h *handler) serveFS(w http.ResponseWriter, req *http.Request) {

	relpath := filepath.Clean("/" + req.URL.Path)
	path, err := expandPath(h.root + relpath)

	if h.excludedPath(path) {
		log.Printf("Excluded repo path access: %s\n", relpath)
		h.serve404(w)
		return
	}

	if h.hiddenRepoPath(relpath, log.Default()) {
		log.Printf("Hidden repo path access: %s\n", relpath)
		h.serve404(w)
		return
	}

	if err != nil {
		log.Printf("expandPath(): %v", err)
		h.serve500(w)
		return
	}

	// Outside of the root directory
	if !strings.HasPrefix(path + "/", filepath.Clean(h.root) + "/") {
		log.Printf("External path '%s' was requested", path)
		h.serve400(w)
		return
	}

	info, err := os.Stat(path)

	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("Can't stat '%s': %v", path, err)
			h.serve500(w)
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
