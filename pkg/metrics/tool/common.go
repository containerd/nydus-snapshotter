/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/containerd/log"
)

const (
	// Constant value for linux platform except alpha and ia64.
	defaultClkTck = 100
)

func FormatFloat64(f float64, point int) float64 {
	var value float64
	switch point {
	case 6:
		value, _ = strconv.ParseFloat(fmt.Sprintf("%.6f", f), 64)
	case 2:
		fallthrough
	default:
		value, _ = strconv.ParseFloat(fmt.Sprintf("%.2f", f), 64)
	}

	return value
}

// FIXME: return error
func ParseFloat64(val string) float64 {
	floatVal, _ := strconv.ParseFloat(val, 64)
	return floatVal
}

func GetClkTck() float64 {
	getconfPath, err := exec.LookPath("getconf")
	if err != nil {
		log.L.Warnf("can not find getconf in the system PATH, error %v", err)
		return defaultClkTck
	}
	out, err := exec.Command(getconfPath, "CLK_TCK").Output()
	if err != nil {
		log.L.Warnf("get CLK_TCK failed: %v", err)
		return defaultClkTck
	}
	return ParseFloat64(strings.ReplaceAll(string(out), "\n", ""))
}

func GetPageSize() float64 {
	return float64(os.Getpagesize())
}
