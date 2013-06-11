package main

import (
	"flag"
	"github.com/robryk/zproxy/hasher"
	"net/http"
	"log"
)

var laddr = flag.String("addr", ":9000", "Address to listen on")

func main() {
	flag.Parse()
	log.Fatal(http.ListenAndServe(*laddr, &hasher.Proxy{}))
}
