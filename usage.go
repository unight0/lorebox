package main

import "fmt"

func usage() {
	fmt.Printf("gitbox server and remote control panel\n")
	fmt.Printf("Verbs:\n\n")

	fmt.Printf("Remote control:\n")
	fmt.Printf("status              Query box status\n")
	fmt.Printf("evict <repo>        Evict (delete) a repo\n")
	fmt.Printf("refresh <repo>      Refresh a repo\n")
	fmt.Printf("fetch <remote>      Cache remote repo\n")
	fmt.Printf("refresh-all         Refresh all repos in a box\n")
	fmt.Printf("effective-config    Print current effective config\n")
	fmt.Printf("list                List all cached repos in a box\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  -box              Override the remote box\n")
	fmt.Printf("  -auth             Override auth token (syntax <id>:<token>)\n")
	fmt.Printf("  -config           Point to the client config YAML file\n")
	fmt.Printf("  -insecure         Connect over HTTP instead of HTTPS\n")
	fmt.Printf("  -help             Options help\n\n")

	fmt.Printf("Utilities:\n")
	fmt.Printf("gen-token       Generate access token credentials\n\n")

	fmt.Printf("Server:\n")
	fmt.Printf("serve           Run the gitbox server\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  -listen       Override the bind port and address\n")
	fmt.Printf("  -root         Override the document (git database) root\n")
	fmt.Printf("  -config       Point to the config YAML file\n")
	fmt.Printf("  -insecure     Use HTTP instead of HTTPS\n")
	fmt.Printf("  -help         Options help\n")
}

