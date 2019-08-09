# fouracc

[![GoDoc](https://godoc.org/github.com/lsst-lpc/fouracc?status.svg)](https://godoc.org/github.com/lsst-lpc/fouracc)

`fouracc` is a simple Fourier analysis of acceleration data for the LSST testbench.

## fouracc cmd

```
$> go get github.com/lsst-lpc/fouracc/cmd/fouracc
$> fouracc ./testdata/msr-accel-2019-08-06.csv 
fouracc: chunk size: 256
fouracc: file:       ./testdata/msr-accel-2019-08-06.csv
fouracc: data: 16384
fouracc: coeffs: 64
fouracc: dims: (c=64, r=128)

$> fouracc -chunks=128 ./testdata/msr-accel-2019-08-06.csv 
fouracc: chunk size: 128
fouracc: file:       ./testdata/msr-accel-2019-08-06.csv
fouracc: data: 16384
fouracc: coeffs: 128
fouracc: dims: (c=128, r=64)
```
![img](https://github.com/lsst-lpc/fouracc/raw/master/testdata/out_golden.png) 

## fouracc-srv

`fouracc` also provides a web server that can run Fourier analyses over HTTP.

```
$> go get github.com/lsst-lpc/fouracc/cmd/fouracc-srv
$> fouracc-srv
fouracc-srv: http server listening on :8080
fouracc-srv: fname: msr-accel-2019-08-06.csv
fouracc-srv: chunks: 256
fouracc-srv: xs: 16384, 16384
fouracc-srv: coeffs: 64
fouracc-srv: dims: (c=64, r=128)
^Cfouracc-srv: shutdown sequence...
```

![srv](https://github.com/lsst-lpc/fouracc/raw/master/testdata/fouracc-srv.png) 

