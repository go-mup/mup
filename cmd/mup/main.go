package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins"
)

const defaultDir = "~/.config/mup"

var dir = flag.String("dir", defaultDir, "Configuration and data directory.")
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

	envdir := os.Getenv("MUPDIR")
	if *dir == defaultDir && envdir != "" {
		*dir = envdir
	}

	db, err := mup.OpenDB(*dir)
	if err != nil {
		return fmt.Errorf("cannot open %q: %v", *dir, err)
	}

	config.DB = db

	server, err := mup.Start(&config)
	if err != nil {
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
	signal.Notify(ch, syscall.Signal(15)) // SIGTERM
	<-ch
	return server.Stop()
}
