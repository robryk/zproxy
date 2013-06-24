package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/robryk/zproxy/cache"
	"github.com/robryk/zproxy/hasher"
	"github.com/robryk/zproxy/proxy"
	"io"
	"io/ioutil"
	"log"
	"net/http"
)

const SizeCutoff = 10

var laddr = flag.String("addr", ":8000", "Address to listen on")
var hasherUrl = flag.String("hasher", "http://127.0.0.1:9000", "Hasher's address")
var cacheDir = flag.String("cache_dir", "", "Cache directory")

type Proxy struct {
	Hasher hasher.Hasher
	Cache  cache.Cache
}

type byteRange struct {
	begin int64
	end   int64
}

func getContents(req *proxy.Request, r byteRange) (*http.Response, error) {
	httpReq := proxy.UnmarshalRequest(req)
	if r.begin != 0 || r.end != 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.begin, r.end))
	}
	resp, err := http.DefaultClient.Do(httpReq)
	return resp, err
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
}

func canHash(req *http.Request) (etag string, ok bool) {
	if req.Method != "GET" {
		return "", false
	}

	headReq := proxy.SanitizeRequest(req)
	headReq.Method = "HEAD"
	headReq.Body = nil
	headReq.ContentLength = 0
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		log.Printf("Head request to %s failed: %v", req.URL, err)
		return "", false
	}
	headResp.Body.Close()
	if headResp.ContentLength < SizeCutoff || headResp.ContentLength == -1 {
		// If the server can't return a Content-Length, chances are high the page changes often
		// even if the server claims otherwise.
		return "", false
	}
	etag = headResp.Header.Get("ETag")
	if etag == "" || etag[0] == 'W' {
		// We require strong ETags to be sure that the hasher received the same entity.
		return "", false
	}
	// FIXME: Cache-Control
	return etag, true
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// FIXME: Deal with recursion
	url := req.URL.String()
	log.Printf("Serving %s", url)

	if _, ok := canHash(req); !ok {
		log.Printf("Serving directly")
		directProxy(rw, req)
		return
	}
	proxyReq, err := proxy.MarshalRequest(req)
	if err != nil {
		directProxy(rw, req)
		return
	}

	cancel := make(chan bool)
	defer close(cancel)
	chunked := p.Hasher.GetChunked(proxyReq, cancel)
	//if err != nil {
	//	directProxy(rw, req)
	//	return
	//}

	// TODO: deal with status and headers
	// TODO: emit content-length!
	// TODO: check hasher's header against ours to verify if we can use it

	log.Printf("Split into %d chunks", len(chunked.Chunks))

	sema := make(chan struct{}, 2)
	sema <- struct{}{}
	sema <- struct{}{}
	output := make(chan chan io.Reader, 5)

	go func() {
		defer close(output)
		for chunk := range chunked.Chunks {
			r, err := p.Cache.Read(string(chunk.Digest))
			if err == nil {
				log.Printf("Chunk [%v] from cache", chunk)
				ch := make(chan io.Reader, 1)
				ch <- r
				output <- ch
			} else {
				log.Printf("Chunk [%v] from server", chunk)
				<-sema
				ch := make(chan io.Reader, 1)
				output <- ch
				go func(chunk hasher.Chunk, ch chan io.Reader) {
					resp, err := getContents(proxyReq, byteRange{int64(chunk.Offset), int64(chunk.Offset + chunk.Length)})
					if err != nil {
						log.Printf("Error retrieving missing chunk: %v", err)
						return
					}
					contents, err := ioutil.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						log.Printf("Error retrieving missing chunk: %v", err)
						return
					}
					go func(chunk hasher.Chunk, contents []byte) {
						log.Printf("%v", chunk)
						err := p.Cache.Write(string(chunk.Digest), bytes.NewReader(contents))
						if err != nil {
							log.Printf("Can't write chunk %v to cache: %v", chunk, err)
						} else {
							log.Printf("Chunk %v successfully cached", chunk)
						}
					}(chunk, contents)
					ch <- bytes.NewReader(contents)
					sema <- struct{}{}
				}(chunk, ch)
			}
		}
		fmt.Printf("%v\n", chunked.Err)
	}()

	for ch := range output {
		io.Copy(rw, <-ch)
	}

}

func main() {
	flag.Parse()
	var c cache.Cache
	c = &cache.NoCache{}
	if *cacheDir != "" {
		c = &cache.DiskCache{Dir: *cacheDir}
	}
	var cr hasher.Hasher
	cr = hasher.SimpleRetriever(0)
	if *hasherUrl != "" {
		cr = &hasher.Client{Url: *hasherUrl}
	}
	p := &Proxy{
		Hasher: cr,
		Cache:  c,
	}
	log.Fatal(http.ListenAndServe(*laddr, p))
}
