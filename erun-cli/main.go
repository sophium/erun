package main

import (
	"log"

	"github.com/sophium/erun/cmd"
)

var runCLI = cmd.Execute

func main() {
	if err := runCLI(); err != nil {
		log.Fatal(err)
	}
}
