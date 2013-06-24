package hasher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"log"
	"net/http"
)

type Server struct {
	Hasher   Hasher
}

type remoteResponse struct {
	Chunk *Chunk
	Err   *string
}

func (p *Server) hasher() Hasher {
	if p.Hasher != nil {
		return p.Hasher
	}
	return defaultRetriever
}

func (p *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
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

	cancel := make(chan bool)
	chunked := p.hasher().GetChunked(&proxyRequest, cancel)
	defer close(cancel)

	rw.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(rw)

	if err := enc.Encode(chunked.Header); err != nil {
		log.Printf("Error encoding header: %v", err)
		return
	}

	for chunk := range chunked.Chunks {
		if err := enc.Encode(remoteResponse{Chunk: &chunk}); err != nil {
			log.Printf("Error encoding chunk: %v", err)
			return
		}
	}

	var trailer remoteResponse
	if err := *chunked.Err; err != nil {
		errString := err.Error()
		trailer.Err = &errString
	}
	if err := enc.Encode(trailer); err != nil {
		log.Printf("Error encoding trailer: %v", err)
		return
	}
}

type Client struct {
	Url string
}

func (r Client) GetChunked(req *proxy.Request, cancel <-chan bool) *Chunked {
	var finalErr error
	chunked := &Chunked{
		Err: &finalErr,
	}

	returnCh := make(chan bool, 1)
	chunkCh := make(chan Chunk, 20)

	go func() {
		defer close(chunkCh)
		defer close(returnCh)

		serializedReq, err := json.Marshal(req)
		if err != nil {
			finalErr = err
			return
		}

		resp, err := http.Post(r.Url, "application/json", bytes.NewReader(serializedReq))
		if err != nil {
			finalErr = err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			finalErr = fmt.Errorf("HTTP request failed: %v", resp.Status)
			return
		}

		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&chunked.Header); err != nil {
			finalErr = err
			return
		}

		returnCh <- true

		for {
			var response remoteResponse
			if err := dec.Decode(&response); err != nil {
				finalErr = err
				return
			}
			if response.Chunk == nil {
				// Last response
				if response.Err != nil {
					finalErr = fmt.Errorf("Remote error: %s", *response.Err)
				}
				return
			}
			if response.Err != nil {
				finalErr = fmt.Errorf("Unexpected response: %v", response)
				return
			}
			select {
			case chunkCh <- *response.Chunk:
			case <-cancel:
				finalErr = ErrCancel
				return
			}
		}
	}()

	<-returnCh

	buffer := NewBuffer(chunkCh)
	chunked.Chunks = buffer.NewReader(cancel) // or maybe NewReader(nil)? we don't actually need it to cancel itself
	return chunked
}
