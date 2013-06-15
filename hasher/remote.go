package hasher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"log"
	"net/http"
)

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

	chunked := p.hasher().GetChunked(&proxyRequest)
	defer close(chunked.Cancel)

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
	if chunked.Err != nil {
		errString := chunked.Err.Error()
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

func (r Client) GetChunked(req *proxy.Request) *Chunked {
	chunked := &Chunked{
		Chunks: make(chan Chunk, 20),
		Cancel: make(chan bool),
	}

	returnCh := make(chan bool, 1)

	go func() {
		defer close(chunked.Chunks)
		defer close(returnCh)

		serializedReq, err := json.Marshal(req)
		if err != nil {
			chunked.Err = err
			return
		}

		resp, err := http.Post(r.Url, "application/json", bytes.NewReader(serializedReq))
		if err != nil {
			chunked.Err = err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			chunked.Err = fmt.Errorf("HTTP request failed: %v", resp.Status)
			return
		}

		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&chunked.Header); err != nil {
			chunked.Err = err
			return
		}

		returnCh <- true

		for {
			var response remoteResponse
			if err := dec.Decode(&response); err != nil {
				chunked.Err = err
				return
			}
			if response.Chunk == nil {
				// Last response
				if response.Err != nil {
					chunked.Err = fmt.Errorf("Remote error: %s", *response.Err)
				}
				return
			}
			if response.Err != nil {
				chunked.Err = fmt.Errorf("Unexpected response: %v", response)
				return
			}
			select {
			case chunked.Chunks <- *response.Chunk:
			case <-chunked.Cancel:
				chunked.Err = ErrCancel
				return
			}
		}
	}()

	<-returnCh

	return chunked
}
