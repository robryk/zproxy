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
	GetChunked(req *proxy.Request, cancel <-chan bool) (*Chunked, error)
}

type Chunked struct {
	Err    *error
	Header Header
	Chunks <-chan Chunk
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

func (sr SimpleRetriever) GetChunked(req *proxy.Request, cancel <-chan bool) (*Chunked, error) {
	chunkCh := make(chan Chunk, 20)

	var finalErr error
	chunked := &Chunked{
		Err:    &finalErr,
		Chunks: chunkCh,
	}

	resp, err := http.DefaultClient.Do(proxy.UnmarshalRequest(req))
	if err != nil {
		return nil, err
	}

	chunked.Header = Header{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		ContentLength: resp.ContentLength,
		Header:        resp.Header,
	}

	go func() {
		defer resp.Body.Close()
		defer close(chunkCh)
		offset := 0
		finalErr = split.SplitFun(resp.Body, func(buf []byte) error {
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
			case <-cancel:
				return ErrCancel
			default:
			}
			select {
			case chunkCh <- chunk:
			case <-cancel:
				return ErrCancel
			}
			offset += len(buf)
			return nil
		})
	}()
	return chunked, nil
}
