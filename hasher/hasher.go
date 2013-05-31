package hasher

import (
	"camlistore.org/pkg/singleflight"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/split"
	"io"
	"log"
	"net/http"
	"net/url"
)

// TODO: find better names for stuff
// TODO: cache responses
// TODO: figure out how to pass enough of an http.Request

type ChunkedRetriever interface {
	GetChunked(url string) (*Chunked, error)
}

type Chunked struct {
	Chunks []split.Chunk
}

func hashUrl(url string) (*Chunked, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	ch := make(chan split.Chunk, 10)
	var splitErr error
	go func() {
		splitErr = split.Split(resp.Body, ch)
		close(ch)
	}()
	ret := &Chunked{}
	for chunk := range ch {
		chunk.Contents = nil
		ret.Chunks = append(ret.Chunks, chunk)
	}
	if splitErr != nil {
		return nil, splitErr
	}
	return ret, nil
}

type Proxy struct {
	group singleflight.Group
}

func (p *Proxy) GetChunked(url string) (*Chunked, error) {
	log.Printf("Getting chunked version of [%s]", url)
	v, err := p.group.Do(url, func() (interface{}, error) {
		return hashUrl(url)
	})
	return v.(*Chunked), err
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	url := req.FormValue("url")
	if url == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	chunked, err := p.GetChunked(url)
	if err != nil {
		rw.WriteHeader(http.StatusNotFound)
		io.WriteString(rw, err.Error())
		return
	}

	rw.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(rw)
	if err := enc.Encode(chunked); err != nil {
		log.Printf("Error encoding response: %v", err)
		return
	}
}

type RemoteProxy struct {
	Url string
}

func (r RemoteProxy) GetChunked(urlString string) (*Chunked, error) {
	resp, err := http.Get(r.Url + "?url=" + url.QueryEscape(urlString))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed: %v", resp.Status)
	}

	dec := json.NewDecoder(resp.Body)
	var chunked Chunked
	if err := dec.Decode(&chunked); err != nil {
		return nil, err
	}

	return &chunked, nil
}
