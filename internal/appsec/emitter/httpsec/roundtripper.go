// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"context"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/orchestrion"
)

var badInputContextOnce sync.Once

func ProtectRoundTrip(ctx context.Context, url string) error {
	opArgs := types.RoundTripOperationArgs{
		URL: url,
	}

	parent, _ := orchestrion.CtxOrGLS(ctx).Value(listener.ContextKey{}).(dyngo.Operation)
	if parent == nil { // No parent operation => we can't monitor the request
		badInputContextOnce.Do(func() {
			log.Debug("appsec: outgoing http request monitoring ignored: could not find the handler " +
				"instrumentation metadata in the request context: the request handler is not being monitored by a " +
				"middleware function or the incoming request context has not be forwarded correctly to the roundtripper")
		})
		return nil
	}

	op := &types.RoundTripOperation{
		Operation: dyngo.NewOperation(parent),
	}

	var err *events.BlockingSecurityEvent
	// TODO: move the data listener as a setup function of httpsec.StartRoundTripperOperation(ars, <setup>)
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})

	dyngo.StartOperation(op, opArgs)
	dyngo.FinishOperation(op, types.RoundTripOperationRes{})

	if err != nil {
		log.Debug("appsec: outgoing http request blocked by the WAF on URL: %s", url)
		return err
	}

	return nil
}
