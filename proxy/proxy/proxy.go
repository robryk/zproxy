package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/robryk/zproxy/cache"
	"github.com/robryk/zproxy/hasher"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/split"
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

	log.Printf("Split into %d chunks", len(chunked.Chunks))

	sema := make(chan struct{}, 2)
	sema <- struct{}{}
	sema <- struct{}{}
	output := make(chan chan io.Reader, 5)

	go func() {
		defer close(output)
		for i, chunk := range chunked.Chunks {
			r, err := p.Cache.Read(string(chunk.Digest))
			if err == nil {
				log.Printf("Chunk %d[%v] from cache", i, chunk)
				ch := make(chan io.Reader, 1)
				ch <- r
				output <- ch
			} else {
				log.Printf("Chunk %d[%v] from server", i, chunk)
				<-sema
				ch := make(chan io.Reader, 1)
				output <- ch
				go func(chunk split.Chunk, ch chan io.Reader) {
					contents, _, _, err := getContents(string(url), byteRange{int64(chunk.Offset), int64(chunk.Offset + chunk.Length)})
					if err != nil {
						log.Printf("Error retrieving missing chunk: %v", err)
						return
					}
					go func(chunk split.Chunk, contents []byte) {
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
	var cr hasher.ChunkedRetriever
	cr = &hasher.Proxy{}
	if *hasherUrl != "" {
		cr = &hasher.RemoteProxy{Url: *hasherUrl}
	}
	p := &Proxy{
		Cr:    cr,
		Cache: c,
	}
	log.Fatal(http.ListenAndServe(*laddr, p))
}
