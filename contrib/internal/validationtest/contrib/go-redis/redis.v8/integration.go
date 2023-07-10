// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis.v8"
)

type Integration struct {
	client   redis.UniversalClient
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/go-redis/redis.v8"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}

	i.client = redistrace.NewClient(opts)

	return func() {
		i.client.Close()
	}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	i.client.Set(ctx, "test_key", "test_value", 0)
	i.client.Get(ctx, "test_key")
	i.client.Incr(ctx, "int_key")
	i.client.ClientList(ctx)
	i.numSpans += 4

	pipeline := i.client.Pipeline()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.Exec(ctx)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
