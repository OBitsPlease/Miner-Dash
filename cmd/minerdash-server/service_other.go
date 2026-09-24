//go:build !windows

package main

import "context"

func runAsWindowsService(_ string, _ func(context.Context) error) (bool, error) {
	return false, nil
}
