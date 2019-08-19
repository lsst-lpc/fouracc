// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fouracc provides a function to analyze a time series by chunks.
package fouracc

import (
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/fourier"
)

type FFT struct {
	Data struct {
		X []float64
		Y []float64
	}
	Ts     []float64   // chunked time series
	Freqs  []float64   // frequencies
	Coeffs [][]float64 // FFT coefficients

	Name   string
	Chunks int
	Scale  float64 // Frequency scale
}

func ChunkedFFT(fname string, chunksz int, xs, ys []float64, freq float64) FFT {
	scale := 1.0
	if freq > 0 {
		scale = freq
	}
	var (
		wrk   = make([]complex128, chunksz)
		fft   = fourier.NewFFT(chunksz)
		N     = fft.Len() / 2
		freqs = make([]float64, 0, N)
		ts    = make([]float64, 0, len(xs)/chunksz)
		out   = make([][]float64, 0, len(ys)/chunksz)
	)
	for i := 0; i < len(ys); i += chunksz {
		beg := i
		end := i + chunksz
		if len(ys) < end {
			end = len(ys)
		}
		sub := ys[beg:end]
		fft.Reset(len(sub))
		cs := fft.Coefficients(wrk[:len(sub)/2+1], sub)
		if i == 0 {
			for i := range cs {
				freqs = append(freqs, fft.Freq(i)*scale)
			}
		}
		cs = cs[1:]
		vs := make([]float64, len(cs), N)
		for i, c := range cs {
			vs[i] = cmplx.Abs(c)
		}
		if len(vs) != N {
			n := N - len(vs)
			for i := 0; i < n; i++ {
				vs = append(vs, math.NaN())
			}
		}
		ts = append(ts, xs[i])
		out = append(out, vs)
	}

	cfft := FFT{
		Ts: ts, Freqs: freqs, Coeffs: out,
		Name:   fname,
		Chunks: chunksz,
		Scale:  freq,
	}
	cfft.Data.X = xs
	cfft.Data.Y = ys
	return cfft
}

func (fft FFT) Dims() (c, r int)   { return len(fft.Coeffs), len(fft.Coeffs[0]) }
func (fft FFT) Z(c, r int) float64 { return fft.Coeffs[c][r] }
func (fft FFT) X(c int) float64    { return fft.Ts[c] }
func (fft FFT) Y(r int) float64    { return fft.Freqs[r] }
