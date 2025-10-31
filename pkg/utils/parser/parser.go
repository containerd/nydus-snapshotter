/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package parser

import (
	"regexp"
	"strconv"
	"sync"

	"github.com/pkg/errors"
)

var (
	unitMultipliers     map[string]int64
	unitMultipliersOnce sync.Once
)

func InitUnitMultipliers() {
	unitMultipliers = make(map[string]int64, 10)

	unitMultipliers["KiB"] = 1024
	unitMultipliers["MiB"] = unitMultipliers["KiB"] * 1024
	unitMultipliers["GiB"] = unitMultipliers["MiB"] * 1024
	unitMultipliers["TiB"] = unitMultipliers["GiB"] * 1024
	unitMultipliers["PiB"] = unitMultipliers["TiB"] * 1024

	unitMultipliers["Ki"] = 1024
	unitMultipliers["Mi"] = unitMultipliers["Ki"] * 1024
	unitMultipliers["Gi"] = unitMultipliers["Mi"] * 1024
	unitMultipliers["Ti"] = unitMultipliers["Gi"] * 1024
	unitMultipliers["Pi"] = unitMultipliers["Ti"] * 1024
}

func MemoryConfigToBytes(data string, totalMemoryBytes int) (int64, error) {
	if data == "" {
		return -1, nil
	}

	// Memory value without unit.
	value, err := strconv.ParseFloat(data, 64)
	if err == nil {
		return int64(value), nil
	}

	re := regexp.MustCompile(`(\d*\.?\d+)([a-zA-Z\%]+)`)
	matches := re.FindStringSubmatch(data)
	if len(matches) != 3 {
		return 0, errors.Errorf("Falied to convert data to bytes: Unknown unit in %s", data)
	}

	// Parse memory value and unit.
	valueString, unit := matches[1], matches[2]
	value, err = strconv.ParseFloat(valueString, 64)
	if err != nil {
		return 0, errors.Wrap(err, "Failed  to parse memory limit")
	}

	// Return if the unit is byte.
	if unit == "B" {
		return int64(value), nil
	}

	// Calculate value if the unit is "%".
	if unit == "%" {
		limitMemory := float64(totalMemoryBytes) * value / 100
		return int64(limitMemory + 0.5), nil
	}

	unitMultipliersOnce.Do(InitUnitMultipliers)

	multiplier := unitMultipliers[unit]
	return int64(value * float64(multiplier)), nil
}
