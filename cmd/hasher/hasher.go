package main

import (
	"github.com/robryk/zproxy/split"
	"encoding/json"
	"flag"
	"os"
)

func main() {
	flag.Parse()
	// TODO: files and urls

	ch := make(chan split.Chunk, 10)
	enc := json.NewEncoder(os.Stdout)
	var err error
	go func() {
		err = split.Split(os.Stdin, ch)
		close(ch)
	}()
	for chunk := range ch {
		chunk.Contents = nil
		if e := enc.Encode(chunk); e != nil {
			panic(e)
		}
	}
	if err != nil {
		panic(err)
	}
}

