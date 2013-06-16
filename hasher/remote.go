package hasher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"log"
	"net/http"
	"sync"
)

type requestEntry struct {
	wg     sync.WaitGroup
	once   sync.Once
	buffer *Buffer
}

type Server struct {
	Hasher   Hasher
	inflight map[string]*requestEntry
	mu       sync.RWMutex
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

func (p *Server) getBuffer(req proxy.Request) *requestEntry {
	s := fmt.Sprint(req)
	p.mu.RLock()
	if r, ok := p.inflight[s]; ok {
		p.mu.RUnlock()
		r.wg.Add(1)
		return r
	}
	p.mu.Lock()
	if r, ok := p.inflight[s]; ok {
		p.mu.Unlock()
		r.wg.Add(1)
		return r
	}
	r := &requestEntry{}
	p.inflight[s] = r
	p.mu.Unlock()

	r.wg.Add(1)
	go func() {
		r.wg.Wait()
		p.mu.Lock()
		delete(p.inflight, s)
		p.mu.Unlock()
	}()
	return r
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

	reqEntry := p.getBuffer(proxyRequest)
	defer reqEntry.wg.Done()
	reqEntry.once.Do(func() {
		chunked := p.hasher().GetChunked(&proxyRequest)
		reqEntry.buffer = NewBuffer(chunked)
	})

	chunked := reqEntry.buffer.NewReader()
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

	buffer := NewBuffer(chunked)
	return buffer.NewReader()
}
