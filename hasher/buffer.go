package hasher

import (
	"sync"
)

type Buffer struct {
	chunked   *Chunked
	buf       []Chunk
	eof       bool
	readerCnt int
	mu        sync.RWMutex
	cond      sync.Cond
	// todo: handle cancelling somehow: zero clients also happen just after creation
}

func NewBuffer(chunked *Chunked) *Buffer {
	b := &Buffer{chunked: chunked}
	b.cond.L = &b.mu
	go func() {
		for chunk := range b.chunked.Chunks {
			b.mu.Lock()
			b.buf = append(b.buf, chunk)
			b.mu.Unlock()
			b.cond.Broadcast()
		}
		b.mu.Lock()
		b.eof = true
		b.mu.Unlock()
		b.cond.Broadcast()
	}()
	return b
}

func (b *Buffer) get(idx int) (chunk Chunk, eof bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for len(b.buf) <= idx && !b.eof {
		b.cond.Wait()
	}

	if b.eof {
		return Chunk{}, true
	} else {
		return b.buf[idx], false
	}
}

func (b *Buffer) NewReader() *Chunked {
	b.mu.Lock()
	b.readerCnt++
	b.mu.Unlock()
	reader := &Chunked{
		Header: b.chunked.Header,
		Chunks: make(chan Chunk, 10),
		Cancel: make(chan bool),
	}
	go func() {
		for idx := 0; true; idx++ {
			chunk, eof := b.get(idx)
			if eof {
				reader.Err = b.chunked.Err
				break
			}
			select {
			case reader.Chunks <- chunk:
			case <-reader.Cancel:
				reader.Err = ErrCancel
				break
			}
		}
		b.mu.Lock()
		b.readerCnt--
		if b.readerCnt == 0 && !b.eof {
			close(b.chunked.Cancel)
		}
		b.mu.Unlock()
		close(reader.Chunks)
	}()
	return reader
}
