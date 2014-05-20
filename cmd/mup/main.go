package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/niemeyer/mup.v0"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	mup.SetLogger(logger)
	mup.SetDebug(true)
	config := &mup.BridgeConfig{
		Database: "localhost/mup",
	}
	if _, err := mup.StartBridge(config); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	select{}
}
