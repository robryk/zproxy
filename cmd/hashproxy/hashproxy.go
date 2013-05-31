package main

import (
	"net/http"
	"flag"
	"log"
	"github.com/robryk/zproxy/hasher"
)

var addr = flag.String("addr", ":8080", "Address to listen on")

func main() {
	flag.Parse()
	http.Handle("/", &hasher.Proxy{})
	log.Fatal(http.ListenAndServe(*addr, nil))
}
