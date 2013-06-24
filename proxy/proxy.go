package proxy

import (
	"fmt"
	"net/http"
	"net/url"
)

const SizeCutoff = 10 // for testing

type Request struct {
	Method string
	URL    *url.URL
	Host   string //???
	Header http.Header
}

func SanitizeRequest(req *http.Request) *http.Request {
	r := &http.Request{}
	// FIXME: What if old proto?

	r.Method = req.Method
	r.URL = req.URL // FIXME: Should this be deeper?
	// Proto* are ignored on outgoing requests
	r.Header = make(http.Header)
	// TODO: parse Connection header to remove additional hop-by-hop headers
	for k, vv := range req.Header {
		if k == "Connection" || k == "Keep-Alive" || k == "Proxy-Authorization" || k == "TE" || k == "Trailers" || k == "Transfer-Encoding" || k == "Upgrade" {
			continue
		}
		// Temporary: we can't handle range requests
		if k == "Range" {
			continue
		}
		for _, v := range vv {
			r.Header.Add(k, v)
		}
	}
	r.Body = req.Body // We might wish to warn about this
	r.ContentLength = req.ContentLength
	if r.ContentLength == -1 {
		r.ContentLength = 0 // necessary?
	}
	// FIXME: Deal better with Transfer-Encoding
	r.TransferEncoding = nil
	r.Close = false
	r.Host = req.Host // necessary?
	// *Form is ignored on outgoing requests
	// We set nil Trailer, because there is no way for us to set it early enough (or so I think)
	// RemoteAddr, RequestURI and TLS make no sense on an outgoing request
	return r
}

func MarshalRequest(req *http.Request) (*Request, error) {
	if req.ContentLength != 0 {
		return nil, fmt.Errorf("Cannot marshall a request with nonzero length body")
	}
	sanitizedReq := SanitizeRequest(req)
	r := &Request{
		Method: sanitizedReq.Method,
		URL:    sanitizedReq.URL,
		Header: sanitizedReq.Header,
		Host:   sanitizedReq.Host,
	}
	return r, nil
}

func UnmarshalRequest(req *Request) *http.Request {
	return &http.Request{
		Method: req.Method,
		URL:    req.URL,
		Header: req.Header,
		Host:   req.Host,
	}
}
