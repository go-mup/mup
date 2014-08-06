package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins"

	"gopkg.in/mgo.v2"
	"strings"
)

var db = flag.String("db", "localhost/mup", "MongoDB database URL including database name to use.")
var accounts = flag.String("accounts", "*", "Configured account names to connect to, comma-separated. Defaults to all.")
var noaccounts = flag.Bool("no-accounts", false, "Do not connect to accounts in this instance.")
var plugins = flag.String("plugins", "*", "Configured plugin names to run, comma-separated. Defaults to all.")
var noplugins = flag.Bool("no-plugins", false, "Do not run plugins in this instance.")
var debug = flag.Bool("debug", false, "Print debugging messages as well.")

var help = `Usage: mup [options]

Options:

`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, help)
		flag.PrintDefaults()
	}

	flag.Parse()

	if len(flag.Args()) > 0 {
		flag.Usage()
		os.Exit(1)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	mup.SetLogger(logger)
	mup.SetDebug(*debug)

	var config mup.Config

	if *noaccounts {
		if *accounts != "*" {
			return fmt.Errorf("cannot use -accounts and -no-accounts together")
		}
		*accounts = ""
	}
	if *noplugins {
		if *plugins != "*" {
			return fmt.Errorf("cannot use -plugins and -no-plugins together")
		}
		*plugins = ""
	}
	if *accounts != "*" {
		config.Accounts = strings.Split(*accounts, ",")
	}
	if *plugins != "*" {
		config.Plugins = strings.Split(*plugins, ",")
	}


	logger.Printf("Connecting to MongoDB: %s", *db)
	session, err := mgo.Dial(*db)
	if err != nil {
		return fmt.Errorf("cannot connect to database %s: %v", *db, err)
	}

	config.Database = session.DB("")

	server, err := mup.Start(&config)
	if err != nil {
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
	<-ch
	return server.Stop()
}
