package proxy

import (
	"bytes"
	"fmt"
	"github.com/robryk/zproxy/cache"
	"github.com/robryk/zproxy/hasher"
	"io"
	"io/ioutil"
	"log"
	"net/http"
)

const SizeCutoff = 10 // for testing

type Proxy struct {
	Cr    hasher.ChunkedRetriever
	Cache cache.Cache
}

func getSize(url string) (int64, error) {
	resp, err := http.Head(url)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.ContentLength, nil
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

func directProxy(rw http.ResponseWriter, url string) {
	resp, err := http.Get(url)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	copyHeaders(rw.Header(), resp.Header)
	rw.WriteHeader(resp.StatusCode)
	io.Copy(rw, resp.Body)
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	url := req.URL.String()
	log.Printf("Serving %s", url)

	if size, err := getSize(url); size < SizeCutoff && size != -1 && err == nil {
		log.Printf("Size %d, serving directly", size)
		directProxy(rw, url)
		return
	}
	chunked, err := p.Cr.GetChunked(url)
	if err != nil {
		directProxy(rw, url)
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
