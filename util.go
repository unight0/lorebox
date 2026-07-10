package main

import ( 
	"path/filepath"
	"strings"
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

func (h *handler) chopRoot(path string) string {
	if strings.HasPrefix(path, filepath.Clean(h.root + "/")) {
		path = path[len(h.root):]
	}
	return path
}

