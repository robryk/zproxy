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

func sanitizeRequest(req *http.Request) *http.Request {
	r := &http.Request{}
	// FIXME: What if old proto?

	r.Method = req.Method
	r.URL = req.URL // FIXME: Should this be deeper?
	// Proto* are ignored on outgoing requests
	r.Header = make(http.Header)
	for k, vv := range req.Header {
		if k == "Connection" {
			continue
		}
		for _, v := range vv {
			r.Header.Add(k, v)
		}
		// FIXME: Remove other headers
	}
	r.Body = req.Body // We might wish to warn about this
	r.ContentLength = req.ContentLength
	if r.ContentLength == -1 {
		r.ContentLength = 0 // necessary?
	}
	// FIXME: Deal better with Transfer-Encoding
	r.TransferEncoding = nil
	r.Close = false
	r.Host = req.Host // necessary?
	// *Form is ignored on outgoing requests
	// We set nil Trailer, because there is no way for us to set it early enough (or so I think)
	// RemoteAddr, RequestURI and TLS make no sense on an outgoing request
	return r
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

func isDirect(req *http.Request) bool {
	if req.Method != "GET" {
		return true
	}

	headReq := sanitizeRequest(req)
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
	// FIXME: Cache-Control
	return false
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	url := req.URL.String()
	log.Printf("Serving %s", url)

	if isDirect(req) {
		log.Printf("Serving directly")
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
