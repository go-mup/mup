package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"

	"gopkg.in/niemeyer/mup.v0"
	_ "gopkg.in/niemeyer/mup.v0/plugins/aql"
	_ "gopkg.in/niemeyer/mup.v0/plugins/echo"
	_ "gopkg.in/niemeyer/mup.v0/plugins/launchpad"
	_ "gopkg.in/niemeyer/mup.v0/plugins/ldap"
	_ "gopkg.in/niemeyer/mup.v0/plugins/publishbot"
	_ "gopkg.in/niemeyer/mup.v0/plugins/sendraw"

	"gopkg.in/mgo.v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	mup.SetLogger(logger)
	mup.SetDebug(true)

	db := "localhost/mup"

	session, err := mgo.Dial("localhost/mup")
	if err != nil {
		return fmt.Errorf("cannot connect to database %s: %v", db, err)
	}

	config := &mup.Config{
		Database: session.DB(""),
	}
	server, err := mup.Start(config)
	if err != nil {
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
	<-ch
	return server.Stop()
}
