// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package msr parses CSV files directly produced by MSR acceleration sensors.
package msr

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type File struct {
	Start time.Time
	Cols  []Column
}

func (f File) TimeSeries() []float64 {
	col := f.Cols[0]
	if col.Name != "Time" {
		return nil
	}
	ts := col.Data.([]time.Time)
	xs := make([]float64, len(ts))
	for i, t := range ts {
		xs[i] = float64(t.Sub(f.Start).Milliseconds())
	}
	return xs
}

func (f File) AccX() []float64 {
	col := f.Cols[3]
	if col.Name != "ACC x" {
		return nil
	}
	return col.Data.([]float64)
}

func (f File) AccY() []float64 {
	col := f.Cols[4]
	if col.Name != "ACC y" {
		return nil
	}
	return col.Data.([]float64)
}

func (f File) AccZ() []float64 {
	col := f.Cols[5]
	if col.Name != "ACC z" {
		return nil
	}
	return col.Data.([]float64)
}

type Column struct {
	Name      string // title of the associated data
	Unit      string // units of the associated data
	Sensor    string // name of the sensor collecting the data
	SensorID  string // id of the sensor collecting the data
	TimeDelay time.Duration
	Limits    Limits
	CalibData CalibData
	Data      interface{}
}

type Row struct {
	Time time.Time
	Data []float64
}

type Limits struct {
	Alarm    float64
	Recorded float64
	Limit1   float64
	Limit2   float64
}

type CalibData struct {
	Info string
	Date time.Time
	X0   float64
	Y0   float64
	X1   float64
	Y1   float64
}

// Parse parses a MSR stream.
func Parse(r io.Reader) (File, error) {
	var (
		err  error
		cols []Column
		rows []Row
		sec  sectionKind
		msr  File
	)

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		txt := strings.TrimSpace(sc.Text())
		if len(txt) == 0 {
			continue
		}
		if txt[0] == '*' {
			switch txt {
			case "*CREATOR":
				sec = CreatorSection

			case "*STARTTIME":
				sec = StartTimeSection

			case "*MODUL":
				sec = ModuleSection

			case "*NAME":
				sec = NameSection

			case "*TIMEDELAY":
				sec = TimeDelaySection

			case "*CHANNEL":
				sec = ChannelSection

			case "*UNIT":
				sec = UnitSection

			case "*LIMITS":
				sec = LimitsSection

			case "*CALIBRATION":
				sec = CalibrationSection

			case "*DATA":
				sec = DataSection
			}
			continue
		}

		tokens := strings.Split(txt, ";")
		switch sec {
		case CreatorSection:
		case StartTimeSection:
			start, err := time.Parse("2006-01-02;15:04:05;", txt)
			if err != nil {
				return msr, errors.Wrapf(err, "could not parse start-time %q", txt)
			}
			msr.Start = start

		case ModuleSection:
			cols = make([]Column, len(tokens))
			for i, tok := range tokens {
				cols[i].Sensor = tok
				switch i {
				case 0:
					cols[i].Data = []time.Time{}
				default:
					cols[i].Data = []float64{}
				}
			}

		case NameSection:

		case TimeDelaySection:
			for i, tok := range tokens {
				if i > 0 {
					delay, err := time.ParseDuration(tok + tokens[0])
					if err != nil {
						return msr, errors.Wrapf(err, "could not parse #%d-th time-delay %q", i, txt)
					}
					cols[i].TimeDelay = delay
				}
			}

		case ChannelSection:
			for i, tok := range tokens {
				if tok == "TIME" {
					tok = "Time"
				}
				cols[i].Name = tok
			}

		case UnitSection:
			for i, tok := range tokens {
				if i == 0 {
					continue
				}
				cols[i].Unit = tok
			}

		case LimitsSection:
			// TODO(sbinet)
		case CalibrationSection:
			// TODO(sbinet)
		case DataSection:
			var row Row
			row.Time, err = time.Parse("2006-01-02 15:04:05.999", tokens[0])
			if err != nil {
				return msr, errors.Wrapf(err, "could not parse data row[%d] %q", len(rows), txt)
			}
			cols[0].Data = append(cols[0].Data.([]time.Time), row.Time)
			row.Data = make([]float64, len(tokens)-1)
			for i, tok := range tokens[1:] {
				vs := cols[i+1].Data.([]float64)
				switch tok {
				case "":
					switch len(vs) {
					case 0:
						row.Data[i] = 0.0
					default:
						row.Data[i] = vs[len(vs)-1]
					}
				default:
					val, err := strconv.ParseFloat(tok, 64)
					if err != nil {
						return msr, errors.Wrapf(err, "could not parse float %q in row %d", tok, len(rows))
					}
					row.Data[i] = val
				}
				cols[i+1].Data = append(vs, row.Data[i])
			}
			rows = append(rows, row)
		}
	}

	err = sc.Err()
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		return msr, errors.Wrap(err, "could not scan MSR file")
	}

	msr.Cols = cols
	return msr, nil
}

type sectionKind byte

const (
	UndefinedSection sectionKind = iota
	CreatorSection
	StartTimeSection
	ModuleSection
	NameSection
	TimeDelaySection
	ChannelSection
	UnitSection
	LimitsSection
	CalibrationSection
	DataSection
)
