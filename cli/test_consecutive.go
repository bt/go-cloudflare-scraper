package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"time"

	scraper "github.com/cardigann/go-cloudflare-scraper"
)

func makeGet(c *http.Client, url string) {
	t := time.Now()

	log.Printf("Requesting %s", url)
	resp, err := c.Get(url)
	if err != nil {
		log.Fatal(err)
	}

	body, _ := ioutil.ReadAll(resp.Body)
	log.Printf("Fetched %s in %s, %d bytes (status %d)",
		url, time.Now().Sub(t), len(body), resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		log.Fatal("Invalid response code")
	}
}

func main() {
	t, err := scraper.NewTransport(http.DefaultTransport)
	if err != nil {
		log.Fatal(err)
	}

	client := &http.Client{Transport: t}

	makeGet(client, "https://liqui.io")
	makeGet(client, "https://liqui.io/About")
}
