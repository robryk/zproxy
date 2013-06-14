package split

import (
	"bufio"
	"bytes"
	"camlistore.org/pkg/rollsum"
	"encoding/hex"
	"io"
)

// TODO: max chunk size

type Chunk struct {
	Offset   int
	Length   int
	Digest   []byte
	Contents []byte `json:"-"`
}

func (c Chunk) String() string {
	return hex.EncodeToString(c.Digest)
}

const minBits = 18
const minChunkSize = 1 << 15

func SplitFun(r io.Reader, outFn func([]byte) error) error {
	br := bufio.NewReader(r)
	rs := rollsum.New()
	chunk := bytes.Buffer{}
	for {
		chunk.Reset()
		var err error
		for !(rs.OnSplit() && rs.Bits() >= minBits) || chunk.Len() < minChunkSize {
			var c byte
			c, err = br.ReadByte()
			if err != nil {
				break
			}
			rs.Roll(c)
			if e := chunk.WriteByte(c); e != nil {
				return e
			}
		}
		if err != nil && err != io.EOF {
			return err
		}
		if err1 := outFn(chunk.Bytes()); err1 != nil {
			return err1
		}
		if err == io.EOF {
			return nil
		}
	}
}

func Split(r io.Reader, out chan<- []byte) error {
	return SplitFun(r, func(buf []byte) error {
		out <- buf
		return nil
	})
}
