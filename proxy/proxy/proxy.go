package main

import (
	"flag"
	"github.com/robryk/zproxy/proxy"
	"log"
	"net/http"
)

var laddr = flag.String("addr", ":8000", "Address to listen on")

func main() {
	flag.Parse()
	p := &proxy.Proxy{}
	log.Fatal(http.ListenAndServe(*laddr, p))
}
