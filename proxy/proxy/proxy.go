package main

import (
	"flag"
	"github.com/robryk/zproxy/proxy"
	"github.com/robryk/zproxy/cache"
	"github.com/robryk/zproxy/hasher"
	"log"
	"net/http"
)

var laddr = flag.String("addr", ":8000", "Address to listen on")
var cacheDir = flag.String("cache_dir", "", "Cache directory")

func main() {
	flag.Parse()
	var c cache.Cache
	c = &cache.NoCache{}
	if *cacheDir != "" {
		c = &cache.DiskCache{Dir:*cacheDir}
	}
	p := &proxy.Proxy{
		Cr: &hasher.Proxy{},
		Cache: c,
	}
	log.Fatal(http.ListenAndServe(*laddr, p))
}
