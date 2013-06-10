package hasher

import (
	"bytes"
	"camlistore.org/pkg/singleflight"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/split"
	"log"
	"net/http"
)

// TODO: find better names for stuff
// TODO: cache responses
// TODO: figure out how to pass enough of an http.Request

type ChunkedRetriever interface {
	GetChunked(req *proxy.Request) (*Chunked, error)
}

type Chunked struct {
	Chunks []split.Chunk
}

func hashRequest(req *proxy.Request) (*Chunked, error) {
	resp, err := http.DefaultClient.Do(proxy.UnmarshalRequest(req))
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

func (p *Proxy) GetChunked(req *proxy.Request) (*Chunked, error) {
	log.Printf("Getting chunked version of [%s]", req.URL)
	//	v, err := p.group.Do(url, func() (interface{}, error) {
	return hashRequest(req)
	//	})
	//	return v.(*Chunked), err
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(rw, "Use HTTP POST", http.StatusMethodNotAllowed)
		return
	}

	var proxyRequest proxy.Request
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(&proxyRequest); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	chunked, err := p.GetChunked(&proxyRequest)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
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

func (r RemoteProxy) GetChunked(req *proxy.Request) (*Chunked, error) {
	serializedReq, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(r.Url, "application/json", bytes.NewReader(serializedReq))
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
