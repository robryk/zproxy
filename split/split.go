package split

import (
	"bufio"
	"bytes"
	"camlistore.org/pkg/rollsum"
	"crypto/sha256"
	"io"
)

// TODO: max chunk size

type Chunk struct {
	Offset   int
	Length   int
	Digest   []byte
	Contents []byte `json:"-"`
}

const minBits = 18
const minChunkSize = 1 << 15

func Split(r io.Reader, out chan<- Chunk) error {
	br := bufio.NewReader(r)
	rs := rollsum.New()
	offset := 0
	for {
		var err error
		chunk := bytes.Buffer{}
		digest := sha256.New()
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
			if _, e := digest.Write([]byte{c}); e != nil {
				return e
			}
		}
		if err != nil && err != io.EOF {
			return err
		}
		out <- Chunk{
			Offset:   offset,
			Length:   chunk.Len(),
			Digest:   digest.Sum(nil),
			Contents: chunk.Bytes(),
		}
		offset += chunk.Len()
		if err == io.EOF {
			return nil
		}
	}
}
