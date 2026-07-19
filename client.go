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
	"encoding/json"
	"time"
	"fmt"
)

type ClientConfig struct {
	Default string
	Tokens map[string]string
}

type clientContext struct {
	box, auth, proto string
}

func (c *clientContext) api(name, kind string) string {
	return c.proto + "://" + c.box + kind + name
}

func (c *clientContext) apiRequest(what, apiType string) *http.Response {
	req, err := http.NewRequest(http.MethodGet, c.api(what, apiType), nil)

	if err != nil {
		log.Fatalf("Http request error: %v", err)
	}

	cred := base64.StdEncoding.EncodeToString([]byte(c.auth))

	req.Header.Set("Authorization", "Basic " + cred)
	req.Header.Set("User-Agent", "Gitbox/" + loreboxVersion)
	req.Header.Set("X-Lorebox-Api", "On")

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
	if code != 200 {
		fmt.Printf("status %d\n", code)
	}
}

func (c *clientContext) refreshAll() {
	fmt.Printf("This may take a while -- please be patient...\n")

	resp := c.apiRequest("refresh-all", "/-/")

	sayStatusCode(resp.StatusCode)

	body := readBody(resp.Body)

	refresh := jsonableRefreshAll{}

	if err := json.Unmarshal([]byte(body), &refresh); err != nil {
		fmt.Println(err)
		fmt.Printf("Response is: %s\n", body)
		return
	}

	fmt.Printf("=== Force-refreshing all repos in box %s ===\n", c.box)
	fmt.Printf("Number of repos: %d\n", len(refresh.Repos))
	fmt.Printf("Fails: %d\n", refresh.Fails)
	for p, r := range refresh.Repos {
		fmt.Printf("%s:\n", r.Name)
		fmt.Printf("    Path: %s\n", p)
		fmt.Printf("    Status: %s\n", r.Status)
		fmt.Printf("%s\n", r.Transcript)
	}
}

func (c *clientContext) list() {
	resp := c.apiRequest("list", "/-/")

	sayStatusCode(resp.StatusCode)

	body := readBody(resp.Body)

	list := jsonableRepos{}

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		fmt.Println(err)
		fmt.Printf("Response is: %s\n", body)
		return
	}

	fmt.Printf("=== Listing repos in box %s ===\n", c.box)
	fmt.Printf("Number of repos: %d\n", len(list.Repos))
	fmt.Printf("Total repo size on disk: %d\n", list.TotalSize)
	for p, r := range list.Repos {
		fmt.Printf("%s:\n", r.Name)
		fmt.Printf("    Path: %s\n", p)
		fmt.Printf("    Size: %d\n", r.Size)
		fmt.Printf("    Pinned: %t\n", r.Pinned)
		fmt.Printf("    Requested: %d times\n", r.Requests)
		fmt.Printf("    Self-hosted: %t\n", r.SelfHosted)
		if r.SelfHosted {
			fmt.Printf("    Hidden: %t\n", r.Hidden)
		}
		if !r.LastError.IsZero() {
			fmt.Printf("    Error fetching at: %s\n", r.LastError)
		}
	}
}

func (c *clientContext) status() {
	resp := c.apiRequest("status", "/-/")

	sayStatusCode(resp.StatusCode)

	body := readBody(resp.Body)

	status := jsonableStatus{}

	if err := json.Unmarshal([]byte(body), &status); err != nil {
		fmt.Println(err)
		fmt.Printf("Response is: %s\n", body)
		return
	}

	hits := status.Cache.Hits
	misses := status.Cache.Misses
	cacheHitRatio := 0.0
	if hits + misses != 0 {
		cacheHitRatio = float64(hits)/float64(hits + misses) * 100
	}

	percentDiskUsage := 0.0
	if status.Disk.Max != 0 {
		percentDiskUsage = float64(status.Disk.Usage) / float64(status.Disk.Max) * 100
	}

	if status.Status != "success" {
		fmt.Printf("Some internal error has occured; status: %s\n", status.Status)
		return
	}

	fmt.Printf("=== Status for box %s ===\n", c.box)
	fmt.Printf("Banner: %s\n", status.Banner)
	fmt.Printf("Version: %s\n", status.Version)
	fmt.Printf("ID: %s\n", status.ID)
	fmt.Printf("Uptime:	%s\n", time.Duration(status.UptimeSec) * time.Second)
	fmt.Printf("Total number of http requests: %d\n", status.TotalHTTPReqs)
	fmt.Printf("Disk:\n")
	fmt.Printf("    Usage:  %d\n", status.Disk.Usage)
	fmt.Printf("    Max:    %d\n", status.Disk.Max)
	fmt.Printf("    Used    %.2f%%\n", percentDiskUsage)
	fmt.Printf("    Policy: %s\n", status.Disk.Policy)
	fmt.Printf("Cache:\n")
	fmt.Printf("    Hits:   %d\n", status.Cache.Hits)
	fmt.Printf("    Misses: %d\n", status.Cache.Misses)
	fmt.Printf("    CHR:    %.2f\n", cacheHitRatio)
}

func (c *clientContext) simpleGeneric(what, description string, apiType string) {
	resp := c.apiRequest(what, apiType)

	sayStatusCode(resp.StatusCode)

	body := readBody(resp.Body)

	op := jsonableOperation{}

	if err := json.Unmarshal([]byte(body), &op); err != nil {
		fmt.Printf("Error unmarshaling JSON: %v\n", err)
		fmt.Printf("Reponse: `%s`\n", body)
	}

	fmt.Printf("=== Operation %s in box %s ===\n", description, c.box)
	fmt.Printf("Status: %s\n", op.Status)
	fmt.Printf("%s\n", op.Transcript)
}

func (c *clientContext) simple(what, description string) {
	c.simpleGeneric(what, description, "/-/")
}

func (c *clientContext) simpleSH(what, description string) {
	c.simpleGeneric(what, description, "/+/")
}

func (c *clientContext) refresh() {
	repo := flag.Args()[0]

	c.simple("refresh/" + repo, "refresh repo " + repo)
}

func (c *clientContext) evict() {
	repo := flag.Args()[0]

	c.simple("evict/" + repo, "evict repo " + repo)
}

func (c *clientContext) fetch() {
	repo := flag.Args()[0]

	c.simple("fetch/" + repo, "fetch repo " + repo)
}

func (c *clientContext) fetchHttp() {
	repo := flag.Args()[0]

	fmt.Printf("You are fetching %s over http. Note that this is highly insecure; a MiTM can inject arbitrary code, and you are caching it\n", repo)

	c.simple("fetch-http/" + repo, "fetch repo " + repo + " over http")
}

func (c *clientContext) pin() {
	repo := flag.Args()[0]

	c.simple("pin/" + repo, "pin repo " + repo)
}

func (c *clientContext) unpin() {
	repo := flag.Args()[0]

	c.simple("unpin/" + repo, "unpin repo " + repo)
}

func chopSHPrefix(str string) string {
	if strings.HasPrefix(str, "~/") {
		return str[len("~/"):]
	}
	if strings.HasPrefix(str, "/~/") {
		return str[len("/~/"):]
	}
	return str
}

func (c *clientContext) create() {
	repo := chopSHPrefix(flag.Args()[0])

	c.simpleSH("create/" + repo, "create repo " + repo)
}

func (c *clientContext) delete() {
	repo := chopSHPrefix(flag.Args()[0])

	c.simpleSH("delete/" + repo, "delete repo " + repo)
}
func (c *clientContext) hide() {
	repo := chopSHPrefix(flag.Args()[0])

	c.simpleSH("hide/" + repo, "hide repo " + repo)
}

func (c *clientContext) unhide() {
	repo := chopSHPrefix(flag.Args()[0])

	c.simpleSH("unhide/" + repo, "unhide repo " + repo)
}

func (c *clientContext) effectiveConfig() {
	resp := c.apiRequest("effective-config", "/-/")

	sayStatusCode(resp.StatusCode)

	body := readBody(resp.Body)

	ec := jsonableEffectiveConfig{}

	if err := json.Unmarshal([]byte(body), &ec); err != nil {
		fmt.Printf("Error unmarshaling JSON: %v\n", err)
		fmt.Printf("Reponse: `%s`\n", body)
	}

	fmt.Printf("# === Current effective config of %s ===\n", c.box)
	fmt.Printf("# Query status: %s\n", ec.Status)
	fmt.Printf("%s\n", ec.Config)
}

func getConfigData(configFile string) (configData []byte) {
	silenceNotExists := configFile == defaultConfigFile

	if configFile != "" {
		var err error

		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}

		configFile = strings.ReplaceAll(configFile, "~/", home+"/")

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
	case "evict", "ev":
		if len(flag.Args()) != 1 {
			fmt.Printf("evict requires exactly 1 argument\n")
			usage()
			return
		}
		cl.evict()
		case "refresh", "re":
		if len(flag.Args()) != 1 {
			fmt.Printf("refresh requires exactly 1 argument\n")
			usage()
			return
		}
		cl.refresh()
		case "fetch", "fe":
		if len(flag.Args()) != 1 {
			fmt.Printf("fetch requires exactly 1 argument\n")
			usage()
			return
		}
		cl.fetch()
		case "fetch-http", "feh":
		if len(flag.Args()) != 1 {
			fmt.Printf("fetch-http requires exactly 1 argument\n")
			usage()
			return
		}
		cl.fetchHttp()
		case "pin":
		if len(flag.Args()) != 1 {
			fmt.Printf("pin requires exactly 1 argument\n")
			usage()
			return
		}
		cl.pin()
		case "unpin", "unp":
		if len(flag.Args()) != 1 {
			fmt.Printf("unpin requires exactly 1 argument\n")
			usage()
			return
		}
		cl.unpin()
		case "init", "create", "cr":
		if len(flag.Args()) != 1 {
			fmt.Printf("create requires exactly 1 argument\n")
			usage()
			return
		}
		cl.create()
		case "delete", "del":
		if len(flag.Args()) != 1 {
			fmt.Printf("delete requires exactly 1 argument\n")
			usage()
			return
		}
		cl.delete()
		case "hide", "hid":
		if len(flag.Args()) != 1 {
			fmt.Printf("hide requires exactly 1 argument\n")
			usage()
			return
		}
		cl.hide()
		case "unhide", "unh":
		if len(flag.Args()) != 1 {
			fmt.Printf("unhide requires exactly 1 argument\n")
			usage()
			return
		}
		cl.unhide()
		case "refresh-all", "rea":
		cl.refreshAll()
		case "list", "ls":
		cl.list()
		case "status", "st", "stat":
		cl.status()
		case "effective-config", "conf":
		cl.effectiveConfig()
	default:
		fmt.Printf("Unknown command verb: %v\n", os.Args[1])
		usage()
		return
	}

}

