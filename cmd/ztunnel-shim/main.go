package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"

	cni_shim "github.com/isovalent/ztunnel-shim/cni-shim"
)

var shim *cni_shim.Shim

func main() {

	if len(os.Args) == 1 {
		log.Printf("Usage: [netns_path]")
		os.Exit(1)
	}

	netnsPaths := []string{}
	for i := 1; i < len(os.Args); i++ {
		netnsPaths = append(netnsPaths, os.Args[i])
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	wg := &sync.WaitGroup{}

	log.Printf("Starting CNI shim...")

	var err error

	shim, err = cni_shim.NewShim(ctx, wg, netnsPaths)
	if err != nil {
		log.Printf("Failed to create CNI shim: %v\n", err)
		return
	}

	wg.Wait()
}
