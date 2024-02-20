// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

type TracerV2Adapter struct {
	Tracer v2.Tracer
}

// Extract implements ddtrace.Tracer.
func (ta TracerV2Adapter) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	ctx, err := ta.Tracer.Extract(carrier)
	if err != nil {
		return nil, err
	}
	return SpanContextV2Adapter{Ctx: ctx}, nil
}

var (
	// ErrInvalidSpanContext is returned when the span context found in the
	// carrier is not of the expected type.
	ErrInvalidSpanContext = v2.ErrInvalidSpanContext
)

// Inject implements ddtrace.Tracer.
func (ta TracerV2Adapter) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	sca, ok := context.(SpanContextV2Adapter)
	if !ok {
		return ErrInvalidSpanContext
	}
	return ta.Tracer.Inject(sca.Ctx, carrier)
}

// StartSpan implements ddtrace.Tracer.
func (ta TracerV2Adapter) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	cfg := BuildStartSpanConfigV2(opts...)
	s := ta.Tracer.StartSpan(operationName, v2.WithStartSpanConfig(cfg))
	return SpanV2Adapter{Span: s}
}

func BuildStartSpanConfigV2(opts ...ddtrace.StartSpanOption) *v2.StartSpanConfig {
	ssc := new(ddtrace.StartSpanConfig)
	for _, o := range opts {
		o(ssc)
	}
	var parent *v2.SpanContext
	if ssc.Parent != nil {
		parent = resolveSpantContextV2(ssc.Parent)
	}
	return &v2.StartSpanConfig{
		Context:   ssc.Context,
		Parent:    parent,
		SpanID:    ssc.SpanID,
		SpanLinks: ssc.SpanLinks,
		StartTime: ssc.StartTime,
		Tags:      ssc.Tags,
	}
}

func resolveSpantContextV2(ctx ddtrace.SpanContext) *v2.SpanContext {
	if parent, ok := ctx.(SpanContextV2Adapter); ok {
		return parent.Ctx
	}

	// We may have an otelToDDSpanContext that can be converted to a v2.SpanContext
	// by copying its fields.
	// Other SpanContext may fall through here, but they are not guaranteed to be
	// fully supported, as the resulting v2.SpanContext may be missing data.
	return v2.FromGenericCtx(&SpanContextV1Adapter{Ctx: ctx})
}

// Stop implements ddtrace.Tracer.
func (ta TracerV2Adapter) Stop() {
	ta.Tracer.Stop()
}

type SpanV2Adapter struct {
	Span *v2.Span
}

// BaggageItem implements ddtrace.Span.
func (sa SpanV2Adapter) BaggageItem(key string) string {
	return sa.Span.BaggageItem(key)
}

// Context implements ddtrace.Span.
func (sa SpanV2Adapter) Context() ddtrace.SpanContext {
	ctx := sa.Span.Context()
	return SpanContextV2Adapter{Ctx: ctx}
}

// Finish implements ddtrace.Span.
func (sa SpanV2Adapter) Finish(opts ...ddtrace.FinishOption) {
	cfg := BuildFinishConfigV2(opts...)
	sa.Span.Finish(v2.WithFinishConfig(cfg))
}

func BuildFinishConfigV2(opts ...ddtrace.FinishOption) *v2.FinishConfig {
	fc := new(ddtrace.FinishConfig)
	for _, o := range opts {
		o(fc)
	}
	return &v2.FinishConfig{
		Error:           fc.Error,
		FinishTime:      fc.FinishTime,
		NoDebugStack:    fc.NoDebugStack,
		SkipStackFrames: fc.SkipStackFrames,
		StackFrames:     fc.StackFrames,
	}
}

// SetBaggageItem implements ddtrace.Span.
func (sa SpanV2Adapter) SetBaggageItem(key string, val string) {
	sa.Span.SetBaggageItem(key, val)
}

// SetOperationName implements ddtrace.Span.
func (sa SpanV2Adapter) SetOperationName(operationName string) {
	sa.Span.SetOperationName(operationName)
}

// SetTag implements ddtrace.Span.
func (sa SpanV2Adapter) SetTag(key string, value interface{}) {
	sa.Span.SetTag(key, value)
}

// Root implements appsec.rooter.
func (sa SpanV2Adapter) Root() ddtrace.Span {
	if sa.Span == nil {
		return nil
	}
	r := sa.Span.Root()
	if r == nil {
		return nil
	}
	return SpanV2Adapter{Span: r}
}

// Format implements fmt.Formatter.
func (sa SpanV2Adapter) Format(f fmt.State, c rune) {
	sa.Span.Format(f, c)
}

type SpanContextV2Adapter struct {
	Ctx *v2.SpanContext
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) ForeachBaggageItem(handler func(k string, v string) bool) {
	sca.Ctx.ForeachBaggageItem(handler)
}

// SpanID implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) SpanID() uint64 {
	return sca.Ctx.SpanID()
}

// TraceID implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) TraceID() uint64 {
	return sca.Ctx.TraceIDLower()
}

// TraceID implements ddtrace.SpanContextW3C.
func (sca SpanContextV2Adapter) TraceID128() string {
	return sca.Ctx.TraceID()
}

// TraceID128Bytes implements ddtrace.SpanContextW3C.
func (sca SpanContextV2Adapter) TraceID128Bytes() [16]byte {
	return sca.Ctx.TraceIDBytes()
}

// Partial copy of traceID from ddtrace/tracer/spancontext.go
type traceID [16]byte // traceID in big endian, i.e. <upper><lower>

var emptyTraceID traceID

func (t *traceID) HexEncoded() string {
	return hex.EncodeToString(t[:])
}

func (t *traceID) SetLower(i uint64) {
	binary.BigEndian.PutUint64(t[8:], i)
}

func (t *traceID) Empty() bool {
	return *t == emptyTraceID
}

type SpanContextV1Adapter struct {
	Ctx     ddtrace.SpanContext
	traceID traceID
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) ForeachBaggageItem(handler func(k string, v string) bool) {
	sca.Ctx.ForeachBaggageItem(handler)
}

// SpanID implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) SpanID() uint64 {
	return sca.Ctx.SpanID()
}

// TraceID implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceID() string {
	if sca.traceID.Empty() {
		_ = sca.TraceIDBytes()
	}
	return sca.traceID.HexEncoded()
}

// TraceIDBytes implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceIDBytes() [16]byte {
	if !sca.traceID.Empty() {
		return sca.traceID
	}
	if sc128, ok := sca.Ctx.(ddtrace.SpanContextW3C); ok {
		tID := sc128.TraceID128Bytes()
		copy(sca.traceID[:], tID[:])
		return sca.traceID
	}
	tID := sca.Ctx.TraceID()
	sca.traceID.SetLower(tID)
	return sca.traceID
}

// TraceIDLower implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceIDLower() uint64 {
	return sca.Ctx.TraceID()
}