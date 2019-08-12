// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command fouracc-srv runs a web server for the FFT analysis on an MSR acceleration file.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/crypto/acme/autocert"
)

var (
	addrFlag = flag.String("addr", ":8080", "server address:port")
	servFlag = flag.String("serv", "http", "server protocol")
	hostFlag = flag.String("host", "", "server domain name for TLS ")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			`Usage: fouracct-srv [options]

ex:


 $> fouracc-srv -addr :8080 -serv https -host example.com
 2017/04/06 15:13:59 https server listening on :8080 at example.com

options:
`,
		)
		flag.PrintDefaults()
	}

	flag.Parse()

	log.SetPrefix("fouracc-srv: ")
	log.SetFlags(0)

	dir, err := ioutil.TempDir("", "fouracc-srv-")
	if err != nil {
		log.Panicf("could not create temporary directory: %v", err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	run(dir, c)
}

func run(dir string, c chan os.Signal) {
	defer func() {
		log.Printf("shutdown sequence...")
		log.Printf("removing directory %q...", dir)
		os.RemoveAll(dir)
	}()

	log.Printf("%s server listening on %s", *servFlag, *addrFlag)

	srv := newServer(*addrFlag, dir, http.DefaultServeMux)
	defer srv.Shutdown()

	go func() {
		if *servFlag == "http" {
			log.Fatal(http.ListenAndServe(*addrFlag, nil))
		} else if *servFlag == "https" {
			m := autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(*hostFlag),
				Cache:      autocert.DirCache("certs"), //folder for storing certificates
			}
			server := &http.Server{
				Addr: *addrFlag,
				TLSConfig: &tls.Config{
					GetCertificate: m.GetCertificate,
				},
			}
			log.Fatal(server.ListenAndServeTLS("", ""))
		}
	}()
	<-c
}
