package main

import (
	"log"

	hardenllm "github.com/prls-co/harden-llm"
)

func main() {
	if _, err := hardenllm.New(hardenllm.Options{}); err != nil {
		log.Fatal(err)
	}
}
