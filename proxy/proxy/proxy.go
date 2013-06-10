package main

import (
	"bytes"
	"fmt"
	"flag"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/cache"
	"github.com/robryk/zproxy/hasher"
	"io"
	"io/ioutil"
	"log"
	"net/http"
)

const SizeCutoff = 10

var laddr = flag.String("addr", ":8000", "Address to listen on")
var cacheDir = flag.String("cache_dir", "", "Cache directory")

type Proxy struct {
	Cr    hasher.ChunkedRetriever
	Cache cache.Cache
}

type byteRange struct {
	begin int64
	end   int64
}

func getContents(url string, r byteRange) ([]byte, http.Header, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	if r.begin != 0 || r.end != 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.begin, r.end))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	contents, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, 0, err
	}

	return contents, resp.Header, resp.StatusCode, nil
}

func copyHeaders(dst http.Header, src http.Header) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}

func directProxy(rw http.ResponseWriter, req *http.Request) {
	r := proxy.SanitizeRequest(req)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		http.Error(rw, fmt.Sprintf("Proxy error: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// FIXME: Are there any headers we should omit?
	copyHeaders(rw.Header(), resp.Header)
	rw.WriteHeader(resp.StatusCode)
	io.Copy(rw, resp.Body)
	// FIXME: Maybe deal with Trailer? We should send the Trailer if our request was http/1.1
}

func isDirect(req *http.Request) bool {
	if req.Method != "GET" {
		return true
	}

	headReq := proxy.SanitizeRequest(req)
	headReq.Method = "HEAD"
	headReq.Body = nil
	headReq.ContentLength = 0
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		log.Printf("Head request to %s failed: %v", req.URL, err)
		return true
	}
	headResp.Body.Close()
	if headResp.ContentLength < SizeCutoff || headResp.ContentLength == -1 {
		// If the server can't return a Content-Length, chances are high the page changes often
		// even if the server claims otherwise.
		return true
	}
	// FIXME: StatusMethodNotAvailable?
	// FIXME: Cache-Control
	return false
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// FIXME: Deal with recursion
	url := req.URL.String()
	log.Printf("Serving %s", url)

	if isDirect(req) {
		log.Printf("Serving directly")
		directProxy(rw, req)
		return
	}
	proxyReq, err := proxy.MarshalRequest(req)
	if err != nil {
		directProxy(rw, req)
		return
	}

	chunked, err := p.Cr.GetChunked(proxyReq)
	if err != nil {
		directProxy(rw, req)
		return
	}

	// TODO: deal with status and headers
	// TODO: emit content-length!

	for _, chunk := range chunked.Chunks {
		r, err := p.Cache.Read(string(chunk.Digest))
		if err == nil {
			io.Copy(rw, r)
		} else {
			// TODO: handle cache write failures
			contents, _, _, err := getContents(string(url), byteRange{int64(chunk.Offset), int64(chunk.Offset + chunk.Length)})
			if err != nil {
				log.Printf("Error retrieving missing chunk: %v", err)
				return
			}
			// errors fixme
			go p.Cache.Write(string(chunk.Digest), bytes.NewBuffer(contents))
			rw.Write(contents)
		}
	}
}

func main() {
	flag.Parse()
	var c cache.Cache
	c = &cache.NoCache{}
	if *cacheDir != "" {
		c = &cache.DiskCache{Dir:*cacheDir}
	}
	p := &Proxy{
		Cr: &hasher.Proxy{},
		Cache: c,
	}
	log.Fatal(http.ListenAndServe(*laddr, p))
}
