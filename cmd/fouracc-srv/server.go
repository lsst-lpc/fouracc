// Copyright 2019 The fouracc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	uuid "github.com/hashicorp/go-uuid"
	"github.com/lsst-lpc/fouracc"
	"github.com/lsst-lpc/fouracc/msr"
	"go-hep.org/x/hep/csvutil"
	"golang.org/x/sync/errgroup"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

const cookieName = "FOURACC_SRV"

type server struct {
	dir  string
	quit chan int

	mu      sync.RWMutex
	cookies map[string]*http.Cookie
	ids     map[string]map[string]struct{}
}

func newServer(addr, dir string, mux *http.ServeMux) *server {
	app := &server{
		dir:     dir,
		quit:    make(chan int),
		cookies: make(map[string]*http.Cookie),
		ids:     make(map[string]map[string]struct{}),
	}
	go app.run()

	mux.Handle("/", app.wrap(app.rootHandle))
	mux.Handle("/run", app.wrap(app.runHandle))
	mux.Handle("/dl", app.wrap(app.dlHandle))
	mux.Handle("/rm", app.wrap(app.rmHandle))
	return app
}

func (srv *server) run() {

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	srv.gc()
	for {
		select {
		case <-ticker.C:
			srv.gc()
		case <-srv.quit:
			return
		}
	}
}

func (srv *server) Shutdown() {
	close(srv.quit)
}

func (srv *server) gc() {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for name, cookie := range srv.cookies {
		now := time.Now()
		if now.After(cookie.Expires) {
			delete(srv.cookies, name)
			cookie.MaxAge = -1
			if srv.ids[cookie.Value] != nil {
				for id := range srv.ids[cookie.Value] {
					dir := filepath.Join(srv.dir, "id", id)
					os.RemoveAll(dir)
				}
			}
		}
	}
}

func (srv *server) expired(cookie *http.Cookie) bool {
	now := time.Now()
	return now.After(cookie.Expires)
}

func (srv *server) setCookie(w http.ResponseWriter, r *http.Request) error {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	cookie, err := r.Cookie(cookieName)
	if err != nil && err != http.ErrNoCookie {
		return err
	}

	if cookie != nil {
		return nil
	}

	v, err := uuid.GenerateUUID()
	if err != nil {
		return fmt.Errorf("could not generate UUID: %w", err)
	}

	cookie = &http.Cookie{
		Name:    cookieName,
		Value:   v,
		Expires: time.Now().Add(24 * time.Hour),
	}
	srv.cookies[cookie.Value] = cookie
	srv.ids[cookie.Value] = make(map[string]struct{})
	http.SetCookie(w, cookie)
	return nil
}

func (srv *server) wrap(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := srv.setCookie(w, r)
		if err != nil {
			log.Printf("error retrieving cookie: %v\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := fn(w, r); err != nil {
			log.Printf("error %q: %v\n", r.URL.Path, err.Error())

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)

			json.NewEncoder(w).Encode(struct {
				Err string `json:"error"`
			}{Err: err.Error()})

		}
	}
}

func (srv *server) rootHandle(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		// ok
	default:
		return fmt.Errorf("invalid request %q for /", r.Method)
	}

	crutime := time.Now().Unix()
	h := md5.New()
	io.WriteString(h, strconv.FormatInt(crutime, 10))
	token := fmt.Sprintf("%x", h.Sum(nil))

	t, err := template.New("upload").Parse(page)
	if err != nil {
		return err
	}

	return t.Execute(w, struct {
		Token string
	}{token})
}

func (srv *server) runHandle(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return fmt.Errorf("could not retrieve cookie: %w", err)
	}

	err = r.ParseMultipartForm(500 << 20)
	if err != nil {
		return fmt.Errorf("could not parse multipart form: %w", err)
	}

	id := r.PostFormValue("id")
	if id == "" {
		log.Printf("empty ID")
		return fmt.Errorf("invalid form ID: %w", err)
	}

	srv.mu.Lock()
	if srv.ids[cookie.Value] == nil {
		srv.ids[cookie.Value] = make(map[string]struct{})
	}
	srv.ids[cookie.Value][id] = struct{}{}
	srv.mu.Unlock()

	f, handler, err := r.FormFile("input-file")
	if err != nil {
		return fmt.Errorf("could not access input file: %w", err)
	}
	defer f.Close()
	fname := handler.Filename
	if strings.HasPrefix(fname, `C:\fakepath\`) {
		fname = string(fname[len(`C:\fakepath\`):])
	}
	log.Printf("fname: %v", fname)

	chunksz, err := strconv.Atoi(r.PostFormValue("chunksz"))
	if err != nil {
		return fmt.Errorf("could not parse chunks-size: %w", err)
	}
	log.Printf("chunks: %d", chunksz)

	xmin, err := strconv.Atoi(r.PostFormValue("xmin"))
	if err != nil {
		return fmt.Errorf("could not parse min channel value: %w", err)
	}
	log.Printf("xmin: %d", xmin)

	xmax, err := strconv.Atoi(r.PostFormValue("xmax"))
	if err != nil {
		return fmt.Errorf("could not parse max channel value: %w", err)
	}
	log.Printf("xmax: %d", xmax)

	var head [64]byte
	_, err = io.ReadFull(f, head[:])
	if err != nil {
		return fmt.Errorf("could not read CSV header: %w", err)
	}
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return fmt.Errorf("could not rewind CSV file: %w", err)
	}

	var (
		isMSR = strings.HasPrefix(string(head[:]), "*CREATOR")
		imgs  [][]byte
		names []string
	)

	switch {
	case isMSR:
		msr, err := msr.Parse(f)
		if err != nil {
			return fmt.Errorf("could not parse MSR file: %w", err)
		}
		freq := msr.Freq()
		ts := msr.Axis()
		beg, end, err := clean(len(ts), xmin, xmax)
		if err != nil {
			return fmt.Errorf("could not infer data slice range: %w", err)
		}
		ts = ts[beg:end]
		var (
			grp errgroup.Group
		)
		imgs = make([][]byte, 3)
		names = make([]string, 3)
		for _, tt := range []struct {
			id   int
			name string
			data []float64
		}{
			{0, "x", msr.AccX()[beg:end]},
			{1, "y", msr.AccY()[beg:end]},
			{2, "z", msr.AccZ()[beg:end]},
		} {
			tt := tt
			grp.Go(func() error {
				img, err := srv.process(id, fname, tt.name, chunksz, ts, tt.data, freq)
				if err != nil {
					return fmt.Errorf("could not process axis %s: %w", tt.name, err)
				}
				imgs[tt.id] = img
				names[tt.id] = tt.name
				return nil
			})
		}
		err = grp.Wait()
		if err != nil {
			return fmt.Errorf("could not process MSR file: %w", err)
		}

	default:
		xs, ys, err := fouracc.Load(f)
		if err != nil {
			log.Printf(">>> err load: %v", err)
			return fmt.Errorf("could not load input file: %w", err)
		}
		beg, end, err := clean(len(xs), xmin, xmax)
		if err != nil {
			return fmt.Errorf("could not infer data slice range: %w", err)
		}
		xs = xs[beg:end]
		ys = ys[beg:end]

		img, err := srv.process(id, fname, "", chunksz, xs, ys, -1)
		if err != nil {
			return fmt.Errorf("could not process CSV file: %w", err)
		}
		imgs = append(imgs, img)
		names = append(names, "")
	}

	var (
		stdimgs = make([]string, len(imgs))
	)
	for i, img := range imgs {
		stdimgs[i] = base64.StdEncoding.EncodeToString(img)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(struct {
		Names  []string `json:"names"`
		Images []string `json:"imgs"`
		Error  string   `json:"error"`
	}{
		Names:  names,
		Images: stdimgs,
	})
	if err != nil {
		log.Printf(">>> err json encoder: %v", err)
		return fmt.Errorf("could not encode to json: %w", err)
	}

	return nil
}

func (srv *server) dlHandle(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return fmt.Errorf("could not retrieve cookie: %w", err)
	}

	err = r.ParseForm()
	if err != nil {
		return fmt.Errorf("could not parse multipart form: %w", err)
	}

	id := r.Form.Get("id")
	if id == "" {
		log.Printf(">>> empty ID")
		return fmt.Errorf("invalid ID")
	}

	axis := r.Form.Get("axis")
	switch axis {
	case "", "x", "y", "z":
		// ok
	default:
		return fmt.Errorf("invalid axis %q", axis)
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if _, ok := srv.ids[cookie.Value][id]; !ok {
		log.Printf("unknown ID %s", id)
		return fmt.Errorf("unknown ID %q", id)
	}

	dir := filepath.Join(srv.dir, "id", id)

	glob := "*.csv"
	if axis != "" {
		glob = "*-" + axis + ".processed.*.csv"
	}

	matches, err := filepath.Glob(filepath.Join(dir, glob))
	if err != nil {
		log.Printf("could not find data file report for id %q: %v", id, err)
		return fmt.Errorf("could not find data file report for %q: %w", id, err)
	}

	if len(matches) != 1 {
		log.Printf("invalid number of data file report(s) for id %q (glob=%q): got=%d, want=1\nmatches: %q", id, glob, len(matches), matches)
		return fmt.Errorf("invalid number of data file report(s) for id %q: got=%d, want=1", id, len(matches))
	}

	fname := matches[0]
	f, err := os.Open(fname)
	if err != nil {
		log.Printf("could not open data file report for id %q: %v", id, err)
		return fmt.Errorf("could not open data file report for id %q: %w", id, err)
	}
	defer f.Close()

	w.Header().Set("Content-Description", "File Transfer")
	w.Header().Set("Content-Transfer-Encoding", "binary")
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(fname))
	w.Header().Set("Content-Type", "application/force-download")

	_, err = io.Copy(w, f)
	if err != nil {
		log.Printf("could not copy data file report for id %q: %v", id, err)
		return fmt.Errorf("could not copy data file report for id %q: %w", id, err)
	}

	return nil
}

func (srv *server) rmHandle(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return fmt.Errorf("could not retrieve cookie: %w", err)
	}

	err = r.ParseMultipartForm(500 << 20)
	if err != nil {
		return fmt.Errorf("could not parse multipart form: %w", err)
	}

	id := r.PostFormValue("id")
	if id == "" {
		log.Printf(">>> empty ID")
		return fmt.Errorf("invalid ID")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if _, ok := srv.ids[cookie.Value][id]; !ok {
		log.Printf("unknown ID %s", id)
		return fmt.Errorf("unknown ID %q", id)
	}
	delete(srv.ids[cookie.Value], id)

	dir := filepath.Join(srv.dir, "id", id)
	err = os.RemoveAll(dir)
	if err != nil {
		log.Printf("could not remove output results directory %q: %v", dir, err)
		return fmt.Errorf("could not remove output results directory %q: %w", id, err)
	}

	return nil
}

func (srv *server) save(dir, id, fname, axis string, img []byte, fft fouracc.FFT) error {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		log.Printf("could not create output directory for results %s: %v", dir, err)
		return fmt.Errorf("could not create output directory %s for results: %w", id, err)
	}

	bname := fname[:len(fname)-len(filepath.Ext(fname))]
	if axis != "" {
		bname += "-" + axis
	}
	err = ioutil.WriteFile(filepath.Join(dir, bname+".png"), img, 0644)
	if err != nil {
		log.Printf("could not save plot file %s: %v", bname+".png", err)
		return fmt.Errorf("could not save plot %q: %w", id, err)
	}

	oname := filepath.Join(dir, fmt.Sprintf("%s.processed.chunksz-%d.csv", bname, fft.Chunks))

	tbl, err := csvutil.Create(oname)
	if err != nil {
		log.Printf("could not create output data file %q: %v", oname, err)
		return fmt.Errorf("could not create output data file %q: %w", id, err)
	}
	defer tbl.Close()

	tbl.Writer.Comma = '\t'

	for i, row := range fft.Coeffs {
		args := make([]interface{}, len(row))
		for i, v := range row {
			args[i] = v
		}
		err = tbl.WriteRow(args...)
		if err != nil {
			log.Printf("could not write row %d for output data file %q: %v", i, id, err)
			return fmt.Errorf("could not write row %d for output data file %q: %w", i, id, err)
		}
	}

	err = tbl.Close()
	if err != nil {
		log.Printf("could not close output data file %q: %v", id, err)
		return fmt.Errorf("could not close output data file %q: %w", id, err)
	}

	return nil
}

func (srv *server) process(id, fname, axis string, chunksz int, xs, ys []float64, freq float64) ([]byte, error) {
	name := fname
	if axis != "" {
		name += " [axis=" + axis + "]"
	}

	log.Printf("processing %q...", name)

	fft := fouracc.ChunkedFFT(name, chunksz, xs, ys, freq)

	const (
		width  = 20 * vg.Centimeter
		height = 30 * vg.Centimeter
	)

	c := vgimg.PngCanvas{Canvas: vgimg.New(width, height)}
	err := fouracc.Plot(draw.New(c), fft)
	if err != nil {
		return nil, fmt.Errorf("could not plot FFT: %w", err)
	}

	o := new(bytes.Buffer)
	_, err = c.WriteTo(o)
	if err != nil {
		return nil, fmt.Errorf("could not create output plot: %w", err)
	}

	dir := filepath.Join(srv.dir, "id", id)
	err = srv.save(dir, id, fname, axis, o.Bytes(), fft)
	if err != nil {
		log.Printf("could not save report for %q: %v", name, err)
		return nil, fmt.Errorf("could not save report for %q: %w", name, err)
	}

	log.Printf("processing %q... [done]", name)
	return o.Bytes(), nil
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

const page = `<html>
<head>
    <title>FourAcc Analyzer</title>

	<meta name="viewport" content="width=device-width, initial-scale=1">
	<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/4.7.0/css/font-awesome.min.css" />
	<link rel="stylesheet" href="https://www.w3schools.com/w3css/3/w3.css">
	<script src="https://ajax.googleapis.com/ajax/libs/jquery/3.1.1/jquery.min.js"></script>

	<style>
//	input[type=file] {
//		display: none;
//	}
	input[type=submit] {
		background-color: #F44336;
		padding:5px 15px;
		border:0 none;
		cursor:pointer;
		-webkit-border-radius: 5px;
		border-radius: 5px;
	}
	.flex-container {
		display: -webkit-flex;
		display: flex;
	}
	.flex-item {
		margin: 5px;
	}
	.app-file-upload {
		color: white;
		background-color: #0091EA;
		padding:5px 15px;
		border:0 none;
		cursor:pointer;
		-webkit-border-radius: 5px;
	}

	.loader {
		border: 16px solid #f3f3f3;
		border-radius: 50%;
		border-top: 16px solid #3498db;
		width: 120px;
		height: 120px;
		-webkit-animation: spin 2s linear infinite; /* Safari */
		animation: spin 2s linear infinite;
	}

	/* Safari */
	@-webkit-keyframes spin {
		0% { -webkit-transform: rotate(0deg); }
		100% { -webkit-transform: rotate(360deg); }
	}

	@keyframes spin {
		0% { transform: rotate(0deg); }
		100% { transform: rotate(360deg); }
	}
	</style>

<script type="text/javascript">
	"use strict"
	
	function run() {
		var id = uuidv4();

		var file = $("#app-form input")[0].files[0];
		var uri = $("#input-file").val();
		//$("#input-file").val("");
		
		var chunks = $("#chunksz").val();
		var xmin = $("#xmin").val();
		var xmax = $("#xmax").val();
		var data = new FormData();
		data.append("chunksz", chunks);
		data.append("uri", uri);
		data.append("input-file", file, uri);
		data.append("id", id);
		data.append("xmin", xmin);
		data.append("xmax", xmax);

		plotPlaceholder(id);

		$.ajax({
			url: "/run",
			method: "POST",
			data: data,
			processData: false,
			contentType: false,
			success: function(data, status) {
				plotCallback(data, status, id);
			},
			error: function(e) {
				alert("processing failed: "+JSON.parse(e.responseText).error);
				var node = $("#"+id);
				node.remove();
				updateHeight();
			}
		});
	};

	function uuidv4() {
		return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
			var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
			return v.toString(16);
		});
	}

	var spinner = function() {
		var top = "<div class=\"w3-cell-row\">";
		var left = "<div class=\"w3-container w3-white w3-cell\"></div>";
		var right = "<div class=\"w3-container w3-white w3-cell\"></div>";
		var middle = "<div class=\"w3-container w3-white w3-cell\" style=\"width: 20%\">"
			+"<div class=\"loader w3-white\" style=\"display: block;\"><p></div></div>";
		return top+left+middle+right+"</div>";
	}();

	function plotPlaceholder(id) {
		var node = $("<div></div>");
		node.attr("id", id);
		node.addClass("w3-panel w3-white w3-card-2 w3-display-container w3-content w3-center");
		node.css("width","100%");
		node.html(spinner);

		$("#app-display").prepend(node);
		updateHeight();
	};

	function plotCallback(data, status, id) {
		var node = $("#"+id);
		node.html("<span onclick=\"this.parentElement.style.display='none'; updateHeight(); rmResults('"+id+"')\" class=\"w3-button w3-display-topright w3-hover-red w3-tiny\">X</span>");
		data.imgs.forEach(function(v, i, arr) {
			var button = "Download"
			if (data.names[i] != "") {
				button = "Download "+data.names[i]+"-axis";
			}
			node.append(
				"<br>\n"
				+"<div>\n"
				+"<img src=\"data:image/png;base64, "+ v + "\" />"
				+"<form>\n"
				+" <input type=\"button\" value=\""+button+"\" onclick=\"window.location.href='/dl?id="+id+"&axis="+data.names[i]+"'\"/>\n"
				+"</form>\n"
				+"</div>\n"
			);
		});
		updateHeight();
	};

	function updateHeight() {
		var hmenu = $("#app-sidebar").height();
		var hcont = $("#app-container").height();
		var hdisp = $("#app-display").height();
		if (hdisp > hcont) {
			$("#app-container").height(hdisp);
		}
		if (hdisp < hmenu && hcont > hmenu) {
			$("#app-container").height(hmenu);
		}
	};

	function rmResults(id) {
		var data = new FormData();
		data.append("id", id);

		$.ajax({
			url: "/rm",
			method: "POST",
			data: data,
			processData: false,
			contentType: false,
			error: function(e) {
				alert("removing ["+id+"] failed: "+e);
			}
		});

		$("#"+id).remove();
	}

</script>
</head>
<body>

<!-- Sidebar -->
<div id="app-sidebar" class="w3-sidebar w3-bar-block w3-card-4 w3-light-grey" style="width:25%">
	<div class="w3-bar-item w3-card-2 w3-black">
		<h2>FourAcc analyzer</h2>
	</div>
	<div class="w3-bar-item">

	<div>
		<form id="app-form" enctype="multipart/form-data">
			File:
			<input id="input-file" type="file" name="input-file"/>
			<br>
			Chunk size: <input id="chunksz" type="number" name="chunksz" min="1"  value="256">
			<br>
			x-min: <input id="xmin" type="number" name="xmin" min="0"  value="0">
			<br>
			x-max: <input id="xmax" type="number" name="xmax" min="-1"  value="-1">
			<br>
			<input type="button" onclick="run()" value="Run">
		</form>

	</div>
	<br>

	</div>
</div>

<!-- Page Content -->
<div style="margin-left:25%; height:100%" class="w3-grey" id="app-container">
	<div class="w3-container w3-content w3-cell w3-cell-middle w3-cell-row w3-center w3-justify w3-grey" style="width:100%" id="app-display">
	</div>
</div>

</body>
</html>
`
