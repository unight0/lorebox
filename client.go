package main

import (
	"net/http"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"go.yaml.in/yaml/v4"
	"log"
	"io"
	"flag"
	"fmt"
)

type ClientConfig struct {
	Default string
	Tokens map[string]string
}

type clientContext struct {
	box, auth, proto string
}

func (c *clientContext) api(name string) string {
	return c.proto + "://" + c.box + "/-/" + name
}

func (c *clientContext) apiRequest(what string) *http.Response {
	req, err := http.NewRequest(http.MethodGet, c.api(what), nil)
	if err != nil {
		log.Fatalf("Http request error: %v", err)
	}

	cred := base64.StdEncoding.EncodeToString([]byte(c.auth))

	req.Header.Set("Authorization", "Basic " + cred)
	req.Header.Set("User-Agent", "Gitbox/" + loreboxVersion)
	req.Header.Set("X-Gitbox-Api", "On")

	client := &http.Client{}

	resp, err := client.Do(req)

	if err != nil {
		log.Fatalf("Http request error: %v", err)
	}

	return resp
}

func readBody(body io.Reader) string {
	text, err := io.ReadAll(body)

	if err != nil {
		log.Fatalf("Body read error: %v", err)
	}

	return string(text)
}

func sayStatusCode(code int) {
	fmt.Printf("status %d\n", code)
}

func (c *clientContext) simple(what string) {
	resp := c.apiRequest(what)

	sayStatusCode(resp.StatusCode)

	fmt.Printf("%s", readBody(resp.Body))
}

func (c *clientContext) refreshAll() {
	fmt.Printf("This may take a while -- please be patient...\n")

	resp := c.apiRequest("refresh-all")

	sayStatusCode(resp.StatusCode)

	fmt.Printf("%s", readBody(resp.Body))
}

func (c *clientContext) refresh() {
	repo := flag.Args()[0]

	c.simple("refresh/" + repo)
}

func (c *clientContext) evict() {
	repo := flag.Args()[0]

	c.simple("evict/" + repo)
}

func (c *clientContext) fetch() {
	repo := flag.Args()[0]

	c.simple("fetch/" + repo)
}

func (c *clientContext) fetchHttp() {
	repo := flag.Args()[0]

	fmt.Printf("You are fetching %s over http. Note that this is highly insecure; a MiTM can inject arbitrary code, and you are caching it\n", repo)

	c.simple("fetch-http/" + repo)
}

func (c *clientContext) pin() {
	repo := flag.Args()[0]

	c.simple("pin/" + repo)
}

func (c *clientContext) unpin() {
	repo := flag.Args()[0]

	c.simple("unpin/" + repo)
}
const defaultConfigFile = "~/.config/lorebox/client.yml"

func getConfigData(configFile string) (configData []byte) {
	silenceNotExists := configFile == defaultConfigFile

	if configFile != "" {
		var err error

		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}

		configFile = strings.Replace(configFile, "~/", home+"/", -1)

		configFile, err = filepath.Abs(configFile)
		if err != nil {
			log.Fatal(err)
		}

		info, err := os.Stat(configFile)
		if err != nil {
			if silenceNotExists {
				return
			}
			log.Fatal(err)
		}

		if info.Mode().Perm() ^ 0600 != 0 {
			log.Fatalf("Cannot use '%s' as config file: permissions must be set to 0600", configFile)
		}

		configData, err = os.ReadFile(configFile)

		if err != nil {
			log.Fatal(err)
		}
	}
	return
}

func client() {

	var configFile, auth, box string
	var insecure bool

	flag.StringVar(&box, "box", "", "Override the remote box")
	flag.StringVar(&configFile, "config", defaultConfigFile, "Point to the client config YAML file")
	flag.StringVar(&auth, "auth", "", "Override auth token")
	flag.BoolVar(&insecure, "insecure", false, "Connect over HTTP instead of HTTPS")

	if err := flag.CommandLine.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}

	config := ClientConfig{}

	if err := yaml.Unmarshal(getConfigData(configFile), &config); err != nil {
		log.Fatal(err)
	}

	if box == "" && config.Default == "" {
		log.Fatalf("Specify the box to connect to either via -box or 'default:' config key (config is in %s)", defaultConfigFile)
	}

	if box == "" {
		box = config.Default
	}

	if auth == "" {
		if token, ok := config.Tokens[box]; ok {
			auth = token
		}
	}

	cl := clientContext{
		box: box,
		auth: auth,
		proto: "https",
	}

	if insecure {
		cl.proto = "http"
	}

	switch os.Args[1] {
	case "evict":
		if len(flag.Args()) != 1 {
			fmt.Printf("Evict requires exactly 1 argument\n")
			usage()
			return
		}
		cl.evict()
		return
	case "refresh":
		if len(flag.Args()) != 1 {
			fmt.Printf("Refresh requires exactly 1 argument\n")
			usage()
			return
		}
		cl.refresh()
		return
	case "fetch":
		if len(flag.Args()) != 1 {
			fmt.Printf("Fetch requires exactly 1 argument\n")
			usage()
			return
		}
		cl.fetch()
		return
	case "fetch-http":
		if len(flag.Args()) != 1 {
			fmt.Printf("Fetch requires exactly 1 argument\n")
			usage()
			return
		}
		cl.fetchHttp()
		return
	case "pin":
		if len(flag.Args()) != 1 {
			fmt.Printf("Pin requires exactly 1 argument\n")
			usage()
			return
		}
		cl.pin()
		return
	case "unpin":
		if len(flag.Args()) != 1 {
			fmt.Printf("Unpin requires exactly 1 argument\n")
			usage()
			return
		}
		cl.unpin()
		return
	case "refresh-all":
		cl.refreshAll()
		return
	case "list":
		cl.simple("list")
		return
	case "status":
		cl.simple("status")
		return
	case "effective-config":
		cl.simple("effective-config")
		return
	default:
		fmt.Printf("Unknown command verb: %v\n", os.Args[1])
		usage()
		return
	}

}

