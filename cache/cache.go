package cache

import (
	"encoding/hex"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

var ErrNotExist = errors.New("cache: blob absent")

// TODO: Reader or ReaderCloser?

type Cache interface {
	Read(hash string) (io.Reader, error)
	Write(hash string, r io.Reader) error
}

type NoCache struct{}

func (nc *NoCache) Read(hash string) (io.Reader, error) {
	return nil, ErrNotExist
}

func (nc *NoCache) Write(hash string, r io.Reader) error {
	return nil
}

type DiskCache struct {
	Dir string
}

// TODO: verify hashes

func (dc *DiskCache) path(hash string) string {
	hashHex := hex.EncodeToString([]byte(hash))
	if len(hashHex) < 6 {
		hash = hashHex + "xxxxxx"
	}
	return filepath.Join(dc.Dir, hashHex[0:3], hashHex[3:6], hashHex)
}

func (dc *DiskCache) Read(hash string) (io.Reader, error) {
	return os.Open(dc.path(hash))
}

func (dc *DiskCache) Write(hash string, r io.Reader) error {
	path := dc.path(hash)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tempFile, err := ioutil.TempFile(filepath.Dir(path), ".tmp."+filepath.Base(path))
	tempName := tempFile.Name()
	if err != nil {
		return err
	}
	defer func() {
		os.Remove(tempName)
		tempFile.Close()
	}()
	if _, err := io.Copy(tempFile, r); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	return nil
}
