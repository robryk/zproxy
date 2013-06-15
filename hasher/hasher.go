package hasher

import (
	"bytes"
	"camlistore.org/pkg/singleflight"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/split"
	"io"
	"log"
	"net/http"
)

// TODO: find better names for stuff
// TODO: cache responses

type Hasher interface {
	GetChunked(req *proxy.Request) *Chunked
}

type Chunked struct {
	Err    error
	Header Header
	Chunks chan Chunk
	Cancel chan bool
}

type Header struct {
	StatusCode    int
	Status        string
	ContentLength int64
	Header        http.Header
}

type Chunk struct {
	Offset int
	Length int
	Digest []byte
}

// TODO: http client
type SimpleRetriever int

var defaultRetriever SimpleRetriever

// exp func

var ErrCancel = fmt.Errorf("hasher: Hashing cancelled")

func (sr SimpleRetriever) GetChunked(req *proxy.Request) *Chunked {
	chunked := &Chunked{
		Chunks: make(chan Chunk, 20),
		Cancel: make(chan bool),
	}

	resp, err := http.DefaultClient.Do(proxy.UnmarshalRequest(req))
	if err != nil {
		chunked.Err = err
		close(chunked.Chunks)
		return chunked
	}

	chunked.Header = Header{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		ContentLength: resp.ContentLength,
		Header:        resp.Header,
	}

	go func() {
		defer resp.Body.Close()
		defer close(chunked.Chunks)
		offset := 0
		chunked.Err = split.SplitFun(resp.Body, func(buf []byte) error {
			digest := sha256.New()
			if n, err := digest.Write(buf); err != nil || n < len(buf) {
				if err == nil {
					err = io.ErrShortWrite
				}
				return err
			}
			chunk := Chunk{
				Offset: offset,
				Length: len(buf),
				Digest: digest.Sum(nil),
			}
			select {
			case chunked.Chunks <- chunk:
			case <-chunked.Cancel:
				return ErrCancel
			}
			offset += len(buf)
			return nil
		})
	}()
	return chunked
}

type Server struct {
	Hasher Hasher
	group  singleflight.Group
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
