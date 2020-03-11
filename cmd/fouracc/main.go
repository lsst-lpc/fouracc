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
	"golang.org/x/sync/errgroup"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

func main() {
	log.SetPrefix("fouracc: ")
	log.SetFlags(0)

	var (
		chunksz = flag.Int("chunks", 256, "chunk size of Fourier processing")
		xmin    = flag.Int("xmin", 0, "start of analysis range index")
		xmax    = flag.Int("xmax", -1, "end of analysis range index")
	)

	flag.Parse()

	log.Printf("chunk size: %v", *chunksz)
	log.Printf("file:       %v", flag.Arg(0))
	log.Printf("range:      data[%d:%d]", *xmin, *xmax)

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
		ts := msr.Axis()
		freq := msr.Freq()
		beg, end, err := clean(len(ts), *xmin, *xmax)
		if err != nil {
			log.Fatal(err)
		}
		ts = ts[beg:end]
		var grp errgroup.Group
		for _, tt := range []struct {
			Name string
			Data []float64
		}{
			{"x", msr.AccX()[beg:end]},
			{"y", msr.AccY()[beg:end]},
			{"z", msr.AccZ()[beg:end]},
		} {
			tt := tt
			grp.Go(func() error {
				err := process(filepath.Base(flag.Arg(0)), tt.Name, *chunksz, ts, tt.Data, freq)
				if err != nil {
					return fmt.Errorf("could not process axis %s: %w", tt.Name, err)
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
		beg, end, err := clean(len(xs), *xmin, *xmax)
		if err != nil {
			log.Fatal(err)
		}
		xs = xs[beg:end]
		ys = ys[beg:end]
		err = process(filepath.Base(flag.Arg(0)), "", *chunksz, xs, ys, -1)
		if err != nil {
			log.Fatalf("could not process data: %v", err)
		}
	}
}

func clean(len, beg, end int) (int, int, error) {
	if end == -1 {
		end = len
	}
	switch {
	case end > len:
		return beg, end, fmt.Errorf("invalid data range (end=%d > len=%d)", end, len)
	case beg > end:
		return beg, end, fmt.Errorf("invalid data range (beg=%d > end=%d)", beg, end)
	case beg > len:
		return beg, end, fmt.Errorf("invalid data range (beg=%d > len=%d)", end, len)
	}
	return beg, end, nil
}

func process(fname, title string, chunksz int, xs, ys []float64, freq float64) error {
	log.Printf("data: %d", len(ys))

	if title != "" {
		fname += " [axis=" + title + "]"
	}

	fft := fouracc.ChunkedFFT(fname, chunksz, xs, ys, freq)
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
		return fmt.Errorf("could not plot FFT: %w", err)
	}

	oname := "out.png"
	if title != "" {
		oname = fmt.Sprintf("out-%s.png", title)
	}

	o, err := os.Create(oname)
	if err != nil {
		return fmt.Errorf("could not create output file: %w", err)
	}
	defer o.Close()
	_, err = c.WriteTo(o)
	if err != nil {
		return fmt.Errorf("could not create output plot: %w", err)
	}
	err = o.Close()
	if err != nil {
		return fmt.Errorf("could not close output file: %w", err)
	}

	return nil
}
