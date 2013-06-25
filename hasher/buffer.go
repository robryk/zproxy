package hasher

import (
	"sync"
)

type Buffer struct {
	buf  []Chunk
	eof  chan struct{}
	mu   sync.RWMutex
	cond sync.Cond
}

func NewBuffer(chunked <-chan Chunk) *Buffer {
	b := &Buffer{eof: make(chan struct{})}
	b.cond.L = &b.mu
	go func() {
		for chunk := range chunked {
			b.mu.Lock()
			b.buf = append(b.buf, chunk)
			b.mu.Unlock()
			b.cond.Broadcast()
		}
		close(b.eof)
		b.mu.Lock()
		b.mu.Unlock()
		b.cond.Broadcast()
	}()
	return b
}

func (b *Buffer) isEof() bool {
	select {
	case <-b.eof:
		return true
	default:
		return false
	}
}

func (b *Buffer) Get(idx int) (chunk Chunk, eof bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for len(b.buf) <= idx && !b.isEof() {
		b.cond.Wait()
	}

	if b.isEof() {
		return Chunk{}, true
	} else {
		return b.buf[idx], false
	}
}

func (b *Buffer) Eof() <-chan struct{} {
	return b.eof
}

func (b *Buffer) Len() (length int, finished bool) {
	finished = b.isEof()
	b.mu.Lock()
	length = len(b.buf)
	b.mu.Unlock()
	return
}

func (b *Buffer) NewReader(cancel <-chan bool) <-chan Chunk {
	reader := make(chan Chunk, 20)
	go func() {
		for idx := 0; true; idx++ {
			chunk, eof := b.Get(idx)
			if eof {
				break
			}
			select {
			case reader <- chunk:
			case <-cancel:
				break
			}
		}
		close(reader)
	}()
	return reader
}
