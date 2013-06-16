package hasher

import (
	"crypto/sha256"
	"fmt"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/split"
	"io"
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
			case <-chunked.Cancel:
				return ErrCancel
			default:
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
