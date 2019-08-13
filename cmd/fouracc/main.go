// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command fouracc runs a FFT analysis on an MSR acceleration file.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/lsst-lpc/fouracc"
	"github.com/lsst-lpc/fouracc/msr"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
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

	var head [64]byte

	_, err = io.ReadFull(f, head[:])
	if err != nil {
		log.Fatalf("could not read CSV header: %v", err)
	}
	f.Seek(0, io.SeekStart)

	switch {
	case strings.HasPrefix(string(head[:]), "*CREATOR"):
		msr, err := msr.Parse(f)
		if err != nil {
			log.Fatalf("could not parse MSR file: %v", err)
		}
		ts := msr.TimeSeries()
		var grp errgroup.Group
		for _, tt := range []struct {
			Name string
			Data []float64
		}{
			{"x", msr.AccX()},
			{"y", msr.AccY()},
			{"z", msr.AccZ()},
		} {
			grp.Go(func() error {
				tt := tt
				err := process(filepath.Base(flag.Arg(0)), tt.Name, *chunksz, ts, tt.Data)
				if err != nil {
					return errors.Wrapf(err, "could not process axis %s: %v", tt.Name, err)
				}
				return nil
			})
		}
		err = grp.Wait()
		if err != nil {
			log.Fatal(err)
		}

	default:
		xs, ys, err := fouracc.Load(f)
		if err != nil {
			log.Fatal(err)
		}
		err = process(filepath.Base(flag.Arg(0)), "", *chunksz, xs, ys)
		if err != nil {
			log.Fatalf("could not process data: %v", err)
		}
	}
}

func process(fname, title string, chunksz int, xs, ys []float64) error {
	log.Printf("data: %d", len(ys))

	if title != "" {
		fname += " [axis=" + title + "]"
	}

	fft := fouracc.ChunkedFFT(fname, chunksz, xs, ys)
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
	err := fouracc.Plot(draw.New(c), fft)
	if err != nil {
		return errors.Wrap(err, "could not plot FFT")
	}

	oname := "out.png"
	if title != "" {
		oname = fmt.Sprintf("out-%s.png", title)
	}

	o, err := os.Create(oname)
	if err != nil {
		return errors.Wrapf(err, "could not create output file")
	}
	defer o.Close()
	_, err = c.WriteTo(o)
	if err != nil {
		return errors.Wrapf(err, "could not create output plot")
	}
	err = o.Close()
	if err != nil {
		return errors.Wrapf(err, "could not close output file")
	}

	return nil
}
