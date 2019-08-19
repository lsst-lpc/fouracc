// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fouracc

import (
	"fmt"
	"image/color"

	"github.com/pkg/errors"
	"go-hep.org/x/hep/hplot"
	"gonum.org/v1/plot/palette"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

// Plot plots the provided FFT on the provided canvas.
func Plot(dc draw.Canvas, fft FFT) error {
	var err error

	err = topPlot(dc, fft)
	if err != nil {
		return err
	}

	err = bottomPlot(dc, fft)
	if err != nil {
		return err
	}

	return nil
}

func topPlot(dc draw.Canvas, fft FFT) error {
	var (
		pt     = dc.Size()
		height = pt.Y
		width  = pt.X
	)

	top := draw.Canvas{
		Canvas: dc,
		Rectangle: vg.Rectangle{
			Min: vg.Point{X: 0, Y: 0.6 * height},
			Max: vg.Point{X: width, Y: height},
		},
	}

	p := hplot.New()
	switch {
	case fft.Scale > 0:
		p.Title.Text = fmt.Sprintf("%s -- chunks=%d (freq=%v Hz)", fft.Name, fft.Chunks, fft.Scale)
	default:
		p.Title.Text = fmt.Sprintf("%s -- chunks=%d", fft.Name, fft.Chunks)
	}
	line, err := hplot.NewLine(hplot.ZipXY(fft.Data.X, fft.Data.Y))
	if err != nil {
		return errors.Wrap(err, "fouracc: could not create new-line")
	}
	line.LineStyle.Color = color.RGBA{R: 255, A: 255}

	p.Add(line, hplot.NewGrid())
	p.Draw(top)

	return nil
}

func bottomPlot(dc draw.Canvas, fft FFT) error {
	var (
		pt     = dc.Size()
		height = pt.Y
		width  = pt.X
	)

	bottom := draw.Canvas{
		Canvas: dc,
		Rectangle: vg.Rectangle{
			Min: vg.Point{X: 0, Y: 0},
			Max: vg.Point{X: width, Y: 0.6 * height},
		},
	}

	p := hplot.New()
	pal := palette.Rainbow(255, 0, 1, 1, 1, 1)
	hmap := plotter.NewHeatMap(fft, pal)
	hmap.NaN = color.Black
	p.Add(hmap)
	p.Draw(bottom)

	return nil
}

var (
	_ plotter.GridXYZ = (*FFT)(nil)
)
