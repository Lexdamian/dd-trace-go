// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptreemux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux.v5"

	"github.com/stretchr/testify/assert"
)

func Index(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	fmt.Fprint(w, "Welcome!\n")
}

func Hello(w http.ResponseWriter, _ *http.Request, params map[string]string) {
	fmt.Fprintf(w, "hello, %s!\n", params["name"])
}

type Integration struct {
	router   http.Handler
	numSpans int
	opts     []httptrace.RouterOption
}

func New() *Integration {
	return &Integration{
		opts: make([]httptrace.RouterOption, 0),
	}
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) Name() string {
	return "dimfeld/httptreemux.v5"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.router = router(i)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	assert.Equal(t, 200, w.Code)
	i.numSpans++

	url = "/500"
	r = httptest.NewRequest("GET", url, nil)
	w = httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	assert.Equal(t, 500, w.Code)
	assert.Equal(t, "500!\n", w.Body.String())
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, httptrace.WithServiceName(name))
}

func router(i *Integration) http.Handler {
	router := httptrace.New(i.opts...)

	router.GET("/200", handler200)
	router.GET("/500", handler500)

	return router
}

func handler200(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	http.Error(w, "500!", http.StatusInternalServerError)
}