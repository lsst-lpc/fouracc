// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fouracc

import (
	"bufio"
	"encoding/csv"
	"io"

	"github.com/pkg/errors"
	"go-hep.org/x/hep/csvutil"
)

// Load reads data from the provided io.Reader.
// Load expects 1 or 2 columns of the form ([time series], amplitudes).
func Load(r io.Reader) (xs, ys []float64, err error) {
	tbl := &csvutil.Table{
		Reader: csv.NewReader(bufio.NewReader(r)),
	}
	defer tbl.Close()

	rows, err := tbl.ReadRows(0, -1)
	if err != nil {
		return nil, nil, errors.Wrap(err, "fouracc: could not read rows")
	}
	defer rows.Close()

	id := 0
	for rows.Next() {
		var v float64
		err = rows.Scan(&v)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "fouracc: could not scan row %d", id)
		}
		xs = append(xs, float64(id))
		ys = append(ys, v)
		id++
	}

	if err := rows.Err(); err != nil {
		if err != io.EOF {
			return nil, nil, errors.Wrap(err, "fouracc: error while processing rows")
		}
	}

	return xs, ys, err
}
