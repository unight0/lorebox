package main

import "fmt"

func synonyms() {
	fmt.Printf("Verb synonyms:\n\n")

	fmt.Printf("status              st, stat\n")
	fmt.Printf("evict               ev\n")
	fmt.Printf("refresh             re\n")
	fmt.Printf("unpin               unp\n")
	fmt.Printf("fetch               fe\n")
	fmt.Printf("fetch-http          feh\n")
	fmt.Printf("refresh-all         rea\n")
	fmt.Printf("effective-config    conf\n")
	fmt.Printf("list                ls\n")
	fmt.Printf("create              cr, init\n")
	fmt.Printf("delete              del\n")
	fmt.Printf("hide                hid\n")
	fmt.Printf("unhide              unh\n")
	fmt.Printf("gen-token           gen, gen-tok\n")
	fmt.Printf("register            reg\n")
	fmt.Printf("serve               srv\n")
	fmt.Printf("synonyms            syn, syns\n")
}

func usage() {
	fmt.Printf("lorebox server and remote control panel\n")
	fmt.Printf("Verbs:\n\n")

	fmt.Printf("Remote admin control:\n")
	fmt.Printf("status              Query box status\n")
	fmt.Printf("evict <repo>        Evict (delete) a repo\n")
	fmt.Printf("refresh <repo>      Refresh a repo\n")
	fmt.Printf("pin <repo>          Pin a repo\n")
	fmt.Printf("unpin <repo>        Unpin a repo\n")
	fmt.Printf("fetch <remote>      Cache remote repo\n")
	fmt.Printf("fetch-http <remote> Cache remote repo, use HTTP\n")
	fmt.Printf("refresh-all         Refresh all repos in a box\n")
	fmt.Printf("effective-config    Print current effective config\n")
	fmt.Printf("list                List all cached repos in a box\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  -box              Override the remote box\n")
	fmt.Printf("  -auth             Override auth token (syntax <id>:<token>)\n")
	fmt.Printf("  -config           Point to the client config YAML file\n")
	fmt.Printf("  -insecure         Connect over HTTP instead of HTTPS\n")
	fmt.Printf("  -help             Options help\n")

	fmt.Printf("\nRemote self-hosted repo control:\n")
	fmt.Printf("create <user/repo>  Create a new repo\n")
	fmt.Printf("delete <user/repo>  Delete your repo\n")
	fmt.Printf("hide   <user/repo>  Hide your repo\n")
	fmt.Printf("unhide <user/repo>  Unhide your repo\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  -box              Override the remote box\n")
	fmt.Printf("  -auth             Override auth token (syntax <id>:<token>)\n")
	fmt.Printf("  -config           Point to the client config YAML file\n")
	fmt.Printf("  -insecure         Connect over HTTP instead of HTTPS\n")
	fmt.Printf("  -help             Options help\n")

	fmt.Printf("\nUtilities:\n")
	fmt.Printf("gen-token <username> Generate access token credentials\n")
	fmt.Printf("register             Register lorebox as authenticator for your loreboxes with git\n")
	fmt.Printf("credential get       Credential helper for git, not for manual use\n")
	fmt.Printf("synonyms             List verb synonyms\n")

	fmt.Printf("\nServer:\n")
	fmt.Printf("serve           Run the lorebox server\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  -listen       Override the bind port and address\n")
	fmt.Printf("  -root         Override the document (git database) root\n")
	fmt.Printf("  -config       Point to the config YAML file\n")
	fmt.Printf("  -insecure     Use HTTP instead of HTTPS\n")
	fmt.Printf("  -help         Options help\n")
}

