package main

import ( 
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


func selfHosted(repo RepoPath) bool {
	return strings.HasPrefix(repo.S(), "/~/")
}

var pSHRegexpOnce sync.Once
var pSHRegexp *regexp.Regexp
func parseSelfHosted(repo RepoPath) (owner, name string) {

	pSHRegexpOnce.Do(func() {
		var err error
		pSHRegexp, err = regexp.Compile(`^\/~\/([^\/]*)\/([^\/]*)\/?(.*)$`)
		if err != nil {
			log.Fatal(err)
		}
	})

	matches := pSHRegexp.FindStringSubmatch(repo.S())	

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

// parseDiskSize parses a string in form of approximately [1-9]*(K|M|G)?, where
// K = kilobytes (1024 bytes), M = megabytes (1024 * 1024 bytes), G = gigabytes
// = (1024 * 1024 * 1024 bytes), and no postfix = bytes. Invalid format returns
// (0, error), valid format returns (X, nil).
// Maybe rework this later
func parseDiskSize(size string) (int64, error) {
	if hasPostfix(size, "G") {
		giga, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			return 0, err
		}
		return int64(giga * 1024 * 1024 * 1024), nil
	}
	if hasPostfix(size, "M") {
		mega, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			return 0, err
		}
		return int64(mega * 1024 * 1024), nil
	}
	if hasPostfix(size, "K") {
		kilo, err := strconv.Atoi(size[:len(size)-1])
		if err != nil {
			return 0, err
		}
		return int64(kilo * 1024), nil
	}

	bytes, err := strconv.Atoi(size)
	if err != nil {
		return 0, err
	}
	return int64(bytes), nil
}

// processStaticPage replaces all occurrences of __LOREBOX_VERSION with
// fullSelfID() and __STYLE by style
func processStaticPage(page []byte, style string) []byte {
	spage := strings.ReplaceAll(string(page), "__LOREBOX_VERSION", fullSelfID())
	spage = strings.ReplaceAll(spage, "__STYLE", style)
	return []byte(spage)
}

// processStaticPage runs processStaticPage() on each of the baked-in static
// pages (html400, ...401, ...404, ...405, ...500)
func processStaticPages() {
	html400 = processStaticPage(html400, cssStyle)
	html401 = processStaticPage(html401, cssStyle)
	html404 = processStaticPage(html404, cssStyle)
	html405 = processStaticPage(html405, cssStyle)
	html500 = processStaticPage(html500, cssStyle)
}
