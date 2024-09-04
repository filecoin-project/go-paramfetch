package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	build "github.com/filecoin-project/go-paramfetch"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func cancelOnSignal(ctx context.Context, sig ...os.Signal) context.Context {
	ctx2, cancel := context.WithCancel(ctx)

	sigChan := make(chan os.Signal, 3)
	go func() {
		<-sigChan
		cancel()
	}()
	signal.Notify(sigChan, sig...)

	return ctx2
}

func main() {
	sectorSize := os.Args[1]
	paramsJsonPath := os.Args[2]
	srsJsonPath := os.Args[3]

	n, err := strconv.ParseUint(sectorSize, 10, 64)
	check(err)

	dat, err := os.ReadFile(paramsJsonPath)
	check(err)

	datSrs, err := os.ReadFile(srsJsonPath)
	check(err)

	ctx := cancelOnSignal(context.TODO(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	err = build.GetParams(ctx, dat, datSrs, n)
	check(err)
}
