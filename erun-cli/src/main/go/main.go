package main

import (
	"log"

	"github.com/sophium/erun/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
