package main

import (
	"net/http"
	"encoding/base64"
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
	req.Header.Set("User-Agent", "Gitbox/" + gitboxVersion)
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

func checkStatusCode(code int) {
	if code != 200 {
		log.Fatalf("Bad status code response from server: %d", code)
	}
}

func (c *clientContext) simple(what string) {
	resp := c.apiRequest(what)

	checkStatusCode(resp.StatusCode)

	fmt.Printf("%s", readBody(resp.Body))
}

func (c *clientContext) status() {
	c.simple("status")
}

func (c *clientContext) list() {
	c.simple("list")
}

func (c *clientContext) refreshAll() {
	fmt.Printf("This may take a while -- please be patient...\n")

	resp := c.apiRequest("refresh-all")

	checkStatusCode(resp.StatusCode)

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

