package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"

	"gopkg.in/niemeyer/mup.v0"
	//"labix.org/v2/mgo"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	mup.SetLogger(logger)
	mup.SetDebug(true)
	//mgo.SetLogger(logger)
	//mgo.SetDebug(true)
	config := &mup.BridgeConfig{
		Database: "localhost/mup",
	}
	bridge, err := mup.StartBridge(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
	<-ch
	bridge.Stop()
}
