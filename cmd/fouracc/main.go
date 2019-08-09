// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command fouracc runs a FFT analysis on an MSR acceleration file.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/lsst-lpc/fouracc"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

func main() {
	log.SetPrefix("fouracc: ")
	log.SetFlags(0)

	chunksz := flag.Int("chunks", 256, "chunk size of Fourier processing")

	flag.Parse()

	log.Printf("chunk size: %v", *chunksz)
	log.Printf("file:       %v", flag.Arg(0))

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	xs, ys, err := fouracc.Load(f)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("data: %d", len(ys))

	fft := fouracc.ChunkedFFT(filepath.Base(flag.Arg(0)), *chunksz, xs, ys)
	log.Printf("coeffs: %d", len(fft.Coeffs))
	{
		c, r := fft.Dims()
		log.Printf("dims: (c=%d, r=%d)", c, r)
	}

	const (
		width  = 20 * vg.Centimeter
		height = 30 * vg.Centimeter
	)

	c := vgimg.PngCanvas{Canvas: vgimg.New(width, height)}
	err = fouracc.Plot(draw.New(c), fft)
	if err != nil {
		log.Fatal(err)
	}

	o, err := os.Create("out.png")
	if err != nil {
		log.Fatalf("error: %v\n", err)
	}
	defer o.Close()
	_, err = c.WriteTo(o)
	if err != nil {
		log.Fatal(err)
	}
	err = o.Close()
	if err != nil {
		log.Fatal(err)
	}
}
