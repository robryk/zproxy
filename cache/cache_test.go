package cache

import (
	"crypto/sha256"
	"io/ioutil"
	"strings"
	"testing"
	"os"
)

func newDiskCache() *DiskCache {
	dir, err := ioutil.TempDir("", "disk-cache")
	if err != nil {
		panic("TempDir: " + err.Error())
	}
	return &DiskCache{dir}
}

func destroyDiskCache(dc *DiskCache) {
	os.RemoveAll(dc.Dir)
}

func hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return string(h.Sum(nil))
}

func TestDiskCache(t *testing.T) {
	dc := newDiskCache()
	defer destroyDiskCache(dc)

	const text = `Mary had a little lamb.`

	_, err := dc.Read(hash(text))
	if err == nil {
		t.Fatal("DiskCache.Read succeeded on an empty cache")
	}

	err = dc.Write(hash(text), strings.NewReader(text))
	if err != nil {
		t.Fatalf("DiskCache.Write failed: %v", err)
	}

	r, err := dc.Read(hash(text))
	if err != nil {
		t.Fatalf("DiskCache.Read failed on an existing element: %v", err)
	}
	s, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatalf("Cache reader failed: %v", err)
	}
	if string(s) != text {
		t.Fatalf("DiskCache.Read read [%v] instead of [%v]", s, text)
	}
}
