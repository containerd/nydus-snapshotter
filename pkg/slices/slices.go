/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package slices

// Contains returns true if an element is present in a collection.
func Contains[T comparable](s []T, e T) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}

	return false
}

// FindDuplicate returns duplicate element in a collection.
func FindDuplicate[T comparable](s []T) (T, bool) {
	visited := make(map[T]struct{})
	for _, v := range s {
		if _, ok := visited[v]; ok {
			return v, true
		}

		visited[v] = struct{}{}
	}

	var zero T
	return zero, false
}

// RemoveDuplicates removes duplicate element in a collection.
func RemoveDuplicates[T comparable](s []T) []T {
	var result []T
	visited := make(map[T]bool, len(s))
	for _, v := range s {
		if !visited[v] {
			visited[v] = true
			result = append(result, v)
		}
	}

	return result
}
