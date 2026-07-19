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

func (h *handler) serveDir(w http.ResponseWriter, rpath RepoPath) {
	entries, err := os.ReadDir(rpath.Path(h).S())

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
		filepath.Dir(rpath.S()),
		rpath.S(),
	)

	// Inject /repos.txt
	if rpath.S() == "/" {
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
		if h.excludedPath(rpath.Path(h).Concat(e.Name())) {
			continue
		}
		if h.hiddenRepoPath(rpath.Concat(e.Name()), log.Default()) {
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
				rpath.Concat(e.Name()),
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

func (h *handler) intraRepoPath(path Path) (IRPath, bool) {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	for p := range h.repos {
		ps := p.S() + "/"
		if strings.HasPrefix(path.S(), ps) {
			return IRPath(path.S()[len(p):]), true
		}
	}

	return IRPath(path.S()), false
}

func (h *handler) cacheablePath(path Path) bool {

	ipath, ok := h.intraRepoPath(path)

	if !ok {
		return false
	}

	sipath := ipath.S()

	if hasPostfix(sipath, "/HEAD") ||
		hasPostfix(sipath, "/info/refs") ||
		strings.Contains(sipath, "/objects/info/") {
		return false
	}

	if strings.Contains(sipath, "/objects/") {
		return true
	}

	return false
}

func (h *handler) excludedPath(path Path) bool {

	// /.tmp dir should not be accessible
	if strings.HasPrefix(path.S(), h.tmpDir().S() + "/") {
		return true
	}

	ipath, ok := h.intraRepoPath(path)

	if !ok {
		return false
	}

	sipath := ipath.S()

	return hasPostfix(sipath, "/lorebox.access") ||
		hasPostfix(sipath, "/config") ||
		hasPostfix(sipath, "/description") ||
		strings.Contains(sipath, "/hooks/") ||
		hasPostfix(sipath, "/hooks") ||
		hasPostfix(sipath, "/FETCH_HEAD")
}

func (h *handler) serveFile(w http.ResponseWriter, rpath RepoPath) {

	//abspath := filepath.Clean(h.root + path)
	abspath := rpath.Path(h)

	if hasPostfix(abspath.S(), infoRefs) {
		dirpath := chopInfoRefs(abspath.S())
		recordAccess(Path(dirpath), log.Default())
	}

	file, err := os.Open(abspath.S())

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
	if h.cacheablePath(abspath) {
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

	rpath := NewRepoPath(req.URL.Path)
	path, err := rpath.Path(h).expand()

	if err != nil {
		log.Printf("rpath.Path(h).expand(): %v", err)
		h.serve500(w)
		return
	}

	if h.excludedPath(path) {
		log.Printf("Excluded repo path access: %s\n", rpath)
		h.serve404(w)
		return
	}

	if h.hiddenRepoPath(rpath, log.Default()) {
		log.Printf("Hidden repo path access: %s\n", rpath)
		h.serve404(w)
		return
	}

	// Outside of the root directory
	if !strings.HasPrefix(path.S() + "/", h.root.S() + "/") {
		log.Printf("External path '%s' was requested", path)
		h.serve400(w)
		return
	}

	info, err := os.Stat(path.S())

	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("Can't stat '%s': %v", path, err)
			h.serve500(w)
			return
		}

		log.Printf("'%s' doesn't exist", rpath)
		h.serve404(w)
		return
	}

	if info.IsDir() {
		h.serveDir(w, rpath)
		return
	}

	h.serveFile(w, rpath)
}
