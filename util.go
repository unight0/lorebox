package main

import ( 
	"path/filepath"
	"strconv"
	"log"
	"fmt"
	"strings"
	"runtime/debug"
	"sync"
	"regexp"
)

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

func chopPostfix(str, postfix string) string {
	if hasPostfix(str, postfix) {
		str = str[:len(str)-len(postfix)]
	}
	return str
}

func (h *handler) chopRoot(path string) string {
	if strings.HasPrefix(path, filepath.Clean(h.root) + "/") {
		path = path[len(h.root):]
	}
	return path
}

func selfHosted(repo string) bool {
	return strings.HasPrefix(repo, "/~/")
}

var pSHRegexpOnce sync.Once
var pSHRegexp *regexp.Regexp
func parseSelfHosted(repo string) (owner, name string) {

	pSHRegexpOnce.Do(func() {
		var err error
		pSHRegexp, err = regexp.Compile(`^\/~\/([^\/]*)\/([^\/]*)\/?(.*)$`)
		if err != nil {
			log.Fatal(err)
		}
	})

	matches := pSHRegexp.FindStringSubmatch(repo)	

	if matches == nil {
		return
	}

	// Something went wrong
	if len(matches) != 4 {
		return
	}

	// match[0] is the entire string; match[3] is the path within the repo
	return matches[1], matches[2]
}

// Why global var? fullSelfID() should be accessible from any part of the
// program, not only handler.*. Maybe this design is unnecessary, update this
// later
var loreboxName = "unnamed"
var fullSelfIDOnce sync.Once
var fullSelfIDVar string
func fullSelfID() string {

	fullSelfIDOnce.Do(func() {
		id := fmt.Sprintf(`lorebox %s "%s"`, loreboxVersion, loreboxName)

		info, ok := debug.ReadBuildInfo()

		if !ok {
			return
		}

		id += " "

		for _, s := range info.Settings {
			//if s.Key == "vcs" {
			//	id += s.Value + " "
			//}
			if s.Key == "vcs.revision" {

				// Shorten it
				if len(s.Value) > 7 {
					s.Value = s.Value[:7]
				}

				id += s.Value + " "
			}
			if s.Key == "vcs.modified" {
				if s.Value == "true" {
					id += "modified "
				}
			}
		}
		fullSelfIDVar = id
	})

	return fullSelfIDVar
}

// Maybe rework this later
func parseDiskSize(size string) int64 {
	if hasPostfix(size, "G") {
		giga, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			log.Fatalf("Could not parse '%s': %v", size, err)
		}
		return int64(giga * 1024 * 1024 * 1024)
	}
	if hasPostfix(size, "M") {
		mega, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			log.Fatalf("Could not parse '%s': %v", size, err)
		}
		return int64(mega * 1024 * 1024)
	}
	if hasPostfix(size, "K") {
		kilo, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			log.Fatalf("Could not parse '%s': %v", size, err)
		}
		return int64(kilo * 1024)
	}

	bytes, err := strconv.Atoi(size)
	if err != nil {
		log.Fatalf("Could not parse '%s': %v", size, err)
	}
	return int64(bytes)
}

func processStaticPage(page []byte, style string) []byte {
	spage := strings.ReplaceAll(string(html400), "__LOREBOX_VERSION", fullSelfID())
	spage = strings.ReplaceAll(spage, "__STYLE", style)
	return []byte(spage)
}
