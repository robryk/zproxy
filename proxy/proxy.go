package proxy

import (
	"io"
	"log"
	"net/http"
)

type Proxy struct {
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	url := req.URL.String()
	log.Printf("Serving %s", url)

	resp, err := http.Get(url)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		log.Printf("Error fetching %s: %v", url, err)
		return
	}
	defer resp.Body.Close()

	header := rw.Header()
	for k, v := range resp.Header {
		header[k] = append([]string(nil), v...) // is this copying necessary?
	}

	rw.WriteHeader(resp.StatusCode)
	io.Copy(rw, resp.Body)
}
