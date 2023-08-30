// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
)

type Integration struct {
	router   *chi.Mux
	numSpans int
	opts     []chitrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]chitrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "go-chi/chi"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.router = chi.NewRouter()
	i.router.Use(chitrace.Middleware(i.opts...))
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	assert := assert.New(t)

	i.router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
	})
	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	i.router.ServeHTTP(w, r)
	i.numSpans++

	i.router.Get("/user2/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		id := chi.URLParam(r, "id")
		_, err := w.Write([]byte(id))
		assert.NoError(err)
	})

	r = httptest.NewRequest("GET", "/user2/123", nil)
	w = httptest.NewRecorder()

	// do and verify the request
	i.router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, chitrace.WithServiceName(name))
}