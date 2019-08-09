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
	"github.com/pkg/errors"
	"go-hep.org/x/hep/csvutil"
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
		return errors.Wrapf(err, "could not generate UUID")
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		return errors.Wrap(err, "could not retrieve cookie")
	}

	err = r.ParseMultipartForm(500 << 20)
	if err != nil {
		return errors.Wrapf(err, "could not parse multipart form")
	}

	f, handler, err := r.FormFile("input-file")
	if err != nil {
		return errors.Wrapf(err, "could not access input file")
	}
	defer f.Close()
	fname := handler.Filename
	if strings.HasPrefix(fname, `C:\fakepath\`) {
		fname = string(fname[len(`C:\fakepath\`):])
	}
	log.Printf("fname: %v", fname)

	chunksz, err := strconv.Atoi(r.PostFormValue("chunksz"))
	if err != nil {
		return errors.Wrap(err, "could not parse chunks-size")
	}
	log.Printf("chunks: %d", chunksz)

	xs, ys, err := fouracc.Load(f)
	if err != nil {
		log.Printf(">>> err load: %v", err)
		return errors.Wrapf(err, "could not load input file")
	}

	log.Printf("xs: %d, %d", len(xs), len(ys))

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
	err = fouracc.Plot(draw.New(c), fft)
	if err != nil {
		log.Printf(">>> err plot: %v", err)
		return errors.Wrapf(err, "could not create in-memory plot")
	}

	img := new(bytes.Buffer)
	_, err = c.WriteTo(img)
	if err != nil {
		log.Printf(">>> err write plot: %v", err)
		return errors.Wrapf(err, "could not create image plot")
	}

	id := r.PostFormValue("id")
	if id == "" {
		log.Printf("empty ID")
		return errors.Wrap(err, "invalid form ID")
	}

	srv.mu.Lock()
	if srv.ids[cookie.Value] == nil {
		srv.ids[cookie.Value] = make(map[string]struct{})
	}
	srv.ids[cookie.Value][id] = struct{}{}
	srv.mu.Unlock()

	dir := filepath.Join(srv.dir, "id", id)
	err = srv.save(dir, id, fname, img.Bytes(), fft)
	if err != nil {
		log.Printf("could not save report for %q: %v", fname, err)
		return errors.Wrapf(err, "could not save report for %q", fname)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(struct {
		Image string `json:"data"`
	}{
		Image: base64.StdEncoding.EncodeToString(img.Bytes()),
	})
	if err != nil {
		log.Printf(">>> err json encoder: %v", err)
		return errors.Wrapf(err, "could not encode to json")
	}

	return nil
}

func (srv *server) dlHandle(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return errors.Wrap(err, "could not retrieve cookie")
	}

	err = r.ParseForm()
	if err != nil {
		return errors.Wrapf(err, "could not parse multipart form")
	}

	id := r.Form.Get("id")
	if id == "" {
		log.Printf(">>> empty ID")
		return errors.Errorf("invalid ID")
	}

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if _, ok := srv.ids[cookie.Value][id]; !ok {
		log.Printf("unknown ID %s", id)
		return errors.Errorf("unknown ID %q", id)
	}

	dir := filepath.Join(srv.dir, "id", id)

	matches, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		log.Printf("could not find data file report for id %q: %v", id, err)
		return errors.Wrapf(err, "could not find data file report for %q", id)
	}

	if len(matches) != 1 {
		log.Printf("invalid number of data file report(s) for id %q: got=%d, want=1", id, len(matches))
		return errors.Errorf("invalid number of data file report(s) for id %q: got=%d, want=1", id, len(matches))
	}

	fname := matches[0]
	f, err := os.Open(fname)
	if err != nil {
		log.Printf("could not open data file report for id %q: %v", id, err)
		return errors.Wrapf(err, "could not open data file report for id %q", id)
	}
	defer f.Close()

	w.Header().Set("Content-Description", "File Transfer")
	w.Header().Set("Content-Transfer-Encoding", "binary")
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(fname))
	w.Header().Set("Content-Type", "application/force-download")

	_, err = io.Copy(w, f)
	if err != nil {
		log.Printf("could not copy data file report for id %q: %v", id, err)
		return errors.Wrapf(err, "could not copy data file report for id %q", id)
	}

	return nil
}

func (srv *server) rmHandle(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return errors.Wrap(err, "could not retrieve cookie")
	}

	err = r.ParseMultipartForm(500 << 20)
	if err != nil {
		return errors.Wrapf(err, "could not parse multipart form")
	}

	id := r.PostFormValue("id")
	if id == "" {
		log.Printf(">>> empty ID")
		return errors.Errorf("invalid ID")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if _, ok := srv.ids[cookie.Value][id]; !ok {
		log.Printf("unknown ID %s", id)
		return errors.Errorf("unknown ID %q", id)
	}
	delete(srv.ids[cookie.Value], id)

	dir := filepath.Join(srv.dir, "id", id)
	err = os.RemoveAll(dir)
	if err != nil {
		log.Printf("could not remove output results directory %q: %v", dir, err)
		return errors.Wrapf(err, "could not remove output results directory %q", id)
	}

	return nil
}

func (srv *server) save(dir, id, fname string, img []byte, fft fouracc.FFT) error {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		log.Printf("could not create output directory for results %s: %v", dir, err)
		return errors.Wrapf(err, "could not create output directory %s for results", id)
	}

	bname := fname[:len(fname)-len(filepath.Ext(fname))]
	err = ioutil.WriteFile(filepath.Join(dir, bname+".png"), img, 0644)
	if err != nil {
		log.Printf("could not save plot file %s: %v", bname+".png", err)
		return errors.Wrapf(err, "could not save plot %q", id)
	}

	oname := filepath.Join(dir, fmt.Sprintf("%s.processed.chunksz-%d.csv", bname, fft.Chunks))

	tbl, err := csvutil.Create(oname)
	if err != nil {
		log.Printf("could not create output data file %q: %v", oname, err)
		return errors.Wrapf(err, "could not create output data file %q", id)
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
			return errors.Wrapf(err, "could not write row %d for output data file %q", i, id)
		}
	}

	err = tbl.Close()
	if err != nil {
		log.Printf("could not close output data file %q: %v", id, err)
		return errors.Wrapf(err, "could not close output data file %q", id)
	}

	return nil
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
		var data = new FormData();
		data.append("chunksz", chunks);
		data.append("uri", uri);
		data.append("input-file", file, uri);
		data.append("id", id);

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
				alert("processing failed: "+e);
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
		var img = data;
		var node = $("#"+id);
		node.html(
			"<img src=\"data:image/png;base64, "+ img.data + "\" />"
			+"<span onclick=\"this.parentElement.style.display='none'; updateHeight(); rmResults('"+id+"')\" class=\"w3-button w3-display-topright w3-hover-red w3-tiny\">X</span>"
			+"<form>\n"
			+" <input type=\"button\" value=\"Download\" onclick=\"window.location.href='/dl?id="+id+"'\"/>\n"
			+"</form>\n"
		);
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
			<input type="button" onclick="run()" value="Run">
		</form>


	<!--
	<form id="app-form" enctype="multipart/form-data">
		<label for="file-upload" class="file-upload" style="font-size:16px">
		<i class="fa fa-cloud-upload" aria-hidden="true" style="font-size:16px"></i> Upload
		</label>
		<input id="input-file" type="file" name="input-file"/>
		<input type="hidden" name="token" value="{{.Token}}"/>
		<input type="hidden" value="upload" />
		<br>
		Chunk size: <input id="chunksz" type="number" name="chunksz" min="1"  value="256">
		<br>
		<input type="button" onclick="run()" value="Run">
	</form>
	-->


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
