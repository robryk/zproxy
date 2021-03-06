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

func getContents(req *proxy.Request, r byteRange) ([]byte, error) {
	httpReq := proxy.UnmarshalRequest(req)
	httpReq.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.begin, r.end-1))
	resp, err := http.DefaultTransport.RoundTrip(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Range") == "" {
		// TODO: Do something more intelligent (check earlier if supported and check Content-Range's value)
		return nil, fmt.Errorf("No Content-Range in response")
	}
	contents, err := ioutil.ReadAll(resp.Body)
	if len(contents) != int(r.end - r.begin) {
		return nil, fmt.Errorf("Wrong length of range response %d %d %d", len(contents), r.end, r.begin)
	}
	return contents, err
}

func directProxy(rw http.ResponseWriter, req *http.Request) {
	r := proxy.SanitizeRequest(req)
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(rw, fmt.Sprintf("Proxy error: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	proxy.SanitizeResponseHeaders(rw, resp)
	io.Copy(rw, resp.Body)
}

func canHash(req *http.Request) (headResp *http.Response, etag string, hashingOk bool) {
	if req.Method != "GET" {
		return
	}
	if vias, ok := req.Header["Via"]; ok {
		for _, v := range vias {
			if v == proxy.MyVia {
				// TODO: We should error out here probably
				return
			}
		}
	}

	headReq := proxy.SanitizeRequest(req)
	headReq.Method = "HEAD"
	headReq.Body = nil
	headReq.ContentLength = 0
	headResp, err := http.DefaultTransport.RoundTrip(headReq)
	if err != nil {
		log.Printf("Head request to %s failed: %v", req.URL, err)
		return
	}
	headResp.Body.Close()
	etag = headResp.Header.Get("ETag")
	// TODO: Maybe check that it is well-formed?
	if etag == "" || etag[0] == 'W' {
		// We require strong ETags to be sure that the hasher received the same entity.
		return
	}
	// FIXME: Cache-Control
	return headResp, etag, true
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// FIXME: Deal with recursion
	url := req.URL.String()
	log.Printf("Serving %s", url)

	headResp, etag, canProxy := canHash(req)
	if !canProxy || headResp.ContentLength < SizeCutoff || headResp.ContentLength == -1 {
		// If we didn't get a Content-Length, just serve it directly.
		log.Printf("Serving directly")
		directProxy(rw, req)
		return
	}
	proxyReq, err := proxy.MarshalRequest(req)
	if err != nil {
		log.Printf("Error marshalling request: %s. Serving directly", err.Error())
		directProxy(rw, req)
		return
	}

	cancel := make(chan bool)
	defer close(cancel)
	chunked, err := p.Hasher.GetChunked(&hasher.Request{proxyReq, etag}, cancel)
	if err != nil {
		log.Printf("Error from hasher: %s. Serving directly", err.Error())
		directProxy(rw, req)
		return
	}

	// TODO: deal with status and headers
	// TODO: emit content-length!
	// TODO: check hasher's header against ours to verify if we can use it

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
					contents, err := getContents(proxyReq, byteRange{int64(chunk.Offset), int64(chunk.Offset + chunk.Length)})
					if err != nil {
						log.Printf("Error retrieving missing chunk: %v", err)
						ch <- bytes.NewReader(nil)
						sema <- struct{}{}
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
		fmt.Printf("%v\n", *chunked.Err)
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
