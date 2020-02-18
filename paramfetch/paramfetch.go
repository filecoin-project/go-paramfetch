package main

import (
	"io/ioutil"
	"os"
	"strconv"

	build "github.com/filecoin-project/go-paramfetch"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func main() {
	sectorSize := os.Args[1]
	paramsJsonPath := os.Args[2]

	n, err := strconv.ParseUint(sectorSize, 10, 64)
	check(err)

	dat, err := ioutil.ReadFile(paramsJsonPath)
	check(err)

	err = build.GetParams(dat, n)
	check(err)
}
