package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"

	cni_shim "github.com/isovalent/ztunnel-shim/cni-shim"
	"gopkg.in/yaml.v2"
)

var shim *cni_shim.Shim

func main() {

	if len(os.Args) == 1 {
		log.Printf("Usage: [config]")
		os.Exit(1)
	}

	configPath := os.Args[1]

	f, err := os.Open(configPath)
	if err != nil {
		log.Printf("Failed to open config file: %v\n", err)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		log.Printf("Failed to get file size: %v\n", err)
		return
	}

	buf := make([]byte, stat.Size())
	_, err = f.Read(buf)
	if err != nil {
		log.Printf("Failed to read config file: %v\n", err)
		return
	}

	var c cni_shim.Config
	if err = yaml.Unmarshal(buf, &c); err != nil {
		log.Printf("Failed to unmarshal config file: %v\n", err)
		return
	}

	log.Printf("%v", c)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	wg := &sync.WaitGroup{}

	log.Printf("Starting CNI shim...")

	shim, err = cni_shim.NewShim(ctx, wg, c.Workloads)
	if err != nil {
		log.Printf("Failed to create CNI shim: %v\n", err)
		return
	}

	wg.Wait()
}
