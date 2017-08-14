package scraper

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"math/rand"

	"strings"

	"github.com/robertkrimen/otto"
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 6.1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/41.0.2228.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_10_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/50.0.2661.102 Safari/537.36",
	"Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/52.0.2743.116 Safari/537.36",
	"Mozilla/5.0 (Windows NT 6.1; WOW64; rv:46.0) Gecko/20100101 Firefox/46.0",
	"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:41.0) Gecko/20100101 Firefox/41.0",
}
var userAgent string

type Transport struct {
	upstream http.RoundTripper
	cookies  http.CookieJar
}

func NewTransport(upstream http.RoundTripper) (*Transport, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Transport{upstream, jar}, nil
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", userAgent)
	}

	// Merge cookies
	cookies := t.cookies.Cookies(r.URL)
	cookiesStrings := make([]string, 0)
	for _, cookie := range cookies {
		cookiesStrings = append(cookiesStrings, cookie.String())
	}

	// Prefix the seperator
	curCookies := r.Header.Get("Cookie")
	if len(curCookies) > 0 {
		curCookies = "; " + curCookies
	}

	cookieString := strings.Join(cookiesStrings, "; ") + curCookies
	r.Header.Set("Cookie", cookieString)

	resp, err := t.upstream.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	// Check if Cloudflare anti-bot is on
	if resp.StatusCode == 503 && resp.Header.Get("Server") == "cloudflare-nginx" {
		resp, err := t.solveChallenge(resp)
		return resp, err
	}

	return resp, err
}

var jschlRegexp = regexp.MustCompile(`name="jschl_vc" value="(\w+)"`)
var passRegexp = regexp.MustCompile(`name="pass" value="(.+?)"`)

func (t Transport) solveChallenge(resp *http.Response) (*http.Response, error) {
	b, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = ioutil.NopCloser(bytes.NewReader(b))
	body := string(b)

	// Confirming that we have hit anti-bot
	if strings.Contains(body, "jschl_vc") && strings.Contains(body, "jschl_answer") {
		log.Printf("Solving challenge for %s", resp.Request.URL.Hostname())
		time.Sleep(time.Second * 4) // Cloudflare requires a delay before solving the challenge
		var params = make(url.Values)

		if m := jschlRegexp.FindStringSubmatch(body); len(m) > 0 {
			params.Set("jschl_vc", m[1])
		}

		if m := passRegexp.FindStringSubmatch(body); len(m) > 0 {
			params.Set("pass", m[1])
		}

		chkURL, _ := url.Parse("/cdn-cgi/l/chk_jschl")
		u := resp.Request.URL.ResolveReference(chkURL)

		js, err := t.extractJS(body)
		if err != nil {
			return nil, err
		}

		answer, err := t.evaluateJS(js)
		if err != nil {
			return nil, err
		}

		params.Set("jschl_answer", strconv.Itoa(int(answer)+len(resp.Request.URL.Host)))

		req, err := http.NewRequest("GET", fmt.Sprintf("%s?%s", u.String(), params.Encode()), nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("User-Agent", resp.Request.Header.Get("User-Agent"))
		req.Header.Set("Referer", resp.Request.URL.String())

		log.Printf("Requesting %s?%s", u.String(), params.Encode())
		client := http.Client{
			Transport: t.upstream,
			Jar:       t.cookies,
		}

		resp, err = client.Do(req)
		if err != nil {
			return nil, err
		}

		return resp, nil
	}

	return resp, err
}

func (t Transport) evaluateJS(js string) (int64, error) {
	vm := otto.New()
	result, err := vm.Run(js)
	if err != nil {
		return 0, err
	}
	return result.ToInteger()
}

var jsRegexp = regexp.MustCompile(
	`setTimeout\(function\(\){\s+(var ` +
		`s,t,o,p,b,r,e,a,k,i,n,g,f.+?\r?\n[\s\S]+?a\.value =.+?)\r?\n`,
)
var jsReplace1Regexp = regexp.MustCompile(`a\.value = (parseInt\(.+?\)).+`)
var jsReplace2Regexp = regexp.MustCompile(`\s{3,}[a-z](?: = |\.).+`)
var jsReplace3Regexp = regexp.MustCompile(`[\n\\']`)

func (t Transport) extractJS(body string) (string, error) {
	matches := jsRegexp.FindStringSubmatch(body)
	if len(matches) == 0 {
		return "", errors.New("No matching javascript found")
	}

	js := matches[1]
	js = jsReplace1Regexp.ReplaceAllString(js, "$1")
	js = jsReplace2Regexp.ReplaceAllString(js, "")

	// Strip characters that could be used to exit the string context
	// These characters are not currently used in Cloudflare's arithmetic snippet
	js = jsReplace3Regexp.ReplaceAllString(js, "")

	return js, nil
}

func init() {
	// Choose a random User-Agent
	s := rand.NewSource(time.Now().Unix())
	r := rand.New(s) // initialize local pseudorandom generator
	uaIdx := r.Intn(len(userAgents))
	userAgent = userAgents[uaIdx]
}
