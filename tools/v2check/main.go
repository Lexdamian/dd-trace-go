// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"github.com/DataDog/dd-trace-go/v2/tools/v2check/v2check"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(v2check.Analyzer)
}
