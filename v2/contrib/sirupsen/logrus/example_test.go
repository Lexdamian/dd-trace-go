// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/sirupsen/logrus"
)

func ExampleHook() {
	tracer.Start()
	defer tracer.Stop()
	// Ensure your tracer is started and stopped
	// Setup logrus, do this once at the beginning of your program
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.AddHook(&DDContextLogHook{})
	logrus.SetOutput(os.Stdout)

	span, sctx := tracer.StartSpanFromContext(context.Background(), "mySpan")
	defer span.Finish()

	// Pass the current span context to the logger (Time is set for consistency in output here)
	cLog := logrus.WithContext(sctx).WithTime(time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC))
	// Log as desired using the context-aware logger
	cLog.Info("Completed some work!")
}