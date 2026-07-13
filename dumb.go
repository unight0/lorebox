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

	// Inject /repos.txt
	if path == "/" {
		bw.WriteString(fmt.Sprintf(
				`<tr>
					<td>**********</td>
					<td>No</td>
					<td>%s</td>
					<td><a href="%s">%s</a></td>
				</tr>`,
				time.Now().Format("2006-01-02 15:04:05"),
				"/repos.txt",
				"repos.txt",
		))
	}

	for _, e := range entries {
		if h.excludedPath(filepath.Clean(h.root + "/" + path + "/" + e.Name())) {
			continue
		}

		dir := "No"

		if e.IsDir() {
			dir = "Yes"
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
				html.EscapeString(name),
		))
	}

	bw.WriteString("</table><hr> " + fullSelfID())
	bw.Write(htmlGenericEnd)
	bw.Flush()
}

func (h *handler) intraRepoPath(path string) (string, bool) {
	h.reposLock.RLock()
	defer h.reposLock.RUnlock()

	for p, _ := range h.repos {
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
